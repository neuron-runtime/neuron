package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/event"
	executionmodel "github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/nore/internal/types"
	core "github.com/neuron-runtime/neuron/shared/types/core"
)

// recoveringCommitter records commits like fakeCommitter and also implements the
// recovery-only intent reset, so the tests can assert the single clear/commit/
// publish sequence that gives recovery its exactly-once behavior.
type recoveringCommitter struct {
	*fakeCommitter
	clears int
}

func (c *recoveringCommitter) ClearDispatchIntents(_ context.Context, _ core.ID) error {
	c.clears++
	return nil
}

type recoveryHarness struct {
	ctx       context.Context
	bus       *event.Bus
	store     *executionmodel.ExecutionStore
	scopes    *executionmodel.ScopeRegistry
	committer *recoveringCommitter
	sched     *Scheduler
	restored  *executionmodel.Execution
	ready     event.Subscription
	failed    event.Subscription
}

func recoveryBlueprint(ids ...core.ID) *types.ExecutionBlueprint {
	bp := &types.ExecutionBlueprint{
		Metadata:           core.Metadata{ID: "bp", Name: "recovery", Version: "1.0.0"},
		Nodes:              make(map[core.ID]types.ExecutionNode, len(ids)),
		EntryCapabilityIDs: ids,
	}
	for _, id := range ids {
		bp.Nodes[id] = stubNode(id, core.RuntimeExecutionModeWait)
	}
	return bp
}

func newRecoveryHarness(t *testing.T, ids ...core.ID) *recoveryHarness {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	bus := event.NewBus()
	store := executionmodel.NewExecutionStore(storage.NewMemoryStore())
	scopes := executionmodel.NewScopeRegistry(ctx)
	committer := &recoveringCommitter{fakeCommitter: &fakeCommitter{}}
	sched, err := New(bus, store, scopes, WithTransitionCommitter(committer))
	if err != nil {
		t.Fatalf("New scheduler: %v", err)
	}
	ready, err := bus.Subscribe(event.CapabilityReady, 16)
	if err != nil {
		t.Fatalf("subscribe CapabilityReady: %v", err)
	}
	t.Cleanup(func() { _ = ready.Close() })
	failed, err := bus.Subscribe(event.ExecutionFailed, 16)
	if err != nil {
		t.Fatalf("subscribe ExecutionFailed: %v", err)
	}
	t.Cleanup(func() { _ = failed.Close() })
	return &recoveryHarness{
		ctx: ctx, bus: bus, store: store, scopes: scopes,
		committer: committer, sched: sched, ready: ready, failed: failed,
	}
}

func makeSchedulerRunning(t *testing.T, exec *executionmodel.Execution, id core.ID) {
	t.Helper()
	if err := exec.MarkCapabilityReady(id, map[string]any{}); err != nil {
		t.Fatalf("MarkCapabilityReady(%s): %v", id, err)
	}
	if err := exec.MarkCapabilityRunning(id); err != nil {
		t.Fatalf("MarkCapabilityRunning(%s): %v", id, err)
	}
}

// restoreWaiting snapshots a fully waiting execution and reloads it with a
// reattached blueprint, exactly as instance recovery does, so RecoverExecution
// sees the durable StatusWaiting instead of an in-memory derived value.
func (h *recoveryHarness) restoreWaiting(t *testing.T, ids []core.ID, drive func(*executionmodel.Execution)) {
	t.Helper()
	bp := recoveryBlueprint(ids...)
	exec, err := executionmodel.NewExecution(bp, core.NewID("corr_"), "inst_1", "", "", "", "")
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	if err := h.store.Add(h.ctx, exec); err != nil {
		t.Fatalf("store.Add: %v", err)
	}
	if err := exec.Start(map[string]any{}, len(ids)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	drive(exec)
	if exec.Status() != executionmodel.StatusWaiting {
		t.Fatalf("test setup: status = %s, want %s before restore", exec.Status(), executionmodel.StatusWaiting)
	}
	data, err := executionmodel.MarshalExecution(exec)
	if err != nil {
		t.Fatalf("MarshalExecution: %v", err)
	}
	restored, err := executionmodel.UnmarshalExecution(data)
	if err != nil {
		t.Fatalf("UnmarshalExecution: %v", err)
	}
	restored.AttachBlueprint(bp)
	if restored.Status() != executionmodel.StatusWaiting {
		t.Fatalf("test setup: restored status = %s, want durable %s", restored.Status(), executionmodel.StatusWaiting)
	}
	h.restored = restored
}

func drainRecoveryEvents(t *testing.T, sub event.Subscription) []event.Event {
	t.Helper()
	got := make([]event.Event, 0)
	for {
		select {
		case evt, open := <-sub.Events():
			if !open {
				return got
			}
			got = append(got, evt)
		default:
			return got
		}
	}
}

func assertRecoveryCommittedIntent(t *testing.T, commits []fakeCommit, capabilityID core.ID) {
	t.Helper()
	if len(commits) != 1 {
		t.Fatalf("committed %d transitions, want 1", len(commits))
	}
	if len(commits[0].intents) != 1 {
		t.Fatalf("committed %d intents, want 1", len(commits[0].intents))
	}
	intent := commits[0].intents[0]
	if intent.CapabilityID != capabilityID || intent.ExecutionID == "" || !intent.ReadyToRun {
		t.Fatalf("committed intent = %+v, want a ready intent for %s", intent, capabilityID)
	}
}

// TestRecoverExecutionKeepsNonDueSignalWaitWaiting proves a healthy non-due
// signal wait is reconciled, not failed or dispatched: recovery commits the
// durable StatusWaiting with zero intents, publishes no CapabilityReady and no
// ExecutionFailed, and leaves the capability waiting with no worker occupied.
func TestRecoverExecutionKeepsNonDueSignalWaitWaiting(t *testing.T) {
	h := newRecoveryHarness(t, "cap")
	h.restoreWaiting(t, []core.ID{"cap"}, func(exec *executionmodel.Execution) {
		makeSchedulerRunning(t, exec, "cap")
		if err := exec.EnterWait("cap", executionmodel.WaitSpec{Type: executionmodel.WaitTypeSignal, ExpectedSignal: "approval"}); err != nil {
			t.Fatalf("EnterWait: %v", err)
		}
	})

	if err := h.sched.RecoverExecution(h.ctx, h.restored); err != nil {
		t.Fatalf("RecoverExecution: %v", err)
	}

	commits := h.committer.snapshot()
	if len(commits) != 1 {
		t.Fatalf("committed %d transitions, want 1", len(commits))
	}
	if commits[0].status != executionmodel.StatusWaiting {
		t.Fatalf("committed status = %s, want %s", commits[0].status, executionmodel.StatusWaiting)
	}
	if len(commits[0].intents) != 0 {
		t.Fatalf("committed %d intents, want 0 for a non-due signal wait", len(commits[0].intents))
	}
	if h.committer.clears != 1 {
		t.Errorf("cleared dispatch intents %d times, want 1", h.committer.clears)
	}
	if got := drainRecoveryEvents(t, h.ready); len(got) != 0 {
		t.Errorf("published %d CapabilityReady events, want 0: %v", len(got), got)
	}
	if got := drainRecoveryEvents(t, h.failed); len(got) != 0 {
		t.Errorf("published %d ExecutionFailed events for a healthy wait, want 0: %v", len(got), got)
	}
	if got := h.restored.Status(); got != executionmodel.StatusWaiting {
		t.Errorf("status = %s, want %s", got, executionmodel.StatusWaiting)
	}
	if got := h.restored.InFlight(); got != 0 {
		t.Errorf("in-flight = %d, want 0", got)
	}
	if got := h.restored.CapabilityState("cap").Status; got != executionmodel.CapabilityWaiting {
		t.Errorf("capability status = %s, want %s", got, executionmodel.CapabilityWaiting)
	}
}

// TestRecoverExecutionDispatchesElapsedTimerWaitExactlyOnce covers the missed
// timer at the scheduler boundary: an elapsed timer wait on a durable waiting
// execution must clear its wait state, commit one intent and publish exactly one
// CapabilityReady.
func TestRecoverExecutionDispatchesElapsedTimerWaitExactlyOnce(t *testing.T) {
	h := newRecoveryHarness(t, "cap")
	past := time.Now().UTC().Add(-time.Hour)
	h.restoreWaiting(t, []core.ID{"cap"}, func(exec *executionmodel.Execution) {
		makeSchedulerRunning(t, exec, "cap")
		if err := exec.EnterWait("cap", executionmodel.WaitSpec{Type: executionmodel.WaitTypeTimer, WaitUntil: &past}); err != nil {
			t.Fatalf("EnterWait: %v", err)
		}
	})

	if err := h.sched.RecoverExecution(h.ctx, h.restored); err != nil {
		t.Fatalf("RecoverExecution: %v", err)
	}

	commits := h.committer.snapshot()
	assertRecoveryCommittedIntent(t, commits, "cap")
	if commits[0].status != executionmodel.StatusRunning {
		t.Fatalf("committed status = %s, want %s after resume", commits[0].status, executionmodel.StatusRunning)
	}
	if h.committer.clears != 1 {
		t.Errorf("cleared dispatch intents %d times, want 1", h.committer.clears)
	}
	ready := drainRecoveryEvents(t, h.ready)
	if len(ready) != 1 {
		t.Fatalf("published %d CapabilityReady events, want 1: %v", len(ready), ready)
	}
	if ready[0].Type != event.CapabilityReady {
		t.Errorf("published event = %s, want %s", ready[0].Type, event.CapabilityReady)
	}
	if got := drainRecoveryEvents(t, h.failed); len(got) != 0 {
		t.Errorf("published %d ExecutionFailed events, want 0: %v", len(got), got)
	}
	state := h.restored.CapabilityState("cap")
	if state.Status != executionmodel.CapabilityReady {
		t.Errorf("capability status = %s, want %s", state.Status, executionmodel.CapabilityReady)
	}
	if state.Resume == nil || state.Resume.Cause != executionmodel.ResumeCauseTimer {
		t.Errorf("resume = %+v, want a timer resume descriptor", state.Resume)
	}
	if state.WaitUntil != nil || state.WaitType != "" {
		t.Errorf("wait metadata was not cleared on resume: %+v", state)
	}
	if got := h.restored.InFlight(); got != 1 {
		t.Errorf("in-flight = %d, want 1", got)
	}
	if got := h.restored.Status(); got != executionmodel.StatusRunning {
		t.Errorf("status = %s, want %s", got, executionmodel.StatusRunning)
	}
}

// TestRecoverExecutionDispatchesElapsedRetryWaitExactlyOnce covers the retry
// half of D5: an elapsed NextAttemptAt is dispatched once after recovery.
func TestRecoverExecutionDispatchesElapsedRetryWaitExactlyOnce(t *testing.T) {
	h := newRecoveryHarness(t, "cap")
	past := time.Now().UTC().Add(-time.Minute)
	h.restoreWaiting(t, []core.ID{"cap"}, func(exec *executionmodel.Execution) {
		makeSchedulerRunning(t, exec, "cap")
		if err := exec.BeginRetryWait("cap", past); err != nil {
			t.Fatalf("BeginRetryWait: %v", err)
		}
	})

	if err := h.sched.RecoverExecution(h.ctx, h.restored); err != nil {
		t.Fatalf("RecoverExecution: %v", err)
	}

	commits := h.committer.snapshot()
	assertRecoveryCommittedIntent(t, commits, "cap")
	if got := drainRecoveryEvents(t, h.ready); len(got) != 1 {
		t.Fatalf("published %d CapabilityReady events, want 1: %v", len(got), got)
	}
	if got := drainRecoveryEvents(t, h.failed); len(got) != 0 {
		t.Errorf("published %d ExecutionFailed events, want 0: %v", len(got), got)
	}
	state := h.restored.CapabilityState("cap")
	if state.Status != executionmodel.CapabilityReady || state.NextAttemptAt != nil {
		t.Errorf("retry capability = %+v, want ready with NextAttemptAt cleared", state)
	}
	if state.Resume == nil || state.Resume.Cause != executionmodel.ResumeCauseTimer {
		t.Errorf("resume = %+v, want a timer resume descriptor", state.Resume)
	}
	if got := h.restored.InFlight(); got != 1 {
		t.Errorf("in-flight = %d, want 1", got)
	}
	if got := h.restored.Status(); got != executionmodel.StatusRunning {
		t.Errorf("status = %s, want %s", got, executionmodel.StatusRunning)
	}
}

// TestRecoverExecutionDispatchesOnlyElapsedWaitsInMixedSet proves recovery is
// selective: with one elapsed timer and one non-due signal wait on the same
// restored execution, only the elapsed capability is dispatched and committed.
func TestRecoverExecutionDispatchesOnlyElapsedWaitsInMixedSet(t *testing.T) {
	h := newRecoveryHarness(t, "elapsed", "signaller")
	past := time.Now().UTC().Add(-time.Hour)
	h.restoreWaiting(t, []core.ID{"elapsed", "signaller"}, func(exec *executionmodel.Execution) {
		makeSchedulerRunning(t, exec, "elapsed")
		if err := exec.EnterWait("elapsed", executionmodel.WaitSpec{Type: executionmodel.WaitTypeTimer, WaitUntil: &past}); err != nil {
			t.Fatalf("EnterWait elapsed: %v", err)
		}
		makeSchedulerRunning(t, exec, "signaller")
		if err := exec.EnterWait("signaller", executionmodel.WaitSpec{Type: executionmodel.WaitTypeSignal, ExpectedSignal: "approval"}); err != nil {
			t.Fatalf("EnterWait signaller: %v", err)
		}
	})

	if err := h.sched.RecoverExecution(h.ctx, h.restored); err != nil {
		t.Fatalf("RecoverExecution: %v", err)
	}

	commits := h.committer.snapshot()
	assertRecoveryCommittedIntent(t, commits, "elapsed")
	ready := drainRecoveryEvents(t, h.ready)
	if len(ready) != 1 {
		t.Fatalf("published %d CapabilityReady events, want 1: %v", len(ready), ready)
	}
	if ready[0].Metadata.CapabilityID != "elapsed" {
		t.Errorf("published readiness for %s, want only elapsed", ready[0].Metadata.CapabilityID)
	}
	if got := drainRecoveryEvents(t, h.failed); len(got) != 0 {
		t.Errorf("published %d ExecutionFailed events, want 0: %v", len(got), got)
	}
	if got := h.restored.CapabilityState("elapsed").Status; got != executionmodel.CapabilityReady {
		t.Errorf("elapsed status = %s, want %s", got, executionmodel.CapabilityReady)
	}
	if got := h.restored.CapabilityState("signaller").Status; got != executionmodel.CapabilityWaiting {
		t.Errorf("non-due signal status = %s, want %s", got, executionmodel.CapabilityWaiting)
	}
	if got := h.restored.InFlight(); got != 1 {
		t.Errorf("in-flight = %d, want 1 (only the resumed capability)", got)
	}
	if got := h.restored.Status(); got != executionmodel.StatusRunning {
		t.Errorf("status = %s, want %s once runnable work exists", got, executionmodel.StatusRunning)
	}
}

// orderedRecoveryCommitter records the scheduler's clear/rebuild ordering while
// reusing the standard in-memory committer for transition bookkeeping.
type orderedRecoveryCommitter struct {
	*fakeCommitter
	order *orderLog
}

func (c *orderedRecoveryCommitter) ClearDispatchIntents(context.Context, core.ID) error {
	c.order.add("clear")
	return nil
}

func (c *orderedRecoveryCommitter) SaveTransition(ctx context.Context, execution *executionmodel.Execution, intents []*executionmodel.DispatchIntent) error {
	c.order.add("commit")
	return c.fakeCommitter.SaveTransition(ctx, execution, intents)
}

// TestRecoverExecutionClearsStaleIntentsBeforePostRotationRebuild covers ADR
// 0010 D3: recovery drops pre-rotation work before rebuilding intents from the
// recovered, rotated generation.
func TestRecoverExecutionClearsStaleIntentsBeforePostRotationRebuild(t *testing.T) {
	h := newRecoveryHarness(t, "cap")
	past := time.Now().UTC().Add(-time.Minute)
	h.restoreWaiting(t, []core.ID{"cap"}, func(exec *executionmodel.Execution) {
		makeSchedulerRunning(t, exec, "cap")
		if err := exec.EnterWait("cap", executionmodel.WaitSpec{Type: executionmodel.WaitTypeTimer, WaitUntil: &past}); err != nil {
			t.Fatalf("EnterWait: %v", err)
		}
	})
	prior := h.restored.AttemptID

	committer := &orderedRecoveryCommitter{fakeCommitter: &fakeCommitter{}, order: &orderLog{}}
	sched, err := New(h.bus, h.store, h.scopes, WithTransitionCommitter(committer))
	if err != nil {
		t.Fatalf("New scheduler: %v", err)
	}
	if err := sched.RecoverExecution(h.ctx, h.restored); err != nil {
		t.Fatalf("RecoverExecution: %v", err)
	}
	if h.restored.AttemptID == prior {
		t.Fatal("recovery did not rotate the generation")
	}

	if got := committer.order.snapshot(); len(got) != 2 || got[0] != "clear" || got[1] != "commit" {
		t.Fatalf("clear/commit order = %v, want [clear commit]", got)
	}
	commits := committer.snapshot()
	if len(commits) != 1 || len(commits[0].intents) != 1 {
		t.Fatalf("recovery commits = %d with intents %d, want one commit with one intent", len(commits), len(commits[0].intents))
	}
	intent := commits[0].intents[0]
	if intent.CapabilityID != "cap" || intent.AttemptID != h.restored.AttemptID {
		t.Fatalf("rebuilt intent = cap %q attempt %q, want cap cap attempt %q", intent.CapabilityID, intent.AttemptID, h.restored.AttemptID)
	}
}
