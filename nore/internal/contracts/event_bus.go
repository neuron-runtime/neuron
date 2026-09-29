package contracts

import (
	"context"

	"github.com/neuron-runtime/neuron/nore/internal/event"
)

type EventBus interface {
	Subscribe(eventType event.Type, buffer int) (event.Subscription, error)
	Publish(ctx context.Context, event event.Event) error
	Close() error
}
