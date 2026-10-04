package execution

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/types"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

func newScopedExecution(t *testing.T, instanceID shared.ID) *Execution {
	t.Helper()
	exec, err := NewExecution(blueprintFor("cap_a", "cap_b"), shared.NewID("request_"), instanceID)
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	if err := exec.Start(map[string]any{}, 2); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := exec.MarkCapabilityReady("cap_a", map[string]any{}); err != nil {
		t.Fatalf("MarkCapabilityReady: %v", err)
	}
	if err := exec.MarkCapabilityRunning("cap_a"); err != nil {
		t.Fatalf("MarkCapabilityRunning: %v", err)
	}
	if err := exec.MarkCapabilityReady("cap_b", map[string]any{}); err != nil {
		t.Fatalf("MarkCapabilityReady: %v", err)
	}
	return exec
}

// blueprintFor builds the simplest plan that exercises the execution state
// machine: independent capabilities, no bindings, so nothing is scheduled by the
// planner and every transition is driven explicitly by the test.
func blueprintFor(capabilityIDs ...shared.ID) *types.ExecutionBlueprint {
	blueprint := &types.ExecutionBlueprint{
		Metadata:           shared.Metadata{ID: "sys_test", Name: "sys_test", Version: "1.0.0"},
		Nodes:              make(map[shared.ID]types.ExecutionNode, len(capabilityIDs)),
		EntryCapabilityIDs: capabilityIDs,
	}
	for _, capabilityID := range capabilityIDs {
		blueprint.Nodes[capabilityID] = types.ExecutionNode{
			Capability: shared.Capability{Metadata: shared.Metadata{ID: capabilityID, Name: string(capabilityID)}},
		}
	}
	return blueprint
}

// Cancellation is only reachable if it produces the state the rest of the system
// already treats as terminal. StatusCancelled existed but nothing could set it.
func TestMarkCancelledReachesTheCancelledTerminalState(t *testing.T) {
	exec := newScopedExecution(t, "inst_1")

	if !exec.MarkCancelled(errors.New("cancelled by operator")) {
		t.Fatal("MarkCancelled() = false, want true for a running execution")
	}
	if got := exec.Status(); got != StatusCancelled {
		t.Fatalf("Status() = %s, want %s", got, StatusCancelled)
	}
	if !exec.IsTerminal() {
		t.Fatal("IsTerminal() = false, want true after cancellation")
	}
	if got := exec.CompletedAt(); got.IsZero() {
		t.Fatal("CompletedAt() is zero, want the moment the execution became terminal")
	}
	if exec.Error() != "cancelled by operator" {
		t.Fatalf("Error() = %q, want the cancellation reason", exec.Error())
	}
}

// A terminal execution must not report work as outstanding, or its persisted
// snapshot contradicts its own status.
func TestMarkCancelledDrainsTheInFlightCounter(t *testing.T) {
	exec := newScopedExecution(t, "inst_1")
	if exec.InFlight() != 2 {
		t.Fatalf("InFlight() = %d, want 2 before cancelling", exec.InFlight())
	}
	if !exec.MarkCancelled(nil) {
		t.Fatal("MarkCancelled() = false, want true")
	}
	if got := exec.InFlight(); got != 0 {
		t.Fatalf("InFlight() = %d, want 0 on a cancelled execution", got)
	}
}

// A capability that was abandoned was not necessarily broken. Reporting it as
// failed would blame the implementation for a decision somebody else took.
func TestMarkCancelledAbandonsUnfinishedCapabilitiesWithoutBlamingThem(t *testing.T) {
	exec := newScopedExecution(t, "inst_1")

	if err := exec.MarkCapabilityCompleted("cap_a", map[string]any{"ok": true}); err != nil {
		t.Fatalf("MarkCapabilityCompleted: %v", err)
	}
	if !exec.MarkCancelled(errors.New("stop")) {
		t.Fatal("MarkCancelled() = false, want true")
	}

	if got := exec.CapabilityState("cap_a").Status; got != CapabilityCompleted {
		t.Errorf("cap_a status = %s, want %s (it finished before the cancellation)", got, CapabilityCompleted)
	}
	if got := exec.CapabilityState("cap_b").Status; got != CapabilityCancelled {
		t.Errorf("cap_b status = %s, want %s", got, CapabilityCancelled)
	}
	if got := exec.CapabilityState("cap_b").Error; got != "stop" {
		t.Errorf("cap_b error = %q, want the cancellation reason", got)
	}
	if got := exec.CapabilityState("cap_a").Error; got != "" {
		t.Errorf("cap_a error = %q, want empty (it completed successfully)", got)
	}
}

// A capability that already reported an outcome keeps it. Cancelling an
// execution must not rewrite history.
func TestMarkCancelledPreservesAnAlreadyFailedCapability(t *testing.T) {
	exec := newScopedExecution(t, "inst_1")
	exec.MarkCapabilityFailed("cap_b", errors.New("capability broke"))

	if !exec.MarkCancelled(errors.New("stop")) {
		t.Fatal("MarkCancelled() = false, want true")
	}
	state := exec.CapabilityState("cap_b")
	if state.Status != CapabilityFailed {
		t.Errorf("cap_b status = %s, want %s", state.Status, CapabilityFailed)
	}
	if state.Error != "capability broke" {
		t.Errorf("cap_b error = %q, want the capability's own failure", state.Error)
	}
}

// Exactly one terminal transition may take effect. Two callers racing to end the
// same execution must not both be told they succeeded, and the second must not
// overwrite the first's reason.
func TestTerminalTransitionsAreExclusive(t *testing.T) {
	reason := errors.New("first")

	exec := newScopedExecution(t, "inst_1")
	if !exec.MarkCancelled(reason) {
		t.Fatal("MarkCancelled() = false, want true for the first terminal transition")
	}
	if exec.MarkCompleted() {
		t.Error("MarkCompleted() = true after cancellation, want false")
	}
	if exec.MarkFailed(errors.New("second")) {
		t.Error("MarkFailed() = true after cancellation, want false")
	}
	if exec.MarkCancelled(errors.New("second")) {
		t.Error("MarkCancelled() = true for a second cancellation, want false")
	}
	if got := exec.Status(); got != StatusCancelled {
		t.Errorf("Status() = %s, want %s", got, StatusCancelled)
	}
	if exec.Error() != "first" {
		t.Errorf("Error() = %q, want the first reason to survive", exec.Error())
	}
}

// A cancelled execution must be observable without polling: Wait returns as soon
// as the execution is terminal rather than blocking until its caller's context
// expires.
// The engine observes an aborted invocation *after* the cancellation is already
// recorded. That late error is a consequence of the stop, not a capability fault,
// so it must not overwrite CapabilityCancelled -- otherwise an operator who
// cancels a slow call sees the capability blamed for it.
func TestAFailureArrivingAfterCancellationDoesNotBlamTheCapability(t *testing.T) {
	exec := newScopedExecution(t, "inst_1")
	if !exec.MarkCancelled(errors.New("stopped by operator")) {
		t.Fatal("MarkCancelled() = false, want true")
	}

	if exec.MarkCapabilityFailed("cap_b", context.Canceled) {
		t.Error("MarkCapabilityFailed() = true after cancellation, want false")
	}
	state := exec.CapabilityState("cap_b")
	if state.Status != CapabilityCancelled {
		t.Errorf("cap_b status = %s, want %s", state.Status, CapabilityCancelled)
	}
	if state.Error != "stopped by operator" {
		t.Errorf("cap_b error = %q, want the cancellation reason to survive", state.Error)
	}
}

// The same guard protects a genuine success: a late error for a capability that
// already completed must not rewrite success into failure.
func TestAFailureArrivingAfterCompletionDoesNotRewriteSuccess(t *testing.T) {
	exec := newScopedExecution(t, "inst_1")
	if err := exec.MarkCapabilityCompleted("cap_a", map[string]any{"ok": true}); err != nil {
		t.Fatalf("MarkCapabilityCompleted: %v", err)
	}

	if exec.MarkCapabilityFailed("cap_a", errors.New("late error")) {
		t.Error("MarkCapabilityFailed() = true after completion, want false")
	}
	state := exec.CapabilityState("cap_a")
	if state.Status != CapabilityCompleted {
		t.Errorf("cap_a status = %s, want %s", state.Status, CapabilityCompleted)
	}
	if state.Error != "" {
		t.Errorf("cap_a error = %q, want empty", state.Error)
	}
}

// Work that was actually still running must still be reportable as failed, so the
// guard cannot be so broad that real failures are swallowed.
func TestAFailureIsStillRecordedWhileTheCapabilityIsRunning(t *testing.T) {
	exec := newScopedExecution(t, "inst_1")

	if !exec.MarkCapabilityFailed("cap_a", errors.New("genuinely broken")) {
		t.Error("MarkCapabilityFailed() = false for a running capability, want true")
	}
	if got := exec.CapabilityState("cap_a").Status; got != CapabilityFailed {
		t.Errorf("cap_a status = %s, want %s", got, CapabilityFailed)
	}
	if got := exec.CapabilityState("cap_a").Error; got != "genuinely broken" {
		t.Errorf("cap_a error = %q, want the capability's own failure", got)
	}
}

// A capability that is not in the blueprint has no state to record, and saying so
// is what lets the caller skip announcing a failure that nothing holds.
func TestAFailureForAnUnknownCapabilityReportsNoChange(t *testing.T) {
	exec := newScopedExecution(t, "inst_1")
	if exec.MarkCapabilityFailed("cap_missing", errors.New("nope")) {
		t.Error("MarkCapabilityFailed() = true for an unknown capability, want false")
	}
}

// Detached work is tracked by its own execution. Cancelling the parent must not
// report the handoff as cancelled -- the task is still running, under its own
// scope, and its state belongs to it.
func TestMarkCancelledLeavesDetachedCapabilitiesToTheirOwnTask(t *testing.T) {
	exec := newScopedExecution(t, "inst_1")
	if err := exec.MarkCapabilityDetached("cap_b"); err != nil {
		t.Fatalf("MarkCapabilityDetached: %v", err)
	}

	if !exec.MarkCancelled(errors.New("stop")) {
		t.Fatal("MarkCancelled() = false, want true")
	}
	if got := exec.CapabilityState("cap_b").Status; got != CapabilityDetached {
		t.Errorf("cap_b status = %s, want %s", got, CapabilityDetached)
	}
}

func TestWaitUnblocksOnCancellation(t *testing.T) {
	exec := newScopedExecution(t, "inst_1")
	if !exec.MarkCancelled(errors.New("stop")) {
		t.Fatal("MarkCancelled() = false, want true")
	}

	// A generous guard: the assertion is that Wait returns on its own, not that
	// it returns quickly.
	waitCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- exec.Wait(waitCtx) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Wait() = nil, want the cancellation reason")
		}
	case <-waitCtx.Done():
		t.Fatal("Wait() blocked after the execution was cancelled")
	}
}

// A cancelled execution is reloaded from a snapshot by a process that will never
// resume it, so the terminal signal has to survive serialization.
func TestCancelledExecutionRoundTripsThroughSnapshot(t *testing.T) {
	exec := newScopedExecution(t, "inst_1")
	if !exec.MarkCancelled(errors.New("stop")) {
		t.Fatal("MarkCancelled() = false, want true")
	}

	data, err := MarshalExecution(exec)
	if err != nil {
		t.Fatalf("MarshalExecution: %v", err)
	}
	restored, err := UnmarshalExecution(data)
	if err != nil {
		t.Fatalf("UnmarshalExecution: %v", err)
	}

	if got := restored.Status(); got != StatusCancelled {
		t.Fatalf("restored Status() = %s, want %s", got, StatusCancelled)
	}
	if restored.Error() != "stop" {
		t.Errorf("restored Error() = %q, want the cancellation reason to persist", restored.Error())
	}
	if got := restored.CapabilityState("cap_b").Status; got != CapabilityCancelled {
		t.Errorf("restored cap_b status = %s, want %s", got, CapabilityCancelled)
	}
	if got := restored.InFlight(); got != 0 {
		t.Errorf("restored InFlight() = %d, want 0", got)
	}
}
