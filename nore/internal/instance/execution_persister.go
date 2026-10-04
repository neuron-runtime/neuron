package instance

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
	"github.com/neuron-runtime/neuron/nore/internal/event"
)

// stateChangingEventTypes are the event kinds after which an execution
// snapshot must be flushed to the execution store. Log records are excluded:
// they carry no execution state and are already persisted by the event store.
// Retries are excluded for the same reason: a re-attempt does not change which
// capability is running.
//
// A cancellation counts as state-changing. A capability stopped by a sibling's
// failure reaches a terminal capability status through this path, and without a
// flush that status would only ever exist in memory and be lost on restart.
var stateChangingEventTypes = []event.Type{
	event.ExecutionStarted,
	event.CapabilityReady,
	event.CapabilityStarted,
	event.CapabilityCompleted,
	event.CapabilityFailed,
	event.CapabilityCancelled,
	event.CapabilityDetached,
	event.ExecutionCompleted,
	event.ExecutionFailed,
	event.ExecutionCancelled,
}

// executionPersister keeps the durable execution snapshot aligned with the
// in-memory execution by saving it whenever a state-changing event is
// observed. It is the only writer of execution snapshots beyond the initial
// Add; the scheduler and engine remain persistence free.
type executionPersister struct {
	store  contracts.ExecutionRepository
	events event.Subscription
}

func newExecutionPersister(bus *event.Bus, store contracts.ExecutionRepository) (*executionPersister, error) {
	if bus == nil || store == nil {
		return nil, fmt.Errorf("event bus and execution repository are required")
	}
	events, err := bus.Subscribe(event.All, 256)
	if err != nil {
		return nil, err
	}
	return &executionPersister{store: store, events: events}, nil
}

func (p *executionPersister) Run(ctx context.Context) error {
	defer p.events.Close()
	for {
		select {
		case <-ctx.Done():
			return nil
		case received, open := <-p.events.Events():
			if !open {
				return nil
			}
			if !isStateChanging(received.Type) {
				continue
			}
			execution, exists := p.store.Get(received.Metadata.ExecutionID)
			if !exists {
				continue
			}
			if err := p.store.Save(ctx, execution); err != nil {
				slog.Error("persist execution snapshot",
					slog.String("execution_id", string(execution.ID)),
					slog.String("error", err.Error()),
				)
				continue
			}
		}
	}
}

func isStateChanging(eventType event.Type) bool {
	for _, candidate := range stateChangingEventTypes {
		if eventType == candidate {
			return true
		}
	}
	return false
}
