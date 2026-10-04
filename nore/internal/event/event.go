package event

import (
	"fmt"
	"reflect"
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

// CapabilityFailedPayload carries the reason a capability failed on its own
// account. It is distinct from CapabilityCancelledPayload because the two mean
// opposite things to whoever reacts to them: one is a fault to investigate, the
// other is work that was stopped for a reason already accounted for.
type CapabilityFailedPayload struct{ Message string }

// CapabilityCancelledPayload explains why a capability stopped without reaching
// an outcome of its own. The message names the cause that ended it, not a fault
// in the capability.
type CapabilityCancelledPayload struct{ Message string }

// reasoner is implemented by every payload that carries a human-readable reason.
//
// The accessors use value receivers deliberately. That makes both a payload and
// a pointer to one satisfy this interface, so a payload travelling through an
// `any` field is read identically whichever form a producer published it in --
// which is the whole reason Message exists.
type reasoner interface{ Reason() string }

func (p CapabilityFailedPayload) Reason() string    { return p.Message }
func (p CapabilityCancelledPayload) Reason() string { return p.Message }
func (p ExecutionFailedPayload) Reason() string     { return p.Message }
func (p ExecutionCancelledPayload) Reason() string  { return p.Message }

// Message extracts the human-readable reason from an event payload, reporting
// whether one was present.
//
// Payloads travel through an `any` field, so a caller that asserted one concrete
// type silently discarded the reason of every other form it might legally arrive
// in -- a pointer, or a sibling payload type -- and substituted a generic
// message. That hides the actual cause from whoever has to diagnose the run,
// which is the opposite of what a reason is for. Keeping the extraction here
// also keeps the several consumers from disagreeing about what a payload means.
//
// A reason that is present but empty is still present, and is returned as an
// empty string: a caller must be able to distinguish "carries no reason" from
// "no reason was given", because only the first warrants substituting text.
//
// Callers keep their own fallback for the absent case, since the scheduler and
// the structured log deliberately report it differently.
func Message(payload any) (string, bool) {
	if payload == nil {
		return "", false
	}
	// A typed nil pointer held in an interface is not == nil, so it reaches the
	// accessors below and would panic when dereferenced. Reject it first, so
	// that stays true for every payload type added later without enumerating
	// pointer cases here.
	if v := reflect.ValueOf(payload); v.Kind() == reflect.Pointer && v.IsNil() {
		return "", false
	}
	switch p := payload.(type) {
	case string:
		return p, true
	case fmt.Stringer:
		return p.String(), true
	case reasoner:
		return p.Reason(), true
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
