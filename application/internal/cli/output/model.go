// The execution view model. Events fold into a small state structure that the
// renderers present. The model holds only what presentation needs: which
// services ran, their final state, and the execution result.
package output

import (
	"encoding/json"
	"time"

	"github.com/Muhammad-Jay/neuron/shared/types/protocol"
)

// ServiceState is the lifecycle state of a single service.
type ServiceState int

const (
	ServiceWaiting ServiceState = iota
	ServiceRunning
	ServiceCompleted
	ServiceFailed
)

// ServiceView is the presentation state of a single service execution.
type ServiceView struct {
	ID    string
	State ServiceState
	// Output holds the service's result payload, shown once it completes.
	Output map[string]any
	// Message is the failure message for failed services.
	Message string
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
	System  string
	Status  Status
	Started time.Time
	// Services preserves insertion (execution) order.
	Services []*ServiceView
	byID     map[string]*ServiceView
	// Message is the terminal failure message, when present.
	Message string
}

// NewExecutionView creates an empty view for the named system.
func NewExecutionView(system string) *ExecutionView {
	return &ExecutionView{System: system, byID: map[string]*ServiceView{}}
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

	sv, ok := v.byID[string(evt.ServiceID)]
	if !ok && evt.ServiceID != "" {
		sv = &ServiceView{ID: string(evt.ServiceID), State: ServiceWaiting}
		v.Services = append(v.Services, sv)
		v.byID[string(evt.ServiceID)] = sv
	}

	switch evt.Type {
	case "service.ready", "service.started":
		if sv != nil {
			sv.State = ServiceRunning
		}
	case "service.completed":
		if sv != nil {
			sv.State = ServiceCompleted
			var p struct {
				Output map[string]any `json:"Output"`
			}
			if err := json.Unmarshal(evt.Payload, &p); err == nil && p.Output != nil {
				sv.Output = p.Output
			}
		}
	case "service.failed":
		if sv != nil {
			sv.State = ServiceFailed
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
