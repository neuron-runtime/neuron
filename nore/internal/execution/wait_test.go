package execution

import (
	"errors"
	"strings"
	"testing"
	"time"

	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

// newWaitExecution builds a running execution whose capabilities are all entry
// points and all pending, so a test can drive each transition explicitly.
func newWaitExecution(t *testing.T, capabilityIDs ...shared.ID) *Execution {
	t.Helper()
	exec, err := NewExecution(blueprintFor(capabilityIDs...), shared.NewID("corr_"), "inst_1", "", "", "", "")
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	if err := exec.Start(map[string]any{}, len(capabilityIDs)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return exec
}

func makeRunning(t *testing.T, exec *Execution, capabilityID shared.ID) {
	t.Helper()
	if err := exec.MarkCapabilityReady(capabilityID, map[string]any{}); err != nil {
		t.Fatalf("MarkCapabilityReady(%s): %v", capabilityID, err)
	}
	if err := exec.MarkCapabilityRunning(capabilityID); err != nil {
		t.Fatalf("MarkCapabilityRunning(%s): %v", capabilityID, err)
	}
}

func signalWaitSpec(until *time.Time, policy WaitDeadlinePolicy) WaitSpec {
	return WaitSpec{
		Type:           WaitTypeSignal,
		Reason:         "awaiting approval",
		ExpectedSignal: "approval",
		WaitUntil:      until,
		PayloadSchema:  `{"type":"object"}`,
		DeadlinePolicy: policy,
	}
}

func TestEnterWaitSetsCompleteTypedMetadata(t *testing.T) {
	exec := newWaitExecution(t, "cap_a", "cap_b")
	makeRunning(t, exec, "cap_a")

	until := time.Now().UTC().Add(time.Hour)
	spec := signalWaitSpec(&until, WaitDeadlinePolicyCancel)
	if err := exec.EnterWait("cap_a", spec); err != nil {
		t.Fatalf("EnterWait: %v", err)
	}

	state := exec.CapabilityState("cap_a")
	if state.Status != CapabilityWaiting {
		t.Errorf("status = %s, want %s", state.Status, CapabilityWaiting)
	}
	if state.WaitType != WaitTypeSignal {
		t.Errorf("wait type = %q, want %q", state.WaitType, WaitTypeSignal)
	}
	if state.WaitReason != "awaiting approval" {
		t.Errorf("wait reason = %q", state.WaitReason)
	}
	if state.CorrelationKey != "cap_a" {
		t.Errorf("correlation key = %q, want derived capability ID", state.CorrelationKey)
	}
	if state.ExpectedSignal != "approval" {
		t.Errorf("expected signal = %q", state.ExpectedSignal)
	}
	if state.WaitUntil == nil || !state.WaitUntil.Equal(until) {
		t.Errorf("wait until = %v, want %v", state.WaitUntil, until)
	}
	if state.PayloadSchema != `{"type":"object"}` {
		t.Errorf("payload schema = %q", state.PayloadSchema)
	}
	if state.WaitDeadlinePolicy != WaitDeadlinePolicyCancel {
		t.Errorf("deadline policy = %q, want %q", state.WaitDeadlinePolicy, WaitDeadlinePolicyCancel)
	}
	if state.WaitDeadlineReason != "" {
		t.Errorf("deadline reason = %q, want empty on entry", state.WaitDeadlineReason)
	}
	if state.DeliveredPayload != nil {
		t.Errorf("delivered payload = %v, want nil on entry", state.DeliveredPayload)
	}
	if state.DispatchState != "" {
		t.Errorf("dispatch state = %q, want cleared on entry", state.DispatchState)
	}
	if got := exec.InFlight(); got != 1 {
		t.Errorf("in-flight = %d, want 1 (cap_a released its worker)", got)
	}
}

func TestEnterWaitDefaultsPolicyToFailAndDerivesCorrelationKey(t *testing.T) {
	exec := newWaitExecution(t, "cap_a")
	makeRunning(t, exec, "cap_a")

	if err := exec.EnterWait("cap_a", WaitSpec{
		Type:           WaitTypeTimer,
		ExpectedSignal: "unused",
		WaitUntil:      timePtr(time.Now().UTC().Add(time.Hour)),
	}); err != nil {
		t.Fatalf("EnterWait: %v", err)
	}
	state := exec.CapabilityState("cap_a")
	if state.WaitDeadlinePolicy != WaitDeadlinePolicyFail {
		t.Errorf("deadline policy = %q, want default %q", state.WaitDeadlinePolicy, WaitDeadlinePolicyFail)
	}
	if state.CorrelationKey != "cap_a" {
		t.Errorf("correlation key = %q, want derived capability ID", state.CorrelationKey)
	}
}

func TestEnterWaitValidatesSpec(t *testing.T) {
	cases := []struct {
		name       string
		spec       WaitSpec
		wantErrSub string
	}{
		{"unknown type", WaitSpec{Type: "bogus"}, "invalid wait type"},
		{"signal without expected signal", WaitSpec{Type: WaitTypeSignal}, "expected signal"},
		{"timer without deadline", WaitSpec{Type: WaitTypeTimer}, "requires WaitUntil"},
		{"deadline without deadline", WaitSpec{Type: WaitTypeDeadline}, "requires WaitUntil"},
		{"unknown policy", signalWaitSpec(nil, "bogus"), "invalid wait deadline policy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exec := newWaitExecution(t, "cap_a")
			err := exec.EnterWait("cap_a", tc.spec)
			if err == nil || !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Fatalf("EnterWait error = %v, want substring %q", err, tc.wantErrSub)
			}
			if got := exec.CapabilityState("cap_a").Status; got != CapabilityPending {
				t.Errorf("status = %s, want unchanged %s after rejected wait", got, CapabilityPending)
			}
		})
	}
}

func TestResolveWaitResumesAndRecordsDeliveredPayload(t *testing.T) {
	exec := newWaitExecution(t, "cap_a", "cap_b")
	makeRunning(t, exec, "cap_a")
	if err := exec.EnterWait("cap_a", signalWaitSpec(nil, WaitDeadlinePolicyFail)); err != nil {
		t.Fatalf("EnterWait: %v", err)
	}
	if err := exec.ResolveWait("cap_a", map[string]any{"approved": true}); err != nil {
		t.Fatalf("ResolveWait: %v", err)
	}

	state := exec.CapabilityState("cap_a")
	if state.Status != CapabilityReady {
		t.Errorf("status = %s, want %s", state.Status, CapabilityReady)
	}
	if state.WaitType != "" || state.WaitUntil != nil || state.ExpectedSignal != "" ||
		state.CorrelationKey != "" || state.PayloadSchema != "" || state.WaitReason != "" ||
		state.WaitDeadlinePolicy != "" || state.WaitDeadlineReason != "" {
		t.Errorf("wait metadata was not cleared on resolution: %+v", state)
	}
	if state.DeliveredPayload["approved"] != true {
		t.Errorf("delivered payload = %v, want the resume input", state.DeliveredPayload)
	}
	if got := exec.InFlight(); got != 2 {
		t.Errorf("in-flight = %d, want 2 (worker reacquired)", got)
	}
}

func TestSignalDelegatesToResolveWait(t *testing.T) {
	exec := newWaitExecution(t, "cap_a")
	makeRunning(t, exec, "cap_a")
	if err := exec.EnterWait("cap_a", signalWaitSpec(nil, WaitDeadlinePolicyFail)); err != nil {
		t.Fatalf("EnterWait: %v", err)
	}
	if err := exec.Signal("cap_a"); err != nil {
		t.Fatalf("Signal: %v", err)
	}
	if got := exec.CapabilityState("cap_a").Status; got != CapabilityReady {
		t.Errorf("status = %s, want %s", got, CapabilityReady)
	}
}

func TestStatusWaitingDerivedOnlyWithoutRunnableWork(t *testing.T) {
	exec := newWaitExecution(t, "cap_a", "cap_b")
	makeRunning(t, exec, "cap_a")
	makeRunning(t, exec, "cap_b")

	if err := exec.EnterWait("cap_a", signalWaitSpec(nil, WaitDeadlinePolicyFail)); err != nil {
		t.Fatalf("EnterWait cap_a: %v", err)
	}
	if exec.Status() != StatusRunning {
		t.Errorf("status = %s, want %s while cap_b is runnable", exec.Status(), StatusRunning)
	}
	if exec.IsWaiting() {
		t.Error("IsWaiting() = true while cap_b is runnable")
	}
	if got := exec.WaitingCapabilities(); len(got) != 1 || got[0] != "cap_a" {
		t.Errorf("waiting capabilities = %v, want [cap_a]", got)
	}

	if err := exec.EnterWait("cap_b", signalWaitSpec(nil, WaitDeadlinePolicyFail)); err != nil {
		t.Fatalf("EnterWait cap_b: %v", err)
	}
	if exec.Status() != StatusWaiting {
		t.Errorf("status = %s, want %s once no runnable work remains", exec.Status(), StatusWaiting)
	}
	if !exec.IsWaiting() {
		t.Error("IsWaiting() = false while every capability waits")
	}

	if err := exec.ResolveWait("cap_a", nil); err != nil {
		t.Fatalf("ResolveWait: %v", err)
	}
	if exec.Status() != StatusRunning {
		t.Errorf("status = %s, want %s once work resumes", exec.Status(), StatusRunning)
	}
}

func TestBeginRetryWaitReleasesWorkerAndDerivesWaiting(t *testing.T) {
	exec := newWaitExecution(t, "cap_a")
	makeRunning(t, exec, "cap_a")

	next := time.Now().UTC().Add(time.Minute)
	if err := exec.BeginRetryWait("cap_a", next); err != nil {
		t.Fatalf("BeginRetryWait: %v", err)
	}
	state := exec.CapabilityState("cap_a")
	if state.Status != CapabilityRetryWait {
		t.Errorf("status = %s, want %s", state.Status, CapabilityRetryWait)
	}
	if state.NextAttemptAt == nil || !state.NextAttemptAt.Equal(next) {
		t.Errorf("next attempt = %v, want %v", state.NextAttemptAt, next)
	}
	if got := exec.InFlight(); got != 0 {
		t.Errorf("in-flight = %d, want 0 (retry waits occupy no worker)", got)
	}
	if exec.Status() != StatusWaiting {
		t.Errorf("status = %s, want %s", exec.Status(), StatusWaiting)
	}

	if err := exec.ResumeRetryWait("cap_a"); err != nil {
		t.Fatalf("ResumeRetryWait: %v", err)
	}
	resumed := exec.CapabilityState("cap_a")
	if resumed.Status != CapabilityReady {
		t.Errorf("status = %s, want %s", resumed.Status, CapabilityReady)
	}
	if resumed.NextAttemptAt != nil {
		t.Errorf("next attempt = %v, want cleared", resumed.NextAttemptAt)
	}
	if got := exec.InFlight(); got != 1 {
		t.Errorf("in-flight = %d, want 1", got)
	}
	if exec.Status() != StatusRunning {
		t.Errorf("status = %s, want %s", exec.Status(), StatusRunning)
	}
}

func TestBeginRetryWaitRejectsMissingAttemptTime(t *testing.T) {
	exec := newWaitExecution(t, "cap_a")
	if err := exec.BeginRetryWait("cap_a", time.Time{}); err == nil {
		t.Fatal("BeginRetryWait with zero next attempt time succeeded, want error")
	}
}

func TestNilWaitUntilIsNeverDue(t *testing.T) {
	exec := newWaitExecution(t, "cap_a", "cap_b")
	makeRunning(t, exec, "cap_a")
	if err := exec.EnterWait("cap_a", signalWaitSpec(nil, WaitDeadlinePolicyFail)); err != nil {
		t.Fatalf("EnterWait: %v", err)
	}
	if exec.Status() != StatusRunning {
		t.Fatalf("status = %s, want %s while cap_b is runnable", exec.Status(), StatusRunning)
	}

	// Even a far-future clock must not make a nil-deadline wait expire.
	if _, err := exec.ExpireWait("cap_a", time.Now().Add(1000*time.Hour)); err == nil {
		t.Fatal("ExpireWait on a nil WaitUntil succeeded, want error")
	}

	restored := restoreForRecovery(t, exec, blueprintFor("cap_a", "cap_b"))
	dispatch := restored.RecoverableDispatch()
	if len(dispatch) != 1 || dispatch[0] != "cap_b" {
		t.Fatalf("dispatch = %v, want only [cap_b]", dispatch)
	}
	if got := restored.CapabilityState("cap_a").Status; got != CapabilityWaiting {
		t.Errorf("nil-deadline signal wait status = %s, want unchanged %s", got, CapabilityWaiting)
	}
	if got := restored.InFlight(); got != 1 {
		t.Errorf("in-flight = %d, want 1 (waiting capability not counted)", got)
	}
	// A nil WaitUntil is never due, so recovery cannot clear this wait and the
	// derived status must track the resumed sibling rather than report waiting.
	if got := restored.Status(); got != StatusRunning {
		t.Errorf("status = %s, want %s with a runnable sibling revived", got, StatusRunning)
	}
}

func TestRecoverableDispatchResumesOnlyElapsedTimeWaits(t *testing.T) {
	exec := newWaitExecution(t, "timer", "deadline", "signal", "worker")
	past := time.Now().UTC().Add(-time.Hour)
	future := time.Now().UTC().Add(time.Hour)

	makeRunning(t, exec, "timer")
	if err := exec.EnterWait("timer", WaitSpec{Type: WaitTypeTimer, WaitUntil: &past}); err != nil {
		t.Fatalf("EnterWait timer: %v", err)
	}
	makeRunning(t, exec, "deadline")
	if err := exec.EnterWait("deadline", WaitSpec{Type: WaitTypeDeadline, WaitUntil: &future}); err != nil {
		t.Fatalf("EnterWait deadline: %v", err)
	}
	makeRunning(t, exec, "signal")
	// A signal wait with an elapsed WaitUntil must still not resume from time.
	if err := exec.EnterWait("signal", signalWaitSpec(&past, WaitDeadlinePolicyFail)); err != nil {
		t.Fatalf("EnterWait signal: %v", err)
	}

	restored := restoreForRecovery(t, exec, blueprintFor("timer", "deadline", "signal", "worker"))
	dispatch := restored.RecoverableDispatch()
	if want := []shared.ID{"timer", "worker"}; !equalIDs(dispatch, want) {
		t.Fatalf("dispatch = %v, want %v", dispatch, want)
	}
	for _, id := range []shared.ID{"deadline", "signal"} {
		if got := restored.CapabilityState(id).Status; got != CapabilityWaiting {
			t.Errorf("%s status = %s, want unchanged %s", id, got, CapabilityWaiting)
		}
	}
	if got := restored.InFlight(); got != 2 {
		t.Errorf("in-flight = %d, want 2 (only resumed capabilities)", got)
	}
}

func TestExpireWaitPolicies(t *testing.T) {
	cases := []struct {
		policy       WaitDeadlinePolicy
		wantStatus   CapabilityStatus
		wantReason   WaitDeadlineReason
		wantError    string
		wantInFlight int
	}{
		{WaitDeadlinePolicyFail, CapabilityFailed, WaitDeadlineReasonTimeout, "wait_timeout", 1},
		{WaitDeadlinePolicyCancel, CapabilityCancelled, WaitDeadlineReasonCancelled, "wait deadline exceeded: cancel policy", 1},
		{WaitDeadlinePolicyContinue, CapabilityReady, WaitDeadlineReasonContinued, "", 2},
	}
	for _, tc := range cases {
		t.Run(string(tc.policy), func(t *testing.T) {
			exec := newWaitExecution(t, "cap_a", "cap_b")
			makeRunning(t, exec, "cap_a")
			past := time.Now().UTC().Add(-time.Hour)
			if err := exec.EnterWait("cap_a", signalWaitSpec(&past, tc.policy)); err != nil {
				t.Fatalf("EnterWait: %v", err)
			}
			got, err := exec.ExpireWait("cap_a", time.Now())
			if err != nil {
				t.Fatalf("ExpireWait: %v", err)
			}
			if got != tc.policy {
				t.Errorf("applied policy = %q, want %q", got, tc.policy)
			}
			state := exec.CapabilityState("cap_a")
			if state.Status != tc.wantStatus {
				t.Errorf("status = %s, want %s", state.Status, tc.wantStatus)
			}
			if state.WaitDeadlineReason != tc.wantReason {
				t.Errorf("deadline reason = %q, want %q", state.WaitDeadlineReason, tc.wantReason)
			}
			if state.Error != tc.wantError {
				t.Errorf("error = %q, want %q", state.Error, tc.wantError)
			}
			if inFlight := exec.InFlight(); inFlight != tc.wantInFlight {
				t.Errorf("in-flight = %d, want %d", inFlight, tc.wantInFlight)
			}
		})
	}
}

func TestExpireWaitRejectsNonDueWaits(t *testing.T) {
	future := time.Now().UTC().Add(time.Hour)
	t.Run("future deadline", func(t *testing.T) {
		exec := newWaitExecution(t, "cap_a")
		makeRunning(t, exec, "cap_a")
		if err := exec.EnterWait("cap_a", signalWaitSpec(&future, WaitDeadlinePolicyFail)); err != nil {
			t.Fatalf("EnterWait: %v", err)
		}
		if _, err := exec.ExpireWait("cap_a", time.Now()); err == nil {
			t.Fatal("ExpireWait on a future deadline succeeded, want error")
		}
	})
	t.Run("not waiting", func(t *testing.T) {
		exec := newWaitExecution(t, "cap_a")
		if _, err := exec.ExpireWait("cap_a", time.Now()); err == nil {
			t.Fatal("ExpireWait on a pending capability succeeded, want error")
		}
	})
}

func TestCancellationRetainsWaitMetadataAndDiscardsJoinInputs(t *testing.T) {
	exec, err := NewExecution(recoveryJoinBlueprint(), shared.NewID("corr"), "inst_1", "", "", "", "")
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	if err := exec.Start(map[string]any{}, 2); err != nil {
		t.Fatalf("Start: %v", err)
	}
	makeRunning(t, exec, "a")
	past := time.Now().UTC().Add(-time.Hour)
	if err := exec.EnterWait("a", signalWaitSpec(&past, WaitDeadlinePolicyFail)); err != nil {
		t.Fatalf("EnterWait: %v", err)
	}
	if _, _, err := exec.RecordJoinContribution("c", "a-c", map[string]any{"x": "buffered"}); err != nil {
		t.Fatalf("RecordJoinContribution: %v", err)
	}
	before := exec.CapabilityState("a")

	if !exec.MarkCancelled(errors.New("operator cancel")) {
		t.Fatal("MarkCancelled() = false, want true")
	}
	after := exec.CapabilityState("a")
	if after.Status != CapabilityCancelled {
		t.Errorf("status = %s, want %s", after.Status, CapabilityCancelled)
	}
	if after.WaitType != before.WaitType || after.ExpectedSignal != before.ExpectedSignal ||
		after.CorrelationKey != before.CorrelationKey || after.PayloadSchema != before.PayloadSchema ||
		after.WaitReason != before.WaitReason || after.WaitDeadlinePolicy != before.WaitDeadlinePolicy {
		t.Errorf("wait diagnostics were not retained on cancellation: before=%+v after=%+v", before, after)
	}
	if after.WaitUntil == nil || !after.WaitUntil.Equal(*before.WaitUntil) {
		t.Errorf("wait until = %v, want retained %v", after.WaitUntil, before.WaitUntil)
	}
	if got := exec.JoinState("c"); got != nil {
		t.Errorf("buffered join inputs were not discarded on cancellation: %+v", got)
	}
}

func timePtr(t time.Time) *time.Time {
	return &t
}

func equalIDs(a, b []shared.ID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
