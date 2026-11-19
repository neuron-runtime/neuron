package execution

import (
	"encoding/json"
	"testing"
	"time"

	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

func TestSnapshotV3CarriesExecutionDeadlineAndWaitMetadata(t *testing.T) {
	exec := newWaitExecution(t, "cap_a")
	makeRunning(t, exec, "cap_a")

	deadline := time.Now().UTC().Add(24 * time.Hour)
	exec.SetExecutionDeadline(&deadline)
	until := time.Now().UTC().Add(time.Hour)
	if err := exec.EnterWait("cap_a", signalWaitSpec(&until, WaitDeadlinePolicyCancel)); err != nil {
		t.Fatalf("EnterWait: %v", err)
	}

	snap := exec.Snapshot()
	if snap.SchemaVersion != "3" {
		t.Errorf("schema version = %q, want %q", snap.SchemaVersion, "3")
	}
	if snap.ExecutionDeadline == nil || !snap.ExecutionDeadline.Equal(deadline) {
		t.Errorf("snapshot deadline = %v, want %v", snap.ExecutionDeadline, deadline)
	}

	data, err := MarshalExecution(exec)
	if err != nil {
		t.Fatalf("MarshalExecution: %v", err)
	}
	restored, err := UnmarshalExecution(data)
	if err != nil {
		t.Fatalf("UnmarshalExecution: %v", err)
	}
	if got := restored.ExecutionDeadline(); got == nil || !got.Equal(deadline) {
		t.Errorf("restored deadline = %v, want %v", got, deadline)
	}
	if got := restored.Status(); got != StatusWaiting {
		t.Errorf("restored status = %s, want %s", got, StatusWaiting)
	}

	state := restored.CapabilityState("cap_a")
	if state.Status != CapabilityWaiting || state.WaitType != WaitTypeSignal ||
		state.ExpectedSignal != "approval" || state.CorrelationKey != "cap_a" ||
		state.PayloadSchema != `{"type":"object"}` ||
		state.WaitDeadlinePolicy != WaitDeadlinePolicyCancel {
		t.Errorf("wait metadata did not survive v3 round trip: %+v", state)
	}
	if state.WaitUntil == nil || !state.WaitUntil.Equal(until) {
		t.Errorf("restored wait until = %v, want %v", state.WaitUntil, until)
	}
}

func TestSetExecutionDeadlineCopiesValue(t *testing.T) {
	exec := newWaitExecution(t, "cap_a")
	original := time.Now().UTC().Add(24 * time.Hour)
	callerValue := original
	exec.SetExecutionDeadline(&callerValue)

	// Mutating the caller's value must not change the stored deadline.
	callerValue = callerValue.Add(time.Hour)
	if got := exec.ExecutionDeadline(); got == nil || !got.Equal(original) {
		t.Errorf("stored deadline = %v, want the original %v", got, original)
	}

	exec.SetExecutionDeadline(nil)
	if got := exec.ExecutionDeadline(); got != nil {
		t.Errorf("deadline after clearing = %v, want nil", got)
	}
}

func TestSnapshotBackwardCompatibleDecode(t *testing.T) {
	exec := newWaitExecution(t, "cap_a")
	makeRunning(t, exec, "cap_a")
	deadline := time.Now().UTC().Add(24 * time.Hour)
	exec.SetExecutionDeadline(&deadline)
	if err := exec.EnterWait("cap_a", signalWaitSpec(nil, WaitDeadlinePolicyFail)); err != nil {
		t.Fatalf("EnterWait: %v", err)
	}
	base, err := MarshalExecution(exec)
	if err != nil {
		t.Fatalf("MarshalExecution: %v", err)
	}

	for _, version := range []string{"", "1", "2", "3"} {
		t.Run("version_"+version, func(t *testing.T) {
			var raw map[string]any
			if err := json.Unmarshal(base, &raw); err != nil {
				t.Fatalf("unmarshal base snapshot: %v", err)
			}
			raw["schema_version"] = version
			if version != "3" {
				delete(raw, "execution_deadline")
			}
			encoded, err := json.Marshal(raw)
			if err != nil {
				t.Fatalf("marshal adjusted snapshot: %v", err)
			}

			restored, err := UnmarshalExecution(encoded)
			if err != nil {
				t.Fatalf("UnmarshalExecution(version %q): %v", version, err)
			}
			if got := restored.Snapshot().SchemaVersion; got != "3" {
				t.Errorf("migrated schema version = %q, want %q", got, "3")
			}
			if restored.ID != exec.ID {
				t.Errorf("id = %s, want %s", restored.ID, exec.ID)
			}
			gotDeadline := restored.ExecutionDeadline()
			if version == "3" {
				if gotDeadline == nil || !gotDeadline.Equal(deadline) {
					t.Errorf("v3 deadline = %v, want %v", gotDeadline, deadline)
				}
			} else if gotDeadline != nil {
				t.Errorf("legacy deadline = %v, want nil", gotDeadline)
			}
			state := restored.CapabilityState("cap_a")
			if state.Status != CapabilityWaiting || state.WaitType != WaitTypeSignal ||
				state.ExpectedSignal != "approval" {
				t.Errorf("wait metadata did not decode for v%q: %+v", version, state)
			}
		})
	}
}

func TestSnapshotRejectsUnknownSchemaVersion(t *testing.T) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(`{"schema_version":"4"}`), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := UnmarshalExecution(encoded); err == nil {
		t.Fatal("UnmarshalExecution accepted unknown schema version 4")
	}
}

func TestExecutionDeadlineSurvivesRestoreMethod(t *testing.T) {
	exec := newWaitExecution(t, "cap_a")
	deadline := time.Now().UTC().Add(72 * time.Hour)
	exec.SetExecutionDeadline(&deadline)

	target, err := NewExecution(blueprintFor("cap_a"), shared.NewID("corr"), "inst_1", "", "", "", "")
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	target.Restore(exec.Snapshot())
	if got := target.ExecutionDeadline(); got == nil || !got.Equal(deadline) {
		t.Errorf("restored deadline = %v, want %v", got, deadline)
	}
}
