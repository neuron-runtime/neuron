package event

import (
	"time"

	"github.com/Muhammad-Jay/neuron/shared/types/core"
)

type Metadata struct {
	EventID       core.ID
	CorrelationID core.ID
	ExecutionID   core.ID
	ServiceID     core.ID
	OccurredAt    time.Time
}

type Event struct {
	Type     Type
	Metadata Metadata
	Payload  any
}

func New(eventType Type, executionID, correlationID, serviceID core.ID, payload any) Event {
	return Event{
		Type: eventType,
		Metadata: Metadata{
			EventID:       core.NewID("evt_"),
			ExecutionID:   executionID,
			CorrelationID: correlationID,
			ServiceID:     serviceID,
			OccurredAt:    time.Now().UTC(),
		},
		Payload: payload,
	}
}

type ExecutionStartedPayload struct{ Input map[string]any }
type ExecutionCompletedPayload struct {
	// Outputs carries the aggregate service outputs at completion, keyed by
	// service ID. It is populated at the terminal event so clients rendering
	// the final result of a run do not need a second round-trip.
	Outputs map[string]map[string]any
}
type ExecutionFailedPayload struct{ Message string }
type ServiceReadyPayload struct{ Input map[string]any }
type ServiceStartedPayload struct{}
type ServiceCompletedPayload struct{ Output map[string]any }
type ServiceFailedPayload struct{ Message string }
