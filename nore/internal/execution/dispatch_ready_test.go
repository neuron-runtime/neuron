package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/storage"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

// listRecordingStore records every List prefix while delegating to a real
// backend. It proves which namespaces discovery touches without faking storage
// semantics.
type listRecordingStore struct {
	storage.Store
	mu       sync.Mutex
	prefixes []string
}

func (s *listRecordingStore) List(ctx context.Context, prefix string) ([]string, error) {
	s.mu.Lock()
	s.prefixes = append(s.prefixes, prefix)
	s.mu.Unlock()
	return s.Store.List(ctx, prefix)
}

func (s *listRecordingStore) listedPrefixes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.prefixes...)
}

// executionReadCountingStore counts point reads of execution records while
// delegating to a real backend. It proves a bounded claim pass does not read
// more executions than the keys it examined.
type executionReadCountingStore struct {
	storage.Store
	prefix string
	mu     sync.Mutex
	reads  int
}

func (s *executionReadCountingStore) Get(ctx context.Context, key string) ([]byte, error) {
	if strings.HasPrefix(key, s.prefix) {
		s.mu.Lock()
		s.reads++
		s.mu.Unlock()
	}
	return s.Store.Get(ctx, key)
}

func (s *executionReadCountingStore) executionReads() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads
}

func readyIndexKeys(t *testing.T, store storage.Store) []string {
	t.Helper()
	prefix, err := dispatchReadyPrefix()
	if err != nil {
		t.Fatalf("ready index prefix: %v", err)
	}
	keys, err := store.List(context.Background(), prefix)
	if err != nil {
		t.Fatalf("list ready index: %v", err)
	}
	return keys
}

func TestClaimReadyDispatchIntentsUsesOnlyReadyIndex(t *testing.T) {
	base := openTestStore(t)
	recorder := &listRecordingStore{Store: base}
	s := NewExecutionStore(recorder)
	ctx := context.Background()

	intent := dispatchIntentForInstance(t, s, "inst_test")
	if err := s.EnqueueDispatchIntent(ctx, intent); err != nil {
		t.Fatalf("enqueue dispatch intent: %v", err)
	}

	// A historical intent record with no ready-index entry must stay invisible
	// to discovery even though it lives under the old intent prefix.
	stale := newDispatchIntent()
	staleKey, err := dispatchIntentKey(stale.ExecutionID, stale.CapabilityID, stale.AttemptID)
	if err != nil {
		t.Fatalf("stale intent key: %v", err)
	}
	encoded, err := json.Marshal(stale)
	if err != nil {
		t.Fatalf("marshal stale intent: %v", err)
	}
	if err := base.Put(ctx, staleKey, encoded); err != nil {
		t.Fatalf("write stale intent: %v", err)
	}

	recorder.mu.Lock()
	recorder.prefixes = nil
	recorder.mu.Unlock()
	claimed, err := s.ClaimReadyDispatchIntents(ctx, "inst_test", "reconciler", time.Minute, 10)
	if err != nil {
		t.Fatalf("claim ready dispatch intents: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed %d intents, want 1", len(claimed))
	}
	if claimed[0].ExecutionID != intent.ExecutionID || claimed[0].AttemptID != intent.AttemptID {
		t.Fatalf("claimed identity = (%s, %s), want the indexed intent", claimed[0].ExecutionID, claimed[0].AttemptID)
	}
	for _, prefix := range recorder.listedPrefixes() {
		if strings.HasPrefix(prefix, dispatchIntentNamespace) {
			t.Fatalf("discovery listed intent prefix %q; want only the ready index", prefix)
		}
	}
}

func TestDispatchReadyIndexLifecycleBackends(t *testing.T) {
	backends := map[string]func(*testing.T) storage.Store{
		"memory": func(t *testing.T) storage.Store { return storage.NewMemoryStore() },
		"sqlite": openTestStore,
	}
	for name, open := range backends {
		t.Run(name, func(t *testing.T) {
			store := open(t)
			ctx := context.Background()
			s := NewExecutionStore(store)
			intent := dispatchIntentForInstance(t, s, "inst_test")

			if err := s.EnqueueDispatchIntent(ctx, intent); err != nil {
				t.Fatalf("enqueue: %v", err)
			}
			if got := readyIndexKeys(t, store); len(got) != 1 {
				t.Fatalf("ready keys after enqueue = %v, want 1", got)
			}

			claimed, err := s.ClaimReadyDispatchIntents(ctx, "inst_test", "owner-a", time.Minute, 10)
			if err != nil || len(claimed) != 1 {
				t.Fatalf("batch claim = %v, %v; want 1 claim", claimed, err)
			}
			if got := readyIndexKeys(t, store); len(got) != 1 {
				t.Fatalf("ready keys after claim = %v, want 1 (expired leases must stay discoverable)", got)
			}

			if err := s.ReleaseDispatchIntent(ctx, intent.ExecutionID, intent.CapabilityID, intent.AttemptID, "owner-a"); err != nil {
				t.Fatalf("release: %v", err)
			}
			claimed, err = s.ClaimReadyDispatchIntents(ctx, "inst_test", "owner-b", time.Minute, 10)
			if err != nil || len(claimed) != 1 {
				t.Fatalf("claim after release = %v, %v; want 1 claim", claimed, err)
			}

			if err := s.CompleteDispatchIntent(ctx, intent.ExecutionID, intent.CapabilityID, intent.AttemptID, "owner-b"); err != nil {
				t.Fatalf("complete: %v", err)
			}
			if got := readyIndexKeys(t, store); len(got) != 0 {
				t.Fatalf("ready keys after complete = %v, want none", got)
			}
			if _, err := readDispatchIntent(t, store, intent); !errors.Is(err, storage.ErrNotFound) {
				t.Fatalf("intent after complete = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestClaimReadyDispatchIntentsRediscoversExpiredLease(t *testing.T) {
	store := openTestStore(t)
	s := NewExecutionStore(store)
	ctx := context.Background()
	intent := dispatchIntentForInstance(t, s, "inst_test")

	if err := s.EnqueueDispatchIntent(ctx, intent); err != nil {
		t.Fatalf("enqueue dispatch intent: %v", err)
	}
	claimed, err := s.ClaimReadyDispatchIntents(ctx, "inst_test", "crashed", time.Millisecond, 10)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("first claim = %v, %v; want 1 claim", claimed, err)
	}
	time.Sleep(5 * time.Millisecond)

	rediscovered, err := s.ClaimReadyDispatchIntents(ctx, "inst_test", "replacement", time.Minute, 10)
	if err != nil || len(rediscovered) != 1 {
		t.Fatalf("claim after lease expiry = %v, %v; want 1 claim", rediscovered, err)
	}
	if rediscovered[0].Owner != "replacement" {
		t.Fatalf("replacement owner = %q, want replacement", rediscovered[0].Owner)
	}
}

func TestDispatchReadyIndexRemovedOnTerminalTransition(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	s := NewExecutionStore(store)
	exec := testExecution(t)
	if err := s.Add(ctx, exec); err != nil {
		t.Fatalf("add execution: %v", err)
	}
	intent := NewDispatchIntent(exec.ID, shared.NewID("cap_"), shared.NewID("att_"))
	if err := s.SaveTransition(ctx, exec, []*DispatchIntent{intent}); err != nil {
		t.Fatalf("save transition: %v", err)
	}
	if got := readyIndexKeys(t, store); len(got) != 1 {
		t.Fatalf("ready keys after transition = %v, want 1", got)
	}

	if !exec.MarkFailed(errors.New("terminal")) {
		t.Fatal("MarkFailed returned false")
	}
	if err := s.SaveTransition(ctx, exec, nil); err != nil {
		t.Fatalf("save terminal transition: %v", err)
	}
	if got := readyIndexKeys(t, store); len(got) != 0 {
		t.Fatalf("ready keys after terminal transition = %v, want none", got)
	}
	claimed, err := s.ClaimReadyDispatchIntents(ctx, "inst_test", "reconciler", time.Minute, 10)
	if err != nil || len(claimed) != 0 {
		t.Fatalf("claim after terminal transition = %v, %v; want no claims", claimed, err)
	}
}

func TestDispatchReadyIndexRemovedOnCancelledWaitTransition(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	s := NewExecutionStore(store)
	exec := testExecution(t)
	if err := s.Add(ctx, exec); err != nil {
		t.Fatalf("add execution: %v", err)
	}
	intent := NewDispatchIntent(exec.ID, shared.NewID("cap_"), shared.NewID("att_"))
	if err := s.SaveTransition(ctx, exec, []*DispatchIntent{intent}); err != nil {
		t.Fatalf("save transition: %v", err)
	}
	if !exec.MarkCancelled(errors.New("cancelled")) {
		t.Fatal("MarkCancelled returned false")
	}
	if _, err := s.SaveWaitTransition(ctx, WaitTransitionRequest{Execution: exec}); err != nil {
		t.Fatalf("save cancelled wait transition: %v", err)
	}
	if got := readyIndexKeys(t, store); len(got) != 0 {
		t.Fatalf("ready keys after cancelled wait transition = %v, want none", got)
	}
	claimed, err := s.ClaimReadyDispatchIntents(ctx, "inst_test", "reconciler", time.Minute, 10)
	if err != nil || len(claimed) != 0 {
		t.Fatalf("claim after cancellation = %v, %v; want no claims", claimed, err)
	}
}

func TestClearDispatchIntentsRemovesReadyIndex(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	s := NewExecutionStore(store)
	intent := dispatchIntentForInstance(t, s, "inst_test")
	if err := s.EnqueueDispatchIntent(ctx, intent); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if got := readyIndexKeys(t, store); len(got) != 1 {
		t.Fatalf("ready keys after enqueue = %v, want 1", got)
	}

	if err := s.ClearDispatchIntents(ctx, intent.ExecutionID); err != nil {
		t.Fatalf("clear dispatch intents: %v", err)
	}
	if got := readyIndexKeys(t, store); len(got) != 0 {
		t.Fatalf("ready keys after clear = %v, want none", got)
	}
	claimed, err := s.ClaimReadyDispatchIntents(ctx, "inst_test", "reconciler", time.Minute, 10)
	if err != nil || len(claimed) != 0 {
		t.Fatalf("claim after clear = %v, %v; want no claims", claimed, err)
	}
}

func TestClaimReadyDispatchIntentsInstanceIsolation(t *testing.T) {
	backends := map[string]func(*testing.T) storage.Store{
		"memory": func(t *testing.T) storage.Store { return storage.NewMemoryStore() },
		"sqlite": openTestStore,
	}
	for name, open := range backends {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			store := open(t)
			s := NewExecutionStore(store)

			own := dispatchIntentForInstance(t, s, "inst_own")
			foreign := dispatchIntentForInstance(t, s, "inst_foreign")
			for _, intent := range []*DispatchIntent{own, foreign} {
				if err := s.EnqueueDispatchIntent(ctx, intent); err != nil {
					t.Fatalf("enqueue dispatch intent: %v", err)
				}
			}

			foreignKey, err := dispatchIntentKey(foreign.ExecutionID, foreign.CapabilityID, foreign.AttemptID)
			if err != nil {
				t.Fatalf("foreign intent key: %v", err)
			}
			foreignBefore, err := store.Get(ctx, foreignKey)
			if err != nil {
				t.Fatalf("read foreign intent before claim: %v", err)
			}

			claimed, err := s.ClaimReadyDispatchIntents(ctx, "inst_own", "reconciler", time.Minute, 10)
			if err != nil {
				t.Fatalf("claim for inst_own: %v", err)
			}
			if len(claimed) != 1 || claimed[0].ExecutionID != own.ExecutionID {
				t.Fatalf("claimed = %v, want only inst_own's intent %s", claimed, own.ExecutionID)
			}
			if claimed[0].Owner != "reconciler" {
				t.Fatalf("claimed owner = %q, want reconciler", claimed[0].Owner)
			}

			// The foreign intent must not be written at all: no owner, no lease,
			// and no other persisted field changed.
			foreignAfter, err := store.Get(ctx, foreignKey)
			if err != nil {
				t.Fatalf("read foreign intent after claim: %v", err)
			}
			if !bytes.Equal(foreignBefore, foreignAfter) {
				t.Fatalf("foreign intent changed after another instance's claim: before %s, after %s", foreignBefore, foreignAfter)
			}

			claimed, err = s.ClaimReadyDispatchIntents(ctx, "inst_foreign", "reconciler-f", time.Minute, 10)
			if err != nil {
				t.Fatalf("claim for inst_foreign: %v", err)
			}
			if len(claimed) != 1 || claimed[0].ExecutionID != foreign.ExecutionID {
				t.Fatalf("claimed = %v, want only inst_foreign's intent %s", claimed, foreign.ExecutionID)
			}
			storedForeign, err := readDispatchIntent(t, store, foreign)
			if err != nil {
				t.Fatalf("read foreign intent after its owner claimed: %v", err)
			}
			if storedForeign.Owner != "reconciler-f" {
				t.Fatalf("foreign intent owner = %q, want reconciler-f", storedForeign.Owner)
			}
			storedOwn, err := readDispatchIntent(t, store, own)
			if err != nil {
				t.Fatalf("read own intent: %v", err)
			}
			if storedOwn.Owner != "reconciler" {
				t.Fatalf("own intent owner = %q, want reconciler", storedOwn.Owner)
			}
		})
	}
}

func TestClaimReadyDispatchIntentsBoundsExamination(t *testing.T) {
	backends := map[string]func(*testing.T) storage.Store{
		"memory": func(t *testing.T) storage.Store { return storage.NewMemoryStore() },
		"sqlite": openTestStore,
	}
	for name, open := range backends {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			base := open(t)

			// Fixed execution IDs make ready-key order deterministic: exec_a is
			// foreign, exec_b belongs to inst_own but is held by a live lease,
			// and exec_c belongs to inst_own and is claimable.
			setup := NewExecutionStore(base)
			foreign := dispatchIntentForInstanceID(t, setup, "exec_a", "inst_foreign")
			live := dispatchIntentForInstanceID(t, setup, "exec_b", "inst_own")
			ready := dispatchIntentForInstanceID(t, setup, "exec_c", "inst_own")
			for _, intent := range []*DispatchIntent{foreign, live, ready} {
				if err := setup.EnqueueDispatchIntent(ctx, intent); err != nil {
					t.Fatalf("enqueue dispatch intent: %v", err)
				}
			}
			if _, won, err := setup.ClaimDispatchIntent(ctx, live, "other-owner", time.Minute); err != nil || !won {
				t.Fatalf("claim live intent = won %v, err %v; want success", won, err)
			}

			executionPrefix, err := storage.Prefix("executions")
			if err != nil {
				t.Fatalf("execution prefix: %v", err)
			}
			counter := &executionReadCountingStore{Store: base, prefix: executionPrefix}
			// A fresh store has an empty memory cache, so each instance lookup is
			// a durable execution read that the wrapper counts.
			claimStore := NewExecutionStore(counter)

			// Both examined keys are skipped, so the batch is under-filled and
			// exec_c is left for a later pass.
			claimed, err := claimStore.ClaimReadyDispatchIntents(ctx, "inst_own", "reconciler", time.Minute, 2)
			if err != nil {
				t.Fatalf("bounded claim: %v", err)
			}
			if len(claimed) != 0 {
				t.Fatalf("claimed %d intents, want 0 under a cap of two skipped keys", len(claimed))
			}
			if got := counter.executionReads(); got != 2 {
				t.Fatalf("execution reads = %d, want 2", got)
			}

			storedLive, err := readDispatchIntent(t, base, live)
			if err != nil {
				t.Fatalf("read live intent: %v", err)
			}
			if storedLive.Owner != "other-owner" {
				t.Fatalf("live-lease intent owner = %q, want other-owner", storedLive.Owner)
			}
			storedForeign, err := readDispatchIntent(t, base, foreign)
			if err != nil {
				t.Fatalf("read foreign intent: %v", err)
			}
			if storedForeign.Owner != "" || storedForeign.LeaseExpiresAt != nil {
				t.Fatalf("foreign intent was written: owner %q, lease %v", storedForeign.Owner, storedForeign.LeaseExpiresAt)
			}

			// A later pass with a larger cap reaches the skipped own intent.
			claimed, err = claimStore.ClaimReadyDispatchIntents(ctx, "inst_own", "reconciler", time.Minute, 10)
			if err != nil {
				t.Fatalf("later claim: %v", err)
			}
			if len(claimed) != 1 || claimed[0].ExecutionID != ready.ExecutionID {
				t.Fatalf("claimed = %v, want only exec_c", claimed)
			}
			if got := counter.executionReads(); got != 5 {
				t.Fatalf("execution reads after second pass = %d, want 5", got)
			}

			// Foreign work stays discoverable and is claimed only by its owner.
			claimed, err = claimStore.ClaimReadyDispatchIntents(ctx, "inst_foreign", "reconciler-f", time.Minute, 10)
			if err != nil {
				t.Fatalf("foreign claim: %v", err)
			}
			if len(claimed) != 1 || claimed[0].ExecutionID != foreign.ExecutionID {
				t.Fatalf("claimed = %v, want only exec_a", claimed)
			}
			if got := counter.executionReads(); got != 8 {
				t.Fatalf("execution reads after third pass = %d, want 8", got)
			}
		})
	}
}

func TestClaimReadyDispatchIntentsSkipsUnattributedIntents(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	s := NewExecutionStore(store)

	orphan := newDispatchIntent() // ready intent with no execution record
	noInstance := dispatchIntentForInstanceID(t, s, "exec_no_instance", "")
	stale := newDispatchIntent() // ready key with no intent record
	for _, intent := range []*DispatchIntent{orphan, noInstance} {
		if err := s.EnqueueDispatchIntent(ctx, intent); err != nil {
			t.Fatalf("enqueue dispatch intent: %v", err)
		}
	}
	staleReadyKey, err := dispatchReadyKey(stale.ExecutionID, stale.CapabilityID, stale.AttemptID)
	if err != nil {
		t.Fatalf("stale ready key: %v", err)
	}
	if err := store.Put(ctx, staleReadyKey, []byte("missing-intent")); err != nil {
		t.Fatalf("write stale ready key: %v", err)
	}

	claimed, err := s.ClaimReadyDispatchIntents(ctx, "inst_test", "reconciler", time.Minute, 10)
	if err != nil {
		t.Fatalf("claim ready dispatch intents: %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("claimed %d intents, want 0 for unattributed records", len(claimed))
	}
	for _, intent := range []*DispatchIntent{orphan, noInstance} {
		stored, err := readDispatchIntent(t, store, intent)
		if err != nil {
			t.Fatalf("read dispatch intent: %v", err)
		}
		if stored.Owner != "" || stored.LeaseExpiresAt != nil {
			t.Fatalf("unattributed intent %s was written: owner %q, lease %v", intent.ExecutionID, stored.Owner, stored.LeaseExpiresAt)
		}
	}
}

func TestClaimReadyDispatchIntentsRejectsEmptyInstanceID(t *testing.T) {
	listed := false
	store := &faultStore{list: func(string) ([]string, error) {
		listed = true
		return nil, errInjected
	}}
	s := NewExecutionStore(store)

	claimed, err := s.ClaimReadyDispatchIntents(context.Background(), "", "reconciler", time.Minute, 10)
	if claimed != nil {
		t.Fatalf("claimed = %v, want nil", claimed)
	}
	if err == nil || err.Error() != "instance ID is required" {
		t.Fatalf("error = %v, want the instance ID validation error", err)
	}
	if listed {
		t.Fatal("dispatch intents were listed before the instance ID was validated")
	}
}
