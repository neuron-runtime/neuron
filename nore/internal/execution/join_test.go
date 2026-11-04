package execution

import (
	"testing"

	"github.com/neuron-runtime/neuron/nore/internal/types"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

var capA = shared.NewID("cap_a")
var capB = shared.NewID("cap_b")
var capC1 = shared.NewID("cap_c1")

func blueprintForJoin(capC1ID shared.ID) *types.ExecutionBlueprint {
	nodes := map[shared.ID]types.ExecutionNode{
		capA: {
			Capability: shared.Capability{Metadata: shared.Metadata{ID: capA}},
		},
		capB: {
			Capability: shared.Capability{Metadata: shared.Metadata{ID: capB}},
		},
		capC1: {
			Capability: shared.Capability{Metadata: shared.Metadata{ID: capC1}},
			Predecessors: []types.ExecutionPredecessor{
				{BindingID: "a-c1", CapabilityID: capA},
				{BindingID: "b-c1", CapabilityID: capB},
			},
			Next: []types.ExecutionTransition{{BindingID: "c1-d", TargetCapabilityID: shared.NewID("cap_d")}},
		},
	}

	return &types.ExecutionBlueprint{
		Metadata:           shared.Metadata{ID: "test_bp", Name: "test", Version: "1.0.0"},
		Nodes:              nodes,
		EntryCapabilityIDs: []shared.ID{capA, capB},
	}
}

func TestJoinCompletingAllPredecessorsYieldsReady(t *testing.T) {
	bp := blueprintForJoin(capC1)
	exec, err := NewExecution(bp, shared.NewID("corr"), shared.NewID("inst"), "", "", "", "")
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	if err := exec.Start(map[string]any{}, 2); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Complete first predecessor for c1
	ready1, _, _ := exec.RecordJoinContribution(capC1, "a-c1", map[string]any{"x": "from-a"})
	if ready1 {
		t.Error("expected ready=false after first contribution")
	}

	// Complete second predecessor for c1
	ready2, _, _ := exec.RecordJoinContribution(capC1, "b-c1", map[string]any{"y": "from-b"})
	if !ready2 {
		t.Error("expected ready=true after all predecessors completed")
	}

	// Completing extra after dispatch returns false
	ready3, _, _ := exec.RecordJoinContribution(capC1, "extra", map[string]any{"z": "extra"})
	if ready3 {
		t.Error("expected ready=false after dispatch, extra contribution ignored")
	}
}

func TestJoinUnknownBindingCannotSatisfy(t *testing.T) {
	bp := blueprintForJoin(capC1)
	exec, err := NewExecution(bp, shared.NewID("corr"), shared.NewID("inst"), "", "", "", "")
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	if err := exec.Start(map[string]any{}, 2); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Supplying an unknown bindingID should not satisfy the join
	ready, _, _ := exec.RecordJoinContribution(capC1, "unknown-binding", map[string]any{"x": "foo"})
	if ready {
		t.Error("expected ready=false when unknown binding supplied; only blueprint predecessors count")
	}
}

func TestJoinSnapshotRoundTrip(t *testing.T) {
	bp := blueprintForJoin(capC1)
	exec, err := NewExecution(bp, shared.NewID("corr"), shared.NewID("inst"), "", "", "", "")
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	if err := exec.Start(map[string]any{}, 2); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Record first contribution (only a-c1)
	ready1, _, _ := exec.RecordJoinContribution(capC1, "a-c1", map[string]any{"x": "from-a"})
	if ready1 {
		t.Error("expected ready=false after first contribution")
	}

	// Snapshot the execution
	data, err := MarshalExecution(exec)
	if err != nil {
		t.Fatalf("MarshalExecution: %v", err)
	}

	// Restore into a new execution with the same blueprint
	restored, err := UnmarshalExecution(data)
	if err != nil {
		t.Fatalf("UnmarshalExecution: %v", err)
	}

	// Check that the restored execution has the right status
	if restored.Status() != exec.Status() {
		t.Errorf("restored Status() = %s, want %s", restored.Status(), exec.Status())
	}

	// Record the second contribution on the restored execution
	ready2, _, _ := restored.RecordJoinContribution(capC1, "b-c1", map[string]any{"y": "from-b"})
	if !ready2 {
		t.Error("expected ready=true after restoring and recording remaining contribution")
	}
}
