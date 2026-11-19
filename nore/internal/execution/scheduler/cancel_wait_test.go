package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	executionmodel "github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/storage"
	core "github.com/neuron-runtime/neuron/shared/types/core"
)

// cancelCaptureStore records the durable write a cancellation makes and can
// force timer discovery to fail, so the fail-closed path is observable.
type cancelCaptureStore struct {
	*executionmodel.ExecutionStore
	commits int
	effects executionmodel.WaitEffects
	listErr error
}

func (s *cancelCaptureStore) WaitTimerKeys(ctx context.Context, executionID, capabilityID core.ID) ([]string, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.ExecutionStore.WaitTimerKeys(ctx, executionID, capabilityID)
}

func (s *cancelCaptureStore) SaveWaitTransition(ctx context.Context, req executionmodel.WaitTransitionRequest) (*executionmodel.WaitTransitionResult, error) {
	s.commits++
	s.effects = req.Effects
	return s.ExecutionStore.SaveWaitTransition(ctx, req)
}

func putWaitTimer(t *testing.T, h *waitHarness, capID, attemptID string, due time.Time) string {
	t.Helper()
	key, err := executionmodel.WaitTimerKey(h.exec.ID, core.ID(capID), core.ID(attemptID))
	if err != nil {
		t.Fatalf("WaitTimerKey: %v", err)
	}
	data, err := executionmodel.EncodeWaitTimer(executionmodel.WaitTimerPayload{
		ExecutionID: h.exec.ID, CapabilityID: core.ID(capID), WaitType: executionmodel.WaitTypeTimer,
	})
	if err != nil {
		t.Fatalf("EncodeWaitTimer: %v", err)
	}
	if err := h.raw.PutTimer(h.ctx, key, due, data); err != nil {
		t.Fatalf("PutTimer: %v", err)
	}
	return key
}

// A cancelled execution can never wake again, so every timer it owns must be
// removed by the same durable transition that makes it terminal. Leaving even
// one would keep a claimable timer that the poller repeatedly picks up for an
// execution that can no longer resume.
func TestCancelExecutionRemovesAllOwnedWaitTimers(t *testing.T) {
	h := newWaitHarness(t)
	capture := &cancelCaptureStore{ExecutionStore: h.store}
	h.waitStore = capture
	sched := h.scheduler(t)

	due := time.Now().UTC().Add(time.Hour)
	enterDueWait(t, h, sched, executionmodel.WaitTypeTimer, "", due)
	owned := []string{
		func() string {
			key, err := executionmodel.WaitTimerKey(h.exec.ID, "cap", h.exec.AttemptID)
			if err != nil {
				t.Fatalf("WaitTimerKey: %v", err)
			}
			return key
		}(),
		putWaitTimer(t, h, "cap", "stale_attempt", due),
		putWaitTimer(t, h, "cap_other", "attempt_1", due),
	}
	for _, key := range owned {
		if _, err := h.raw.GetTimer(h.ctx, key); err != nil {
			t.Fatalf("test setup: timer %s was not written: %v", key, err)
		}
	}

	// EnterWait already committed once; only the cancellation's commit counts.
	if capture.commits != 1 {
		t.Fatalf("commits before cancellation = %d, want 1", capture.commits)
	}
	cancelled, err := sched.CancelExecution(h.ctx, h.exec.ID, errors.New("operator stop"))
	if err != nil || !cancelled {
		t.Fatalf("CancelExecution = (%v, %v), want (true, nil)", cancelled, err)
	}
	if capture.commits != 2 {
		t.Fatalf("commits after cancellation = %d, want exactly one cancellation commit", capture.commits)
	}
	if len(capture.effects.DeleteTimers) != len(owned) {
		t.Fatalf("cancellation deleted %d timers, want all %d", len(capture.effects.DeleteTimers), len(owned))
	}
	for _, del := range capture.effects.DeleteTimers {
		if del.Owner != "" || del.RequireExisting {
			t.Errorf("cancellation delete %q used owner %q / requireExisting %v; cancellation must remove all timers unconditionally", del.Key, del.Owner, del.RequireExisting)
		}
	}
	for _, key := range owned {
		if _, err := h.raw.GetTimer(h.ctx, key); err == nil {
			t.Errorf("timer %s survived cancellation", key)
		}
	}
	keys, err := h.store.WaitTimerKeys(h.ctx, h.exec.ID, "")
	if err != nil {
		t.Fatalf("WaitTimerKeys: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("remaining wait timers = %v, want none", keys)
	}

	reloaded, err := h.store.Reload(h.ctx, h.exec.ID)
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if reloaded.Status() != executionmodel.StatusCancelled {
		t.Fatalf("durable status = %s, want %s", reloaded.Status(), executionmodel.StatusCancelled)
	}
	state := reloaded.CapabilityState("cap")
	if state.WaitType != executionmodel.WaitTypeTimer || state.WaitReason != "test" || state.WaitUntil == nil {
		t.Fatalf("wait metadata was not retained on the terminal snapshot: %+v", state)
	}
}

// A poller may already hold a lease on a timer when cancellation arrives. The
// cancellation's unconditional delete must still remove it: the poller's later
// fenced commit fails harmlessly, and the timer must not survive as live work.
func TestCancelExecutionRemovesLeasedTimer(t *testing.T) {
	h := newWaitHarness(t)
	sched := h.scheduler(t)
	// The timer must already be due for a poller to hold a claim on it.
	due := time.Now().UTC().Add(-time.Minute)
	enterDueWait(t, h, sched, executionmodel.WaitTypeTimer, "", due)
	key, err := executionmodel.WaitTimerKey(h.exec.ID, "cap", h.exec.AttemptID)
	if err != nil {
		t.Fatalf("WaitTimerKey: %v", err)
	}
	claimed, err := h.store.ClaimWaitTimer(h.ctx, key, "poller_owner", time.Minute)
	if err != nil || !claimed {
		t.Fatalf("ClaimWaitTimer = (%v, %v), want (true, nil)", claimed, err)
	}

	if cancelled, err := sched.CancelExecution(h.ctx, h.exec.ID, errors.New("stop")); err != nil || !cancelled {
		t.Fatalf("CancelExecution = (%v, %v), want (true, nil)", cancelled, err)
	}
	if _, err := h.raw.GetTimer(h.ctx, key); err == nil {
		t.Fatal("leased timer survived cancellation")
	}
}

// If timer discovery fails, cancellation must fail closed: no in-memory state
// change, no terminal event, and no durable write. Otherwise the execution
// would be cancelled in memory while stale timers still point at live work.
func TestCancelExecutionFailsClosedWhenTimerDiscoveryFails(t *testing.T) {
	h := newWaitHarness(t)
	capture := &cancelCaptureStore{ExecutionStore: h.store, listErr: errors.New("list unavailable")}
	h.waitStore = capture
	sched := h.scheduler(t)
	due := time.Now().UTC().Add(time.Hour)
	enterDueWait(t, h, sched, executionmodel.WaitTypeTimer, "", due)

	sub, err := h.bus.SubscribeExecution(h.exec.ID, 8)
	if err != nil {
		t.Fatalf("SubscribeExecution: %v", err)
	}
	defer func() { _ = sub.Close() }()
	before := capture.commits

	cancelled, err := sched.CancelExecution(h.ctx, h.exec.ID, errors.New("stop"))
	if err == nil {
		t.Fatal("cancellation succeeded despite a failed timer listing")
	}
	if cancelled {
		t.Fatal("cancellation reported success despite a failed timer listing")
	}
	if capture.commits != before {
		t.Fatalf("commits = %d, want unchanged %d after discovery failure", capture.commits, before)
	}
	if h.exec.Status() == executionmodel.StatusCancelled {
		t.Fatal("execution was made terminal despite a failed timer listing")
	}

	select {
	case evt := <-sub.Events():
		t.Fatalf("event %s was published for a cancellation that did not commit", evt.Type)
	case <-time.After(50 * time.Millisecond):
	}
}

// Non-durable schedulers have no wait store to discover timers from. Their
// cancellation path is unchanged: it still makes the execution terminal through
// the ordinary commit.
func TestCancelExecutionStillWorksWithoutWaitStore(t *testing.T) {
	h := newWaitHarness(t)
	sched, err := New(h.bus, h.store, h.scopes)
	if err != nil {
		t.Fatalf("New scheduler: %v", err)
	}
	cancelled, err := sched.CancelExecution(h.ctx, h.exec.ID, errors.New("stop"))
	if err != nil || !cancelled {
		t.Fatalf("CancelExecution = (%v, %v), want (true, nil)", cancelled, err)
	}
	if h.exec.Status() != executionmodel.StatusCancelled {
		t.Fatalf("status = %s, want %s", h.exec.Status(), executionmodel.StatusCancelled)
	}
}

// A non-durable scheduler has no wait store to reload from after a commit
// conflict. Cancellation must report the conflict instead of dereferencing the
// missing wait store.
func TestCancelExecutionConflictWithoutWaitStoreDoesNotPanic(t *testing.T) {
	h := newWaitHarness(t)
	committer := &fakeCommitter{failWhen: func(*executionmodel.Execution) error {
		return storage.ErrConflict
	}}
	sched, err := New(h.bus, h.store, h.scopes, WithTransitionCommitter(committer))
	if err != nil {
		t.Fatalf("New scheduler: %v", err)
	}
	sub, err := h.bus.SubscribeExecution(h.exec.ID, 8)
	if err != nil {
		t.Fatalf("SubscribeExecution: %v", err)
	}
	defer func() { _ = sub.Close() }()

	cancelled, err := sched.CancelExecution(h.ctx, h.exec.ID, errors.New("stop"))
	if !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("CancelExecution error = %v, want %v", err, storage.ErrConflict)
	}
	if !cancelled {
		t.Fatal("cancellation reported false after a commit conflict")
	}
	select {
	case evt := <-sub.Events():
		t.Fatalf("event %s was published for a conflicted cancellation", evt.Type)
	case <-time.After(50 * time.Millisecond):
	}
}

// A prefix listing must stay inside one execution and, when requested, one
// capability. It is the bounded primitive cancellation uses instead of scanning
// the whole timer space.
func TestWaitTimerKeysIsScoped(t *testing.T) {
	h := newWaitHarness(t)
	due := time.Now().UTC().Add(time.Hour)
	putWaitTimer(t, h, "cap_a", "attempt_1", due)
	putWaitTimer(t, h, "cap_a", "attempt_2", due)
	putWaitTimer(t, h, "cap_b", "attempt_1", due)

	otherKey := "timers/other/cap_a/attempt_1"
	if err := h.raw.PutTimer(h.ctx, otherKey, due, []byte("{}")); err != nil {
		t.Fatalf("PutTimer: %v", err)
	}

	all, err := h.store.WaitTimerKeys(h.ctx, h.exec.ID, "")
	if err != nil {
		t.Fatalf("WaitTimerKeys all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("execution timers = %v, want 3", all)
	}
	capA, err := h.store.WaitTimerKeys(h.ctx, h.exec.ID, "cap_a")
	if err != nil {
		t.Fatalf("WaitTimerKeys cap_a: %v", err)
	}
	if len(capA) != 2 {
		t.Fatalf("cap_a timers = %v, want 2", capA)
	}
	for _, key := range capA {
		prefix, err := storage.TimerPrefix(string(h.exec.ID), "cap_a")
		if err != nil {
			t.Fatalf("TimerPrefix: %v", err)
		}
		if len(key) <= len(prefix) || key[:len(prefix)] != prefix {
			t.Errorf("key %q is outside prefix %q", key, prefix)
		}
	}
}
