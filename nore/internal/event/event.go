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

// CapabilityDetachedPayload records a lifecycle boundary, so it carries no data
// of its own. The capability ID on the event identifies the boundary, and the
// task that now owns the work is reachable from the parent execution through the
// execution store.
type CapabilityDetachedPayload struct{}

// CapabilityRetryPayload reports a scheduled re-attempt of a capability
// invocation so an observer can see that a capability is still being worked on
// rather than stalled.
type CapabilityRetryPayload struct {
	// Attempt is the attempt that just failed.
	Attempt int

	// NextAttempt is the attempt about to be made.
	NextAttempt int

	// Delay is how long N.O.R.E. waited before NextAttempt.
	Delay string

	// Message is the failure that caused the retry.
	Message string
}
