package scheduler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/event"
	executionmodel "github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/storage"
)

// newTimerOwnerToken returns a fresh 32-byte random fencing token for one timer
// claim. Tokens are never derived from a worker identity: reusing a token across
// claims would let a delayed owner delete a timer it no longer holds.
func newTimerOwnerToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate wait timer owner token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// PollDueTimers processes one bounded batch of due wait timers. It is callable
// rather than self-scheduling so the host owns concurrency, cancellation, and
// the poll cadence. It returns the number of timers whose processing reached a
// durable commit, plus any per-timer errors joined together; one bad timer does
// not block the rest of the batch.
//
// Without a wait store the poller is a no-op, preserving non-durable behavior.
func (s *Scheduler) PollDueTimers(ctx context.Context) (int, error) {
	if s.waitStore == nil {
		return 0, nil
	}
	due, err := s.waitStore.DueWaitTimers(ctx, time.Now().UTC(), s.pollConfig.BatchSize)
	if err != nil {
		return 0, fmt.Errorf("list due wait timers: %w", err)
	}
	processed := 0
	var errs []error
	for _, timer := range due {
		if ctx.Err() != nil {
			break
		}
		n, err := s.processDueTimer(ctx, timer)
		processed += n
		if err != nil {
			errs = append(errs, err)
		}
	}
	return processed, errors.Join(errs...)
}

// processDueTimer claims one due timer, applies the wait's expiry rule, commits
// the resulting state with a fenced delete of the claimed timer, and only then
// publishes the event that announces the outcome.
func (s *Scheduler) processDueTimer(ctx context.Context, timer storage.Timer) (int, error) {
	payload, err := executionmodel.DecodeWaitTimer(timer.Data)
	if err != nil {
		return 0, fmt.Errorf("decode wait timer %s: %w", timer.Key, err)
	}
	if timer.Key == "" || timer.DueAt.IsZero() {
		return 0, fmt.Errorf("due wait timer has no key or due time")
	}
	now := time.Now().UTC()
	if timer.DueAt.After(now) {
		return 0, nil
	}

	owner, err := newTimerOwnerToken()
	if err != nil {
		return 0, err
	}
	claimed, err := s.waitStore.ClaimWaitTimer(ctx, timer.Key, owner, s.pollConfig.Lease)
	if err != nil {
		return 0, fmt.Errorf("claim wait timer %s: %w", timer.Key, err)
	}
	if !claimed {
		return 0, nil
	}

	// Re-check ownership before touching execution state. A claim whose lease
	// was already lost must not mutate a live execution, and the fenced commit
	// below is what finally removes the timer.
	if err := s.waitStore.RenewWaitTimer(ctx, timer.Key, owner, s.pollConfig.Lease); err != nil {
		if errors.Is(err, executionmodel.ErrWaitTimerLeaseLost) {
			return 0, nil
		}
		return 0, fmt.Errorf("renew wait timer %s: %w", timer.Key, err)
	}

	execution, err := s.executions.Get(ctx, payload.ExecutionID)
	if errors.Is(err, storage.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("load execution %s: %w", payload.ExecutionID, err)
	}
	if execution.IsTerminal() {
		return 0, nil
	}

	state := execution.CapabilityState(payload.CapabilityID)
	if state.Status != executionmodel.CapabilityWaiting {
		return 0, nil
	}
	// The snapshot is authoritative. A timer whose state says the deadline is
	// still in the future, or that no longer has one, is stale and is left
	// alone so it cannot wake work prematurely.
	if state.WaitUntil == nil || state.WaitUntil.After(now) {
		return 0, nil
	}

	before := execution.Snapshot()
	var publish func() error
	needsIntent := false
	switch state.WaitType {
	case executionmodel.WaitTypeTimer, executionmodel.WaitTypeDeadline:
		if err := execution.ResolveWait(payload.CapabilityID, nil); err != nil {
			return 0, err
		}
		publish = func() error { return s.publishReady(ctx, execution, payload.CapabilityID) }
		needsIntent = true
	case executionmodel.WaitTypeSignal:
		policy, err := execution.ExpireWait(payload.CapabilityID, now)
		if err != nil {
			return 0, err
		}
		switch policy {
		case executionmodel.WaitDeadlinePolicyContinue:
			publish = func() error { return s.publishReady(ctx, execution, payload.CapabilityID) }
			needsIntent = true
		case executionmodel.WaitDeadlinePolicyFail:
			reason := fmt.Errorf("wait deadline exceeded for capability %s", payload.CapabilityID)
			execution.MarkFailed(reason)
			publish = func() error {
				return s.bus.Publish(ctx, event.New(event.ExecutionFailed, execution.ID, execution.CorrelationID, "", event.ExecutionFailedPayload{
					Message: reason.Error(),
				}))
			}
		case executionmodel.WaitDeadlinePolicyCancel:
			reason := fmt.Errorf("wait deadline cancelled for capability %s", payload.CapabilityID)
			execution.MarkCancelled(reason)
			s.scopes.Release(execution.ID)
			publish = func() error {
				return s.bus.Publish(ctx, event.New(event.ExecutionCancelled, execution.ID, execution.CorrelationID, "", event.ExecutionCancelledPayload{
					Message: reason.Error(),
				}))
			}
		default:
			return 0, fmt.Errorf("unknown wait deadline policy %q", policy)
		}
	default:
		return 0, fmt.Errorf("wait timer %s has unknown wait type %q", timer.Key, state.WaitType)
	}

	// The fenced delete is an effect of SaveWaitTransition, so it runs inside
	// the same timer transaction as the snapshot write: either both commit or
	// neither does. An unfenced DeleteWaitTimerOwned call here could remove a
	// timer that another claimant had already taken over.
	effects := executionmodel.WaitEffects{
		DeleteTimers: []executionmodel.WaitTimerDelete{{Key: timer.Key, Owner: owner}},
	}
	if needsIntent {
		attemptID := payload.AttemptID
		if attemptID == "" {
			attemptID = execution.AttemptID
		}
		effects.Intents = append(effects.Intents, executionmodel.NewDispatchIntent(execution.ID, payload.CapabilityID, attemptID))
	}

	result, err := s.waitStore.SaveWaitTransition(ctx, executionmodel.WaitTransitionRequest{
		Execution: execution,
		Effects:   effects,
	})
	if err != nil {
		// A terminal transition has already signalled Done; restoring it would
		// leave a closed completion channel on a non-terminal snapshot. Follow
		// the existing failure-transition pattern and keep the truthful in-memory
		// outcome while logging that it is not durable.
		if !execution.IsTerminal() {
			execution.Restore(before)
		} else {
			slog.Warn("wait deadline transition was not durably committed",
				slog.String("execution_id", string(execution.ID)),
				slog.String("timer", timer.Key),
				slog.String("error", err.Error()),
			)
		}
		if errors.Is(err, executionmodel.ErrWaitTimerLeaseLost) {
			return 0, nil
		}
		return 0, fmt.Errorf("commit due wait timer %s: %w", timer.Key, err)
	}
	if result.Replay {
		return 0, fmt.Errorf("due wait timer %s produced an unexpected delivery replay", timer.Key)
	}
	if err := publish(); err != nil {
		return 1, err
	}
	return 1, nil
}
