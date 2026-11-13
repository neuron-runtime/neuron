package execution

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/data"
	"github.com/neuron-runtime/neuron/nore/internal/types"
	"github.com/neuron-runtime/neuron/shared/types/core"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

type ExecutionSnapshot struct {
	SchemaVersion     string                               `json:"schema_version"`
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
	// DetachedCapabilityID names the parent capability a detached task runs.
	DetachedCapabilityID core.ID `json:"detached_capability_id,omitempty"`
	// LogicalOperationID and AttemptID are stable identities used for
	// idempotency. They are persisted so a restored execution does not
	// fabricate new identities for work that may already have run.
	LogicalOperationID shared.ID `json:"logical_operation_id,omitempty"`
	AttemptID          shared.ID `json:"attempt_id,omitempty"`
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
		SchemaVersion:        "2",
		ID:                   e.ID,
		CorrelationID:        e.CorrelationID,
		InstanceID:           e.InstanceID,
		ParentExecutionID:    e.ParentExecutionID,
		DetachedCapabilityID: e.DetachedCapabilityID,
		Revision:             revision,
		Status:               e.status,
		InitialParams:        data.CanonicalMap(e.initialParams),
		Params:               cloneParamsMap(e.params),
		Results:              cloneParamsMap(e.results),
		States:               states,
		JoinState:            joinState,
		InFlight:             e.inFlight,
		StartedAt:            e.startedAt,
		CompletedAt:          e.completedAt,
		Error:                e.executionError,
		AssemblyID:           e.AssemblyID,
		Version:              e.Version,
		Hash:                 e.Hash,
		Env:                  e.Env,
		LogicalOperationID:   e.LogicalOperationID,
		AttemptID:            e.AttemptID,
	}
}

func (e *Execution) Restore(snapshot *ExecutionSnapshot) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.status = snapshot.Status
	e.revision = snapshot.Revision
	e.DetachedCapabilityID = snapshot.DetachedCapabilityID
	e.initialParams = data.CanonicalMap(snapshot.InitialParams)
	e.params = cloneParamsMap(snapshot.Params)
	e.results = cloneParamsMap(snapshot.Results)
	e.inFlight = snapshot.InFlight
	e.startedAt = snapshot.StartedAt
	e.completedAt = snapshot.CompletedAt
	e.executionError = snapshot.Error
	if snapshot.LogicalOperationID != "" {
		e.LogicalOperationID = snapshot.LogicalOperationID
	}
	if snapshot.AttemptID != "" {
		e.AttemptID = snapshot.AttemptID
	}

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

// AttachBlueprint installs the compiled blueprint an execution was created
// from. The blueprint is deliberately not part of the persisted snapshot:
// recovery recompiles it from the immutable assembly artifact rather than
// copying the graph into every execution. A restored execution therefore has no
// blueprint until its owner attaches the recompiled one.
func (e *Execution) AttachBlueprint(blueprint *types.ExecutionBlueprint) {
	if blueprint == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Blueprint = blueprint
}

func MarshalExecution(e *Execution) ([]byte, error) {
	return json.Marshal(e.Snapshot())
}

func UnmarshalExecution(data []byte) (*Execution, error) {
	var snap ExecutionSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, err
	}

	// Schema version migration. An empty version means the snapshot predates
	// version tracking and is treated as v1. Unknown newer versions are
	// rejected rather than silently reinterpreted.
	switch snap.SchemaVersion {
	case "":
		// v1 → v2: no structural transformation is required; the new
		// fields are absent and decode to their zero values.
		snap.SchemaVersion = "2"
	case "1":
		snap.SchemaVersion = "2"
	case "2":
		// Current version.
	default:
		return nil, fmt.Errorf("unsupported execution snapshot schema version %q", snap.SchemaVersion)
	}

	// Build the execution from snapshot fields.
	e := &Execution{
		ID:                   snap.ID,
		CorrelationID:        snap.CorrelationID,
		InstanceID:           snap.InstanceID,
		ParentExecutionID:    snap.ParentExecutionID,
		DetachedCapabilityID: snap.DetachedCapabilityID,
		revision:             snap.Revision,
		status:               snap.Status,
		initialParams:        snap.InitialParams,
		params:               snap.Params,
		results:              snap.Results,
		states:               snap.States,
		inFlight:             snap.InFlight,
		startedAt:            snap.StartedAt,
		completedAt:          snap.CompletedAt,
		executionError:       snap.Error,
		done:                 make(chan struct{}),
	}

	// Restore assembly identity from snapshot.
	e.AssemblyID = snap.AssemblyID
	e.Version = snap.Version
	e.Hash = snap.Hash
	e.Env = snap.Env

	// Restore stable identities. A v1 snapshot has neither, so generate them
	// once; the generated values are persisted on the next save.
	e.LogicalOperationID = snap.LogicalOperationID
	if e.LogicalOperationID == "" {
		e.LogicalOperationID = shared.NewID("logop_")
	}
	e.AttemptID = snap.AttemptID
	if e.AttemptID == "" {
		e.AttemptID = shared.NewID("attempt_")
	}

	// Ensure every capability state has the new fields initialized.
	// This handles both fresh (v2) snapshots and migrated (v1) snapshots.
	if e.states == nil {
		e.states = make(map[shared.ID]CapabilityExecutionState)
	}
	if e.initialParams == nil {
		e.initialParams = make(map[string]any)
	}
	if e.params == nil {
		e.params = make(map[shared.ID]map[string]any)
	}
	if e.results == nil {
		e.results = make(map[shared.ID]map[string]any)
	}

	// Rebuild join state from snapshot if present.
	if snap.JoinState != nil {
		e.joinState = make(map[shared.ID]*joinStateInfo, len(snap.JoinState))
		for id, js := range snap.JoinState {
			e.joinState[id] = joinStateFromSnapshot(js)
		}
	}
	if e.joinState == nil {
		e.joinState = make(map[shared.ID]*joinStateInfo)
	}
	// A terminal snapshot must not block a Wait caller forever, so signal
	// completion eagerly. A non-terminal snapshot is resumed by recovery, which
	// attaches its blueprint and re-dispatches unfinished work.
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
