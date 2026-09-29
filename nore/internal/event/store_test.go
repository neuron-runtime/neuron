package event

import (
	"context"
	"testing"

	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/nore/internal/storage/sqlite"
	"github.com/neuron-runtime/neuron/shared/types/core"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := sqlite.New(storage.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return NewStore(s)
}

func TestStoreRoundTripAndListAfter(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	execID := core.NewID("exec_")

	first := New(ExecutionStarted, execID, "corr", "", ExecutionStartedPayload{Params: map[string]any{"a": 1}})
	if err := store.Save(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := New(CapabilityStarted, execID, "corr", "svc", CapabilityStartedPayload{})
	if err := store.Save(ctx, second); err != nil {
		t.Fatal(err)
	}
	third := New(CapabilityCompleted, execID, "corr", "svc", CapabilityCompletedPayload{Result: map[string]any{"b": 2}})
	if err := store.Save(ctx, third); err != nil {
		t.Fatal(err)
	}

	all, err := store.List(ctx, execID)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 events, got %d", len(all))
	}
	// All three share the same millisecond; listing must follow occurrence time.
	if all[0].Metadata.EventID != first.Metadata.EventID {
		t.Fatalf("expected chronological order, first got %s", all[0].Metadata.EventID)
	}
	if all[0].Type != first.Type || all[0].Metadata.CapabilityID != first.Metadata.CapabilityID {
		t.Fatalf("round-trip mismatch: %+v", all[0])
	}
	for i := 1; i < len(all); i++ {
		if all[i].Metadata.OccurredAt.Before(all[i-1].Metadata.OccurredAt) {
			t.Fatalf("events out of order at index %d", i)
		}
	}

	after, err := store.ListAfter(ctx, execID, first.Metadata.EventID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 2 {
		t.Fatalf("expected 2 events after cursor, got %d", len(after))
	}
	if after[0].Metadata.EventID != second.Metadata.EventID {
		t.Fatalf("expected second first after cursor, got %s", after[0].Metadata.EventID)
	}
}
