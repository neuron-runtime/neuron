package execution

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
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

func TestConcurrentClaimDispatchIntentWinnerLoserAndRecovery(t *testing.T) {
	store := openTestStore(t)
	s := NewExecutionStore(store)
	ctx := context.Background()
	intent := newDispatchIntent()

	if err := s.EnqueueDispatchIntent(ctx, intent); err != nil {
		t.Fatalf("enqueue dispatch intent: %v", err)
	}

	// The lease must outlive the whole concurrent phase. With a short lease the
	// winner's claim can expire while the two goroutines are being scheduled, so
	// the loser sees an expired claim, wins its own compare-and-swap, and both
	// callers report a win. A long lease keeps the loser's check deterministic;
	// expiry is modelled explicitly below instead of racing the wall clock.
	lease := time.Minute

	type claimResult struct {
		owner  string
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
			claimed, won, err := s.ClaimDispatchIntent(ctx, intent, owner, lease)
			results <- claimResult{owner: owner, intent: claimed, won: won, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	// Collect results: exactly one winner, exactly one loser.
	var winnerResult, loserResult claimResult
	foundWinner := false
	foundLoser := false
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent claim: %v", result.err)
		}
		if result.won {
			if foundWinner {
				t.Fatal("more than one concurrent claim won")
			}
			winnerResult = result
			foundWinner = true
		} else {
			if foundLoser {
				t.Fatal("more than one concurrent claim lost")
			}
			loserResult = result
			foundLoser = true
		}
	}
	if !foundWinner {
		t.Fatal("no concurrent claim won")
	}
	if !foundLoser {
		t.Fatal("no concurrent claim lost")
	}

	// Verify winner: should have a claimed intent with the winning owner.
	if winnerResult.intent == nil {
		t.Fatal("winner intent should not be nil when won is true")
	}
	if winnerResult.intent.Owner != winnerResult.owner {
		t.Fatalf("winner intent owner = %q, want %q", winnerResult.intent.Owner, winnerResult.owner)
	}

	// Verify loser: should have gotten (nil, false, nil) — the claim fails
	// because the intent is held under a live lease by the winner.
	if loserResult.intent != nil {
		t.Fatalf("loser should have nil intent, got %v", loserResult.intent)
	}
	if loserResult.won {
		t.Fatal("loser won should be false")
	}
	if loserResult.err != nil {
		t.Fatalf("loser error = %v, want nil", loserResult.err)
	}

	// Verify the stored intent reflects the winner's ownership.
	stored, err := readDispatchIntent(t, store, intent)
	if err != nil {
		t.Fatalf("read intent: %v", err)
	}
	if stored.Owner != winnerResult.owner {
		t.Fatalf("stored owner = %q, want %q", stored.Owner, winnerResult.owner)
	}

	// Model the lease expiring explicitly: move the stored expiry into the past
	// and prove the loser can then claim through the same public API. Waiting on
	// a short wall-clock lease here would reintroduce the timing dependence that
	// made the concurrent phase flaky.
	key, err := dispatchIntentKey(intent.ExecutionID, intent.CapabilityID, intent.AttemptID)
	if err != nil {
		t.Fatalf("dispatch intent key: %v", err)
	}
	expired := time.Now().UTC().Add(-time.Second)
	stored.LeaseExpiresAt = &expired
	encoded, err := json.Marshal(stored)
	if err != nil {
		t.Fatalf("marshal expired dispatch intent: %v", err)
	}
	if err := store.Put(ctx, key, encoded); err != nil {
		t.Fatalf("expire dispatch intent lease: %v", err)
	}

	claimed, won, err := s.ClaimDispatchIntent(ctx, intent, loserResult.owner, lease)
	if err != nil {
		t.Fatalf("claim after expiry: %v", err)
	}
	if !won || claimed == nil {
		t.Fatalf("claim after expiry = %v, %v; want a claimed intent", claimed, won)
	}
	if claimed.Owner != loserResult.owner {
		t.Fatalf("claimed owner = %q, want %q", claimed.Owner, loserResult.owner)
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

	claimed, err := s.ClaimReadyDispatchIntents(ctx, "reconciler", time.Minute, 10)
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

func TestClaimReadyDispatchIntentsRejectsNonPositiveLimit(t *testing.T) {
	for _, tc := range []struct {
		name  string
		limit int
	}{
		{name: "zero", limit: 0},
		{name: "negative", limit: -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listed := false
			store := &faultStore{list: func(string) ([]string, error) {
				listed = true
				return nil, errInjected
			}}
			s := NewExecutionStore(store)

			claimed, err := s.ClaimReadyDispatchIntents(context.Background(), "reconciler", time.Minute, tc.limit)
			if claimed != nil {
				t.Fatalf("claimed = %v, want nil", claimed)
			}
			if err == nil || err.Error() != "dispatch intent claim limit must be positive" {
				t.Fatalf("error = %v, want the limit validation error", err)
			}
			if listed {
				t.Fatal("dispatch intents were listed before the limit was validated")
			}
		})
	}
}

func TestClaimReadyDispatchIntentsRespectsLimit(t *testing.T) {
	store := openTestStore(t)
	s := NewExecutionStore(store)
	ctx := context.Background()

	intents := make([]*DispatchIntent, 5)
	for i := range intents {
		intents[i] = newDispatchIntent()
		if err := s.EnqueueDispatchIntent(ctx, intents[i]); err != nil {
			t.Fatalf("enqueue dispatch intent %d: %v", i, err)
		}
	}

	claimed, err := s.ClaimReadyDispatchIntents(ctx, "reconciler", time.Minute, 2)
	if err != nil {
		t.Fatalf("claim ready dispatch intents: %v", err)
	}
	if len(claimed) != 2 {
		t.Fatalf("claimed %d intents with limit 2, want 2", len(claimed))
	}
	for _, intent := range claimed {
		if intent.ExecutionID == "" || intent.CapabilityID == "" || intent.AttemptID == "" {
			t.Fatalf("claimed intent has incomplete identity: %+v", intent)
		}
		if intent.Owner != "reconciler" {
			t.Fatalf("claimed owner = %q, want reconciler", intent.Owner)
		}
		if intent.LeaseExpiresAt == nil || !intent.LeaseExpiresAt.After(time.Now()) {
			t.Fatalf("claimed lease = %v, want a live lease", intent.LeaseExpiresAt)
		}
	}

	// The keys the bounded batch never examined must stay claimable.
	remaining, err := s.ClaimReadyDispatchIntents(ctx, "reconciler-2", time.Minute, 10)
	if err != nil {
		t.Fatalf("claim remaining dispatch intents: %v", err)
	}
	if len(remaining) != 3 {
		t.Fatalf("claimed %d remaining intents, want 3", len(remaining))
	}
}

func TestClaimReadyDispatchIntentsUnderfilledBatch(t *testing.T) {
	store := openTestStore(t)
	s := NewExecutionStore(store)
	ctx := context.Background()

	ready := newDispatchIntent()
	unready := newDispatchIntent()
	unready.ReadyToRun = false
	for _, intent := range []*DispatchIntent{ready, unready} {
		if err := s.EnqueueDispatchIntent(ctx, intent); err != nil {
			t.Fatalf("enqueue dispatch intent: %v", err)
		}
	}

	claimed, err := s.ClaimReadyDispatchIntents(ctx, "reconciler", time.Minute, 10)
	if err != nil {
		t.Fatalf("claim ready dispatch intents: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed %d intents with limit 10, want 1", len(claimed))
	}
	got := claimed[0]
	if got.ExecutionID != ready.ExecutionID || got.CapabilityID != ready.CapabilityID || got.AttemptID != ready.AttemptID {
		t.Fatalf("claimed identity = (%s, %s, %s), want the ready intent", got.ExecutionID, got.CapabilityID, got.AttemptID)
	}
	if got.Owner != "reconciler" {
		t.Fatalf("claimed owner = %q, want reconciler", got.Owner)
	}
	if got.LeaseExpiresAt == nil || !got.LeaseExpiresAt.After(time.Now()) {
		t.Fatalf("claimed lease = %v, want a live lease", got.LeaseExpiresAt)
	}

	stored, err := readDispatchIntent(t, store, unready)
	if err != nil {
		t.Fatalf("read unready intent: %v", err)
	}
	if stored.Owner != "" || stored.LeaseExpiresAt != nil {
		t.Fatalf("unready intent was claimed: owner %q, lease %v", stored.Owner, stored.LeaseExpiresAt)
	}
}

func TestClaimReadyDispatchIntentsPartialFailureReleasesClaims(t *testing.T) {
	base := openTestStore(t)
	store := &dispatchClaimFaultStore{Store: base, failOnAttempt: 2, claimErr: errInjected}
	s := NewExecutionStore(store)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store.cancelOnClaimFail = cancel

	intents := []*DispatchIntent{newDispatchIntent(), newDispatchIntent(), newDispatchIntent()}
	for _, intent := range intents {
		if err := s.EnqueueDispatchIntent(ctx, intent); err != nil {
			t.Fatalf("enqueue dispatch intent: %v", err)
		}
	}
	store.armed = true

	claimed, err := s.ClaimReadyDispatchIntents(ctx, "reconciler", time.Minute, 10)
	if claimed != nil {
		t.Fatalf("claimed = %v, want nil on partial failure", claimed)
	}
	if !errors.Is(err, errInjected) {
		t.Fatalf("error = %v, want the injected claim failure", err)
	}
	if store.claimAttempts != 2 || store.releasePutCalls != 1 || store.successfulReleases != 1 {
		t.Fatalf("claim attempts = %d, release puts = %d, successful releases = %d; want 2, 1, 1",
			store.claimAttempts, store.releasePutCalls, store.successfulReleases)
	}
	if ctx.Err() == nil {
		t.Fatal("claim failure did not cancel the caller context")
	}

	// Cleanup must have detached from the cancelled caller context: every
	// already-won claim was released and no intent remains owned.
	for _, intent := range intents {
		stored, err := readDispatchIntent(t, base, intent)
		if err != nil {
			t.Fatalf("read dispatch intent: %v", err)
		}
		if stored.Owner != "" || stored.LeaseExpiresAt != nil {
			t.Fatalf("intent %s still owned after partial failure: owner %q, lease %v", intent.ExecutionID, stored.Owner, stored.LeaseExpiresAt)
		}
	}

	reclaimed, err := NewExecutionStore(base).ClaimReadyDispatchIntents(context.Background(), "reconciler-2", time.Minute, 10)
	if err != nil {
		t.Fatalf("reclaim after partial failure: %v", err)
	}
	if len(reclaimed) != len(intents) {
		t.Fatalf("reclaimed %d intents, want %d", len(reclaimed), len(intents))
	}
}

func TestClaimReadyDispatchIntentsReleaseFailureNamesLeak(t *testing.T) {
	base := openTestStore(t)
	releaseErr := errors.New("injected release failure")
	store := &dispatchClaimFaultStore{
		Store:         base,
		failOnAttempt: 2,
		claimErr:      errInjected,
		failRelease:   true,
		releaseErr:    releaseErr,
	}
	s := NewExecutionStore(store)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if err := s.EnqueueDispatchIntent(ctx, newDispatchIntent()); err != nil {
			t.Fatalf("enqueue dispatch intent: %v", err)
		}
	}
	store.armed = true

	claimed, err := s.ClaimReadyDispatchIntents(ctx, "reconciler", time.Minute, 10)
	if claimed != nil {
		t.Fatalf("claimed = %v, want nil on partial failure", claimed)
	}
	if !errors.Is(err, errInjected) {
		t.Fatalf("error = %v, want the injected claim failure", err)
	}
	if !errors.Is(err, releaseErr) {
		t.Fatalf("error = %v, want the injected release failure too", err)
	}
	if !strings.Contains(err.Error(), "remain leased until expiry") {
		t.Fatalf("error = %q, want it to name the leaked claims", err.Error())
	}
	if store.releasePutCalls != 1 || store.successfulReleases != 0 {
		t.Fatalf("release puts = %d, successful releases = %d; want 1 and 0", store.releasePutCalls, store.successfulReleases)
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
