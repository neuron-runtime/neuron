package execution

import (
	"errors"
	"fmt"
	"strings"
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

	// revision is the durable compare-and-swap version of this execution. It is
	// serialized in snapshots and incremented only after a successful save.
	revision uint64

	// saveMu serializes saves of this Execution value. Two writers sharing the
	// same value cannot produce a false conflict; only a distinct copy with an
	// older revision triggers storage.ErrConflict.
	saveMu sync.Mutex

	// joinState tracks completion of predecessor bindings for join targets.
	// Each key is a target capability ID; the value records which predecessor
	// bindings have completed and what inputs they contributed.
	joinState map[shared.ID]*joinStateInfo

	// Assembly identity: the instance key that created this execution.
	// Populated when the execution is created from an Instance; used by
	// durable execution recovery (Phase 4) to resume against the correct
	// immutable assembly artifact.
	AssemblyID  string
	Version     string
	Hash        string
	Env         string
}

func NewExecution(blueprint *types.ExecutionBlueprint, correlationID shared.ID, instanceID shared.ID, assemblyID, version, hash, env string) (*Execution, error) {
	if blueprint == nil {
		return nil, errors.New("execution blueprint is required")
	}
	if correlationID == "" {
		correlationID = shared.NewID("corr_")
	}
	states := make(map[shared.ID]CapabilityExecutionState, len(blueprint.Nodes))
	joinState := make(map[shared.ID]*joinStateInfo)
	for capabilityID, node := range blueprint.Nodes {
		states[capabilityID] = CapabilityExecutionState{Status: CapabilityPending}
		// Every capability with an incoming edge is a join target: it becomes
		// ready only once its predecessor bindings are satisfied. The state must
		// exist up front, because contributions arrive through the scheduler and
		// nothing else may create it lazily under a different lock.
		if len(node.Predecessors) > 0 {
			joinState[capabilityID] = newJoinState()
		}
	}
	return &Execution{
		ID:            shared.NewID("exec_"),
		CorrelationID: correlationID,
		InstanceID:    instanceID,
		Blueprint:     blueprint,
		status:        StatusPending,
		initialParams: make(map[string]any),
		params:        make(map[shared.ID]map[string]any),
		results:       make(map[shared.ID]map[string]any),
		states:        states,
		done:          make(chan struct{}),
		joinState:     joinState,
		AssemblyID:    assemblyID,
		Version:       version,
		Hash:          hash,
		Env:           env,
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
	// The capability that just reported an outcome is no longer in flight. Any
	// capabilities scheduled from its completion replace it, so the count moves
	// by nextCapabilityCount-1. That is true even when nothing is scheduled (a
	// blocked fan-in or a detached hand-off): the completed capability must
	// still be removed, or the execution can never drain.
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
	e.joinState = make(map[shared.ID]*joinStateInfo)
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

// Revision reports the durable revision this execution was last saved at. A
// newly created execution has revision 0 until its first successful Add.
func (e *Execution) Revision() uint64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.revision
}

func (e *Execution) setRevision(revision uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.revision = revision
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

// ResolveAssemblyResult resolves the Assembly result fields from the blueprint's
// result contract. It builds an environment from all capability results and the
// initial params, then evaluates each result field's source reference.
// Returns the assembled result object, or an error if any field cannot be resolved.
func (e *Execution) ResolveAssemblyResult() (map[string]any, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.Blueprint == nil || e.Blueprint.Result == nil {
		return nil, nil // No result contract defined
	}

	result := make(map[string]any, len(e.Blueprint.Result.Fields))
	for _, field := range e.Blueprint.Result.Fields {
		if field.Source == nil {
			return nil, fmt.Errorf("assembly result field %q has no source", field.Name)
		}
		value, err := resolveAssemblyResultField(e, field.Source)
		if err != nil {
			return nil, fmt.Errorf("assembly result field %q: %w", field.Name, err)
		}
		result[field.Name] = value
	}
	return result, nil
}

// resolveAssemblyResultField resolves a single Assembly result field's source
// reference against the execution's capability results and initial params.
func resolveAssemblyResultField(e *Execution, source *shared.ValueRef) (any, error) {
	switch source.Kind {
	case shared.ValueRefCapabilityResult:
		// Look up the capability's result
		capResult, ok := e.results[shared.ID(source.Capability)]
		if !ok {
			return nil, fmt.Errorf("capability %q result not found", source.Capability)
		}
		return pathValue(capResult, source.Path, source)
	case shared.ValueRefCapabilityParams:
		capParams, ok := e.params[shared.ID(source.Capability)]
		if !ok {
			return nil, fmt.Errorf("capability %q params not found", source.Capability)
		}
		return pathValue(capParams, source.Path, source)
	case shared.ValueRefAssemblyParams:
		return pathValue(e.initialParams, source.Path, source)
	case shared.ValueRefLiteral:
		return source.Value, nil
	case shared.ValueRefVariable:
		return nil, fmt.Errorf("%s references are not resolvable: no variable provider is registered", source.Kind)
	default:
		return nil, fmt.Errorf("unknown reference kind %q", source.Kind)
	}
}

// pathValue walks a ValueRef path into a payload map. A missing key or a
// non-object intermediate reports an error with the reference rendered, so a
// failed binding names the exact field that was absent.
func pathValue(payload any, path []string, source *shared.ValueRef) (any, error) {
	if len(path) == 0 {
		return payload, nil
	}
	current := payload
	for i, segment := range path {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s reference path %q: expected object at %q, got %T", source.Kind, strings.Join(path[:i+1], "."), segment, current)
		}
		next, ok := m[segment]
		if !ok {
			return nil, fmt.Errorf("%s reference path %q: key %q not found", source.Kind, strings.Join(path[:i+1], "."), segment)
		}
		if i == len(path)-1 {
			return next, nil
		}
		current = next
	}
	return nil, fmt.Errorf("%s reference path %q: unexpected end", source.Kind, strings.Join(path, "."))
}

// joinStateInfo holds the completion and contribution state for a join target
// capability. It is created for every capability with at least one incoming edge.
type joinStateInfo struct {
	CompletedBindings map[shared.ID]bool
	PredecessorInputs map[shared.ID]map[string]any
	Dispatched        bool
}

func newJoinState() *joinStateInfo {
	return &joinStateInfo{
		CompletedBindings: make(map[shared.ID]bool),
		PredecessorInputs: make(map[shared.ID]map[string]any),
	}
}

// RecordJoinContribution records one predecessor binding's contribution to a
// join target and reports whether this call completed the join.
//
// It is atomic and idempotent: only the call that satisfies the final required
// predecessor returns ready=true, so a target can never be dispatched twice even
// if completion events arrive concurrently or more than once. A call made after
// the target was dispatched returns false and contributes nothing further.
//
// partialInput is the target params produced by this predecessor's binding
// mappings. Contributions are merged in blueprint predecessor order (binding ID,
// then capability ID), which makes the merged input deterministic regardless of
// completion order; target paths are disjoint by construction (R3-4).
func (e *Execution) RecordJoinContribution(capabilityID shared.ID, bindingID shared.ID, partialInput map[string]any) (bool, map[string]any, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	node, exists := e.Blueprint.Nodes[capabilityID]
	if !exists {
		return false, nil, fmt.Errorf("capability %s is not in the blueprint", capabilityID)
	}
	ji, exists := e.joinState[capabilityID]
	if !exists {
		return false, nil, fmt.Errorf("capability %s is not a join target", capabilityID)
	}
	if ji.Dispatched {
		return false, nil, nil
	}
	if ji.CompletedBindings == nil {
		ji.CompletedBindings = make(map[shared.ID]bool)
	}
	if ji.PredecessorInputs == nil {
		ji.PredecessorInputs = make(map[shared.ID]map[string]any)
	}

	ji.CompletedBindings[bindingID] = true
	ji.PredecessorInputs[bindingID] = data.CanonicalMap(partialInput)

	// Readiness is decided from the blueprint's predecessor set, not from the
	// number of contributions received: a stray or unknown binding can never
	// satisfy a join.
	for _, predecessor := range node.Predecessors {
		if !ji.CompletedBindings[predecessor.BindingID] {
			return false, nil, nil
		}
	}

	ji.Dispatched = true
	merged := make(map[string]any)
	for _, predecessor := range node.Predecessors {
		mergeInto(merged, ji.PredecessorInputs[predecessor.BindingID])
	}
	return true, merged, nil
}

// JoinState returns a deep copy of the target's join state for serialization.
// It returns nil for a capability that is not a join target.
func (e *Execution) JoinState(capabilityID shared.ID) *JoinState {
	e.mu.RLock()
	defer e.mu.RUnlock()
	ji, exists := e.joinState[capabilityID]
	if !exists {
		return nil
	}
	return ji.snapshot()
}

// joinStateSnapshot returns a serializable copy. The caller must hold e.mu.
func (ji *joinStateInfo) snapshot() *JoinState {
	completed := make([]shared.ID, 0, len(ji.CompletedBindings))
	for bindingID, done := range ji.CompletedBindings {
		if done {
			completed = append(completed, bindingID)
		}
	}
	inputs := make(map[shared.ID]map[string]any, len(ji.PredecessorInputs))
	for bindingID, input := range ji.PredecessorInputs {
		inputs[bindingID] = data.CanonicalMap(input)
	}
	return &JoinState{
		CompletedBindings: completed,
		PredecessorInputs: inputs,
		Dispatched:        ji.Dispatched,
	}
}

func joinStateFromSnapshot(snapshot *JoinState) *joinStateInfo {
	ji := newJoinState()
	if snapshot == nil {
		return ji
	}
	for _, bindingID := range snapshot.CompletedBindings {
		ji.CompletedBindings[bindingID] = true
	}
	for bindingID, input := range snapshot.PredecessorInputs {
		ji.PredecessorInputs[bindingID] = data.CanonicalMap(input)
	}
	ji.Dispatched = snapshot.Dispatched
	return ji
}

// mergeInto deep-merges src into dst. Object values are merged recursively so
// two bindings targeting sibling paths ("profile.name" and "profile.age") both
// survive. Leaf paths are disjoint by construction, so leaf overwrites only
// occur when a caller supplies duplicate paths, which the compiler rejects.
func mergeInto(dst, src map[string]any) {
	for key, value := range src {
		if existing, ok := dst[key].(map[string]any); ok {
			if incoming, ok := value.(map[string]any); ok {
				mergeInto(existing, incoming)
				continue
			}
		}
		dst[key] = value
	}
}

// JoinState is a serializable snapshot of a join target's completion state.
// It is returned by the Execution.JoinState method.
type JoinState struct {
	CompletedBindings []shared.ID
	PredecessorInputs map[shared.ID]map[string]any
	Dispatched        bool
}
