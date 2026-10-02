package execution

import (
	"errors"
	"fmt"
	"sync"
	"time"

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
	e.initialParams = cloneMap(initialParams)
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
	e.params[capabilityID] = cloneMap(input)
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
	e.results[capabilityID] = cloneMap(output)
	return nil
}

func (e *Execution) MarkCapabilityFailed(capabilityID shared.ID, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	state, exists := e.states[capabilityID]
	if !exists {
		return
	}
	now := time.Now().UTC()
	state.Status = CapabilityFailed
	state.CompletedAt = &now
	if err != nil {
		state.Error = err.Error()
	}
	e.states[capabilityID] = state
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
	if e.status == StatusCompleted || e.status == StatusFailed || e.status == StatusCancelled {
		return false
	}
	now := time.Now().UTC()
	e.status = StatusFailed
	e.completedAt = &now
	if err != nil {
		e.executionError = err.Error()
	}
	e.signalTerminal()
	return true
}

func (e *Execution) IsTerminal() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.status == StatusCompleted || e.status == StatusFailed || e.status == StatusCancelled
}

func (e *Execution) Params(capabilityID shared.ID) map[string]any {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return cloneMap(e.params[capabilityID])
}

func (e *Execution) Result(capabilityID shared.ID) map[string]any {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return cloneMap(e.results[capabilityID])
}

func (e *Execution) InitialParams() map[string]any {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return cloneMap(e.initialParams)
}

func cloneMap(source map[string]any) map[string]any {
	if source == nil {
		return map[string]any{}
	}
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = cloneValue(value)
	}
	return result
}

func cloneValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneMap(typed)
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = cloneValue(item)
		}
		return result
	default:
		return typed
	}
}
