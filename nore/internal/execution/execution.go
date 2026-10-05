package execution

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/data"
	"github.com/neuron-runtime/neuron/nore/internal/types"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

type CapabilityStatus string

const (
	CapabilityPending   CapabilityStatus = "pending"
	CapabilityReady     CapabilityStatus = "ready"
	CapabilityRunning   CapabilityStatus = "running"
	CapabilityCompleted CapabilityStatus = "completed"
	CapabilityFailed    CapabilityStatus = "failed"

	// CapabilityDetached marks a capability whose work was handed off to a
	// separate execution instead of being run in place. It is terminal for the
	// capability within this execution but not an error: the work continues,
	// tracked under the task created for it.
	CapabilityDetached CapabilityStatus = "detached"

	// CapabilityCancelled marks a capability that was abandoned because its
	// execution was cancelled before the capability reported an outcome. It is
	// terminal for the capability and distinct from CapabilityFailed: nothing
	// about the capability itself went wrong, so reporting it as a failure would
	// attribute a decision to the implementation.
	CapabilityCancelled CapabilityStatus = "cancelled"
)

type CapabilityExecutionState struct {
	Status      CapabilityStatus
	StartedAt   *time.Time
	CompletedAt *time.Time
	Error       string
}

type Execution struct {
	ID            shared.ID
	CorrelationID shared.ID
	InstanceID    shared.ID

	// ParentExecutionID links a detached execution to the execution that handed
	// its work off. It is empty for a root execution.
	ParentExecutionID shared.ID

	Blueprint *types.ExecutionBlueprint
	mu        sync.RWMutex
	status    Status
	// initialParams is the input a root execution was invoked with. A detached
	// execution is invoked with one capability's input instead, so it has none.
	initialParams  map[string]any
	params         map[shared.ID]map[string]any
	results        map[shared.ID]map[string]any
	states         map[shared.ID]CapabilityExecutionState
	inFlight       int
	startedAt      *time.Time
	completedAt    *time.Time
	executionError string

	// done is closed exactly once when the execution reaches a terminal
	// state; Wait and Done poll it. It is a runtime-only device and is not
	// serialized in snapshots.
	done chan struct{}
}

func NewExecution(blueprint *types.ExecutionBlueprint, correlationID shared.ID, instanceID shared.ID) (*Execution, error) {
	if blueprint == nil {
		return nil, errors.New("execution blueprint is required")
	}
	if correlationID == "" {
		correlationID = shared.NewID("corr_")
	}
	states := make(map[shared.ID]CapabilityExecutionState, len(blueprint.Nodes))
	for capabilityID := range blueprint.Nodes {
		states[capabilityID] = CapabilityExecutionState{Status: CapabilityPending}
	}
	return &Execution{
		ID: shared.NewID("exec_"), CorrelationID: correlationID, InstanceID: instanceID, Blueprint: blueprint,
		status: StatusPending, initialParams: make(map[string]any),
		params: make(map[shared.ID]map[string]any), results: make(map[shared.ID]map[string]any), states: states,
		done: make(chan struct{}),
	}, nil
}

// signalTerminal closes the done channel. It must be called exactly once,
// and only by a mutator that has already transitioned the execution into a
// terminal status (the bool-returning terminators are the sole valid callers).
func (e *Execution) signalTerminal() {
	select {
	case <-e.done:
	default:
		close(e.done)
	}
}

func (e *Execution) Start(initialParams map[string]any, initialCapabilityCount int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.status != StatusPending {
		return fmt.Errorf("execution %s cannot start from status %s", e.ID, e.status)
	}
	if initialCapabilityCount <= 0 {
		return errors.New("execution must start with at least one entry capability")
	}
	now := time.Now().UTC()
	e.status = StatusRunning
	e.startedAt = &now
	e.inFlight = initialCapabilityCount
	e.initialParams = data.CanonicalMap(initialParams)
	return nil
}

func (e *Execution) MarkCapabilityReady(capabilityID shared.ID, input map[string]any) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.status != StatusRunning {
		return fmt.Errorf("execution %s is not running", e.ID)
	}
	state, exists := e.states[capabilityID]
	if !exists {
		return fmt.Errorf("capability %s is not in the blueprint", capabilityID)
	}
	if state.Status != CapabilityPending {
		return fmt.Errorf("capability %s cannot become ready from %s", capabilityID, state.Status)
	}
	state.Status = CapabilityReady
	e.states[capabilityID] = state
	e.params[capabilityID] = data.CanonicalMap(input)
	return nil
}

func (e *Execution) MarkCapabilityRunning(capabilityID shared.ID) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	state, exists := e.states[capabilityID]
	if !exists {
		return fmt.Errorf("capability %s is not in the blueprint", capabilityID)
	}
	if state.Status != CapabilityReady {
		return fmt.Errorf("capability %s cannot run from %s", capabilityID, state.Status)
	}
	now := time.Now().UTC()
	state.Status = CapabilityRunning
	state.StartedAt = &now
	e.states[capabilityID] = state
	return nil
}

func (e *Execution) MarkCapabilityCompleted(capabilityID shared.ID, output map[string]any) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	state, exists := e.states[capabilityID]
	if !exists {
		return fmt.Errorf("capability %s is not in the blueprint", capabilityID)
	}
	if state.Status != CapabilityRunning {
		return fmt.Errorf("capability %s cannot complete from %s", capabilityID, state.Status)
	}
	now := time.Now().UTC()
	state.Status = CapabilityCompleted
	state.CompletedAt = &now
	e.states[capabilityID] = state
	e.results[capabilityID] = data.CanonicalMap(output)
	return nil
}

// MarkCapabilityFailed records a capability's failure.
//
// A capability that already reported an outcome keeps it. The engine observes an
// aborted invocation *after* a cancellation has already been recorded, and that
// late error is a consequence of the stop somebody else requested -- reporting it
// as a capability failure would blame the implementation for a decision it did
// not make, and would overwrite a success the runtime had legitimately reported.
//
// It reports whether the state changed so the caller can skip announcing an
// outcome the execution no longer holds.
func (e *Execution) MarkCapabilityFailed(capabilityID shared.ID, err error) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	state, exists := e.states[capabilityID]
	if !exists {
		return false
	}
	if isTerminalCapability(state.Status) {
		return false
	}
	now := time.Now().UTC()
	state.Status = CapabilityFailed
	state.CompletedAt = &now
	if err != nil {
		state.Error = err.Error()
	}
	e.states[capabilityID] = state
	return true
}

// MarkCapabilityCancelled records that a capability stopped without reaching an
// outcome of its own, because something outside it ended first.
//
// This is deliberately not the same record as MarkCapabilityFailed. A capability
// stopped because the execution was cancelled, or because a sibling failed, did
// not break, and recording it as a fault blames an implementation for a decision
// it did not make — and, because the failure carries no distinguishing type,
// leaves a consumer with no way to tell the two apart except by reading the
// message text.
//
// It reports whether the state changed, for the same reason as
// MarkCapabilityFailed: the engine can observe a stop after the execution has
// already recorded an outcome for this capability, and must not overwrite it.
func (e *Execution) MarkCapabilityCancelled(capabilityID shared.ID, reason error) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	state, exists := e.states[capabilityID]
	if !exists {
		return false
	}
	if isTerminalCapability(state.Status) {
		return false
	}
	now := time.Now().UTC()
	state.Status = CapabilityCancelled
	state.CompletedAt = &now
	if reason != nil {
		state.Error = reason.Error()
	}
	e.states[capabilityID] = state
	return true
}

// isTerminalCapability reports whether a capability state is final. Pending, ready,
// and running are not: work may still be scheduled or is still in flight.
func isTerminalCapability(status CapabilityStatus) bool {
	switch status {
	case CapabilityCompleted, CapabilityFailed, CapabilityCancelled, CapabilityDetached:
		return true
	default:
		return false
	}
}

func (e *Execution) CompleteCurrentAndSchedule(nextCapabilityCount int) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.status != StatusRunning {
		return e.inFlight, fmt.Errorf("execution %s is not running", e.ID)
	}
	if e.inFlight <= 0 {
		return e.inFlight, errors.New("execution in-flight counter is invalid")
	}
	e.inFlight += nextCapabilityCount - 1
	return e.inFlight, nil
}

func (e *Execution) MarkCompleted() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.status != StatusRunning {
		return false
	}
	now := time.Now().UTC()
	e.status = StatusCompleted
	e.completedAt = &now
	e.signalTerminal()
	return true
}

func (e *Execution) MarkFailed(err error) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.isTerminalLocked() {
		return false
	}
	now := time.Now().UTC()
	e.status = StatusFailed
	e.completedAt = &now
	// The execution is terminal, so nothing is in flight any more. Capabilities
	// that never ran keep whatever status they had, but the counter must not
	// leave a snapshot claiming work is outstanding on a finished execution.
	e.inFlight = 0
	if err != nil {
		e.executionError = err.Error()
	}
	e.signalTerminal()
	return true
}

// MarkCancelled ends an execution that was cancelled before it completed,
// abandoning every capability that had not yet reported an outcome.
//
// Cancellation is not failure and is reported separately. A cancelled capability
// may have been perfectly healthy; it simply was not allowed to finish, and
// whoever reads the execution needs to be able to tell that apart from a
// capability that broke. Keeping the two apart is also what lets a deadline stay
// a failure: an exceeded timeout is something the author must fix, while a
// cancellation is a decision somebody took.
func (e *Execution) MarkCancelled(reason error) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.isTerminalLocked() {
		return false
	}
	now := time.Now().UTC()
	e.status = StatusCancelled
	e.completedAt = &now
	e.inFlight = 0
	for capabilityID, state := range e.states {
		// Anything already terminal -- completed, failed, or handed off to a
		// detached task -- reported its own outcome and keeps it. Cancelling must
		// not rewrite history, and a detached task is cancelled through its own
		// scope, not by cancelling the execution that handed it off.
		if isTerminalCapability(state.Status) {
			continue
		}
		state.Status = CapabilityCancelled
		state.CompletedAt = &now
		if reason != nil {
			state.Error = reason.Error()
		}
		e.states[capabilityID] = state
	}
	if reason != nil {
		e.executionError = reason.Error()
	}
	e.signalTerminal()
	return true
}

func (e *Execution) isTerminalLocked() bool {
	return e.status == StatusCompleted || e.status == StatusFailed || e.status == StatusCancelled
}

func (e *Execution) IsTerminal() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.isTerminalLocked()
}

func (e *Execution) Params(capabilityID shared.ID) map[string]any {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return data.CanonicalMap(e.params[capabilityID])
}

func (e *Execution) Result(capabilityID shared.ID) map[string]any {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return data.CanonicalMap(e.results[capabilityID])
}

func (e *Execution) InitialParams() map[string]any {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return data.CanonicalMap(e.initialParams)
}
