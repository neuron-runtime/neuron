package execution

import (
	"reflect"
	"testing"
	"time"

	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

// restoreDurableWaitingExecution round-trips a fully waiting execution through
// the durable snapshot and reattaches its blueprint, mirroring instance
// recovery. It asserts the durable status is StatusWaiting so a test cannot pass
// with a running execution that trivially reaches RecoverableDispatch.
func restoreDurableWaitingExecution(t *testing.T, exec *Execution, ids ...shared.ID) *Execution {
	t.Helper()
	if exec.Status() != StatusWaiting {
		t.Fatalf("test setup: status = %s, want %s before restore", exec.Status(), StatusWaiting)
	}
	restored := restoreForRecovery(t, exec, blueprintFor(ids...))
	if restored.Status() != StatusWaiting {
		t.Fatalf("test setup: restored status = %s, want durable %s", restored.Status(), StatusWaiting)
	}
	return restored
}

func assertWaitMetadataCleared(t *testing.T, state CapabilityExecutionState) {
	t.Helper()
	if state.WaitType != "" || state.WaitUntil != nil || state.WaitReason != "" ||
		state.CorrelationKey != "" || state.ExpectedSignal != "" || state.PayloadSchema != "" ||
		state.WaitDeadlinePolicy != "" || state.WaitDeadlineReason != "" || state.DeliveredPayload != nil {
		t.Errorf("wait metadata was not cleared on resume: %+v", state)
	}
}

// TestRecoverableDispatchResumesElapsedWaitsFromDurableWaiting covers the core
// defect: a fully waiting execution is restored with durable StatusWaiting and
// its elapsed timer/deadline waits must still resume. Before the fix the
// Running-only guard returned nil, leaving elapsed timers stalled forever.
func TestRecoverableDispatchResumesElapsedWaitsFromDurableWaiting(t *testing.T) {
	exec := newWaitExecution(t, "timer", "deadline")
	past := time.Now().UTC().Add(-time.Hour)

	makeRunning(t, exec, "timer")
	if err := exec.EnterWait("timer", WaitSpec{Type: WaitTypeTimer, WaitUntil: &past}); err != nil {
		t.Fatalf("EnterWait timer: %v", err)
	}
	makeRunning(t, exec, "deadline")
	if err := exec.EnterWait("deadline", WaitSpec{Type: WaitTypeDeadline, WaitUntil: &past}); err != nil {
		t.Fatalf("EnterWait deadline: %v", err)
	}
	restored := restoreDurableWaitingExecution(t, exec, "timer", "deadline")

	dispatch := restored.RecoverableDispatch()
	if want := []shared.ID{"deadline", "timer"}; !reflect.DeepEqual(dispatch, want) {
		t.Fatalf("dispatch = %v, want %v", dispatch, want)
	}
	for _, id := range []shared.ID{"timer", "deadline"} {
		state := restored.CapabilityState(id)
		if state.Status != CapabilityReady {
			t.Errorf("%s status = %s, want %s after elapsed resume", id, state.Status, CapabilityReady)
		}
		assertWaitMetadataCleared(t, state)
	}
	if got := restored.InFlight(); got != 2 {
		t.Errorf("in-flight = %d, want 2 (both resumed capabilities reacquired a worker)", got)
	}
	if got := restored.Status(); got != StatusRunning {
		t.Errorf("status = %s, want re-derived %s once no waiting work remains", got, StatusRunning)
	}
}

// TestRecoverableDispatchResumesElapsedRetryWaitFromDurableWaiting covers the
// durable retry path: a retry wait is represented as CapabilityRetryWait with
// execution-level StatusWaiting, and an elapsed NextAttemptAt must be dispatched
// once after recovery.
func TestRecoverableDispatchResumesElapsedRetryWaitFromDurableWaiting(t *testing.T) {
	exec := newWaitExecution(t, "retrier")
	past := time.Now().UTC().Add(-time.Minute)
	makeRunning(t, exec, "retrier")
	if err := exec.BeginRetryWait("retrier", past); err != nil {
		t.Fatalf("BeginRetryWait: %v", err)
	}
	restored := restoreDurableWaitingExecution(t, exec, "retrier")

	dispatch := restored.RecoverableDispatch()
	if want := []shared.ID{"retrier"}; !reflect.DeepEqual(dispatch, want) {
		t.Fatalf("dispatch = %v, want %v", dispatch, want)
	}
	state := restored.CapabilityState("retrier")
	if state.Status != CapabilityReady {
		t.Fatalf("status = %s, want %s after elapsed retry resume", state.Status, CapabilityReady)
	}
	if state.NextAttemptAt != nil {
		t.Errorf("next attempt = %v, want cleared after retry resume", state.NextAttemptAt)
	}
	if got := restored.InFlight(); got != 1 {
		t.Errorf("in-flight = %d, want 1", got)
	}
	if got := restored.Status(); got != StatusRunning {
		t.Errorf("status = %s, want re-derived %s", got, StatusRunning)
	}
}

// TestRecoverableDispatchKeepsNonDueSignalWaitWaiting is a guard rail: recovering
// a healthy signal wait must not dispatch it, change its capability state, count
// it in flight, or turn the derived status into Running.
func TestRecoverableDispatchKeepsNonDueSignalWaitWaiting(t *testing.T) {
	exec := newWaitExecution(t, "signaller")
	makeRunning(t, exec, "signaller")
	if err := exec.EnterWait("signaller", signalWaitSpec(nil, WaitDeadlinePolicyFail)); err != nil {
		t.Fatalf("EnterWait: %v", err)
	}
	restored := restoreDurableWaitingExecution(t, exec, "signaller")

	dispatch := restored.RecoverableDispatch()
	if len(dispatch) != 0 {
		t.Fatalf("dispatch = %v, want none for a non-due signal wait", dispatch)
	}
	state := restored.CapabilityState("signaller")
	if state.Status != CapabilityWaiting {
		t.Errorf("status = %s, want %s for a non-due signal wait", state.Status, CapabilityWaiting)
	}
	if got := restored.InFlight(); got != 0 {
		t.Errorf("in-flight = %d, want 0 (a waiting capability occupies no worker)", got)
	}
	if got := restored.Status(); got != StatusWaiting {
		t.Errorf("status = %s, want durable %s to be preserved", got, StatusWaiting)
	}
}

// TestRecoverableDispatchKeepsFutureDeadlineWaitWaiting guards the same rule for
// a time-typed wait whose deadline has not passed: a future WaitUntil is not due,
// so recovery must leave the execution waiting.
func TestRecoverableDispatchKeepsFutureDeadlineWaitWaiting(t *testing.T) {
	exec := newWaitExecution(t, "timer")
	future := time.Now().UTC().Add(time.Hour)
	makeRunning(t, exec, "timer")
	if err := exec.EnterWait("timer", WaitSpec{Type: WaitTypeTimer, WaitUntil: &future}); err != nil {
		t.Fatalf("EnterWait: %v", err)
	}
	restored := restoreDurableWaitingExecution(t, exec, "timer")

	if dispatch := restored.RecoverableDispatch(); len(dispatch) != 0 {
		t.Fatalf("dispatch = %v, want none before the deadline passes", dispatch)
	}
	if got := restored.Status(); got != StatusWaiting {
		t.Errorf("status = %s, want durable %s to be preserved", got, StatusWaiting)
	}
}
