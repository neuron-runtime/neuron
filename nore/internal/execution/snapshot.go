package execution

import (
	"encoding/json"
	"time"

	"github.com/neuron-runtime/neuron/shared/types/core"
)

type ExecutionSnapshot struct {
	ID                core.ID                              `json:"id"`
	CorrelationID     core.ID                              `json:"correlation_id"`
	InstanceID        core.ID                              `json:"instance_id,omitempty"`
	ParentExecutionID core.ID                              `json:"parent_execution_id,omitempty"`
	Status            Status                               `json:"status"`
	InitialParams     map[string]any                       `json:"initial_params,omitempty"`
	Params            map[core.ID]map[string]any           `json:"params,omitempty"`
	Results           map[core.ID]map[string]any           `json:"results,omitempty"`
	States            map[core.ID]CapabilityExecutionState `json:"states,omitempty"`
	InFlight          int                                  `json:"in_flight"`
	StartedAt         *time.Time                           `json:"started_at,omitempty"`
	CompletedAt       *time.Time                           `json:"completed_at,omitempty"`
	Error             string                               `json:"error,omitempty"`
}

func (e *Execution) Snapshot() *ExecutionSnapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()

	states := make(map[core.ID]CapabilityExecutionState, len(e.states))
	for id, state := range e.states {
		states[id] = state
	}

	return &ExecutionSnapshot{
		ID:                e.ID,
		CorrelationID:     e.CorrelationID,
		InstanceID:        e.InstanceID,
		ParentExecutionID: e.ParentExecutionID,
		Status:            e.status,
		InitialParams:     cloneMap(e.initialParams),
		Params:            cloneParamsMap(e.params),
		Results:           cloneParamsMap(e.results),
		States:            states,
		InFlight:          e.inFlight,
		StartedAt:         e.startedAt,
		CompletedAt:       e.completedAt,
		Error:             e.executionError,
	}
}

func (e *Execution) Restore(snapshot *ExecutionSnapshot) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.status = snapshot.Status
	e.initialParams = cloneMap(snapshot.InitialParams)
	e.params = cloneParamsMap(snapshot.Params)
	e.results = cloneParamsMap(snapshot.Results)
	e.inFlight = snapshot.InFlight
	e.startedAt = snapshot.StartedAt
	e.completedAt = snapshot.CompletedAt
	e.executionError = snapshot.Error

	for id, state := range snapshot.States {
		e.states[id] = state
	}
}

func MarshalExecution(e *Execution) ([]byte, error) {
	return json.Marshal(e.Snapshot())
}

func UnmarshalExecution(data []byte) (*Execution, error) {
	var snap ExecutionSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, err
	}
	e := &Execution{
		ID:                snap.ID,
		CorrelationID:     snap.CorrelationID,
		InstanceID:        snap.InstanceID,
		ParentExecutionID: snap.ParentExecutionID,
		status:            snap.Status,
		initialParams:     snap.InitialParams,
		params:            snap.Params,
		results:           snap.Results,
		states:            snap.States,
		inFlight:          snap.InFlight,
		startedAt:         snap.StartedAt,
		completedAt:       snap.CompletedAt,
		executionError:    snap.Error,
		done:              make(chan struct{}),
	}
	// Restored executions are never resumed, but a terminal snapshot must not
	// block a Wait caller forever, so signal completion eagerly.
	if e.IsTerminal() {
		e.signalTerminal()
	}
	return e, nil
}

func cloneParamsMap(source map[core.ID]map[string]any) map[core.ID]map[string]any {
	if source == nil {
		return make(map[core.ID]map[string]any)
	}
	result := make(map[core.ID]map[string]any, len(source))
	for id, m := range source {
		result[id] = cloneMap(m)
	}
	return result
}
