package execution

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/nore/internal/storage/sqlite"
)

func openSQLiteAt(t *testing.T, dir string) *sqlite.Store {
	t.Helper()
	store, err := sqlite.New(context.Background(), storage.Config{DataDir: dir})
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestClaimDispatchIntentAcrossStoresHasOneWinner(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	firstStore := openSQLiteAt(t, dir)
	secondStore := openSQLiteAt(t, dir)
	first := NewExecutionStore(firstStore)
	second := NewExecutionStore(secondStore)

	intent := newDispatchIntent()
	if err := first.EnqueueDispatchIntent(ctx, intent); err != nil {
		t.Fatalf("enqueue dispatch intent: %v", err)
	}

	type claimResult struct {
		intent *DispatchIntent
		won    bool
		err    error
	}
	results := make(chan claimResult, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, candidate := range []struct {
		store *ExecutionStore
		owner string
	}{
		{store: first, owner: "owner-a"},
		{store: second, owner: "owner-b"},
	} {
		candidate := candidate
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			claimed, won, err := candidate.store.ClaimDispatchIntent(ctx, intent, candidate.owner, time.Minute)
			results <- claimResult{intent: claimed, won: won, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	var winner *DispatchIntent
	for result := range results {
		if result.err != nil {
			t.Fatalf("cross-store claim: %v", result.err)
		}
		if result.won {
			if winner != nil {
				t.Fatal("more than one cross-store claim won")
			}
			winner = result.intent
		}
	}
	if winner == nil {
		t.Fatal("no cross-store claim won")
	}

	stored, err := readDispatchIntent(t, firstStore, intent)
	if err != nil {
		t.Fatalf("read intent: %v", err)
	}
	if stored.Owner != winner.Owner {
		t.Fatalf("stored owner = %q, winner owner = %q", stored.Owner, winner.Owner)
	}
}

func TestSaveAcrossStoresHasOneWinner(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	first := NewExecutionStore(openSQLiteAt(t, dir))
	second := NewExecutionStore(openSQLiteAt(t, dir))

	exec := testExecution(t)
	if err := first.Add(ctx, exec); err != nil {
		t.Fatalf("add execution: %v", err)
	}
	firstCopy, err := UnmarshalExecution(mustMarshal(t, exec))
	if err != nil {
		t.Fatalf("copy execution: %v", err)
	}
	secondCopy, err := UnmarshalExecution(mustMarshal(t, exec))
	if err != nil {
		t.Fatalf("copy execution: %v", err)
	}

	errs := make(chan error, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, candidate := range []struct {
		store     *ExecutionStore
		execution *Execution
	}{
		{store: first, execution: firstCopy},
		{store: second, execution: secondCopy},
	} {
		candidate := candidate
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- candidate.store.Save(ctx, candidate.execution)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	var successes, conflicts int
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, storage.ErrConflict):
			conflicts++
		default:
			t.Fatalf("cross-store save error = %v, want success or ErrConflict", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("cross-store saves: %d successes, %d conflicts; want 1 and 1", successes, conflicts)
	}
}

func TestSaveAcrossStoresRejectsStaleWriter(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	firstStore := openSQLiteAt(t, dir)
	secondStore := openSQLiteAt(t, dir)
	first := NewExecutionStore(firstStore)
	second := NewExecutionStore(secondStore)

	exec := testExecution(t)
	if err := first.Add(ctx, exec); err != nil {
		t.Fatalf("add execution: %v", err)
	}
	stale, err := second.Get(ctx, exec.ID)
	if err != nil {
		t.Fatalf("load stale execution: %v", err)
	}
	if err := first.Save(ctx, exec); err != nil {
		t.Fatalf("advance stored execution: %v", err)
	}

	if err := second.Save(ctx, stale); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("stale cross-store Save error = %v, want ErrConflict", err)
	}
}

func TestSaveTransitionAcrossStoresRejectsStaleWriter(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	firstStore := openSQLiteAt(t, dir)
	secondStore := openSQLiteAt(t, dir)
	first := NewExecutionStore(firstStore)
	second := NewExecutionStore(secondStore)

	exec := testExecution(t)
	if err := first.Add(ctx, exec); err != nil {
		t.Fatalf("add execution: %v", err)
	}
	stale, err := second.Get(ctx, exec.ID)
	if err != nil {
		t.Fatalf("load stale execution: %v", err)
	}
	if err := first.Save(ctx, exec); err != nil {
		t.Fatalf("advance stored execution: %v", err)
	}

	intent := dispatchIntentFor(stale.ID, "cap_a", "attempt_a")
	err = second.SaveTransition(ctx, stale, []*DispatchIntent{intent})
	if !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("stale cross-store SaveTransition error = %v, want ErrConflict", err)
	}
	if _, err := readDispatchIntent(t, firstStore, intent); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("stale transition intent = %v, want ErrNotFound", err)
	}
}
