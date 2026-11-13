package execution

import (
	"context"
	"testing"

	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/nore/internal/storage/sqlite"
	"github.com/neuron-runtime/neuron/nore/internal/types"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

func openTestStore(t *testing.T) storage.Store {
	t.Helper()
	ctx := context.Background()
	store, err := sqlite.New(ctx, storage.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func newTestExecution(t *testing.T, bp *types.ExecutionBlueprint, instanceID shared.ID) *Execution {
	t.Helper()
	exec, err := NewExecution(bp, shared.NewID("corr_"), instanceID, "", "", "", "")
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	return exec
}

func TestNewExecutionCarriesInstanceID(t *testing.T) {
	bp := &types.ExecutionBlueprint{Nodes: map[shared.ID]types.ExecutionNode{}}
	exec := newTestExecution(t, bp, "inst_a")
	if exec.InstanceID != "inst_a" {
		t.Errorf("InstanceID = %q, want inst_a", exec.InstanceID)
	}
}

func TestListByInstanceScopesExecutions(t *testing.T) {
	store := openTestStore(t)
	s := NewExecutionStore(store)
	bp := &types.ExecutionBlueprint{Nodes: map[shared.ID]types.ExecutionNode{}}

	a1 := newTestExecution(t, bp, "inst_a")
	a2 := newTestExecution(t, bp, "inst_a")
	b1 := newTestExecution(t, bp, "inst_b")

	for _, exec := range []*Execution{a1, a2, b1} {
		if err := s.Add(context.Background(), exec); err != nil {
			t.Fatalf("add execution: %v", err)
		}
	}

	gotA, err := s.ListByInstance(context.Background(), "inst_a")
	if err != nil {
		t.Fatalf("list inst_a: %v", err)
	}
	if len(gotA) != 2 {
		t.Fatalf("ListByInstance(inst_a) = %d executions, want 2", len(gotA))
	}
	gotB, err := s.ListByInstance(context.Background(), "inst_b")
	if err != nil {
		t.Fatalf("list inst_b: %v", err)
	}
	if len(gotB) != 1 {
		t.Fatalf("ListByInstance(inst_b) = %d executions, want 1", len(gotB))
	}
}

func TestExecutionInstanceIDRoundTripsSnapshot(t *testing.T) {
	store := openTestStore(t)
	s := NewExecutionStore(store)
	bp := &types.ExecutionBlueprint{Nodes: map[shared.ID]types.ExecutionNode{}}

	exec := newTestExecution(t, bp, "inst_x")
	if err := s.Add(context.Background(), exec); err != nil {
		t.Fatalf("add execution: %v", err)
	}
	if err := exec.Start(nil, 1); err != nil {
		t.Fatalf("start execution: %v", err)
	}
	if !exec.MarkCompleted() {
		t.Fatalf("mark completed: not completed")
	}
	if err := s.Save(context.Background(), exec); err != nil {
		t.Fatalf("save execution: %v", err)
	}

	loaded, err := s.Get(context.Background(), exec.ID)
	if err != nil {
		t.Fatalf("get execution: %v", err)
	}
	if loaded.InstanceID != "inst_x" {
		t.Errorf("loaded InstanceID = %q, want inst_x", loaded.InstanceID)
	}
}

func TestReferencedArtifactHashesIncludesOnlyUnfinishedExecutions(t *testing.T) {
	ctx := context.Background()
	store := NewExecutionStore(openTestStore(t))

	running := testExecution(t)
	running.Hash = "hash-running"
	if err := store.Add(ctx, running); err != nil {
		t.Fatalf("add running execution: %v", err)
	}

	finished := testExecution(t)
	finished.Hash = "hash-finished"
	if err := store.Add(ctx, finished); err != nil {
		t.Fatalf("add finished execution: %v", err)
	}
	if err := finished.Start(nil, 1); err != nil {
		t.Fatalf("start finished execution: %v", err)
	}
	if !finished.MarkCompleted() {
		t.Fatal("MarkCompleted() = false")
	}
	if err := store.Save(ctx, finished); err != nil {
		t.Fatalf("save finished execution: %v", err)
	}

	hashes, err := store.ReferencedArtifactHashes(ctx)
	if err != nil {
		t.Fatalf("ReferencedArtifactHashes: %v", err)
	}
	if !hashes["hash-running"] {
		t.Error("unfinished execution hash was not referenced")
	}
	if hashes["hash-finished"] {
		t.Error("terminal execution hash was reported as referenced")
	}
}
