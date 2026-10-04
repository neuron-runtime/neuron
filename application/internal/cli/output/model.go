// The execution view model. Events fold into a small state structure that the
// renderers present. The model holds only what presentation needs: which
// capabilities ran, their final state, and the execution result.
package output

import (
	"encoding/json"
	"time"

	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

// CapabilityState is the lifecycle state of a single capability.
type CapabilityState int

const (
	CapabilityWaiting CapabilityState = iota
	CapabilityRunning
	// CapabilityDetached marks a capability whose work was handed to a separate
	// execution. It is not an error, but it is also not a result: the work
	// continues under a task this view does not track.
	CapabilityDetached
	CapabilityCompleted
	CapabilityFailed
	// CapabilityCancelled marks a capability that was stopped without reaching an
	// outcome of its own, because the execution was cancelled or a sibling
	// failed. It is deliberately not CapabilityFailed: the capability did not
	// break, and rendering it with the failure glyph would point an operator at
	// working code as though it were the cause.
	CapabilityCancelled
)

// CapabilityView is the presentation state of a single capability execution.
type CapabilityView struct {
	ID    string
	State CapabilityState
	// Output holds the capability's result payload, shown once it completes.
	Output map[string]any
	// Message is the failure message for failed capabilities.
	Message string
	// Retries counts the re-attempts reported for a running capability, so a
	// capability that is being retried does not look stalled.
	Retries int
}

// Status is the execution-level state.
type Status int

const (
	StatusRunning Status = iota
	StatusCompleted
	StatusFailed
	StatusCancelled
)

// ExecutionView is the presentation state of one execution.
type ExecutionView struct {
	Assembly string
	Status   Status
	Started  time.Time
	// Capabilities preserves insertion (execution) order.
	Capabilities []*CapabilityView
	byID         map[string]*CapabilityView
	// Message is the terminal failure message, when present.
	Message string
}

// NewExecutionView creates an empty view for the named assembly.
func NewExecutionView(assembly string) *ExecutionView {
	return &ExecutionView{Assembly: assembly, byID: map[string]*CapabilityView{}}
}

// Duration returns the execution duration for a finished run.
func (v *ExecutionView) Duration() time.Duration {
	if v.Status == StatusRunning || v.Started.IsZero() {
		return 0
	}
	return time.Since(v.Started)
}

// Fold applies one event to the view.
func (v *ExecutionView) Fold(evt protocol.StreamEvent) error {
	if v.Started.IsZero() && evt.OccurredAt > 0 {
		v.Started = time.Unix(0, evt.OccurredAt)
	}

	sv, ok := v.byID[string(evt.CapabilityID)]
	if !ok && evt.CapabilityID != "" {
		sv = &CapabilityView{ID: string(evt.CapabilityID), State: CapabilityWaiting}
		v.Capabilities = append(v.Capabilities, sv)
		v.byID[string(evt.CapabilityID)] = sv
	}

	switch evt.Type {
	case "capability.ready", "capability.started":
		if sv != nil {
			sv.State = CapabilityRunning
		}
	case "capability.detached":
		if sv != nil {
			sv.State = CapabilityDetached
		}
	case "capability.retry":
		if sv != nil {
			sv.State = CapabilityRunning
			sv.Retries++
		}
	case "capability.completed":
		if sv != nil {
			sv.State = CapabilityCompleted
			var p struct {
				Output map[string]any `json:"Output"`
			}
			if err := json.Unmarshal(evt.Payload, &p); err == nil && p.Output != nil {
				sv.Output = p.Output
			}
		}
	case "capability.failed":
		if sv != nil {
			sv.State = CapabilityFailed
			var p struct {
				Message string `json:"Message"`
			}
			_ = json.Unmarshal(evt.Payload, &p)
			sv.Message = p.Message
		}
	case "capability.cancelled":
		if sv != nil {
			sv.State = CapabilityCancelled
			var p struct {
				Message string `json:"Message"`
			}
			_ = json.Unmarshal(evt.Payload, &p)
			sv.Message = p.Message
		}
	case "execution.completed":
		v.Status = StatusCompleted
	case "execution.failed":
		v.Status = StatusFailed
		var p struct {
			Message string `json:"Message"`
		}
		_ = json.Unmarshal(evt.Payload, &p)
		v.Message = p.Message
	case "execution.cancelled":
		v.Status = StatusCancelled
		var p struct {
			Message string `json:"Message"`
		}
		_ = json.Unmarshal(evt.Payload, &p)
		v.Message = p.Message
	}
	return nil
}
