package execution

import (
	"sort"
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

// AttemptIdentity snapshots the current dispatch generation together with the
// stable logical operation ID under the domain lock. Callers that derive wire
// identity or dispatch-intent keys from these values must capture both once per
// invocation or claim: a peer resolving a wait can rotate AttemptID at any time.
func (e *Execution) AttemptIdentity() (core.ID, core.ID) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.AttemptID, e.LogicalOperationID
}

// CapabilityState reports what happened to one capability of this execution. The
// zero value means the capability is not in the blueprint.
func (e *Execution) CapabilityState(capabilityID core.ID) CapabilityExecutionState {
	e.mu.RLock()
	defer e.mu.RUnlock()
	state := e.states[capabilityID]
	state.Resume = cloneCapabilityResume(state.Resume)
	return state
}

// UnfinishedCapabilityCount reports how many capabilities have not reached a
// terminal capability state. It is used by recovery to distinguish an execution
// that drained normally from a corrupt snapshot that has no work left to
// dispatch but still claims unfinished capabilities.
func (e *Execution) UnfinishedCapabilityCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	count := 0
	for _, state := range e.states {
		switch state.Status {
		case CapabilityCompleted, CapabilityFailed, CapabilityDetached, CapabilityCancelled:
		default:
			count++
		}
	}
	return count
}

// InFlight reports how many capabilities of this execution are still scheduled
// to run. A terminal execution must report zero: anything else means work was
// abandoned without being accounted for.
func (e *Execution) InFlight() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.inFlight
}

// ExecutionDeadline returns the maximum-lifetime deadline for this execution,
// or nil when no limit is set. The returned pointer is a copy.
func (e *Execution) ExecutionDeadline() *time.Time {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.executionDeadline == nil {
		return nil
	}
	deadline := e.executionDeadline.UTC()
	return &deadline
}

// IsWaiting reports whether the execution is in the derived waiting state: it
// has waiting capabilities and no runnable work remains.
func (e *Execution) IsWaiting() bool {
	return e.Status() == StatusWaiting
}

// WaitingCapabilities returns the IDs of capabilities currently in a wait,
// including retry waits, in deterministic order. These capabilities occupy no
// worker and are not included in InFlight.
func (e *Execution) WaitingCapabilities() []core.ID {
	e.mu.RLock()
	defer e.mu.RUnlock()
	ids := make([]core.ID, 0)
	for id, state := range e.states {
		if state.Status == CapabilityWaiting || state.Status == CapabilityRetryWait {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
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
