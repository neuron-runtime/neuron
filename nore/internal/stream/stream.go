// Package stream composes durable event history with the live event bus into a
// single, ordered stream for external consumers (API, CLI, inspector). It is
// the only place that combines event.Store replay with event.Bus subscription.
package stream

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Muhammad-Jay/neuron/nore/internal/event"
	"github.com/Muhammad-Jay/neuron/shared/types/core"
)

// Message is the normalized stream unit. Payload is raw JSON regardless of
// whether the source was a typed live event or persisted history.
type Message struct {
	EventID       core.ID
	Type          event.Type
	CorrelationID core.ID
	CapabilityID     core.ID
	OccurredAt    time.Time
	Payload       json.RawMessage
}

// Subscribe delivers every event for the execution: persisted history first
// (with an optional resume cursor), then live bus events. Events already
// delivered via history are skipped when they also arrive live, so clients
// see each event exactly once. The returned channel is closed when ctx ends
// or the subscription is released.
func Subscribe(ctx context.Context, bus *event.Bus, store *event.Store, executionID core.ID, after core.ID) (<-chan Message, error) {
	if store == nil {
		return nil, fmt.Errorf("event store is required")
	}
	if executionID == "" {
		return nil, fmt.Errorf("execution id is required")
	}

	out := make(chan Message, 256)
	go func() {
		defer close(out)
		live := subscribeLive(ctx, bus, executionID)

		delivered := make(map[core.ID]struct{})
		history, err := store.ListAfter(ctx, executionID, after)
		if err == nil {
			for _, evt := range history {
				delivered[evt.Metadata.EventID] = struct{}{}
				if !emit(ctx, out, normalize(evt)) {
					return
				}
			}
		}

		if live == nil {
			return
		}
		for {
			select {
			case <-ctx.Done():
				return
			case evt, ok := <-live.Events():
				if !ok {
					return
				}
				if _, seen := delivered[evt.Metadata.EventID]; seen {
					continue
				}
				if !emit(ctx, out, normalize(evt)) {
					return
				}
			}
		}
	}()
	return out, nil
}

// subscribeLive registers for the execution's live events and ensures the
// subscription is released when ctx ends, so a disconnected consumer stops
// blocking publishers. Restored instances have no bus and return nil.
func subscribeLive(ctx context.Context, bus *event.Bus, executionID core.ID) event.Subscription {
	if bus == nil {
		return nil
	}
	live, err := bus.SubscribeExecution(executionID, 256)
	if err != nil {
		return nil
	}
	go func() {
		<-ctx.Done()
		_ = live.Close()
	}()
	return live
}

func normalize(evt event.Event) Message {
	return Message{
		EventID:       evt.Metadata.EventID,
		Type:          evt.Type,
		CorrelationID: evt.Metadata.CorrelationID,
		CapabilityID:     evt.Metadata.CapabilityID,
		OccurredAt:    evt.Metadata.OccurredAt,
		Payload:       mustPayload(evt.Payload),
	}
}

// mustPayload converts a typed live payload or a persisted json.RawMessage to
// a uniform raw JSON slice.
func mustPayload(payload any) json.RawMessage {
	data, err := json.Marshal(payload)
	if err != nil {
		return []byte("null")
	}
	return data
}

func emit(ctx context.Context, out chan<- Message, msg Message) bool {
	select {
	case out <- msg:
		return true
	case <-ctx.Done():
		return false
	}
}
