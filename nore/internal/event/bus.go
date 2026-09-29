package event

import (
	"context"
	"errors"
	"sync"

	"github.com/neuron-runtime/neuron/shared/types/core"
)

var ErrBusClosed = errors.New("event bus is closed")

type Subscription interface {
	Events() <-chan Event
	Close() error
}

// subscription routes events to a single consumer. It is registered either
// by event type (subscriptions[eventType]) or by execution ID
// (byExecution[executionID]); executionID is empty for type subscriptions.
type subscription struct {
	id          uint64
	eventType   Type
	executionID core.ID
	channel     chan Event
	bus         *Bus
	mu          sync.RWMutex
	closed      bool
}

func (s *subscription) Events() <-chan Event { return s.channel }
func (s *subscription) Close() error         { return s.bus.unsubscribe(s) }

func (s *subscription) deliver(ctx context.Context, event Event) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil
	}
	select {
	case s.channel <- event:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *subscription) closeLocal() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	close(s.channel)
}

type Bus struct {
	mu                 sync.RWMutex
	nextSubscriptionID uint64
	subscriptions      map[Type]map[uint64]*subscription
	byExecution        map[core.ID]map[uint64]*subscription
	closed             bool
}

func NewBus() *Bus {
	return &Bus{
		subscriptions: make(map[Type]map[uint64]*subscription),
		byExecution:   make(map[core.ID]map[uint64]*subscription),
	}
}

func (b *Bus) Subscribe(eventType Type, buffer int) (Subscription, error) {
	sub := newSubscription(buffer)
	sub.eventType = eventType
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, ErrBusClosed
	}
	b.nextSubscriptionID++
	sub.id = b.nextSubscriptionID
	sub.bus = b
	if b.subscriptions[eventType] == nil {
		b.subscriptions[eventType] = make(map[uint64]*subscription)
	}
	b.subscriptions[eventType][sub.id] = sub
	return sub, nil
}

// SubscribeExecution delivers every event belonging to the given execution ID.
func (b *Bus) SubscribeExecution(executionID core.ID, buffer int) (Subscription, error) {
	if executionID == "" {
		return nil, errors.New("execution id is required")
	}
	sub := newSubscription(buffer)
	sub.executionID = executionID
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, ErrBusClosed
	}
	b.nextSubscriptionID++
	sub.id = b.nextSubscriptionID
	sub.bus = b
	if b.byExecution[executionID] == nil {
		b.byExecution[executionID] = make(map[uint64]*subscription)
	}
	b.byExecution[executionID][sub.id] = sub
	return sub, nil
}

func newSubscription(buffer int) *subscription {
	if buffer <= 0 {
		buffer = 64
	}
	return &subscription{channel: make(chan Event, buffer)}
}

func (b *Bus) Publish(ctx context.Context, event Event) error {
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return ErrBusClosed
	}
	current := b.subscriptions[event.Type]
	all := b.subscriptions[All]
	execution := b.byExecution[event.Metadata.ExecutionID]
	subscribers := make([]*subscription, 0, len(current)+len(all)+len(execution))
	for _, sub := range current {
		subscribers = append(subscribers, sub)
	}
	for _, sub := range all {
		subscribers = append(subscribers, sub)
	}
	for _, sub := range execution {
		subscribers = append(subscribers, sub)
	}
	b.mu.RUnlock()
	for _, sub := range subscribers {
		if err := sub.deliver(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func (b *Bus) unsubscribe(sub *subscription) error {
	b.mu.Lock()
	if !b.closed {
		if sub.executionID != "" {
			current := b.byExecution[sub.executionID]
			delete(current, sub.id)
			if len(current) == 0 {
				delete(b.byExecution, sub.executionID)
			}
		} else {
			current := b.subscriptions[sub.eventType]
			delete(current, sub.id)
			if len(current) == 0 {
				delete(b.subscriptions, sub.eventType)
			}
		}
	}
	b.mu.Unlock()
	sub.closeLocal()
	return nil
}

func (b *Bus) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	var subscribers []*subscription
	for _, current := range b.subscriptions {
		for _, sub := range current {
			subscribers = append(subscribers, sub)
		}
	}
	for _, current := range b.byExecution {
		for _, sub := range current {
			subscribers = append(subscribers, sub)
		}
	}
	b.subscriptions = nil
	b.byExecution = nil
	b.mu.Unlock()
	for _, sub := range subscribers {
		sub.closeLocal()
	}
	return nil
}
