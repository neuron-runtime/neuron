package event

import (
	"fmt"
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

// ExecutionCancelledPayload carries why an execution was cancelled. The message
// is empty when the cancellation needs no explanation, so a client must treat it
// as optional rather than as a missing value.
type ExecutionCancelledPayload struct{ Message string }

type CapabilityReadyPayload struct{ Params map[string]any }
type CapabilityStartedPayload struct{}
type CapabilityCompletedPayload struct{ Result map[string]any }
type CapabilityFailedPayload struct{ Message string }

// CapabilityFailedMessage extracts the failure message carried by a
// capability-failed payload, reporting whether one was present.
//
// Both the value and the pointer form are accepted. A payload travels through an
// `any` field, so either form is a legitimate thing for a producer to publish,
// and a caller that asserts only one of them silently discards the other's
// message and reports a generic failure instead. That hides the actual cause
// from whoever has to diagnose the run, which is the opposite of what a failure
// message is for.
//
// Callers keep their own fallback policy for the not-present case: the scheduler
// substitutes its generic text, while analytics reports whatever it received.
// The extraction lives here so the two cannot disagree about what a payload means.
func CapabilityFailedMessage(payload any) (string, bool) {
	switch p := payload.(type) {
	case CapabilityFailedPayload:
		return p.Message, true
	case *CapabilityFailedPayload:
		if p == nil {
			return "", false
		}
		return p.Message, true
	case string:
		return p, true
	case fmt.Stringer:
		return p.String(), true
	default:
		return "", false
	}
}

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
