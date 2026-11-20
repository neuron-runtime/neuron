package scheduler

import (
	"context"
	"errors"
	"fmt"
	"time"

	executionmodel "github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/storage"
	core "github.com/neuron-runtime/neuron/shared/types/core"
)

// Wait-lookup failures are typed so the API layer can map each case to its own
// status without inspecting strings or knowing scheduler internals. They are
// distinct from the delivery-id conflict, which lives in the execution model.
var (
	// ErrSignalNoMatchingWait reports that no persisted wait expects this signal.
	ErrSignalNoMatchingWait = errors.New("scheduler: no waiting capability matches the signal")
	// ErrSignalAmbiguousWait reports that more than one persisted wait expects
	// this signal. The caller must narrow the delivery with a correlation key.
	ErrSignalAmbiguousWait = errors.New("scheduler: multiple waiting capabilities match the signal")
	// ErrSignalWaitExpired reports that a matching wait's deadline has passed;
	// the wait can no longer be resolved by a delivery.
	ErrSignalWaitExpired = errors.New("scheduler: wait deadline has already passed")
	// ErrSignalWaitTerminal reports a delivery to an execution that already
	// reached a terminal state.
	ErrSignalWaitTerminal = errors.New("scheduler: execution is terminal")
	// ErrSignalInvalidPayload reports a payload that failed the wait's schema.
	ErrSignalInvalidPayload = errors.New("scheduler: signal payload is invalid")
)

// SignalDeliveryRequest describes one external delivery before the matching
// wait has been resolved. Execution is the live or reloaded execution carrying
// persisted wait state; the scheduler never traverses the blueprint to find a
// match. Now, when non-zero, supplies the clock for deadline comparisons so
// callers can test expiry deterministically.
type SignalDeliveryRequest struct {
	Execution      *executionmodel.Execution
	DeliveryID     string
	SignalName     string
	CorrelationKey string
	Payload        map[string]any
	Now            time.Time
}

// SignalDeliveryResult is the outcome of one delivery. Replay reports that the
// delivery ID was already accepted with the same payload and that no transition
// was committed.
type SignalDeliveryResult struct {
	Delivery *executionmodel.SignalDelivery
	Replay   bool
}

// DeliverSignal accepts one external signal delivery for an execution.
//
// It first checks the durable delivery record by delivery ID and payload hash:
// a different payload for a known ID is a conflict and an identical one is a
// replay that writes nothing. For a new delivery it resolves the single waiting
// capability from persisted wait state (WaitingCapabilities/CapabilityState),
// validates the payload against that wait's schema, and delegates the atomic
// transition to ResolveSignal with the resolved capability ID. ResolveSignal
// remains the authority for commit-before-publish; this method adds lookup and
// validation but never performs its own state transition.
func (s *Scheduler) DeliverSignal(ctx context.Context, req SignalDeliveryRequest) (*SignalDeliveryResult, error) {
	if s.waitStore == nil {
		return nil, ErrWaitStoreUnavailable
	}
	if req.Execution == nil {
		return nil, fmt.Errorf("execution is required")
	}
	if req.DeliveryID == "" {
		return nil, fmt.Errorf("delivery id is required")
	}
	if req.SignalName == "" {
		return nil, fmt.Errorf("signal name is required")
	}

	hash, err := executionmodel.SignalPayloadHash(req.Payload)
	if err != nil {
		return nil, err
	}

	// Replay/conflict first: a repeated delivery may target a wait that has
	// since been resolved, so it must be recognized before any wait lookup.
	existing, err := s.waitStore.GetSignalDelivery(ctx, req.Execution.ID, req.DeliveryID)
	if err == nil {
		if existing.PayloadHash != hash {
			return nil, executionmodel.ErrSignalDeliveryConflict
		}
		// A replay writes nothing. Restore durable state so no stale mutation in
		// the caller's execution can survive into a later transition.
		if err := s.reloadInto(ctx, req.Execution); err != nil {
			return nil, err
		}
		return &SignalDeliveryResult{Delivery: existing, Replay: true}, nil
	}
	if !errors.Is(err, storage.ErrNotFound) {
		return nil, fmt.Errorf("read signal delivery %s: %w", req.DeliveryID, err)
	}

	if req.Execution.IsTerminal() {
		return nil, fmt.Errorf("%w: %s", ErrSignalWaitTerminal, req.Execution.ID)
	}

	capabilityID, wait, err := resolveSignalWait(req.Execution, req.SignalName, req.CorrelationKey, req.Now)
	if err != nil {
		return nil, err
	}
	if err := executionmodel.ValidateSignalPayload(wait.PayloadSchema, req.Payload); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSignalInvalidPayload, err)
	}

	resolution, err := s.ResolveSignal(ctx, SignalResolutionRequest{
		Execution:      req.Execution,
		CapabilityID:   capabilityID,
		DeliveryID:     req.DeliveryID,
		SignalName:     req.SignalName,
		CorrelationKey: req.CorrelationKey,
		Payload:        req.Payload,
	})
	if err != nil {
		return nil, err
	}
	return &SignalDeliveryResult{Delivery: resolution.Delivery, Replay: resolution.Replay}, nil
}

// resolveSignalWait finds the single capability whose persisted wait expects
// signalName. It reads wait state from the execution's capability states, never
// from the blueprint, so metadata-only restored executions resolve correctly.
func resolveSignalWait(execution *executionmodel.Execution, signalName, correlationKey string, now time.Time) (id core.ID, state executionmodel.CapabilityExecutionState, err error) {
	matches := 0
	for _, capabilityID := range execution.WaitingCapabilities() {
		candidate := execution.CapabilityState(capabilityID)
		if candidate.WaitType != executionmodel.WaitTypeSignal {
			continue
		}
		if candidate.ExpectedSignal != signalName {
			continue
		}
		if correlationKey != "" && candidate.CorrelationKey != correlationKey {
			continue
		}
		matches++
		id = capabilityID
		state = candidate
	}
	switch {
	case matches == 0:
		return "", state, fmt.Errorf("%w: %s", ErrSignalNoMatchingWait, signalName)
	case matches > 1:
		return "", state, fmt.Errorf("%w: %s", ErrSignalAmbiguousWait, signalName)
	}
	if state.WaitUntil != nil {
		if now.IsZero() {
			now = time.Now()
		}
		if !state.WaitUntil.After(now) {
			return "", state, fmt.Errorf("%w: %s", ErrSignalWaitExpired, id)
		}
	}
	return id, state, nil
}
