package analytics

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
	"github.com/neuron-runtime/neuron/nore/internal/event"
)

type Analytics struct {
	bus    contracts.EventBus
	logger *slog.Logger

	executionStarted    event.Subscription
	executionFailed     event.Subscription
	capabilityCompleted event.Subscription
	capabilityFailed    event.Subscription
}

func New(bus contracts.EventBus, logger *slog.Logger) (*Analytics, error) {
	if bus == nil {
		return nil, fmt.Errorf("event bus is required")
	}
	if logger == nil {
		logger = slog.Default()
	}

	started, err := bus.Subscribe(event.ExecutionStarted, 64)
	if err != nil {
		return nil, err
	}

	failed, err := bus.Subscribe(event.ExecutionFailed, 64)
	if err != nil {
		_ = started.Close()
		return nil, err
	}

	completed, err := bus.Subscribe(event.CapabilityCompleted, 64)
	if err != nil {
		_ = started.Close()
		_ = failed.Close()
		return nil, err
	}

	svcFailed, err := bus.Subscribe(event.CapabilityFailed, 64)
	if err != nil {
		_ = started.Close()
		_ = failed.Close()
		_ = completed.Close()
		return nil, err
	}

	return &Analytics{
		bus:                 bus,
		logger:              logger,
		executionStarted:    started,
		executionFailed:     failed,
		capabilityCompleted: completed,
		capabilityFailed:    svcFailed,
	}, nil
}

func (a *Analytics) Serve(ctx context.Context) error {
	defer a.closeSubscriptions()

	for {
		select {
		case <-ctx.Done():
			return nil
		case evt, open := <-a.executionStarted.Events():
			if !open {
				return nil
			}
			a.logger.Info("Execution started",
				slog.String("execution_id", string(evt.Metadata.ExecutionID)),
				slog.String("correlation_id", string(evt.Metadata.CorrelationID)),
				slog.Time("occurred_at", evt.Metadata.OccurredAt),
			)
		case evt, open := <-a.executionFailed.Events():
			if !open {
				return nil
			}
			var msg string
			switch p := evt.Payload.(type) {
			case event.ExecutionFailedPayload:
				msg = p.Message
			case string:
				msg = p
			case fmt.Stringer:
				msg = p.String()
			default:
				msg = fmt.Sprintf("%v", evt.Payload)
			}
			a.logger.Info("Execution failed",
				slog.String("execution_id", string(evt.Metadata.ExecutionID)),
				slog.String("correlation_id", string(evt.Metadata.CorrelationID)),
				slog.String("message", msg),
			)
		case evt, open := <-a.capabilityCompleted.Events():
			if !open {
				return nil
			}
			a.logger.Info("Capability completed",
				slog.String("execution_id", string(evt.Metadata.ExecutionID)),
				slog.String("capability_id", string(evt.Metadata.CapabilityID)),
			)
		case evt, open := <-a.capabilityFailed.Events():
			if !open {
				return nil
			}
			var msg string
			if message, ok := event.CapabilityFailedMessage(evt.Payload); ok {
				msg = message
			} else {
				msg = fmt.Sprintf("%v", evt.Payload)
			}
			a.logger.Error("Capability failed",
				slog.String("execution_id", string(evt.Metadata.ExecutionID)),
				slog.String("capability_id", string(evt.Metadata.CapabilityID)),
				slog.String("message", msg),
			)
		}
	}
}

func (a *Analytics) closeSubscriptions() {
	_ = a.executionStarted.Close()
	_ = a.executionFailed.Close()
	_ = a.capabilityCompleted.Close()
	_ = a.capabilityFailed.Close()
}
