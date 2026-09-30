// Package stream composes durable event history with the live event bus into a
// single, ordered stream for external consumers (API, CLI, inspector). It is
// the only place that combines event.Store replay with event.Bus subscription.
package stream

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/event"
	"github.com/neuron-runtime/neuron/shared/types/core"
)

// Message is the normalized stream unit. Payload is raw JSON regardless of
// whether the source was a typed live event or persisted history.
type Message struct {
	EventID       core.ID
	Type          event.Type
	CorrelationID core.ID
	CapabilityID  core.ID
	OccurredAt    time.Time
	Payload       json.RawMessage
}

// reconcileInterval is how often a live stream re-reads persisted history while
// it waits for events.
const reconcileInterval = 25 * time.Millisecond

// Subscribe delivers every event for the execution: persisted history first
// (with an optional resume cursor), then live bus events. Events already
// delivered are skipped when they arrive again from the other source, so
// clients see each event exactly once. The returned channel is closed when ctx
// ends or the subscription is released.
//
// History is not a one-time snapshot. The bus hands an event to the persister
// and to live subscribers independently, so an event published just before a
// consumer subscribes can still be missing from the store when the first
// history read happens. That event is then visible to neither the subscription
// nor the snapshot, and a consumer waiting for the terminal event waits
// forever. The stream therefore reconciles with the store while it is live
// rather than trusting the initial snapshot.
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
		if live != nil {
			defer func() { _ = live.Close() }()
		}

		cursor := after
		delivered := make(map[core.ID]struct{})

		// emitOnce delivers evt unless it was already delivered, and reports
		// whether the stream may continue.
		emitOnce := func(evt event.Event) bool {
			if _, seen := delivered[evt.Metadata.EventID]; seen {
				return true
			}
			delivered[evt.Metadata.EventID] = struct{}{}
			return emit(ctx, out, normalize(evt))
		}

		// reconcile emits everything the store has gained since the last read.
		// A failed read is not fatal: live events are still delivered, and the
		// next read retries.
		reconcile := func() bool {
			history, err := store.ListAfter(ctx, executionID, cursor)
			if err != nil {
				return true
			}
			for _, evt := range history {
				if !emitOnce(evt) {
					return false
				}
				cursor = evt.Metadata.EventID
			}
			return true
		}

		if !reconcile() {
			return
		}
		if live == nil {
			return
		}

		tick := time.NewTicker(reconcileInterval)
		defer tick.Stop()
		settled := false

		for {
			select {
			case <-ctx.Done():
				return
			case evt, ok := <-live.Events():
				if !ok {
					return
				}
				if !emitOnce(evt) {
					return
				}
				settled = settled || evt.Type.IsTerminal()
				if !reconcile() {
					return
				}
			case <-tick.C:
				// Once the terminal event is out there is nothing left to wait
				// for, so stop polling the store for a finished execution.
				if settled {
					continue
				}
				if !reconcile() {
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
		CapabilityID:  evt.Metadata.CapabilityID,
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
