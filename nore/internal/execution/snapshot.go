package execution

import (
	"encoding/json"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/data"
	"github.com/neuron-runtime/neuron/shared/types/core"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

type ExecutionSnapshot struct {
	ID                core.ID                              `json:"id"`
	CorrelationID     core.ID                              `json:"correlation_id"`
	InstanceID        core.ID                              `json:"instance_id,omitempty"`
	ParentExecutionID core.ID                              `json:"parent_execution_id,omitempty"`
	Revision          uint64                               `json:"revision"`
	Status            Status                               `json:"status"`
	InitialParams     map[string]any                       `json:"initial_params,omitempty"`
	Params            map[core.ID]map[string]any           `json:"params,omitempty"`
	Results           map[core.ID]map[string]any           `json:"results,omitempty"`
	States            map[core.ID]CapabilityExecutionState `json:"states,omitempty"`
	JoinState         map[shared.ID]*JoinState             `json:"join_state,omitempty"`
	InFlight          int                                  `json:"in_flight"`
	StartedAt         *time.Time                           `json:"started_at,omitempty"`
	CompletedAt       *time.Time                           `json:"completed_at,omitempty"`
	Error             string                               `json:"error,omitempty"`
	AssemblyID        string                               `json:"assembly_id,omitempty"`
	Version           string                               `json:"version,omitempty"`
	Hash              string                               `json:"hash,omitempty"`
	Env               string                               `json:"env,omitempty"`
}

func (e *Execution) Snapshot() *ExecutionSnapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.snapshotLocked(e.revision)
}

func (e *Execution) snapshotAt(revision uint64) *ExecutionSnapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.snapshotLocked(revision)
}

// snapshotLocked builds a snapshot while the caller holds e.mu.
func (e *Execution) snapshotLocked(revision uint64) *ExecutionSnapshot {
	states := make(map[core.ID]CapabilityExecutionState, len(e.states))
	for id, state := range e.states {
		states[id] = state
	}

	joinState := make(map[shared.ID]*JoinState, len(e.joinState))
	for id, ji := range e.joinState {
		joinState[id] = ji.snapshot()
	}

	return &ExecutionSnapshot{
		ID:                e.ID,
		CorrelationID:     e.CorrelationID,
		InstanceID:        e.InstanceID,
		ParentExecutionID: e.ParentExecutionID,
		Revision:          revision,
		Status:            e.status,
		InitialParams:     data.CanonicalMap(e.initialParams),
		Params:            cloneParamsMap(e.params),
		Results:           cloneParamsMap(e.results),
		States:            states,
		JoinState:         joinState,
		InFlight:          e.inFlight,
		StartedAt:         e.startedAt,
		CompletedAt:       e.completedAt,
		Error:             e.executionError,
		AssemblyID:        e.AssemblyID,
		Version:           e.Version,
		Hash:              e.Hash,
		Env:               e.Env,
	}
}

func (e *Execution) Restore(snapshot *ExecutionSnapshot) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.status = snapshot.Status
	e.revision = snapshot.Revision
	e.initialParams = data.CanonicalMap(snapshot.InitialParams)
	e.params = cloneParamsMap(snapshot.Params)
	e.results = cloneParamsMap(snapshot.Results)
	e.inFlight = snapshot.InFlight
	e.startedAt = snapshot.StartedAt
	e.completedAt = snapshot.CompletedAt
	e.executionError = snapshot.Error

	for id, state := range snapshot.States {
		e.states[id] = state
	}

	// Rebuild join state from snapshot if present.
	if snapshot.JoinState != nil {
		e.joinState = make(map[shared.ID]*joinStateInfo, len(snapshot.JoinState))
		for id, js := range snapshot.JoinState {
			e.joinState[id] = joinStateFromSnapshot(js)
		}
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
		revision:          snap.Revision,
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
	// Restore assembly identity from snapshot.
	e.AssemblyID = snap.AssemblyID
	e.Version = snap.Version
	e.Hash = snap.Hash
	e.Env = snap.Env
	// Rebuild join state from snapshot if present.
	if snap.JoinState != nil {
		e.joinState = make(map[shared.ID]*joinStateInfo, len(snap.JoinState))
		for id, js := range snap.JoinState {
			e.joinState[id] = joinStateFromSnapshot(js)
		}
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
		result[id] = data.CanonicalMap(m)
	}
	return result
}
