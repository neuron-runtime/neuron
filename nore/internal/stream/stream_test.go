package stream

import (
	"context"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/event"
	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/nore/internal/storage/sqlite"
	"github.com/neuron-runtime/neuron/shared/types/core"
)

func newTestStore(t *testing.T) *event.Store {
	t.Helper()
	s, err := sqlite.New(storage.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return event.NewStore(s)
}

func TestSubscribeReplaysHistoryThenLive(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus := event.NewBus()
	store := newTestStore(t)
	execID := core.NewID("exec_")

	history := event.New(event.ExecutionStarted, execID, "corr", "", event.ExecutionStartedPayload{})
	if err := store.Save(ctx, history); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)

	events, err := Subscribe(ctx, bus, store, execID, "")
	if err != nil {
		t.Fatal(err)
	}

	select {
	case msg := <-events:
		if msg.EventID != history.Metadata.EventID {
			t.Fatalf("expected history event %s, got %s", history.Metadata.EventID, msg.EventID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for history replay")
	}

	live := event.New(event.CapabilityStarted, execID, "corr", "svc", event.CapabilityStartedPayload{})
	if err := bus.Publish(ctx, live); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-events:
		if msg.EventID != live.Metadata.EventID {
			t.Fatalf("expected live event %s, got %s", live.Metadata.EventID, msg.EventID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for live event")
	}
}

func TestSubscribeDedupsReplayedAndLiveEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus := event.NewBus()
	store := newTestStore(t)
	execID := core.NewID("exec_")

	history := event.New(event.ExecutionStarted, execID, "corr", "", event.ExecutionStartedPayload{})
	if err := store.Save(ctx, history); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)

	events, err := Subscribe(ctx, bus, store, execID, "")
	if err != nil {
		t.Fatal(err)
	}
	if msg := <-events; msg.EventID != history.Metadata.EventID {
		t.Fatalf("expected history event %s, got %s", history.Metadata.EventID, msg.EventID)
	}

	// Republish the already-replayed event, then a fresh one.
	if err := bus.Publish(ctx, history); err != nil {
		t.Fatal(err)
	}
	fresh := event.New(event.CapabilityStarted, execID, "corr", "svc", event.CapabilityStartedPayload{})
	time.Sleep(2 * time.Millisecond)
	if err := bus.Publish(ctx, fresh); err != nil {
		t.Fatal(err)
	}

	select {
	case msg := <-events:
		if msg.EventID != fresh.Metadata.EventID {
			t.Fatalf("expected fresh event, got duplicate %s", msg.EventID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for live event")
	}
}

func TestSubscribeResumesFromCursorWithoutBus(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	execID := core.NewID("exec_")

	first := event.New(event.ExecutionStarted, execID, "corr", "", event.ExecutionStartedPayload{})
	if err := store.Save(ctx, first); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	second := event.New(event.CapabilityStarted, execID, "corr", "svc", event.CapabilityStartedPayload{})
	if err := store.Save(ctx, second); err != nil {
		t.Fatal(err)
	}

	// A restored instance has no bus: history-only stream, resumed from cursor.
	events, err := Subscribe(ctx, nil, store, execID, first.Metadata.EventID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for msg := range events {
		if msg.EventID != second.Metadata.EventID {
			t.Fatalf("expected only event after cursor, got %s", msg.EventID)
		}
		count++
	}
	if count != 1 {
		t.Fatalf("expected 1 event, got %d", count)
	}
}

func TestSubscribeRequiresExecutionID(t *testing.T) {
	store := newTestStore(t)
	if _, err := Subscribe(context.Background(), event.NewBus(), store, "", ""); err == nil {
		t.Fatal("expected error for empty execution id")
	}
}
