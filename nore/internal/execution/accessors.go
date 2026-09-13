package execution

import (
	"time"

	"github.com/Muhammad-Jay/neuron/shared/types/core"
)

// Status returns the current execution status. Every writer holds the write
// lock, so reads are locked to avoid racing against a concurrent transition.
func (e *Execution) Status() Status {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.status
}

func (e *Execution) CompletedAt() time.Time {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.completedAt == nil {
		return time.Time{}
	}
	return e.completedAt.UTC()
}

func (e *Execution) StartedAt() time.Time {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.startedAt == nil {
		return time.Time{}
	}
	return e.startedAt.UTC()
}

func (e *Execution) Error() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.executionError
}

// Outputs returns a deep copy of every service output recorded so far, keyed
// by service ID. It is the aggregate result of a finished execution.
func (e *Execution) Outputs() map[core.ID]map[string]any {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make(map[core.ID]map[string]any, len(e.outputs))
	for id, output := range e.outputs {
		result[id] = cloneMap(output)
	}
	return result
}

// StringKeyedOutputs is Outputs with service IDs rendered as strings, the shape
// used on the wire (execution results and the terminal execution.completed
// event payload).
func (e *Execution) StringKeyedOutputs() map[string]map[string]any {
	outputs := e.Outputs()
	if len(outputs) == 0 {
		return nil
	}
	result := make(map[string]map[string]any, len(outputs))
	for id, output := range outputs {
		result[string(id)] = output
	}
	return result
}
