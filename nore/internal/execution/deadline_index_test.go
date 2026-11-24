package execution

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/nore/internal/types"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

func indexBackends(t *testing.T) map[string]func(*testing.T) storage.Store {
	t.Helper()
	return map[string]func(*testing.T) storage.Store{
		"sqlite": openTestStore,
		"memory": func(t *testing.T) storage.Store { return storage.NewMemoryStore() },
	}
}

func indexedExecution(t *testing.T, instanceID shared.ID, deadline *time.Time) *Execution {
	t.Helper()
	exec := newTestExecution(t, &types.ExecutionBlueprint{Nodes: map[shared.ID]types.ExecutionNode{}}, instanceID)
	if deadline != nil {
		exec.SetExecutionDeadline(deadline)
	}
	return exec
}

func mustDueDeadlines(t *testing.T, s *ExecutionStore, instanceID shared.ID, now time.Time, limit int) []shared.ID {
	t.Helper()
	got, err := s.DueExecutionDeadlines(context.Background(), instanceID, now, limit)
	if err != nil {
		t.Fatalf("DueExecutionDeadlines(%s): %v", instanceID, err)
	}
	return got
}

func assertDeadlineIDs(t *testing.T, got []shared.ID, want ...shared.ID) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("due deadlines = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("due deadlines = %v, want %v", got, want)
		}
	}
}

// noExecutionListStore proves the deadline index is a dedicated namespace: once
// armed it fails if discovery lists the execution record prefix.
type noExecutionListStore struct {
	storage.Store
	mu     sync.Mutex
	armed  bool
	listed []string
}

func (s *noExecutionListStore) List(ctx context.Context, prefix string) ([]string, error) {
	s.mu.Lock()
	armed := s.armed
	s.listed = append(s.listed, prefix)
	s.mu.Unlock()
	if armed && prefix == "executions/" {
		return nil, fmt.Errorf("deadline discovery scanned executions")
	}
	return s.Store.List(ctx, prefix)
}

func (s *noExecutionListStore) arm() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.armed = true
	s.listed = nil
}

func (s *noExecutionListStore) prefixes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.listed...)
}

// TestExecutionDeadlineIndexTracksLifecycle covers every durable execution write
// path that must add, replace, or remove a deadline index entry.
func TestExecutionDeadlineIndexTracksLifecycle(t *testing.T) {
	for backend, open := range indexBackends(t) {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			s := NewExecutionStore(open(t))
			base := time.Now().UTC().Truncate(time.Second)
			early := base.Add(time.Minute)
			later := base.Add(2 * time.Minute)

			exec := indexedExecution(t, "inst_a", &early)
			if err := s.Add(ctx, exec); err != nil {
				t.Fatalf("Add: %v", err)
			}
			assertDeadlineIDs(t, mustDueDeadlines(t, s, "inst_a", base.Add(90*time.Second), 10), exec.ID)

			exec.SetExecutionDeadline(&later)
			if err := s.Save(ctx, exec); err != nil {
				t.Fatalf("Save replaced deadline: %v", err)
			}
			assertDeadlineIDs(t, mustDueDeadlines(t, s, "inst_a", base.Add(90*time.Second), 10))
			assertDeadlineIDs(t, mustDueDeadlines(t, s, "inst_a", base.Add(3*time.Minute), 10), exec.ID)

			exec.SetExecutionDeadline(nil)
			if err := s.Save(ctx, exec); err != nil {
				t.Fatalf("Save cleared deadline: %v", err)
			}
			assertDeadlineIDs(t, mustDueDeadlines(t, s, "inst_a", base.Add(time.Hour), 10))

			exec.SetExecutionDeadline(&early)
			if err := s.Save(ctx, exec); err != nil {
				t.Fatalf("Save restored deadline: %v", err)
			}
			if !exec.MarkFailed(errors.New("terminal")) {
				t.Fatal("MarkFailed did not reach a terminal state")
			}
			if err := s.SaveTransition(ctx, exec, nil); err != nil {
				t.Fatalf("SaveTransition terminal: %v", err)
			}
			assertDeadlineIDs(t, mustDueDeadlines(t, s, "inst_a", base.Add(time.Hour), 10))

			deleted := indexedExecution(t, "inst_a", &early)
			if err := s.Add(ctx, deleted); err != nil {
				t.Fatalf("Add deleted-case execution: %v", err)
			}
			assertDeadlineIDs(t, mustDueDeadlines(t, s, "inst_a", base.Add(time.Hour), 10), deleted.ID)
			if err := s.Delete(ctx, deleted.ID); err != nil {
				t.Fatalf("Delete: %v", err)
			}
			assertDeadlineIDs(t, mustDueDeadlines(t, s, "inst_a", base.Add(time.Hour), 10))
		})
	}
}

// TestExecutionDeadlineDiscoveryIsBoundedAndInstanceScoped protects the ADR
// 0008 contract: one instance's pass cannot scan executions or another
// instance's deadlines, and the batch bound is honored before mutation.
func TestExecutionDeadlineDiscoveryIsBoundedAndInstanceScoped(t *testing.T) {
	ctx := context.Background()
	raw := openTestStore(t)
	guarded := &noExecutionListStore{Store: raw}
	s := NewExecutionStore(guarded)
	base := time.Now().UTC().Add(-10 * time.Minute)

	a1 := indexedExecution(t, "inst_a", deadlinePtr(base.Add(1*time.Minute)))
	a2 := indexedExecution(t, "inst_a", deadlinePtr(base.Add(2*time.Minute)))
	a3 := indexedExecution(t, "inst_a", deadlinePtr(base.Add(3*time.Minute)))
	aFuture := indexedExecution(t, "inst_a", deadlinePtr(base.Add(time.Hour)))
	b1 := indexedExecution(t, "inst_b", deadlinePtr(base.Add(4*time.Minute)))

	for _, exec := range []*Execution{a1, a2, a3, aFuture, b1} {
		if err := s.Add(ctx, exec); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}

	guarded.arm()
	assertDeadlineIDs(t, mustDueDeadlines(t, s, "inst_a", base.Add(5*time.Minute), 2), a1.ID, a2.ID)
	assertDeadlineIDs(t, mustDueDeadlines(t, s, "inst_b", base.Add(5*time.Minute), 10), b1.ID)
	prefixes := guarded.prefixes()
	if len(prefixes) != 2 {
		t.Fatalf("List prefixes = %v, want exactly two scoped reads", prefixes)
	}
	for _, prefix := range prefixes {
		if !strings.HasPrefix(prefix, "execution_deadlines/") {
			t.Fatalf("discovery listed prefix %q, want an execution_deadlines prefix", prefix)
		}
	}
}

func deadlinePtr(t time.Time) *time.Time {
	return &t
}

// TestAdoptBackfillsLegacyExecutionDeadline simulates recovery of a snapshot
// written before the index existed: Adopt must make it discoverable.
func TestAdoptBackfillsLegacyExecutionDeadline(t *testing.T) {
	for backend, open := range indexBackends(t) {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			raw := open(t)
			s := NewExecutionStore(raw)
			deadline := time.Now().UTC().Add(time.Minute)
			exec := indexedExecution(t, "inst_a", &deadline)

			key, err := storage.Key("executions", string(exec.ID))
			if err != nil {
				t.Fatalf("execution key: %v", err)
			}
			data, err := marshalExecutionAt(exec, 3)
			if err != nil {
				t.Fatalf("marshal legacy snapshot: %v", err)
			}
			if err := raw.Put(ctx, key, data); err != nil {
				t.Fatalf("write legacy snapshot: %v", err)
			}

			if err := s.Adopt(ctx, exec); err != nil {
				t.Fatalf("Adopt: %v", err)
			}
			assertDeadlineIDs(t, mustDueDeadlines(t, s, "inst_a", deadline.Add(time.Second), 10), exec.ID)
		})
	}
}

// TestAdoptReconcilesReStampedDeadlineAcrossRestarts proves that a legacy
// deadline-less snapshot cannot accumulate one index entry per restart. Adopt
// deliberately does not write the snapshot back, so each restart re-stamps the
// missing deadline from the current clock; the index must be reconciled by
// execution identity rather than by the stale snapshot deadline.
func TestAdoptReconcilesReStampedDeadlineAcrossRestarts(t *testing.T) {
	for backend, open := range indexBackends(t) {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			raw := open(t)
			legacy := indexedExecution(t, "inst_a", nil)
			key, err := storage.Key("executions", string(legacy.ID))
			if err != nil {
				t.Fatalf("execution key: %v", err)
			}
			data, err := marshalExecutionAt(legacy, 3)
			if err != nil {
				t.Fatalf("marshal legacy snapshot: %v", err)
			}
			if err := raw.Put(ctx, key, data); err != nil {
				t.Fatalf("write legacy snapshot: %v", err)
			}

			// Three recoveries simulate three process restarts. Each one reads the
			// unchanged durable snapshot and stamps a different deadline.
			deadlines := []time.Time{
				time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC),
				time.Date(2031, 1, 2, 0, 0, 0, 0, time.UTC),
				time.Date(2031, 1, 3, 0, 0, 0, 0, time.UTC),
			}
			for i := range deadlines {
				s := NewExecutionStore(raw)
				loaded, err := s.Get(ctx, legacy.ID)
				if err != nil {
					t.Fatalf("restart %d load: %v", i+1, err)
				}
				if loaded.ExecutionDeadline() != nil {
					t.Fatalf("restart %d loaded a deadline from durable state; fixture is invalid", i+1)
				}
				loaded.SetExecutionDeadline(&deadlines[i])
				if err := s.Adopt(ctx, loaded); err != nil {
					t.Fatalf("restart %d Adopt: %v", i+1, err)
				}
			}

			prefix, err := storage.ExecutionDeadlinePrefix(string(legacy.InstanceID))
			if err != nil {
				t.Fatalf("deadline prefix: %v", err)
			}
			keys, err := raw.List(ctx, prefix)
			if err != nil {
				t.Fatalf("list deadline index: %v", err)
			}
			if len(keys) != 1 {
				t.Fatalf("deadline index after %d restarts = %v, want exactly one entry", len(deadlines), keys)
			}
			_, gotDue, gotID, err := storage.ParseExecutionDeadlineKey(keys[0])
			if err != nil {
				t.Fatalf("parse deadline index key %q: %v", keys[0], err)
			}
			if gotID != string(legacy.ID) || !gotDue.Equal(deadlines[len(deadlines)-1]) {
				t.Fatalf("deadline index entry = (%s, %v), want (%s, %v)", gotID, gotDue, legacy.ID, deadlines[len(deadlines)-1])
			}
		})
	}
}

// TestTerminalSaveRemovesAdoptedDeadlineIndexEntry reproduces the stale-index
// finding: Adopt indexes a deadline stamped only in memory, while the durable
// snapshot still carries none. A terminal save has no new key to write, so it
// must remove the adopted entry using the live execution's deadline.
func TestTerminalSaveRemovesAdoptedDeadlineIndexEntry(t *testing.T) {
	for backend, open := range indexBackends(t) {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			raw := open(t)
			s := NewExecutionStore(raw)

			legacy := indexedExecution(t, "inst_a", nil)
			key, err := storage.Key("executions", string(legacy.ID))
			if err != nil {
				t.Fatalf("execution key: %v", err)
			}
			data, err := marshalExecutionAt(legacy, 3)
			if err != nil {
				t.Fatalf("marshal legacy snapshot: %v", err)
			}
			if err := raw.Put(ctx, key, data); err != nil {
				t.Fatalf("write legacy snapshot: %v", err)
			}

			// A second execution keeps an ordinary indexed deadline so cleanup
			// must stay scoped to the recovered execution.
			other := indexedExecution(t, "inst_a", deadlinePtr(time.Date(2032, 1, 1, 0, 0, 0, 0, time.UTC)))
			if err := s.Add(ctx, other); err != nil {
				t.Fatalf("Add other execution: %v", err)
			}

			loaded, err := s.Get(ctx, legacy.ID)
			if err != nil {
				t.Fatalf("load legacy execution: %v", err)
			}
			stamped := time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC)
			loaded.SetExecutionDeadline(&stamped)
			if err := s.Adopt(ctx, loaded); err != nil {
				t.Fatalf("Adopt: %v", err)
			}

			prefix, err := storage.ExecutionDeadlinePrefix(string(legacy.InstanceID))
			if err != nil {
				t.Fatalf("deadline prefix: %v", err)
			}
			if got := deadlineIndexCount(t, raw, prefix, legacy.ID); got != 1 {
				t.Fatalf("precondition: index entries for %s = %d, want 1", legacy.ID, got)
			}

			if !loaded.MarkFailed(errors.New("terminal")) {
				t.Fatal("MarkFailed did not reach a terminal state")
			}
			if err := s.SaveTransition(ctx, loaded, nil); err != nil {
				t.Fatalf("SaveTransition terminal: %v", err)
			}

			if got := deadlineIndexCount(t, raw, prefix, legacy.ID); got != 0 {
				t.Fatalf("deadline index entries for terminal %s = %d, want 0", loaded.ID, got)
			}
			if got := deadlineIndexCount(t, raw, prefix, other.ID); got != 1 {
				t.Fatalf("deadline index entries for untouched %s = %d, want 1", other.ID, got)
			}
		})
	}
}

// TestDeleteRemovesAdoptedDeadlineIndexEntry reproduces the Delete-path
// finding: Adopt indexes a deadline stamped only in memory, so the durable
// snapshot cannot supply the index key. Deleting before any committed
// transition must still remove that exact entry and leave other owners intact.
func TestDeleteRemovesAdoptedDeadlineIndexEntry(t *testing.T) {
	for backend, open := range indexBackends(t) {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			raw := open(t)
			s := NewExecutionStore(raw)

			stamped := time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC)

			legacy := indexedExecution(t, "inst_a", nil)
			key, err := storage.Key("executions", string(legacy.ID))
			if err != nil {
				t.Fatalf("execution key: %v", err)
			}
			data, err := marshalExecutionAt(legacy, 3)
			if err != nil {
				t.Fatalf("marshal legacy snapshot: %v", err)
			}
			if err := raw.Put(ctx, key, data); err != nil {
				t.Fatalf("write legacy snapshot: %v", err)
			}

			// An unrelated execution in the same instance shares the deadline so
			// cleanup must stay keyed by execution identity, not by due time.
			other := indexedExecution(t, "inst_a", &stamped)
			if err := s.Add(ctx, other); err != nil {
				t.Fatalf("Add other execution: %v", err)
			}

			loaded, err := s.Get(ctx, legacy.ID)
			if err != nil {
				t.Fatalf("load legacy execution: %v", err)
			}
			if loaded.ExecutionDeadline() != nil {
				t.Fatal("precondition: durable snapshot carried a deadline")
			}
			loaded.SetExecutionDeadline(&stamped)
			if err := s.Adopt(ctx, loaded); err != nil {
				t.Fatalf("Adopt: %v", err)
			}

			indexPrefix, err := storage.ExecutionDeadlinePrefix(string(legacy.InstanceID))
			if err != nil {
				t.Fatalf("deadline prefix: %v", err)
			}
			if got := deadlineIndexCount(t, raw, indexPrefix, legacy.ID); got != 1 {
				t.Fatalf("precondition: index entries for %s = %d, want 1", legacy.ID, got)
			}
			if got := deadlineIndexCount(t, raw, indexPrefix, other.ID); got != 1 {
				t.Fatalf("precondition: index entries for unrelated %s = %d, want 1", other.ID, got)
			}

			if err := s.Delete(ctx, legacy.ID); err != nil {
				t.Fatalf("Delete: %v", err)
			}

			if got := deadlineIndexCount(t, raw, indexPrefix, legacy.ID); got != 0 {
				t.Fatalf("deadline index entries for deleted %s = %d, want 0", legacy.ID, got)
			}
			if got := deadlineIndexCount(t, raw, indexPrefix, other.ID); got != 1 {
				t.Fatalf("deadline index entries for untouched %s = %d, want 1", other.ID, got)
			}
			if _, err := raw.Get(ctx, key); !errors.Is(err, storage.ErrNotFound) {
				t.Fatalf("durable execution after Delete = %v, want ErrNotFound", err)
			}
			assertDeadlineIDs(t, mustDueDeadlines(t, s, legacy.InstanceID, stamped, 10), other.ID)
		})
	}
}

func deadlineIndexCount(t *testing.T, raw storage.Store, prefix string, executionID shared.ID) int {
	t.Helper()
	keys, err := raw.List(context.Background(), prefix)
	if err != nil {
		t.Fatalf("list deadline index: %v", err)
	}
	count := 0
	for _, key := range keys {
		_, _, ownerID, err := storage.ParseExecutionDeadlineKey(key)
		if err != nil {
			t.Fatalf("parse deadline index key %q: %v", key, err)
		}
		if shared.ID(ownerID) == executionID {
			count++
		}
	}
	return count
}

// TestExecutionDeadlineIndexSurvivesSQLiteRestart checks that the backfilled
// index is durable rather than a memory-only artifact.
func TestExecutionDeadlineIndexSurvivesSQLiteRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	raw := openSQLiteAt(t, dir)
	deadline := time.Now().UTC().Add(time.Minute)
	exec := indexedExecution(t, "inst_a", &deadline)

	key, err := storage.Key("executions", string(exec.ID))
	if err != nil {
		t.Fatalf("execution key: %v", err)
	}
	data, err := marshalExecutionAt(exec, 4)
	if err != nil {
		t.Fatalf("marshal legacy snapshot: %v", err)
	}
	if err := raw.Put(ctx, key, data); err != nil {
		t.Fatalf("write legacy snapshot: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	reopened := openSQLiteAt(t, dir)
	store := NewExecutionStore(reopened)
	loaded, err := store.Get(ctx, exec.ID)
	if err != nil {
		t.Fatalf("reload execution: %v", err)
	}
	if err := store.Adopt(ctx, loaded); err != nil {
		t.Fatalf("Adopt after restart: %v", err)
	}
	assertDeadlineIDs(t, mustDueDeadlines(t, store, "inst_a", deadline.Add(time.Second), 10), exec.ID)

	indexPrefix, err := storage.ExecutionDeadlinePrefix(string(exec.InstanceID))
	if err != nil {
		t.Fatalf("deadline prefix: %v", err)
	}
	keys, err := reopened.List(ctx, indexPrefix)
	if err != nil {
		t.Fatalf("list deadline index: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("deadline index keys = %v, want one", keys)
	}
}

func dueWaitExecution(t *testing.T, s *ExecutionStore, instanceID, capabilityID shared.ID) (*Execution, string, time.Time) {
	t.Helper()
	ctx := context.Background()
	exec := newTestExecution(t, blueprintFor(capabilityID), instanceID)
	if err := s.Add(ctx, exec); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := exec.Start(nil, 1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	makeRunning(t, exec, capabilityID)

	attemptID := shared.NewID("attempt_")
	due := time.Now().UTC().Add(-time.Minute)
	payload, err := EncodeWaitTimer(WaitTimerPayload{
		// InstanceID is intentionally omitted: scheduler writers do not know it,
		// so the store must stamp it from the execution.
		ExecutionID:  exec.ID,
		CapabilityID: capabilityID,
		WaitType:     WaitTypeTimer,
		AttemptID:    attemptID,
	})
	if err != nil {
		t.Fatalf("EncodeWaitTimer: %v", err)
	}
	key, write := waitTimerFixture(t, exec, capabilityID, attemptID, due, payload)
	if _, err := s.SaveWaitTransition(ctx, WaitTransitionRequest{
		Execution: exec,
		Effects:   WaitEffects{PutTimers: []WaitTimerWrite{write}},
	}); err != nil {
		t.Fatalf("SaveWaitTransition: %v", err)
	}
	return exec, key, due
}

// TestDueWaitTimersForInstanceIsScopedAndNonMutating protects the additive
// discovery contract: an instance can only see its own due waits, and discovery
// itself never claims or deletes a timer.
func TestDueWaitTimersForInstanceIsScopedAndNonMutating(t *testing.T) {
	for backend, open := range indexBackends(t) {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			raw := open(t)
			s := NewExecutionStore(raw)
			now := time.Now().UTC()

			execA, keyA, dueA := dueWaitExecution(t, s, "inst_a", "cap_a")
			_, keyB, _ := dueWaitExecution(t, s, "inst_b", "cap_b")
			assertDueWaitKeys(t, s, "inst_a", now, 10, keyA)
			assertDueWaitKeys(t, s, "inst_b", now, 10, keyB)
			assertDueWaitKeys(t, s, "inst_missing", now, 10)

			// Discovery must not claim: both timers are still claimable afterwards.
			if ok, err := s.ClaimWaitTimer(ctx, keyA, "owner_a", time.Minute); err != nil || !ok {
				t.Fatalf("ClaimWaitTimer(A) after discovery = (%v, %v), want true", ok, err)
			}
			if ok, err := s.ClaimWaitTimer(ctx, keyB, "owner_b", time.Minute); err != nil || !ok {
				t.Fatalf("ClaimWaitTimer(B) after discovery = (%v, %v), want true", ok, err)
			}

			data, err := raw.GetTimer(ctx, keyA)
			if err != nil {
				t.Fatalf("GetTimer(A): %v", err)
			}
			payload, err := DecodeWaitTimer(data)
			if err != nil {
				t.Fatalf("DecodeWaitTimer(A): %v", err)
			}
			if payload.InstanceID != execA.InstanceID {
				t.Fatalf("stamped InstanceID = %q, want %q", payload.InstanceID, execA.InstanceID)
			}

			if deleted, err := s.DeleteWaitTimerOwned(ctx, keyA, "owner_a"); err != nil || !deleted {
				t.Fatalf("DeleteWaitTimerOwned(A) = (%v, %v), want true", deleted, err)
			}
			assertDueWaitKeys(t, s, "inst_a", now, 10)
			assertDueWaitKeys(t, s, "inst_b", now, 10, keyB)

			// A stale index entry must not leak across instances or become visible
			// after the timer is deleted through a transition effect.
			if deleted, err := s.DeleteWaitTimerOwned(ctx, keyB, "owner_b"); err != nil || !deleted {
				t.Fatalf("DeleteWaitTimerOwned(B) = (%v, %v), want true", deleted, err)
			}
			assertDueWaitKeys(t, s, "inst_b", now, 10)

			indexPrefix, err := storage.WaitDuePrefix(string(execA.InstanceID))
			if err != nil {
				t.Fatalf("wait due prefix: %v", err)
			}
			keys, err := raw.List(ctx, indexPrefix)
			if err != nil {
				t.Fatalf("list wait due index: %v", err)
			}
			if len(keys) != 0 {
				t.Fatalf("wait due index after deletion = %v, want empty", keys)
			}
			_ = dueA
		})
	}
}

func assertDueWaitKeys(t *testing.T, s *ExecutionStore, instanceID shared.ID, now time.Time, limit int, want ...string) {
	t.Helper()
	got, err := s.DueWaitTimersForInstance(context.Background(), instanceID, now, limit)
	if err != nil {
		t.Fatalf("DueWaitTimersForInstance(%s): %v", instanceID, err)
	}
	if len(got) != len(want) {
		t.Fatalf("due waits for %s = %v, want keys %v", instanceID, timerKeys(got), want)
	}
	for i := range want {
		if got[i].Key != want[i] {
			t.Fatalf("due waits for %s = %v, want keys %v", instanceID, timerKeys(got), want)
		}
	}
}

func timerKeys(timers []storage.Timer) []string {
	keys := make([]string, 0, len(timers))
	for _, timer := range timers {
		keys = append(keys, timer.Key)
	}
	return keys
}
