package execution

import (
	"reflect"
	"testing"

	"github.com/neuron-runtime/neuron/nore/internal/types"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

func recoveryJoinBlueprint() *types.ExecutionBlueprint {
	return &types.ExecutionBlueprint{
		Metadata: shared.Metadata{ID: "recovery_join"},
		Nodes: map[shared.ID]types.ExecutionNode{
			"a": {Capability: shared.Capability{Metadata: shared.Metadata{ID: "a"}}},
			"b": {Capability: shared.Capability{Metadata: shared.Metadata{ID: "b"}}},
			"c": {
				Capability: shared.Capability{Metadata: shared.Metadata{ID: "c"}},
				Predecessors: []types.ExecutionPredecessor{
					{BindingID: "a-c", CapabilityID: "a"},
					{BindingID: "b-c", CapabilityID: "b"},
				},
			},
		},
		EntryCapabilityIDs: []shared.ID{"a", "b"},
	}
}

func restoreForRecovery(t *testing.T, exec *Execution, blueprint *types.ExecutionBlueprint) *Execution {
	t.Helper()
	data, err := MarshalExecution(exec)
	if err != nil {
		t.Fatalf("marshal execution: %v", err)
	}
	restored, err := UnmarshalExecution(data)
	if err != nil {
		t.Fatalf("unmarshal execution: %v", err)
	}
	restored.AttachBlueprint(blueprint)
	return restored
}

// TestRecoverableDispatchDoesNotRerunCompletedCapabilities covers R4-17: a
// capability recorded as completed keeps its result and is never returned for
// another dispatch, while an interrupted sibling is dispatched again.
func TestRecoverableDispatchDoesNotRerunCompletedCapabilities(t *testing.T) {
	blueprint := recoveryJoinBlueprint()
	exec, err := NewExecution(blueprint, shared.NewID("corr"), shared.NewID("inst"), "", "", "", "")
	if err != nil {
		t.Fatalf("new execution: %v", err)
	}
	if err := exec.Start(map[string]any{}, 2); err != nil {
		t.Fatalf("start execution: %v", err)
	}
	for _, capabilityID := range []shared.ID{"a", "b"} {
		if err := exec.MarkCapabilityReady(capabilityID, map[string]any{}); err != nil {
			t.Fatalf("mark %s ready: %v", capabilityID, err)
		}
		if err := exec.MarkCapabilityRunning(capabilityID); err != nil {
			t.Fatalf("mark %s running: %v", capabilityID, err)
		}
	}
	if err := exec.MarkCapabilityCompleted("a", map[string]any{"x": "from-a"}); err != nil {
		t.Fatalf("complete a: %v", err)
	}
	if _, _, err := exec.RecordJoinContribution("c", "a-c", map[string]any{"x": "from-a"}); err != nil {
		t.Fatalf("record a contribution: %v", err)
	}

	restored := restoreForRecovery(t, exec, blueprint)
	dispatch := restored.RecoverableDispatch()

	if want := []shared.ID{"b"}; !reflect.DeepEqual(dispatch, want) {
		t.Fatalf("dispatch = %v, want %v", dispatch, want)
	}
	if state := restored.CapabilityState("a"); state.Status != CapabilityCompleted {
		t.Fatalf("completed capability a status = %s, want %s", state.Status, CapabilityCompleted)
	}
	if got := restored.Result("a"); got["x"] != "from-a" {
		t.Fatalf("completed capability a result = %v, want the recorded result preserved", got)
	}
	if state := restored.CapabilityState("b"); state.Status != CapabilityReady {
		t.Fatalf("interrupted capability b status = %s, want %s", state.Status, CapabilityReady)
	}
	if restored.InFlight() != 1 {
		t.Fatalf("in-flight = %d, want 1", restored.InFlight())
	}
}

// TestRecoverableDispatchRedispatchesRecordedJoin covers the crash window
// between a join becoming ready and its target being marked ready: the recorded
// contributions and branch results must survive, and only the join target is
// dispatched again.
func TestRecoverableDispatchRedispatchesRecordedJoin(t *testing.T) {
	blueprint := recoveryJoinBlueprint()
	exec, err := NewExecution(blueprint, shared.NewID("corr"), shared.NewID("inst"), "", "", "", "")
	if err != nil {
		t.Fatalf("new execution: %v", err)
	}
	if err := exec.Start(map[string]any{}, 2); err != nil {
		t.Fatalf("start execution: %v", err)
	}
	for _, capabilityID := range []shared.ID{"a", "b"} {
		if err := exec.MarkCapabilityReady(capabilityID, map[string]any{}); err != nil {
			t.Fatalf("mark %s ready: %v", capabilityID, err)
		}
		if err := exec.MarkCapabilityRunning(capabilityID); err != nil {
			t.Fatalf("mark %s running: %v", capabilityID, err)
		}
		if err := exec.MarkCapabilityCompleted(capabilityID, map[string]any{"from_" + string(capabilityID): true}); err != nil {
			t.Fatalf("complete %s: %v", capabilityID, err)
		}
	}
	if ready, _, err := exec.RecordJoinContribution("c", "a-c", map[string]any{"x": "from-a"}); err != nil || ready {
		t.Fatalf("first contribution ready = %v, err = %v; want false and no error", ready, err)
	}
	if ready, _, err := exec.RecordJoinContribution("c", "b-c", map[string]any{"y": "from-b"}); err != nil || !ready {
		t.Fatalf("second contribution ready = %v, err = %v; want true and no error", ready, err)
	}

	restored := restoreForRecovery(t, exec, blueprint)
	dispatch := restored.RecoverableDispatch()

	if want := []shared.ID{"c"}; !reflect.DeepEqual(dispatch, want) {
		t.Fatalf("dispatch = %v, want %v", dispatch, want)
	}
	params := restored.Params("c")
	if params["x"] != "from-a" || params["y"] != "from-b" {
		t.Fatalf("recovered join params = %v, want both branch contributions", params)
	}
	for _, capabilityID := range []shared.ID{"a", "b"} {
		if state := restored.CapabilityState(capabilityID); state.Status != CapabilityCompleted {
			t.Fatalf("branch %s status = %s, want %s", capabilityID, state.Status, CapabilityCompleted)
		}
	}
}
