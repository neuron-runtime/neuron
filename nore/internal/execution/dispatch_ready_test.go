package execution

import (
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

	intent := newDispatchIntent()
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
	claimed, err := s.ClaimReadyDispatchIntents(ctx, "reconciler", time.Minute, 10)
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
			intent := newDispatchIntent()

			if err := s.EnqueueDispatchIntent(ctx, intent); err != nil {
				t.Fatalf("enqueue: %v", err)
			}
			if got := readyIndexKeys(t, store); len(got) != 1 {
				t.Fatalf("ready keys after enqueue = %v, want 1", got)
			}

			claimed, err := s.ClaimReadyDispatchIntents(ctx, "owner-a", time.Minute, 10)
			if err != nil || len(claimed) != 1 {
				t.Fatalf("batch claim = %v, %v; want 1 claim", claimed, err)
			}
			if got := readyIndexKeys(t, store); len(got) != 1 {
				t.Fatalf("ready keys after claim = %v, want 1 (expired leases must stay discoverable)", got)
			}

			if err := s.ReleaseDispatchIntent(ctx, intent.ExecutionID, intent.CapabilityID, intent.AttemptID, "owner-a"); err != nil {
				t.Fatalf("release: %v", err)
			}
			claimed, err = s.ClaimReadyDispatchIntents(ctx, "owner-b", time.Minute, 10)
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
	intent := newDispatchIntent()

	if err := s.EnqueueDispatchIntent(ctx, intent); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	claimed, err := s.ClaimReadyDispatchIntents(ctx, "crashed", time.Millisecond, 10)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("first claim = %v, %v; want 1 claim", claimed, err)
	}
	time.Sleep(5 * time.Millisecond)

	rediscovered, err := s.ClaimReadyDispatchIntents(ctx, "replacement", time.Minute, 10)
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
	claimed, err := s.ClaimReadyDispatchIntents(ctx, "reconciler", time.Minute, 10)
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
	claimed, err := s.ClaimReadyDispatchIntents(ctx, "reconciler", time.Minute, 10)
	if err != nil || len(claimed) != 0 {
		t.Fatalf("claim after cancellation = %v, %v; want no claims", claimed, err)
	}
}

func TestClearDispatchIntentsRemovesReadyIndex(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	s := NewExecutionStore(store)
	intent := newDispatchIntent()
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
	claimed, err := s.ClaimReadyDispatchIntents(ctx, "reconciler", time.Minute, 10)
	if err != nil || len(claimed) != 0 {
		t.Fatalf("claim after clear = %v, %v; want no claims", claimed, err)
	}
}
