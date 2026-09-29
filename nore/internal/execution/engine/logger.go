package engine

import (
	"context"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
	"github.com/neuron-runtime/neuron/nore/internal/event"
	"github.com/neuron-runtime/neuron/shared/types/core"
)

// execLogger is a contracts.Logger that turns log calls into CapabilityLog events
// published on the execution's event bus. Every log is therefore persisted by
// the event store and observable through the event stream alongside lifecycle
// events. Publishing is intentionally fire-and-forget: a log failure must not
// fail the capability execution that produced it.
type execLogger struct {
	bus           contracts.EventBus
	executionID   core.ID
	correlationID core.ID
	capabilityID  core.ID
}

func newExecLogger(bus contracts.EventBus, executionID, correlationID, capabilityID core.ID) *execLogger {
	return &execLogger{
		bus:           bus,
		executionID:   executionID,
		correlationID: correlationID,
		capabilityID:  capabilityID,
	}
}

func (l *execLogger) Debug(ctx context.Context, message string, fields ...contracts.Field) {
	l.emit(ctx, event.LogDebug, message, fields)
}

func (l *execLogger) Info(ctx context.Context, message string, fields ...contracts.Field) {
	l.emit(ctx, event.LogInfo, message, fields)
}

func (l *execLogger) Warn(ctx context.Context, message string, fields ...contracts.Field) {
	l.emit(ctx, event.LogWarn, message, fields)
}

func (l *execLogger) Error(ctx context.Context, message string, fields ...contracts.Field) {
	l.emit(ctx, event.LogError, message, fields)
}

func (l *execLogger) emit(ctx context.Context, level event.LogLevel, message string, fields []contracts.Field) {
	if l.bus == nil {
		return
	}
	payload := event.LogPayload{Level: level, Message: message, NodeID: l.capabilityID}
	if len(fields) > 0 {
		evFields := make([]event.Field, len(fields))
		for i, f := range fields {
			evFields[i] = event.Field{Key: f.Key, Value: f.Value}
		}
		payload.Fields = evFields
	}
	_ = l.bus.Publish(ctx, event.New(event.CapabilityLog, l.executionID, l.correlationID, l.capabilityID, payload))
}
