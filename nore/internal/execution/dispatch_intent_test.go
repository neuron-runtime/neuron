package execution

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/nore/internal/storage/postgres"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

func newDispatchIntent() *DispatchIntent {
	return &DispatchIntent{
		ExecutionID:  shared.NewID("exec_"),
		CapabilityID: shared.NewID("cap_"),
		AttemptID:    shared.NewID("att_"),
		ReadyToRun:   true,
	}
}

func readDispatchIntent(t *testing.T, store storage.Store, intent *DispatchIntent) (*DispatchIntent, error) {
	t.Helper()
	key, err := dispatchIntentKey(intent.ExecutionID, intent.CapabilityID, intent.AttemptID)
	if err != nil {
		t.Fatalf("dispatch intent key: %v", err)
	}
	data, err := store.Get(context.Background(), key)
	if err != nil {
		return nil, err
	}
	var stored DispatchIntent
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("decode dispatch intent: %v", err)
	}
	return &stored, nil
}

func TestEnqueueThenClaimDispatchIntent(t *testing.T) {
	store := openTestStore(t)
	s := NewExecutionStore(store)
	ctx := context.Background()
	intent := newDispatchIntent()

	if err := s.EnqueueDispatchIntent(ctx, intent); err != nil {
		t.Fatalf("enqueue dispatch intent: %v", err)
	}
	if intent.CreatedAt.IsZero() {
		t.Fatal("enqueue did not set the intent CreatedAt")
	}
	stored, err := readDispatchIntent(t, store, intent)
	if err != nil {
		t.Fatalf("read enqueued intent: %v", err)
	}
	if !stored.CreatedAt.Equal(intent.CreatedAt) {
		t.Fatalf("stored CreatedAt = %v, intent CreatedAt = %v", stored.CreatedAt, intent.CreatedAt)
	}

	before := time.Now()
	claimed, won, err := s.ClaimDispatchIntent(ctx, intent, "owner-a", time.Minute)
	if err != nil {
		t.Fatalf("claim dispatch intent: %v", err)
	}
	if !won || claimed == nil {
		t.Fatalf("claim = %v, %v; want a claimed intent", claimed, won)
	}
	if claimed.Owner != "owner-a" {
		t.Fatalf("claimed owner = %q, want owner-a", claimed.Owner)
	}
	if claimed.LeaseExpiresAt == nil || !claimed.LeaseExpiresAt.After(before) {
		t.Fatalf("claimed lease expiry = %v, want after %v", claimed.LeaseExpiresAt, before)
	}

	stored, err = readDispatchIntent(t, store, intent)
	if err != nil {
		t.Fatalf("read claimed intent: %v", err)
	}
	if stored.Owner != "owner-a" || stored.LeaseExpiresAt == nil {
		t.Fatalf("stored claim = owner %q, lease %v; want owner-a with expiry", stored.Owner, stored.LeaseExpiresAt)
	}
}

func TestClaimDispatchIntentRejectsLiveLease(t *testing.T) {
	store := openTestStore(t)
	s := NewExecutionStore(store)
	ctx := context.Background()
	intent := newDispatchIntent()

	if err := s.EnqueueDispatchIntent(ctx, intent); err != nil {
		t.Fatalf("enqueue dispatch intent: %v", err)
	}
	if _, won, err := s.ClaimDispatchIntent(ctx, intent, "owner-a", time.Minute); err != nil || !won {
		t.Fatalf("first claim = won %v, err %v; want success", won, err)
	}

	second, won, err := s.ClaimDispatchIntent(ctx, intent, "owner-b", time.Minute)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if won || second != nil {
		t.Fatalf("second claim = %v, %v; want no claim while the lease is live", second, won)
	}

	stored, err := readDispatchIntent(t, store, intent)
	if err != nil {
		t.Fatalf("read intent: %v", err)
	}
	if stored.Owner != "owner-a" {
		t.Fatalf("owner after rejected claim = %q, want owner-a", stored.Owner)
	}
}

func TestClaimDispatchIntentAfterLeaseExpiry(t *testing.T) {
	store := openTestStore(t)
	s := NewExecutionStore(store)
	ctx := context.Background()
	intent := newDispatchIntent()

	if err := s.EnqueueDispatchIntent(ctx, intent); err != nil {
		t.Fatalf("enqueue dispatch intent: %v", err)
	}
	if _, won, err := s.ClaimDispatchIntent(ctx, intent, "owner-a", 5*time.Millisecond); err != nil || !won {
		t.Fatalf("first claim = won %v, err %v; want success", won, err)
	}

	time.Sleep(20 * time.Millisecond)

	claimed, won, err := s.ClaimDispatchIntent(ctx, intent, "owner-b", time.Minute)
	if err != nil {
		t.Fatalf("claim after expiry: %v", err)
	}
	if !won || claimed == nil {
		t.Fatalf("claim after expiry = %v, %v; want a claimed intent", claimed, won)
	}
	if claimed.Owner != "owner-b" {
		t.Fatalf("owner after expiry = %q, want owner-b", claimed.Owner)
	}
}

func TestReleaseDispatchIntent(t *testing.T) {
	store := openTestStore(t)
	s := NewExecutionStore(store)
	ctx := context.Background()
	intent := newDispatchIntent()

	if err := s.EnqueueDispatchIntent(ctx, intent); err != nil {
		t.Fatalf("enqueue dispatch intent: %v", err)
	}
	if _, won, err := s.ClaimDispatchIntent(ctx, intent, "owner-a", time.Minute); err != nil || !won {
		t.Fatalf("claim = won %v, err %v; want success", won, err)
	}

	if err := s.ReleaseDispatchIntent(ctx, intent.ExecutionID, intent.CapabilityID, intent.AttemptID, "owner-b"); err != nil {
		t.Fatalf("release with non-matching owner: %v", err)
	}
	stored, err := readDispatchIntent(t, store, intent)
	if err != nil {
		t.Fatalf("read after non-matching release: %v", err)
	}
	if stored.Owner != "owner-a" || stored.LeaseExpiresAt == nil {
		t.Fatalf("non-matching release changed claim: owner %q, lease %v", stored.Owner, stored.LeaseExpiresAt)
	}

	if err := s.ReleaseDispatchIntent(ctx, intent.ExecutionID, intent.CapabilityID, intent.AttemptID, "owner-a"); err != nil {
		t.Fatalf("release with matching owner: %v", err)
	}
	stored, err = readDispatchIntent(t, store, intent)
	if err != nil {
		t.Fatalf("read after matching release: %v", err)
	}
	if stored.Owner != "" || stored.LeaseExpiresAt != nil {
		t.Fatalf("released intent = owner %q, lease %v; want cleared", stored.Owner, stored.LeaseExpiresAt)
	}

	if claimed, won, err := s.ClaimDispatchIntent(ctx, intent, "owner-b", time.Minute); err != nil || !won || claimed.Owner != "owner-b" {
		t.Fatalf("claim after release = %v, %v, %v; want owner-b", claimed, won, err)
	}
}

func TestCompleteDispatchIntent(t *testing.T) {
	store := openTestStore(t)
	s := NewExecutionStore(store)
	ctx := context.Background()
	intent := newDispatchIntent()

	if err := s.EnqueueDispatchIntent(ctx, intent); err != nil {
		t.Fatalf("enqueue dispatch intent: %v", err)
	}
	if _, won, err := s.ClaimDispatchIntent(ctx, intent, "owner-a", time.Minute); err != nil || !won {
		t.Fatalf("claim = won %v, err %v; want success", won, err)
	}

	if err := s.CompleteDispatchIntent(ctx, intent.ExecutionID, intent.CapabilityID, intent.AttemptID, "owner-b"); err != nil {
		t.Fatalf("complete with non-matching owner: %v", err)
	}
	if stored, err := readDispatchIntent(t, store, intent); err != nil || stored.Owner != "owner-a" {
		t.Fatalf("non-matching complete changed intent: %v, %v", stored, err)
	}

	if err := s.CompleteDispatchIntent(ctx, intent.ExecutionID, intent.CapabilityID, intent.AttemptID, "owner-a"); err != nil {
		t.Fatalf("complete with matching owner: %v", err)
	}
	if _, err := readDispatchIntent(t, store, intent); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("intent after complete = %v, want ErrNotFound", err)
	}

	if err := s.CompleteDispatchIntent(ctx, intent.ExecutionID, intent.CapabilityID, intent.AttemptID, "owner-a"); err != nil {
		t.Fatalf("repeated complete: %v", err)
	}
}

func TestClaimReadyDispatchIntents(t *testing.T) {
	store := openTestStore(t)
	s := NewExecutionStore(store)
	ctx := context.Background()

	ready := newDispatchIntent()
	live := newDispatchIntent()
	unready := newDispatchIntent()
	unready.ReadyToRun = false
	expired := newDispatchIntent()
	expired.Owner = "stale-owner"
	past := time.Now().Add(-time.Minute)
	expired.LeaseExpiresAt = &past

	for _, intent := range []*DispatchIntent{ready, live, unready, expired} {
		if err := s.EnqueueDispatchIntent(ctx, intent); err != nil {
			t.Fatalf("enqueue dispatch intent: %v", err)
		}
	}
	if _, won, err := s.ClaimDispatchIntent(ctx, live, "live-owner", time.Minute); err != nil || !won {
		t.Fatalf("claim live intent = won %v, err %v; want success", won, err)
	}

	claimed, err := s.ClaimReadyDispatchIntents(ctx, "reconciler", time.Minute)
	if err != nil {
		t.Fatalf("claim ready dispatch intents: %v", err)
	}
	if len(claimed) != 2 {
		t.Fatalf("claimed %d intents, want 2", len(claimed))
	}

	want := map[shared.ID]bool{ready.ExecutionID: true, expired.ExecutionID: true}
	for _, intent := range claimed {
		if !want[intent.ExecutionID] {
			t.Fatalf("claimed unexpected intent %s", intent.ExecutionID)
		}
		if intent.Owner != "reconciler" {
			t.Fatalf("claimed intent owner = %q, want reconciler", intent.Owner)
		}
		if intent.LeaseExpiresAt == nil || !intent.LeaseExpiresAt.After(time.Now()) {
			t.Fatalf("claimed intent lease = %v, want a live lease", intent.LeaseExpiresAt)
		}
		delete(want, intent.ExecutionID)
	}
	if len(want) != 0 {
		t.Fatalf("missing claimed intents: %v", want)
	}

	stored, err := readDispatchIntent(t, store, live)
	if err != nil {
		t.Fatalf("read live intent: %v", err)
	}
	if stored.Owner != "live-owner" {
		t.Fatalf("live intent owner = %q, want live-owner", stored.Owner)
	}

	stored, err = readDispatchIntent(t, store, unready)
	if err != nil {
		t.Fatalf("read unready intent: %v", err)
	}
	if stored.Owner != "" {
		t.Fatalf("unready intent was claimed by %q", stored.Owner)
	}
}

func TestConcurrentClaimDispatchIntentHasOneWinner(t *testing.T) {
	store := openTestStore(t)
	s := NewExecutionStore(store)
	ctx := context.Background()
	intent := newDispatchIntent()

	if err := s.EnqueueDispatchIntent(ctx, intent); err != nil {
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
	for _, owner := range []string{"owner-a", "owner-b"} {
		owner := owner
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			claimed, won, err := s.ClaimDispatchIntent(ctx, intent, owner, time.Minute)
			results <- claimResult{intent: claimed, won: won, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	var winner *DispatchIntent
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent claim: %v", result.err)
		}
		if result.won {
			if winner != nil {
				t.Fatal("more than one concurrent claim won")
			}
			winner = result.intent
		}
	}
	if winner == nil {
		t.Fatal("no concurrent claim won")
	}

	stored, err := readDispatchIntent(t, store, intent)
	if err != nil {
		t.Fatalf("read intent: %v", err)
	}
	if stored.Owner != winner.Owner {
		t.Fatalf("stored owner = %q, winner owner = %q", stored.Owner, winner.Owner)
	}
}

func TestDispatchIntentPostgresParity(t *testing.T) {
	dsn := os.Getenv("NEURON_TEST_POSTGRES_DSN")
	if dsn == "" {
		dsn = os.Getenv("NEURON_DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("postgres DSN not set; skipping dispatch intent parity")
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
			exerciseDispatchIntentLifecycle(t, store)
		})
	}
}

func exerciseDispatchIntentLifecycle(t *testing.T, store storage.Store) {
	t.Helper()
	ctx := context.Background()
	s := NewExecutionStore(store)
	intent := newDispatchIntent()

	if err := s.EnqueueDispatchIntent(ctx, intent); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	claimed, won, err := s.ClaimDispatchIntent(ctx, intent, "owner-a", time.Minute)
	if err != nil || !won || claimed == nil || claimed.Owner != "owner-a" {
		t.Fatalf("first claim = %v, %v, %v; want owner-a", claimed, won, err)
	}
	if _, won, err := s.ClaimDispatchIntent(ctx, intent, "owner-b", time.Minute); err != nil || won {
		t.Fatalf("live lease claim = won %v, err %v; want false", won, err)
	}
	if err := s.ReleaseDispatchIntent(ctx, intent.ExecutionID, intent.CapabilityID, intent.AttemptID, "owner-a"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, won, err := s.ClaimDispatchIntent(ctx, intent, "owner-b", time.Minute); err != nil || !won {
		t.Fatalf("claim after release = won %v, err %v; want claimed", won, err)
	}
	if err := s.CompleteDispatchIntent(ctx, intent.ExecutionID, intent.CapabilityID, intent.AttemptID, "owner-b"); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if _, err := readDispatchIntent(t, store, intent); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("intent after complete = %v, want ErrNotFound", err)
	}
}
