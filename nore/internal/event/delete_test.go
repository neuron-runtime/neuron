package event

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/nore/internal/storage/sqlite"
	"github.com/neuron-runtime/neuron/shared/types/core"
)

func TestDeleteExecutionRemovesOnlyThatExecution(t *testing.T) {
	store, err := sqlite.New(context.Background(), storage.Config{DataDir: t.TempDir()})
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

// failingDeleteStore wraps a real store and fails one event delete inside the
// transaction, proving DeleteExecution cannot leave a partially deleted history.
type failingDeleteStore struct {
	storage.Store
	failSuffix string
}

func (s *failingDeleteStore) WithTx(ctx context.Context, fn func(storage.Tx) error) error {
	return s.Store.WithTx(ctx, func(tx storage.Tx) error {
		return fn(failingDeleteTx{Tx: tx, failSuffix: s.failSuffix})
	})
}

type failingDeleteTx struct {
	storage.Tx
	failSuffix string
}

func (tx failingDeleteTx) Delete(ctx context.Context, key string) error {
	if strings.HasSuffix(key, tx.failSuffix) {
		return errors.New("injected event delete failure")
	}
	return tx.Tx.Delete(ctx, key)
}

func TestDeleteExecutionRollsBackOnFailure(t *testing.T) {
	store, err := sqlite.New(context.Background(), storage.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	execID := core.ID("exec_atomic")
	base := NewStore(store)
	for i := 0; i < 3; i++ {
		if err := base.Save(ctx, newTestEvent(execID)); err != nil {
			t.Fatalf("save event: %v", err)
		}
	}

	keys, err := store.List(ctx, "events/"+string(execID)+"/")
	if err != nil {
		t.Fatalf("list event keys: %v", err)
	}
	if len(keys) != 3 {
		t.Fatalf("event keys = %d, want 3", len(keys))
	}
	suffix := keys[len(keys)-1]
	failing := NewStore(&failingDeleteStore{Store: store, failSuffix: suffix})

	if err := failing.DeleteExecution(ctx, execID); err == nil {
		t.Fatal("DeleteExecution should fail when one delete fails")
	}

	remaining, err := base.List(ctx, execID)
	if err != nil {
		t.Fatalf("list after failed delete: %v", err)
	}
	if len(remaining) != 3 {
		t.Fatalf("failed DeleteExecution removed %d events, want 0", 3-len(remaining))
	}
}
