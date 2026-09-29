package event

import (
	"time"

	"github.com/neuron-runtime/neuron/shared/types/core"
)

type Metadata struct {
	EventID       core.ID
	CorrelationID core.ID
	ExecutionID   core.ID
	CapabilityID  core.ID
	OccurredAt    time.Time
}

type Event struct {
	Type     Type
	Metadata Metadata
	Payload  any
}

func New(eventType Type, executionID, correlationID, capabilityID core.ID, payload any) Event {
	return Event{
		Type: eventType,
		Metadata: Metadata{
			EventID:       core.NewID("evt_"),
			ExecutionID:   executionID,
			CorrelationID: correlationID,
			CapabilityID:  capabilityID,
			OccurredAt:    time.Now().UTC(),
		},
		Payload: payload,
	}
}

type ExecutionStartedPayload struct{ Params map[string]any }
type ExecutionCompletedPayload struct {
	// Results carries the aggregate capability results at completion, keyed by
	// capability ID. It is populated at the terminal event so clients rendering
	// the final result of a run do not need a second round-trip.
	Results map[string]map[string]any
}
type ExecutionFailedPayload struct{ Message string }
type CapabilityReadyPayload struct{ Params map[string]any }
type CapabilityStartedPayload struct{}
type CapabilityCompletedPayload struct{ Result map[string]any }
type CapabilityFailedPayload struct{ Message string }
