package execution

import (
	"errors"
	"fmt"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/types"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

// NewDetachedTask creates the execution that takes over a detached capability
// and everything downstream of it.
//
// The scope is the sub-blueprint the planner compiled for that capability, so
// the task runs the same nodes, bindings, and compiled expressions as the parent
// — the only difference is that its entry is the detached capability and its
// input is that capability's params rather than the instance execution's.
//
// A detached execution is not started here. It is created in the pending state
// so the caller can persist it before acknowledging the handoff; Start is called
// only once that record is durable. Creating it before the acknowledgement is
// what keeps detach honest: a caller that sees its work accepted can always find
// the task tracking it, even across a restart.
func NewDetachedTask(scope *types.ExecutionBlueprint, correlationID, instanceID, parentExecutionID shared.ID) (*Execution, error) {
	if scope == nil {
		return nil, errors.New("detached task scope is required")
	}
	if parentExecutionID == "" {
		return nil, errors.New("detached task requires the execution it was handed off from")
	}
	task, err := NewExecution(scope, correlationID, instanceID)
	if err != nil {
		return nil, err
	}
	task.ParentExecutionID = parentExecutionID
	return task, nil
}

// MarkCapabilityDetached records that a capability's work was handed off to a
// separate execution.
//
// The transition runs from ready, not from running: the capability was never
// invoked here, so claiming it started in this execution would misrepresent what
// happened. Its work and timing live on the task.
func (e *Execution) MarkCapabilityDetached(capabilityID shared.ID) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	state, exists := e.states[capabilityID]
	if !exists {
		return fmt.Errorf("capability %s is not in the blueprint", capabilityID)
	}
	if state.Status != CapabilityReady {
		return fmt.Errorf("capability %s cannot detach from %s", capabilityID, state.Status)
	}
	now := time.Now().UTC()
	state.Status = CapabilityDetached
	state.CompletedAt = &now
	e.states[capabilityID] = state
	return nil
}

// DetachedCapabilityIDs returns every capability in this execution whose work was
// handed off. The scheduler uses it to decide when the foreground scope has
// nothing left of its own to do.
func (e *Execution) DetachedCapabilityIDs() []shared.ID {
	e.mu.RLock()
	defer e.mu.RUnlock()
	detached := make([]shared.ID, 0, len(e.states))
	for capabilityID, state := range e.states {
		if state.Status == CapabilityDetached {
			detached = append(detached, capabilityID)
		}
	}
	return detached
}
