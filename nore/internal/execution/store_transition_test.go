package execution

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/nore/internal/storage/postgres"
)

// failingPutStore wraps a real store and fails a single key's transaction
// write. The underlying transaction is still real, so earlier writes in the
// same transition must be rolled back by the backend.
type failingPutStore struct {
	storage.Store
	failKey string
}

func (s *failingPutStore) WithTx(ctx context.Context, fn func(storage.Tx) error) error {
	return s.Store.WithTx(ctx, func(tx storage.Tx) error {
		return fn(&failingPutTx{Tx: tx, failKey: s.failKey})
	})
}

type failingPutTx struct {
	storage.Tx
	failKey string
}

func (t *failingPutTx) Put(ctx context.Context, key string, data []byte) error {
	if key == t.failKey {
		return errInjected
	}
	return t.Tx.Put(ctx, key, data)
}

func TestSaveTransitionPersistsExecutionAndIntents(t *testing.T) {
	store := openTestStore(t)
	s := NewExecutionStore(store)
	ctx := context.Background()

	exec := testExecution(t)
	if err := s.Add(ctx, exec); err != nil {
		t.Fatalf("add execution: %v", err)
	}
	if err := exec.Start(map[string]any{"name": "World"}, 1); err != nil {
		t.Fatalf("start execution: %v", err)
	}

	intents := []*DispatchIntent{
		dispatchIntentFor(exec.ID, "cap_a", "attempt_a"),
		dispatchIntentFor(exec.ID, "cap_b", "attempt_b"),
	}
	if err := s.SaveTransition(ctx, exec, intents); err != nil {
		t.Fatalf("save transition: %v", err)
	}

	if got := exec.Revision(); got != 2 {
		t.Fatalf("execution revision = %d, want 2", got)
	}
	live, err := s.Get(ctx, exec.ID)
	if err != nil {
		t.Fatalf("get live execution: %v", err)
	}
	if live.Revision() != 2 {
		t.Fatalf("live revision = %d, want 2", live.Revision())
	}

	durable, err := store.Get(ctx, mustExecutionKey(t, exec.ID))
	if err != nil {
		t.Fatalf("read durable execution: %v", err)
	}
	loaded, err := UnmarshalExecution(durable)
	if err != nil {
		t.Fatalf("decode durable execution: %v", err)
	}
	if loaded.Revision() != 2 {
		t.Fatalf("durable revision = %d, want 2", loaded.Revision())
	}
	if loaded.status != StatusRunning {
		t.Fatalf("durable status = %q, want %q", loaded.status, StatusRunning)
	}

	for _, intent := range intents {
		if intent.CreatedAt.IsZero() {
			t.Fatalf("intent %s/%s has no CreatedAt", intent.CapabilityID, intent.AttemptID)
		}
		stored, err := readDispatchIntent(t, store, intent)
		if err != nil {
			t.Fatalf("read durable intent %s: %v", intent.CapabilityID, err)
		}
		if !stored.CreatedAt.Equal(intent.CreatedAt) {
			t.Fatalf("stored CreatedAt = %v, intent CreatedAt = %v", stored.CreatedAt, intent.CreatedAt)
		}
		claimed, won, err := s.ClaimDispatchIntent(ctx, intent, "owner-a", time.Minute)
		if err != nil {
			t.Fatalf("claim intent %s: %v", intent.CapabilityID, err)
		}
		if !won || claimed == nil {
			t.Fatalf("intent %s was not claimable after transition", intent.CapabilityID)
		}
	}
}

func TestSaveTransitionRejectsStaleExecution(t *testing.T) {
	store := openTestStore(t)
	s := NewExecutionStore(store)
	ctx := context.Background()

	exec := testExecution(t)
	if err := s.Add(ctx, exec); err != nil {
		t.Fatalf("add execution: %v", err)
	}
	stale, err := UnmarshalExecution(mustMarshal(t, exec))
	if err != nil {
		t.Fatalf("copy execution: %v", err)
	}

	if err := s.Save(ctx, exec); err != nil {
		t.Fatalf("advance stored execution: %v", err)
	}
	intents := []*DispatchIntent{dispatchIntentFor(stale.ID, "cap_a", "attempt_a")}
	err = s.SaveTransition(ctx, stale, intents)
	if !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("SaveTransition error = %v, want ErrConflict", err)
	}
	if stale.Revision() != 1 {
		t.Fatalf("stale revision = %d, want 1", stale.Revision())
	}

	durable, err := store.Get(ctx, mustExecutionKey(t, exec.ID))
	if err != nil {
		t.Fatalf("read durable execution: %v", err)
	}
	loaded, err := UnmarshalExecution(durable)
	if err != nil {
		t.Fatalf("decode durable execution: %v", err)
	}
	if loaded.Revision() != 2 {
		t.Fatalf("durable revision = %d, want 2", loaded.Revision())
	}
	if _, err := readDispatchIntent(t, store, intents[0]); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("stale transition intent = %v, want ErrNotFound", err)
	}
}

func TestSaveTransitionRollsBackExecutionWhenIntentWriteFails(t *testing.T) {
	backend := openTestStore(t)
	ctx := context.Background()

	exec := testExecution(t)
	intent := dispatchIntentFor(exec.ID, "cap_a", "attempt_a")
	intentKey, err := dispatchIntentKey(intent.ExecutionID, intent.CapabilityID, intent.AttemptID)
	if err != nil {
		t.Fatalf("dispatch intent key: %v", err)
	}

	s := NewExecutionStore(&failingPutStore{Store: backend, failKey: intentKey})
	if err := s.Add(ctx, exec); err != nil {
		t.Fatalf("add execution: %v", err)
	}

	err = s.SaveTransition(ctx, exec, []*DispatchIntent{intent})
	if !errors.Is(err, errInjected) {
		t.Fatalf("SaveTransition error = %v, want injected failure", err)
	}
	if exec.Revision() != 1 {
		t.Fatalf("execution revision = %d, want 1 after rollback", exec.Revision())
	}
	if !intent.CreatedAt.IsZero() {
		t.Fatalf("rolled-back transition mutated intent CreatedAt to %v", intent.CreatedAt)
	}

	durable, err := backend.Get(ctx, mustExecutionKey(t, exec.ID))
	if err != nil {
		t.Fatalf("read durable execution: %v", err)
	}
	loaded, err := UnmarshalExecution(durable)
	if err != nil {
		t.Fatalf("decode durable execution: %v", err)
	}
	if loaded.Revision() != 1 {
		t.Fatalf("durable revision = %d, want 1 after rollback", loaded.Revision())
	}
	if _, err := readDispatchIntent(t, backend, intent); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("intent after rollback = %v, want ErrNotFound", err)
	}
}

func TestSaveTransitionWithoutIntentsPersistsExecution(t *testing.T) {
	store := openTestStore(t)
	s := NewExecutionStore(store)
	ctx := context.Background()

	exec := testExecution(t)
	if err := s.Add(ctx, exec); err != nil {
		t.Fatalf("add execution: %v", err)
	}

	for i, intents := range [][]*DispatchIntent{nil, {}} {
		if err := s.SaveTransition(ctx, exec, intents); err != nil {
			t.Fatalf("save transition %d: %v", i, err)
		}
		wantRevision := uint64(i + 2)
		if exec.Revision() != wantRevision {
			t.Fatalf("revision after transition %d = %d, want %d", i, exec.Revision(), wantRevision)
		}

		durable, err := store.Get(ctx, mustExecutionKey(t, exec.ID))
		if err != nil {
			t.Fatalf("read durable execution: %v", err)
		}
		loaded, err := UnmarshalExecution(durable)
		if err != nil {
			t.Fatalf("decode durable execution: %v", err)
		}
		if loaded.Revision() != wantRevision {
			t.Fatalf("durable revision after transition %d = %d, want %d", i, loaded.Revision(), wantRevision)
		}
	}
}

func TestSaveTransitionPostgresParity(t *testing.T) {
	dsn := os.Getenv("NEURON_TEST_POSTGRES_DSN")
	if dsn == "" {
		dsn = os.Getenv("NEURON_DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("postgres DSN not set; skipping save transition parity")
	}

	pg, err := postgres.New(context.Background(), storage.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("open postgres store: %v", err)
	}
	t.Cleanup(func() { _ = pg.Close() })

	for name, store := range map[string]storage.Store{
		"sqlite":   openTestStore(t),
		"postgres": pg,
	} {
		t.Run(name, func(t *testing.T) {
			exerciseSaveTransition(t, store)
		})
	}
}

func exerciseSaveTransition(t *testing.T, store storage.Store) {
	t.Helper()
	ctx := context.Background()
	s := NewExecutionStore(store)

	exec := testExecution(t)
	if err := s.Add(ctx, exec); err != nil {
		t.Fatalf("add execution: %v", err)
	}
	intent := dispatchIntentFor(exec.ID, "cap_a", "attempt_a")
	if err := s.SaveTransition(ctx, exec, []*DispatchIntent{intent}); err != nil {
		t.Fatalf("save transition: %v", err)
	}
	if exec.Revision() != 2 {
		t.Fatalf("execution revision = %d, want 2", exec.Revision())
	}
	if _, err := readDispatchIntent(t, store, intent); err != nil {
		t.Fatalf("read durable intent: %v", err)
	}
	claimed, won, err := s.ClaimDispatchIntent(ctx, intent, "owner-a", time.Minute)
	if err != nil || !won || claimed == nil || claimed.Owner != "owner-a" {
		t.Fatalf("claim after transition = %v, %v, %v; want owner-a", claimed, won, err)
	}
}
