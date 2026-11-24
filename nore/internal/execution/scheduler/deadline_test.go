package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/event"
	executionmodel "github.com/neuron-runtime/neuron/nore/internal/execution"
	core "github.com/neuron-runtime/neuron/shared/types/core"
)

// addDueDeadlineExecution persists a second execution with a max-lifetime
// deadline that is already due, writing its deadline index entry through the
// store rather than only mutating the live snapshot.
func addDueDeadlineExecution(t *testing.T, h *waitHarness, instanceID core.ID, deadline time.Time) *executionmodel.Execution {
	t.Helper()
	exec, err := executionmodel.NewExecution(singleCapabilityBlueprint("cap"), core.NewID("corr_"), instanceID, "", "", "", "")
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	if err := h.store.Add(h.ctx, exec); err != nil {
		t.Fatalf("store.Add: %v", err)
	}
	exec.SetExecutionDeadline(&deadline)
	if err := h.store.Save(h.ctx, exec); err != nil {
		t.Fatalf("store.Save: %v", err)
	}
	return exec
}

func TestExpireExecutionDeadlinesCancelsElapsedExecution(t *testing.T) {
	h := newWaitHarness(t)
	sched := h.scheduler(t)
	past := time.Now().UTC().Add(-time.Minute)
	h.exec.SetExecutionDeadline(&past)
	if err := h.store.Save(h.ctx, h.exec); err != nil {
		t.Fatalf("store.Save: %v", err)
	}

	sub, err := h.bus.SubscribeExecution(h.exec.ID, 8)
	if err != nil {
		t.Fatalf("SubscribeExecution: %v", err)
	}
	defer func() { _ = sub.Close() }()

	count, err := sched.ExpireExecutionDeadlines(h.ctx, h.exec.InstanceID)
	if err != nil || count != 1 {
		t.Fatalf("ExpireExecutionDeadlines = (%d, %v), want (1, nil)", count, err)
	}
	if h.exec.Status() != executionmodel.StatusCancelled {
		t.Fatalf("execution status = %s, want %s", h.exec.Status(), executionmodel.StatusCancelled)
	}
	expectTerminalEvent(t, sub, event.ExecutionCancelled)

	// A terminal execution has no deadline index entry and is never expired twice.
	count, err = sched.ExpireExecutionDeadlines(h.ctx, h.exec.InstanceID)
	if err != nil || count != 0 {
		t.Fatalf("second ExpireExecutionDeadlines = (%d, %v), want (0, nil)", count, err)
	}
}

func TestExpireExecutionDeadlinesIgnoresFutureNilAndTerminal(t *testing.T) {
	t.Run("future deadline", func(t *testing.T) {
		h := newWaitHarness(t)
		sched := h.scheduler(t)
		future := time.Now().UTC().Add(time.Hour)
		h.exec.SetExecutionDeadline(&future)
		if err := h.store.Save(h.ctx, h.exec); err != nil {
			t.Fatalf("store.Save: %v", err)
		}

		count, err := sched.ExpireExecutionDeadlines(h.ctx, h.exec.InstanceID)
		if err != nil || count != 0 {
			t.Fatalf("ExpireExecutionDeadlines = (%d, %v), want (0, nil)", count, err)
		}
		if h.exec.Status() != executionmodel.StatusRunning {
			t.Fatalf("execution status = %s, want running", h.exec.Status())
		}
	})

	t.Run("no deadline", func(t *testing.T) {
		h := newWaitHarness(t)
		sched := h.scheduler(t)
		h.exec.SetExecutionDeadline(nil)

		count, err := sched.ExpireExecutionDeadlines(h.ctx, h.exec.InstanceID)
		if err != nil || count != 0 {
			t.Fatalf("ExpireExecutionDeadlines = (%d, %v), want (0, nil)", count, err)
		}
		if h.exec.Status() != executionmodel.StatusRunning {
			t.Fatalf("execution status = %s, want running", h.exec.Status())
		}
	})

	t.Run("already terminal", func(t *testing.T) {
		h := newWaitHarness(t)
		sched := h.scheduler(t)
		past := time.Now().UTC().Add(-time.Minute)
		h.exec.SetExecutionDeadline(&past)
		if err := h.store.Save(h.ctx, h.exec); err != nil {
			t.Fatalf("store.Save: %v", err)
		}
		if !h.exec.MarkCancelled(errors.New("already finished")) {
			t.Fatal("test setup: could not terminalize execution")
		}

		count, err := sched.ExpireExecutionDeadlines(h.ctx, h.exec.InstanceID)
		if err != nil || count != 0 {
			t.Fatalf("ExpireExecutionDeadlines = (%d, %v), want (0, nil)", count, err)
		}
	})
}

// TestExpireExecutionDeadlinesRespectsBatchBound proves one poll cannot
// terminalize more executions than the scheduler's configured batch allows.
func TestExpireExecutionDeadlinesRespectsBatchBound(t *testing.T) {
	h := newWaitHarness(t)
	sched := h.scheduler(t)
	sched.pollConfig.BatchSize = 1
	past := time.Now().UTC().Add(-time.Minute)

	h.exec.SetExecutionDeadline(&past)
	if err := h.store.Save(h.ctx, h.exec); err != nil {
		t.Fatalf("store.Save: %v", err)
	}
	execs := []*executionmodel.Execution{h.exec}
	for n := 0; n < 2; n++ {
		execs = append(execs, addDueDeadlineExecution(t, h, h.exec.InstanceID, past))
	}

	for i := 0; i < len(execs); i++ {
		count, err := sched.ExpireExecutionDeadlines(h.ctx, h.exec.InstanceID)
		if err != nil || count != 1 {
			t.Fatalf("pass %d: ExpireExecutionDeadlines = (%d, %v), want (1, nil)", i, count, err)
		}
	}
	for _, exec := range execs {
		if !exec.IsTerminal() {
			t.Fatalf("execution %s was not expired", exec.ID)
		}
	}
	count, err := sched.ExpireExecutionDeadlines(h.ctx, h.exec.InstanceID)
	if err != nil || count != 0 {
		t.Fatalf("final ExpireExecutionDeadlines = (%d, %v), want (0, nil)", count, err)
	}
}

// TestExpireExecutionDeadlinesIsInstanceScoped proves one instance's poll
// neither discovers nor terminalizes another instance's due execution when both
// share a store.
func TestExpireExecutionDeadlinesIsInstanceScoped(t *testing.T) {
	h := newWaitHarness(t)
	sched := h.scheduler(t)
	past := time.Now().UTC().Add(-time.Minute)

	h.exec.SetExecutionDeadline(&past)
	if err := h.store.Save(h.ctx, h.exec); err != nil {
		t.Fatalf("store.Save: %v", err)
	}
	otherInstance := core.ID("inst_2")
	other := addDueDeadlineExecution(t, h, otherInstance, past)

	subA, err := h.bus.SubscribeExecution(h.exec.ID, 8)
	if err != nil {
		t.Fatalf("SubscribeExecution(A): %v", err)
	}
	defer func() { _ = subA.Close() }()
	subB, err := h.bus.SubscribeExecution(other.ID, 8)
	if err != nil {
		t.Fatalf("SubscribeExecution(B): %v", err)
	}
	defer func() { _ = subB.Close() }()

	count, err := sched.ExpireExecutionDeadlines(h.ctx, h.exec.InstanceID)
	if err != nil || count != 1 {
		t.Fatalf("expire instance A = (%d, %v), want (1, nil)", count, err)
	}
	expectTerminalEvent(t, subA, event.ExecutionCancelled)
	if other.IsTerminal() {
		t.Fatal("instance B execution was terminalized by instance A's poll")
	}
	due, err := h.store.DueExecutionDeadlines(h.ctx, h.exec.InstanceID, time.Now().UTC(), 8)
	if err != nil || len(due) != 0 {
		t.Fatalf("remaining deadlines for instance A = (%v, %v), want none", due, err)
	}

	count, err = sched.ExpireExecutionDeadlines(h.ctx, otherInstance)
	if err != nil || count != 1 {
		t.Fatalf("expire instance B = (%d, %v), want (1, nil)", count, err)
	}
	expectTerminalEvent(t, subB, event.ExecutionCancelled)
}

// deadlineRecordingStore records the discovery call the scheduler makes so the
// batch bound can be proven to reach the store instead of being applied after
// discovery.
type deadlineRecordingStore struct {
	*executionmodel.ExecutionStore
	gotLimit      int
	gotInstanceID core.ID
}

func (s *deadlineRecordingStore) DueExecutionDeadlines(ctx context.Context, instanceID core.ID, now time.Time, limit int) ([]core.ID, error) {
	s.gotLimit = limit
	s.gotInstanceID = instanceID
	return s.ExecutionStore.DueExecutionDeadlines(ctx, instanceID, now, limit)
}

func TestExpireExecutionDeadlinesPassesBatchBoundToStore(t *testing.T) {
	h := newWaitHarness(t)
	recorder := &deadlineRecordingStore{ExecutionStore: h.store}
	sched, err := New(h.eventBus, recorder, h.scopes, WithWaitStore(h.waitStore, TimerPollConfig{BatchSize: 2}))
	if err != nil {
		t.Fatalf("New scheduler: %v", err)
	}
	past := time.Now().UTC().Add(-time.Minute)
	h.exec.SetExecutionDeadline(&past)
	if err := h.store.Save(h.ctx, h.exec); err != nil {
		t.Fatalf("store.Save: %v", err)
	}
	for n := 0; n < 2; n++ {
		addDueDeadlineExecution(t, h, h.exec.InstanceID, past)
	}

	count, err := sched.ExpireExecutionDeadlines(h.ctx, h.exec.InstanceID)
	if err != nil || count != 2 {
		t.Fatalf("ExpireExecutionDeadlines = (%d, %v), want (2, nil)", count, err)
	}
	if recorder.gotLimit != 2 {
		t.Fatalf("store discovery limit = %d, want configured batch 2", recorder.gotLimit)
	}
	if recorder.gotInstanceID != h.exec.InstanceID {
		t.Fatalf("store discovery instance = %q, want %q", recorder.gotInstanceID, h.exec.InstanceID)
	}
}
