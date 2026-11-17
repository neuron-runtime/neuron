package execution

import (
	"context"
	"fmt"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/data"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

// Done exposes a channel that is closed exactly once the execution reaches a
// terminal state (completed, failed, or cancelled). It lets callers integrate
// execution completion into select-based concurrency without polling.
func (e *Execution) Done() <-chan struct{} {
	if e == nil {
		// A nil execution is never valid; return a never-closed channel so
		// callers that guard with a nil check cannot deadlock on a nil map.
		return make(chan struct{})
	}
	return e.done
}

// Wait blocks until the execution reaches a terminal state. It returns nil
// when the execution completed, the execution's terminal error when it failed
// or was cancelled, or ctx.Err() if the context is cancelled first.
//
// The execution engine always runs asynchronously; Wait only observes the
// terminal signal and never drives the execution itself.
func (e *Execution) Wait(ctx context.Context) error {
	if e == nil {
		return context.Canceled
	}
	select {
	case <-e.done:
		if msg := e.Error(); msg != "" {
			return &ExecutionError{ExecutionID: string(e.ID), Message: msg}
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ExecutionError is the terminal error surfaced by Wait for a failed or
// cancelled execution. It carries the execution identifier for attribution.
type ExecutionError struct {
	ExecutionID string
	Message     string
}

func (e *ExecutionError) Error() string {
	if e.Message == "" {
		return "execution " + e.ExecutionID + " did not complete"
	}
	return e.Message
}

// WaitType classifies how a waiting capability is resumed. Signal waits are
// resumed by an external delivery, timer and deadline waits by an elapsed clock.
type WaitType string

const (
	WaitTypeSignal   WaitType = "signal"
	WaitTypeTimer    WaitType = "timer"
	WaitTypeDeadline WaitType = "deadline"
)

// Valid reports whether w is a wait type the engine understands.
func (w WaitType) Valid() bool {
	switch w {
	case WaitTypeSignal, WaitTypeTimer, WaitTypeDeadline:
		return true
	default:
		return false
	}
}

// WaitDeadlinePolicy is the action applied when a wait's deadline passes for a
// wait that is not inherently time-triggered (a signal wait with a deadline).
type WaitDeadlinePolicy string

const (
	// WaitDeadlinePolicyFail fails the capability with wait_timeout.
	WaitDeadlinePolicyFail WaitDeadlinePolicy = "fail"
	// WaitDeadlinePolicyCancel cancels the waiting capability.
	WaitDeadlinePolicyCancel WaitDeadlinePolicy = "cancel"
	// WaitDeadlinePolicyContinue resumes the capability as though its wait had
	// been satisfied by the deadline.
	WaitDeadlinePolicyContinue WaitDeadlinePolicy = "continue"
)

// Valid reports whether p is a recognized deadline policy. An empty policy is
// not valid on its own; use effective to resolve it to the default.
func (p WaitDeadlinePolicy) Valid() bool {
	switch p {
	case WaitDeadlinePolicyFail, WaitDeadlinePolicyCancel, WaitDeadlinePolicyContinue:
		return true
	default:
		return false
	}
}

// effective resolves an unset policy to the fail default.
func (p WaitDeadlinePolicy) effective() WaitDeadlinePolicy {
	if p == "" {
		return WaitDeadlinePolicyFail
	}
	return p
}

// WaitDeadlineReason records the policy outcome applied when a wait ended by
// its deadline. It is retained for diagnostics and is distinct from the
// capability's free-form Error string.
type WaitDeadlineReason string

const (
	WaitDeadlineReasonTimeout   WaitDeadlineReason = "wait_timeout"
	WaitDeadlineReasonCancelled WaitDeadlineReason = "wait_cancelled"
	WaitDeadlineReasonContinued WaitDeadlineReason = "wait_deadline_continue"
)

// WaitSpec is the complete typed description of a wait a capability is
// entering. EnterWait validates it before mutating execution state.
type WaitSpec struct {
	// Type selects the resume mechanism. Required.
	Type WaitType
	// Reason is a human-readable explanation recorded on the capability.
	Reason string
	// CorrelationKey identifies this wait for delivery matching. If empty,
	// EnterWait derives it from the capability ID.
	CorrelationKey string
	// ExpectedSignal is required for signal waits.
	ExpectedSignal string
	// WaitUntil is required for timer and deadline waits. A nil WaitUntil on a
	// signal wait means the wait has no deadline and is never due.
	WaitUntil *time.Time
	// PayloadSchema describes the expected delivery payload.
	PayloadSchema string
	// DeadlinePolicy selects the action on deadline expiry; empty means fail.
	DeadlinePolicy WaitDeadlinePolicy
}

// EnterWait transitions a capability into CapabilityWaiting with a complete,
// validated set of wait metadata. It clears any prior dispatch state and, when
// the capability was runnable (ready or running), releases its in-flight slot
// so a wait never occupies a worker.
func (e *Execution) EnterWait(capabilityID shared.ID, spec WaitSpec) error {
	if !spec.Type.Valid() {
		return fmt.Errorf("invalid wait type %q for capability %s", spec.Type, capabilityID)
	}
	policy := spec.DeadlinePolicy.effective()
	if !policy.Valid() {
		return fmt.Errorf("invalid wait deadline policy %q for capability %s", spec.DeadlinePolicy, capabilityID)
	}
	if spec.Type == WaitTypeSignal && spec.ExpectedSignal == "" {
		return fmt.Errorf("signal wait for capability %s requires an expected signal", capabilityID)
	}
	if (spec.Type == WaitTypeTimer || spec.Type == WaitTypeDeadline) && spec.WaitUntil == nil {
		return fmt.Errorf("%s wait for capability %s requires WaitUntil", spec.Type, capabilityID)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	state, exists := e.states[capabilityID]
	if !exists {
		return fmt.Errorf("capability %s is not in the blueprint", capabilityID)
	}
	if isTerminalCapability(state.Status) {
		return fmt.Errorf("capability %s cannot enter wait from %s", capabilityID, state.Status)
	}
	if state.Status == CapabilityWaiting || state.Status == CapabilityRetryWait {
		return fmt.Errorf("capability %s is already waiting (%s)", capabilityID, state.Status)
	}

	wasRunnable := state.Status == CapabilityReady || state.Status == CapabilityRunning
	correlationKey := spec.CorrelationKey
	if correlationKey == "" {
		correlationKey = string(capabilityID)
	}
	var until *time.Time
	if spec.WaitUntil != nil {
		t := spec.WaitUntil.UTC()
		until = &t
	}

	state.Status = CapabilityWaiting
	state.WaitType = spec.Type
	state.WaitReason = spec.Reason
	state.CorrelationKey = correlationKey
	state.ExpectedSignal = spec.ExpectedSignal
	state.PayloadSchema = spec.PayloadSchema
	state.WaitUntil = until
	state.WaitDeadlinePolicy = policy
	state.WaitDeadlineReason = ""
	state.DeliveredPayload = nil
	state.DispatchState = ""
	state.NextAttemptAt = nil
	e.states[capabilityID] = state

	if wasRunnable {
		e.releaseWorkerLocked()
	}
	e.refreshStatusLocked()
	return nil
}

// ResolveWait resumes a waiting capability: it becomes ready, the wait metadata
// is cleared, the delivered payload is recorded as its resume input, and its
// in-flight slot is reacquired. Execution status is re-derived so a fully
// waiting execution returns to running once work resumes.
func (e *Execution) ResolveWait(capabilityID shared.ID, payload map[string]any) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.resolveWaitLocked(capabilityID, payload)
}

func (e *Execution) resolveWaitLocked(capabilityID shared.ID, payload map[string]any) error {
	state, exists := e.states[capabilityID]
	if !exists {
		return fmt.Errorf("capability %s is not in the blueprint", capabilityID)
	}
	if state.Status != CapabilityWaiting {
		return fmt.Errorf("capability %s is not waiting, status is %s", capabilityID, state.Status)
	}
	e.clearWaitMetadataLocked(&state)
	state.Status = CapabilityReady
	if payload != nil {
		state.DeliveredPayload = data.CanonicalMap(payload)
	}
	e.states[capabilityID] = state
	e.acquireWorkerLocked()
	e.refreshStatusLocked()
	return nil
}

// ResumeRetryWait resumes a capability whose retry deadline elapsed. It becomes
// ready, its next-attempt marker is cleared, and it reacquires an in-flight
// slot. Retry waits carry no signal metadata, so nothing else is cleared.
func (e *Execution) ResumeRetryWait(capabilityID shared.ID) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	state, exists := e.states[capabilityID]
	if !exists {
		return fmt.Errorf("capability %s is not in the blueprint", capabilityID)
	}
	if state.Status != CapabilityRetryWait {
		return fmt.Errorf("capability %s is not in retry wait, status is %s", capabilityID, state.Status)
	}
	state.Status = CapabilityReady
	state.NextAttemptAt = nil
	e.states[capabilityID] = state
	e.acquireWorkerLocked()
	e.refreshStatusLocked()
	return nil
}

// BeginRetryWait records a durable retry: the capability enters
// CapabilityRetryWait with the earliest time it may be attempted again and
// releases its in-flight slot. A retry wait without a next-attempt time is
// rejected because it could never become due.
func (e *Execution) BeginRetryWait(capabilityID shared.ID, nextAttemptAt time.Time) error {
	if nextAttemptAt.IsZero() {
		return fmt.Errorf("retry for capability %s requires a non-zero next attempt time", capabilityID)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	state, exists := e.states[capabilityID]
	if !exists {
		return fmt.Errorf("capability %s is not in the blueprint", capabilityID)
	}
	if isTerminalCapability(state.Status) {
		return fmt.Errorf("capability %s cannot begin retry wait from %s", capabilityID, state.Status)
	}
	if state.Status == CapabilityWaiting || state.Status == CapabilityRetryWait {
		return fmt.Errorf("capability %s is already waiting (%s)", capabilityID, state.Status)
	}
	t := nextAttemptAt.UTC()
	state.Status = CapabilityRetryWait
	state.NextAttemptAt = &t
	state.DispatchState = ""
	e.states[capabilityID] = state
	e.releaseWorkerLocked()
	e.refreshStatusLocked()
	return nil
}

// FailWait fails a waiting capability and records the reason on its state. It is
// used for deadline expiry under the fail policy and for exhausted retries.
func (e *Execution) FailWait(capabilityID shared.ID, reason error) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.failWaitLocked(capabilityID, reason)
}

func (e *Execution) failWaitLocked(capabilityID shared.ID, reason error) error {
	state, exists := e.states[capabilityID]
	if !exists {
		return fmt.Errorf("capability %s is not in the blueprint", capabilityID)
	}
	if state.Status != CapabilityWaiting {
		return fmt.Errorf("capability %s is not waiting, status is %s", capabilityID, state.Status)
	}
	now := time.Now().UTC()
	state.Status = CapabilityFailed
	state.CompletedAt = &now
	if reason != nil {
		state.Error = reason.Error()
	}
	e.states[capabilityID] = state
	e.refreshStatusLocked()
	return nil
}

// ExpireWait applies a wait's deadline policy after its WaitUntil has elapsed.
// It returns the policy that was applied so the caller can complete any
// execution-level consequence (failing or cancelling the execution).
//
// A wait with a nil or future WaitUntil is never expired. Under fail the
// capability fails with wait_timeout; under cancel it is cancelled; under
// continue it is resolved to ready. In every case the corresponding
// WaitDeadlineReason is recorded on the capability for diagnostics.
func (e *Execution) ExpireWait(capabilityID shared.ID, now time.Time) (WaitDeadlinePolicy, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	state, exists := e.states[capabilityID]
	if !exists {
		return "", fmt.Errorf("capability %s is not in the blueprint", capabilityID)
	}
	if state.Status != CapabilityWaiting {
		return "", fmt.Errorf("capability %s is not waiting, status is %s", capabilityID, state.Status)
	}
	if state.WaitUntil == nil {
		return "", fmt.Errorf("capability %s wait has no deadline", capabilityID)
	}
	if state.WaitUntil.After(now) {
		return "", fmt.Errorf("capability %s wait deadline has not passed", capabilityID)
	}

	policy := state.WaitDeadlinePolicy.effective()
	ts := time.Now().UTC()
	switch policy {
	case WaitDeadlinePolicyFail:
		state.WaitDeadlineReason = WaitDeadlineReasonTimeout
		state.Status = CapabilityFailed
		state.CompletedAt = &ts
		state.Error = string(WaitDeadlineReasonTimeout)
	case WaitDeadlinePolicyCancel:
		state.WaitDeadlineReason = WaitDeadlineReasonCancelled
		state.Status = CapabilityCancelled
		state.CompletedAt = &ts
		state.Error = "wait deadline exceeded: cancel policy"
	case WaitDeadlinePolicyContinue:
		state.WaitDeadlineReason = WaitDeadlineReasonContinued
	}
	e.states[capabilityID] = state

	if policy == WaitDeadlinePolicyContinue {
		if err := e.resolveWaitLocked(capabilityID, nil); err != nil {
			return "", err
		}
		// resolveWaitLocked clears wait metadata; restore the deadline reason so
		// the resume cause is retained for diagnostics.
		resumed := e.states[capabilityID]
		resumed.WaitDeadlineReason = WaitDeadlineReasonContinued
		e.states[capabilityID] = resumed
	}
	e.refreshStatusLocked()
	return policy, nil
}

// SetExecutionDeadline sets the execution's maximum-lifetime deadline. A nil
// deadline disables the limit. The time is copied and normalized to UTC so a
// caller mutating its own value cannot change the stored deadline.
func (e *Execution) SetExecutionDeadline(deadline *time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if deadline == nil {
		e.executionDeadline = nil
		return
	}
	ts := deadline.UTC()
	e.executionDeadline = &ts
}

// RefreshStatus re-derives whether the execution is waiting. It is exposed so a
// caller that changes capability states through other mutators can update the
// derived execution status after a committed transition.
func (e *Execution) RefreshStatus() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.refreshStatusLocked()
}

// refreshStatusLocked derives StatusWaiting while the caller holds e.mu. An
// execution is waiting only while it has waiting capabilities and no runnable
// work. Terminal statuses are never overwritten.
func (e *Execution) refreshStatusLocked() {
	if e.status != StatusRunning && e.status != StatusWaiting {
		return
	}
	if e.hasWaitingWorkLocked() && !e.hasRunnableWorkLocked() {
		e.status = StatusWaiting
		return
	}
	if e.status == StatusWaiting {
		e.status = StatusRunning
	}
}

func (e *Execution) hasWaitingWorkLocked() bool {
	for _, state := range e.states {
		if state.Status == CapabilityWaiting || state.Status == CapabilityRetryWait {
			return true
		}
	}
	return false
}

// hasRunnableWorkLocked reports whether any capability can make progress
// without an external delivery: a ready or running capability, or a pending one
// whose inputs are already satisfied.
func (e *Execution) hasRunnableWorkLocked() bool {
	for id, state := range e.states {
		switch state.Status {
		case CapabilityReady, CapabilityRunning:
			return true
		case CapabilityPending:
			if e.pendingIsRunnableLocked(id) {
				return true
			}
		}
	}
	return false
}

func (e *Execution) pendingIsRunnableLocked(capabilityID shared.ID) bool {
	if e.Blueprint == nil {
		return false
	}
	node, exists := e.Blueprint.Nodes[capabilityID]
	if !exists {
		return false
	}
	if len(node.Predecessors) == 0 {
		for _, entryID := range e.Blueprint.EntryCapabilityIDs {
			if entryID == capabilityID {
				return true
			}
		}
		return false
	}
	ji := e.joinState[capabilityID]
	if ji == nil {
		return false
	}
	if ji.Dispatched {
		return true
	}
	for _, predecessor := range node.Predecessors {
		if !ji.CompletedBindings[predecessor.BindingID] {
			return false
		}
	}
	return true
}

// releaseWorkerLocked and acquireWorkerLocked adjust the in-flight count while
// the caller holds e.mu. The counter is clamped at zero on release because a
// restored snapshot may not agree with the state being released.
func (e *Execution) releaseWorkerLocked() {
	if e.inFlight > 0 {
		e.inFlight--
	}
}

func (e *Execution) acquireWorkerLocked() {
	e.inFlight++
}

// clearWaitMetadataLocked resets wait metadata while the caller holds e.mu.
// Diagnostics recorded by a deadline policy are intentionally cleared on a
// normal resolution; cancellation retains them and does not call this helper.
func (e *Execution) clearWaitMetadataLocked(state *CapabilityExecutionState) {
	state.WaitUntil = nil
	state.WaitReason = ""
	state.CorrelationKey = ""
	state.WaitType = ""
	state.ExpectedSignal = ""
	state.PayloadSchema = ""
	state.WaitDeadlinePolicy = ""
	state.WaitDeadlineReason = ""
	state.DeliveredPayload = nil
}
