package execution

import (
	"testing"

	"github.com/neuron-runtime/neuron/nore/internal/types"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

func detachedScopeBlueprint() *types.ExecutionBlueprint {
	return &types.ExecutionBlueprint{
		Metadata: shared.Metadata{ID: "bp", Name: "detach", Version: "1"},
		Nodes: map[shared.ID]types.ExecutionNode{
			"second": {Capability: shared.Capability{Metadata: shared.Metadata{ID: "second"}}},
			"third":  {Capability: shared.Capability{Metadata: shared.Metadata{ID: "third"}}},
		},
		EntryCapabilityIDs: []shared.ID{"second"},
	}
}

func TestNewDetachedTaskLinksToTheParentExecution(t *testing.T) {
	task, err := NewDetachedTask(detachedScopeBlueprint(), "corr_1", "inst_1", "exec_parent")
	if err != nil {
		t.Fatalf("NewDetachedTask: %v", err)
	}
	if task.ParentExecutionID != "exec_parent" {
		t.Fatalf("ParentExecutionID = %q, want exec_parent", task.ParentExecutionID)
	}
	if task.InstanceID != "inst_1" || task.CorrelationID != "corr_1" {
		t.Fatalf("task carries the wrong identity: %+v", task)
	}
	// The task seeds a pending state for every capability in its scope, exactly
	// like a root execution, so the scheduler can start it the same way.
	if len(task.states) != 2 {
		t.Fatalf("task seeded %d capability states, want 2", len(task.states))
	}
}

func TestNewDetachedTaskRequiresAParent(t *testing.T) {
	if _, err := NewDetachedTask(detachedScopeBlueprint(), "corr_1", "inst_1", ""); err == nil {
		t.Fatal("a detached task without a parent execution must be rejected")
	}
}

func TestMarkCapabilityDetachedRequiresReady(t *testing.T) {
	task, err := NewDetachedTask(detachedScopeBlueprint(), "corr_1", "inst_1", "exec_parent")
	if err != nil {
		t.Fatalf("NewDetachedTask: %v", err)
	}
	if err := task.Start(map[string]any{}, 1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Pending is not a valid detach source; only a capability that was actually
	// scheduled may be handed off.
	if err := task.MarkCapabilityDetached("second"); err == nil {
		t.Fatal("detaching a pending capability must fail")
	}
	if err := task.MarkCapabilityReady("second", map[string]any{"in": true}); err != nil {
		t.Fatalf("MarkCapabilityReady: %v", err)
	}
	if err := task.MarkCapabilityDetached("second"); err != nil {
		t.Fatalf("MarkCapabilityDetached: %v", err)
	}
	if got := task.states["second"].Status; got != CapabilityDetached {
		t.Fatalf("status = %q, want %q", got, CapabilityDetached)
	}
	if got := task.DetachedCapabilityIDs(); len(got) != 1 || got[0] != "second" {
		t.Fatalf("DetachedCapabilityIDs = %v, want [second]", got)
	}
}

func TestDetachedTaskSnapshotRoundTrip(t *testing.T) {
	task, err := NewDetachedTask(detachedScopeBlueprint(), "corr_1", "inst_1", "exec_parent")
	if err != nil {
		t.Fatalf("NewDetachedTask: %v", err)
	}
	data, err := MarshalExecution(task)
	if err != nil {
		t.Fatalf("MarshalExecution: %v", err)
	}
	restored, err := UnmarshalExecution(data)
	if err != nil {
		t.Fatalf("UnmarshalExecution: %v", err)
	}
	if restored.ParentExecutionID != "exec_parent" {
		t.Fatalf("restored ParentExecutionID = %q, want exec_parent", restored.ParentExecutionID)
	}
}

func TestRootExecutionHasNoParent(t *testing.T) {
	root, err := NewExecution(detachedScopeBlueprint(), "corr_1", "inst_1")
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	if root.ParentExecutionID != "" {
		t.Fatalf("a root execution must have no parent, got %q", root.ParentExecutionID)
	}
}
