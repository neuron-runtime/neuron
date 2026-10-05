package execution

import (
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/data"
	"github.com/neuron-runtime/neuron/shared/types/core"
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

// CapabilityState reports what happened to one capability of this execution. The
// zero value means the capability is not in the blueprint.
func (e *Execution) CapabilityState(capabilityID core.ID) CapabilityExecutionState {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.states[capabilityID]
}

// InFlight reports how many capabilities of this execution are still scheduled
// to run. A terminal execution must report zero: anything else means work was
// abandoned without being accounted for.
func (e *Execution) InFlight() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.inFlight
}

// Error returns the execution-level failure reason, empty when the execution
// has not failed.
func (e *Execution) Error() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.executionError
}

// Results returns a deep copy of every capability result recorded so far,
// keyed by capability ID. It is the aggregate result of a finished execution.
func (e *Execution) Results() map[core.ID]map[string]any {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make(map[core.ID]map[string]any, len(e.results))
	for id, output := range e.results {
		result[id] = data.CanonicalMap(output)
	}
	return result
}

// StringKeyedResults is Results with capability IDs rendered as strings, the
// shape used on the wire (execution results and the terminal
// execution.completed event payload).
func (e *Execution) StringKeyedResults() map[string]map[string]any {
	results := e.Results()
	if len(results) == 0 {
		return nil
	}
	result := make(map[string]map[string]any, len(results))
	for id, output := range results {
		result[string(id)] = output
	}
	return result
}
