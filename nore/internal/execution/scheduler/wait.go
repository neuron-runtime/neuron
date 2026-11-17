package scheduler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
	"github.com/neuron-runtime/neuron/nore/internal/event"
	executionmodel "github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/storage"
	core "github.com/neuron-runtime/neuron/shared/types/core"
)

// DefaultTimerPollConfig returns the Phase 5 defaults for the due-timer poller:
// a one-second interval and a bounded batch of 256 timers, with a lease long
// enough to survive normal scheduling jitter but short enough to recover a
// crashed claimant promptly.
func DefaultTimerPollConfig() TimerPollConfig {
	return TimerPollConfig{
		Interval:  defaultTimerPollInterval,
		BatchSize: defaultTimerPollBatchSize,
		Lease:     defaultTimerPollLease,
	}
}

// TimerPollConfig bounds the scheduler's due-timer consumer. Every field is a
// hard resource bound: the loop cadence, the maximum work per pass, and the
// fencing lease a claimant holds while committing. Zero values receive the
// Phase 5 defaults; missing values cannot disable a bound.
type TimerPollConfig struct {
	Interval  time.Duration
	BatchSize int
	Lease     time.Duration
}

const (
	defaultTimerPollInterval  = time.Second
	defaultTimerPollBatchSize = 256
	defaultTimerPollLease     = 30 * time.Second

	maxTimerPollInterval  = time.Minute
	maxTimerPollBatchSize = 4096
	minTimerPollLease     = 5 * time.Second
	maxTimerPollLease     = 5 * time.Minute
)

// validated fills defaults and rejects values that would make polling either
// ineffective (zero batch, sub-clock-margin lease) or unbounded.
func (c TimerPollConfig) validated() (TimerPollConfig, error) {
	if c.Interval == 0 {
		c.Interval = defaultTimerPollInterval
	}
	if c.BatchSize == 0 {
		c.BatchSize = defaultTimerPollBatchSize
	}
	if c.Lease == 0 {
		c.Lease = defaultTimerPollLease
	}
	if c.Interval < 0 || c.Interval > maxTimerPollInterval {
		return c, fmt.Errorf("invalid wait timer poll interval %s", c.Interval)
	}
	if c.BatchSize < 0 || c.BatchSize > maxTimerPollBatchSize {
		return c, fmt.Errorf("invalid wait timer poll batch size %d", c.BatchSize)
	}
	if c.Lease < minTimerPollLease || c.Lease > maxTimerPollLease {
		return c, fmt.Errorf("invalid wait timer lease %s: must be between %s and %s", c.Lease, minTimerPollLease, maxTimerPollLease)
	}
	return c, nil
}

// WaitStore is the durable surface the scheduler needs for waits. It combines
// the atomic transition boundary with the discovery and reload reads a due-timer
// consumer cannot implement from the transition write alone. This is a local
// interface so the shared contracts package does not have to grow a method for
// every storage backend capability.
type WaitStore interface {
	contracts.WaitCommitter
	DueWaitTimers(ctx context.Context, now time.Time, limit int) ([]storage.Timer, error)
	GetSignalDelivery(ctx context.Context, executionID core.ID, deliveryID string) (*executionmodel.SignalDelivery, error)
	Reload(ctx context.Context, executionID core.ID) (*executionmodel.Execution, error)
}

// ErrWaitStoreUnavailable is returned by durable wait operations when the
// scheduler was built without WithWaitStore. Non-durable mode keeps its original
// event-driven behavior and must not silently start persisting waits.
var ErrWaitStoreUnavailable = errors.New("scheduler: durable wait store is not configured")

// DispatchPublishError reports that a dispatch transition was committed but its
// CapabilityReady event could not be published. The durable dispatch intent is
// the source of truth, so the work is not lost; a reconciler must re-announce it.
type DispatchPublishError struct {
	ExecutionID  core.ID
	CapabilityID core.ID
	Err          error
}

func (e *DispatchPublishError) Error() string {
	return fmt.Sprintf("scheduler: dispatch of %s/%s was committed but CapabilityReady could not be published: %v", e.ExecutionID, e.CapabilityID, e.Err)
}

func (e *DispatchPublishError) Unwrap() error { return e.Err }

// WaitEntryRequest describes one capability suspending into a durable wait.
// AttemptID may be empty; it then defaults to the execution's current attempt.
type WaitEntryRequest struct {
	Execution    *executionmodel.Execution
	CapabilityID core.ID
	AttemptID    core.ID
	Spec         executionmodel.WaitSpec
}

// SignalResolutionRequest describes one external delivery resolving a signal
// wait. DeliveryID is the idempotency key: repeating it with the same payload is
// a replay, while repeating it with a different payload is a conflict.
type SignalResolutionRequest struct {
	Execution      *executionmodel.Execution
	CapabilityID   core.ID
	AttemptID      core.ID
	DeliveryID     string
	SignalName     string
	CorrelationKey string
	Payload        map[string]any
}

// SignalResolution reports the delivery record committed or found on replay.
type SignalResolution struct {
	Delivery *executionmodel.SignalDelivery
	Replay   bool
}

// EnterWait records a capability's transition into waiting durably before it is
// observable. A wait with a deadline writes a timer; a wait without one writes
// no timer and can only be resumed by a delivery. No event is published: a
// waiting capability has no ready work to announce, and its worker slot has
// already been released by the domain transition.
func (s *Scheduler) EnterWait(ctx context.Context, req WaitEntryRequest) error {
	if s.waitStore == nil {
		return ErrWaitStoreUnavailable
	}
	if req.Execution == nil {
		return fmt.Errorf("execution is required")
	}
	if req.CapabilityID == "" {
		return fmt.Errorf("capability ID is required")
	}
	attemptID := req.AttemptID
	if attemptID == "" {
		attemptID = req.Execution.AttemptID
	}

	effects := executionmodel.WaitEffects{}
	if req.Spec.WaitUntil != nil {
		if attemptID == "" {
			return fmt.Errorf("wait timer requires an attempt ID for capability %s", req.CapabilityID)
		}
		key, err := executionmodel.WaitTimerKey(req.Execution.ID, req.CapabilityID, attemptID)
		if err != nil {
			return err
		}
		encoded, err := executionmodel.EncodeWaitTimer(executionmodel.WaitTimerPayload{
			ExecutionID:  req.Execution.ID,
			CapabilityID: req.CapabilityID,
			WaitType:     req.Spec.Type,
			AttemptID:    attemptID,
		})
		if err != nil {
			return err
		}
		effects.PutTimers = append(effects.PutTimers, executionmodel.WaitTimerWrite{
			Key:   key,
			DueAt: req.Spec.WaitUntil.UTC(),
			Data:  encoded,
		})
	}

	// Build all effects before mutating so an invalid key or payload cannot
	// leave the live execution waiting without a durable record.
	before := req.Execution.Snapshot()
	if err := req.Execution.EnterWait(req.CapabilityID, req.Spec); err != nil {
		return err
	}
	if _, err := s.waitStore.SaveWaitTransition(ctx, executionmodel.WaitTransitionRequest{
		Execution: req.Execution,
		Effects:   effects,
	}); err != nil {
		req.Execution.Restore(before)
		return fmt.Errorf("persist wait for capability %s: %w", req.CapabilityID, err)
	}
	return nil
}

// ResolveSignal accepts one signal delivery for a waiting capability. The
// state, delivery record, deadline-timer cleanup, and dispatch intent are
// committed as one transition; CapabilityReady is published only afterwards.
//
// A delivery already recorded with the same payload is an idempotent replay: it
// writes nothing and restores the caller's execution from durable state before
// returning, so no further transition can be attempted on a stale mutation.
func (s *Scheduler) ResolveSignal(ctx context.Context, req SignalResolutionRequest) (*SignalResolution, error) {
	if s.waitStore == nil {
		return nil, ErrWaitStoreUnavailable
	}
	if req.Execution == nil {
		return nil, fmt.Errorf("execution is required")
	}
	delivery, err := executionmodel.NewSignalDelivery(
		req.Execution.ID, req.CapabilityID, req.DeliveryID, req.SignalName, req.CorrelationKey, req.Payload,
	)
	if err != nil {
		return nil, err
	}

	existing, err := s.waitStore.GetSignalDelivery(ctx, req.Execution.ID, req.DeliveryID)
	if err == nil {
		if existing.PayloadHash != delivery.PayloadHash {
			return nil, executionmodel.ErrSignalDeliveryConflict
		}
		if err := s.reloadInto(ctx, req.Execution); err != nil {
			return nil, err
		}
		return &SignalResolution{Replay: true, Delivery: existing}, nil
	}
	if !errors.Is(err, storage.ErrNotFound) {
		return nil, fmt.Errorf("read signal delivery %s: %w", req.DeliveryID, err)
	}

	state := req.Execution.CapabilityState(req.CapabilityID)
	if state.Status != executionmodel.CapabilityWaiting {
		return nil, fmt.Errorf("capability %s is not waiting, status is %s", req.CapabilityID, state.Status)
	}
	if state.WaitType != executionmodel.WaitTypeSignal {
		return nil, fmt.Errorf("capability %s is waiting for %s, not a signal", req.CapabilityID, state.WaitType)
	}
	if state.ExpectedSignal != req.SignalName {
		return nil, fmt.Errorf("capability %s expects signal %q, received %q", req.CapabilityID, state.ExpectedSignal, req.SignalName)
	}

	before := req.Execution.Snapshot()
	if err := req.Execution.ResolveWait(req.CapabilityID, req.Payload); err != nil {
		return nil, err
	}
	attemptID := req.AttemptID
	if attemptID == "" {
		attemptID = req.Execution.AttemptID
	}
	effects := executionmodel.WaitEffects{Delivery: delivery}
	if attemptID != "" {
		key, err := executionmodel.WaitTimerKey(req.Execution.ID, req.CapabilityID, attemptID)
		if err != nil {
			req.Execution.Restore(before)
			return nil, err
		}
		// Best-effort cleanup of a signal wait's optional deadline timer. The
		// store's unfenced delete is still part of this same transaction.
		effects.DeleteTimers = append(effects.DeleteTimers, executionmodel.WaitTimerDelete{Key: key})
	}
	effects.Intents = append(effects.Intents, executionmodel.NewDispatchIntent(req.Execution.ID, req.CapabilityID, attemptID))

	result, err := s.waitStore.SaveWaitTransition(ctx, executionmodel.WaitTransitionRequest{
		Execution: req.Execution,
		Effects:   effects,
	})
	if err != nil {
		req.Execution.Restore(before)
		return nil, err
	}
	if result.Replay {
		// A concurrent duplicate won the race inside SaveWaitTransition. The
		// delivery is already durable; our mutation wrote nothing.
		if err := s.reloadInto(ctx, req.Execution); err != nil {
			return nil, err
		}
		return &SignalResolution{Replay: true, Delivery: result.Delivery}, nil
	}
	if err := s.publishReady(ctx, req.Execution, req.CapabilityID); err != nil {
		return nil, err
	}
	return &SignalResolution{Delivery: result.Delivery}, nil
}

// reloadInto replaces the caller's state with the durable snapshot after a
// replay. Replays write nothing, so the caller's optimistic mutation must not
// survive into a later transition.
func (s *Scheduler) reloadInto(ctx context.Context, execution *executionmodel.Execution) error {
	fresh, err := s.waitStore.Reload(ctx, execution.ID)
	if err != nil {
		return fmt.Errorf("reload execution %s after wait replay: %w", execution.ID, err)
	}
	execution.Restore(fresh.Snapshot())
	return nil
}

// publishReady announces committed ready work. A publish failure is a
// reconcile condition, not a rollback: the intent was already committed.
func (s *Scheduler) publishReady(ctx context.Context, execution *executionmodel.Execution, capabilityID core.ID) error {
	err := s.bus.Publish(ctx, event.New(
		event.CapabilityReady,
		execution.ID,
		execution.CorrelationID,
		capabilityID,
		event.CapabilityReadyPayload{Params: execution.Params(capabilityID)},
	))
	if err != nil {
		return &DispatchPublishError{ExecutionID: execution.ID, CapabilityID: capabilityID, Err: err}
	}
	return nil
}
