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
	core "github.com/neuron-runtime/neuron/shared/types/core"
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

// PollDueTimers processes one bounded batch of due wait timers owned by
// instanceID. It is callable rather than self-scheduling so the host owns
// concurrency, cancellation, and the poll cadence. It returns the number of
// timers whose processing reached a durable commit, plus any per-timer errors
// joined together; one bad timer does not block the rest of the batch.
//
// Discovery is instance-scoped at the store call, so a shared store cannot make
// one instance consume another's timers. Without a wait store the poller is a
// no-op, preserving non-durable behavior.
func (s *Scheduler) PollDueTimers(ctx context.Context, instanceID core.ID) (int, error) {
	if s.waitStore == nil {
		return 0, nil
	}
	if instanceID == "" {
		return 0, fmt.Errorf("instance ID is required for due timer polling")
	}
	due, err := s.waitStore.DueWaitTimersForInstance(ctx, instanceID, time.Now().UTC(), s.pollConfig.BatchSize)
	if err != nil {
		return 0, fmt.Errorf("discover due wait timers for instance %s: %w", instanceID, err)
	}
	processed := 0
	var errs []error
	for _, timer := range due {
		if ctx.Err() != nil {
			break
		}
		n, err := s.processDueTimer(ctx, instanceID, timer)
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
func (s *Scheduler) processDueTimer(ctx context.Context, instanceID core.ID, timer storage.Timer) (int, error) {
	payload, err := executionmodel.DecodeWaitTimer(timer.Data)
	if err != nil {
		return 0, fmt.Errorf("decode wait timer %s: %w", timer.Key, err)
	}
	// The store stamps the instance on every timer it writes through a wait
	// transition. A stamped timer from another instance must never be claimed,
	// even if a stale index entry places it in this instance's candidate set;
	// an unstamped legacy timer remains claimable.
	if payload.InstanceID != "" && payload.InstanceID != instanceID {
		return 0, nil
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
		// The execution is gone, so this timer can never resume anything. Discard
		// it under the claim fence; losing the lease here is a safe abort because
		// the new owner will reach the same conclusion.
		return 0, s.discardClaimedTimer(ctx, timer.Key, owner)
	}
	if err != nil {
		return 0, fmt.Errorf("load execution %s: %w", payload.ExecutionID, err)
	}
	if execution.IsTerminal() {
		// A terminal execution's timers belong to its own transition, so one that
		// is still claimable is stale. Clean it up without writing another snapshot.
		return 0, s.discardClaimedTimer(ctx, timer.Key, owner)
	}

	// One composite transition covers the authoritative state check, mutation,
	// and commit. The event is collected and published only after the guard is
	// released, so a losing driver cannot publish a duplicate.
	type dueTimerOutcome struct {
		processed int
		publish   func() error
	}
	var outcome dueTimerOutcome
	err = execution.RunTransition(func() error {
		state := execution.CapabilityState(payload.CapabilityID)
		if state.Status != executionmodel.CapabilityWaiting && state.Status != executionmodel.CapabilityRetryWait {
			// The capability is no longer waiting, so this timer can never wake it.
			return s.discardClaimedTimer(ctx, timer.Key, owner)
		}

		before := execution.Snapshot()
		var publish func() error
		needsIntent := false
		switch state.Status {
		case executionmodel.CapabilityRetryWait:
			// Live retry resumption belongs to this poller. Only a retry whose
			// next attempt time has elapsed is woken. A timer due by its own
			// record but whose NextAttemptAt is still in the future is the T-043
			// reconciliation case and must be left intact.
			if state.NextAttemptAt == nil {
				return s.discardClaimedTimer(ctx, timer.Key, owner)
			}
			if state.NextAttemptAt.After(now) {
				slog.Warn("due wait timer belongs to a retry whose next attempt is in the future; leaving timer intact",
					slog.String("execution_id", string(payload.ExecutionID)),
					slog.String("capability_id", string(payload.CapabilityID)),
					slog.String("timer", timer.Key),
					slog.Time("next_attempt_at", *state.NextAttemptAt),
				)
				return nil
			}
			if err := execution.ResumeRetryWait(payload.CapabilityID); err != nil {
				return err
			}
			publish = func() error { return s.publishReady(ctx, execution, payload.CapabilityID) }
			needsIntent = true
		case executionmodel.CapabilityWaiting:
			// The snapshot is authoritative about whether a timer is still needed.
			if state.WaitUntil == nil {
				// The wait has no deadline, so no timer should exist; this one is stale.
				return s.discardClaimedTimer(ctx, timer.Key, owner)
			}
			if state.WaitUntil.After(now) {
				// The snapshot still needs a timer for a future deadline, and this
				// entry cannot be proven to be a different, stale one. Deleting it
				// would strip the wait of the timer it still needs, so leave it in
				// place and report the mismatch for reconciliation instead.
				slog.Warn("due wait timer conflicts with a future snapshot deadline; leaving timer intact",
					slog.String("execution_id", string(payload.ExecutionID)),
					slog.String("capability_id", string(payload.CapabilityID)),
					slog.String("timer", timer.Key),
					slog.Time("timer_due", timer.DueAt),
					slog.Time("snapshot_wait_until", *state.WaitUntil),
				)
				return nil
			}
			switch state.WaitType {
			case executionmodel.WaitTypeTimer, executionmodel.WaitTypeDeadline:
				if err := execution.ResolveWait(payload.CapabilityID, nil); err != nil {
					return err
				}
				publish = func() error { return s.publishReady(ctx, execution, payload.CapabilityID) }
				needsIntent = true
			case executionmodel.WaitTypeSignal:
				policy, err := execution.ExpireWait(payload.CapabilityID, now)
				if err != nil {
					return err
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
					return fmt.Errorf("unknown wait deadline policy %q", policy)
				}
			default:
				return fmt.Errorf("wait timer %s has unknown wait type %q", timer.Key, state.WaitType)
			}
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

		result, commitErr := s.waitStore.SaveWaitTransition(ctx, executionmodel.WaitTransitionRequest{
			Execution: execution,
			Effects:   effects,
		})
		if commitErr != nil {
			if isDurableRace(commitErr) {
				// The peer that won holds the newer durable state; never replace
				// it with our pre-mutation snapshot.
				if reloadErr := s.reloadInto(ctx, execution); reloadErr != nil {
					return errors.Join(commitErr, reloadErr)
				}
			} else if !execution.IsTerminal() {
				// A terminal transition has already signalled Done; restoring it
				// would leave a closed completion channel on a non-terminal
				// snapshot. Keep the truthful outcome and log the durability gap.
				execution.Restore(before)
			} else {
				slog.Warn("wait deadline transition was not durably committed",
					slog.String("execution_id", string(execution.ID)),
					slog.String("timer", timer.Key),
					slog.String("error", commitErr.Error()),
				)
			}
			if errors.Is(commitErr, executionmodel.ErrWaitTimerLeaseLost) {
				return s.discardClaimedTimer(ctx, timer.Key, owner)
			}
			return fmt.Errorf("commit due wait timer %s: %w", timer.Key, commitErr)
		}
		if result.Replay {
			if err := s.reloadInto(ctx, execution); err != nil {
				return err
			}
			return fmt.Errorf("due wait timer %s produced an unexpected delivery replay", timer.Key)
		}
		outcome.processed = 1
		outcome.publish = publish
		return nil
	})
	if err != nil {
		return 0, err
	}
	if outcome.publish != nil {
		if err := outcome.publish(); err != nil {
			return outcome.processed, err
		}
	}
	return outcome.processed, nil
}

// discardClaimedTimer removes a timer the poller claimed but can no longer use:
// its execution is missing or terminal, or its capability stopped waiting. The
// delete is fenced by the claim owner so a delayed claimant cannot remove a timer
// another owner has since taken over. Losing that fence is a safe abort — the
// new owner reaches its own decision about the timer — and is not reported as an
// error.
func (s *Scheduler) discardClaimedTimer(ctx context.Context, key, owner string) error {
	if _, err := s.waitStore.DeleteWaitTimerOwned(ctx, key, owner); err != nil {
		return fmt.Errorf("discard claimed wait timer %s: %w", key, err)
	}
	return nil
}
