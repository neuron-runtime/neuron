package instance

import (
	"context"
	"errors"
	"fmt"

	"github.com/neuron-runtime/neuron/nore/internal/execution/scheduler"
	"github.com/neuron-runtime/neuron/nore/internal/storage"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

// SignalDeliveryRequest is the domain-level signal delivery accepted by an
// Instance. It deliberately carries no scheduler or transport types so API
// handlers depend only on this boundary.
type SignalDeliveryRequest struct {
	ExecutionID    shared.ID
	DeliveryID     string
	SignalName     string
	CorrelationKey string
	Payload        map[string]any
}

// SignalDeliveryResult is the outcome surfaced to the caller. Replay means the
// delivery had already been accepted with the same payload and nothing changed.
type SignalDeliveryResult struct {
	DeliveryID string
	Replay     bool
}

// DeliverSignal delivers a signal to a capability waiting inside one of this
// instance's executions.
//
// Ownership comes from the durable execution record, not from a caller-supplied
// path: the execution's persisted InstanceID must match this instance, and a
// mismatch is reported identically to a missing execution so cross-instance
// delivery cannot probe execution existence. Once ownership is established the
// instance delegates to its scheduler, which owns wait lookup, replay, the
// atomic transition, and publication.
func (i *Instance) DeliverSignal(ctx context.Context, req SignalDeliveryRequest) (*SignalDeliveryResult, error) {
	if req.ExecutionID == "" || req.DeliveryID == "" || req.SignalName == "" {
		return nil, fmt.Errorf("execution ID, delivery ID, and signal name are required")
	}
	exec, err := i.store.Get(ctx, req.ExecutionID)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, fmt.Errorf("%w: %s", ErrExecutionNotFound, req.ExecutionID)
	}
	if err != nil {
		return nil, fmt.Errorf("load execution %s: %w", req.ExecutionID, err)
	}
	if exec.InstanceID != shared.ID(i.ID) {
		return nil, fmt.Errorf("%w: %s", ErrExecutionNotFound, req.ExecutionID)
	}
	if i.scheduler == nil {
		return nil, fmt.Errorf("instance %s has no scheduler; it cannot deliver signals", i.ID)
	}

	result, err := i.scheduler.DeliverSignal(ctx, scheduler.SignalDeliveryRequest{
		Execution:      exec,
		DeliveryID:     req.DeliveryID,
		SignalName:     req.SignalName,
		CorrelationKey: req.CorrelationKey,
		Payload:        req.Payload,
	})
	if err != nil {
		return nil, err
	}
	return &SignalDeliveryResult{DeliveryID: req.DeliveryID, Replay: result.Replay}, nil
}
