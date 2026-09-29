package event

import (
	"context"
	"testing"

	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/nore/internal/storage/sqlite"
	"github.com/neuron-runtime/neuron/shared/types/core"
)

func TestDeleteExecutionRemovesOnlyThatExecution(t *testing.T) {
	store, err := sqlite.New(storage.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	s := NewStore(store)
	ctx := context.Background()

	execA := core.ID("exec_a")
	execB := core.ID("exec_b")

	for _, execID := range []core.ID{execA, execB} {
		for i := 0; i < 3; i++ {
			if err := s.Save(ctx, newTestEvent(execID)); err != nil {
				t.Fatalf("save event for %s: %v", execID, err)
			}
		}
	}

	if err := s.DeleteExecution(ctx, execA); err != nil {
		t.Fatalf("delete execution A: %v", err)
	}

	remainingA, err := s.List(ctx, execA)
	if err != nil {
		t.Fatalf("list A: %v", err)
	}
	if len(remainingA) != 0 {
		t.Errorf("execution A still has %d events after delete", len(remainingA))
	}

	remainingB, err := s.List(ctx, execB)
	if err != nil {
		t.Fatalf("list B: %v", err)
	}
	if len(remainingB) != 3 {
		t.Errorf("execution B lost events: got %d, want 3", len(remainingB))
	}
}

func newTestEvent(executionID core.ID) Event {
	return New(
		ExecutionStarted,
		executionID,
		core.NewID("corr_"),
		"",
		ExecutionStartedPayload{Params: map[string]any{"execution": string(executionID)}},
	)
}
