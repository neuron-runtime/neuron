package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/event"
	executionmodel "github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/nore/internal/types"
	core "github.com/neuron-runtime/neuron/shared/types/core"
)

// newTwoWaitHarness parks one shared execution with two waiting capabilities:
// "signal" waits for a delivery, and "deadline" waits for a signal whose
// deadline has elapsed under the fail policy (so a poller can terminate it).
func newTwoWaitHarness(t *testing.T) *waitHarness {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	bus := event.NewBus()
	raw := storage.NewMemoryStore()
	store := executionmodel.NewExecutionStore(raw)
	scopes := executionmodel.NewScopeRegistry(ctx)
	bp := &types.ExecutionBlueprint{
		Metadata: core.Metadata{ID: "bp", Name: "concurrent", Version: "1.0.0"},
		Nodes: map[core.ID]types.ExecutionNode{
			"signal":   stubNode("signal", core.RuntimeExecutionModeWait),
			"deadline": stubNode("deadline", core.RuntimeExecutionModeWait),
		},
		EntryCapabilityIDs: []core.ID{"signal", "deadline"},
	}
	exec, err := executionmodel.NewExecution(bp, core.NewID("corr_"), "inst_1", "", "", "", "")
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	if err := store.Add(ctx, exec); err != nil {
		t.Fatalf("store.Add: %v", err)
	}
	if err := exec.Start(map[string]any{}, 2); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for _, id := range []core.ID{"signal", "deadline"} {
		if err := exec.MarkCapabilityReady(id, map[string]any{}); err != nil {
			t.Fatalf("MarkCapabilityReady(%s): %v", id, err)
		}
		if err := exec.MarkCapabilityRunning(id); err != nil {
			t.Fatalf("MarkCapabilityRunning(%s): %v", id, err)
		}
	}
	return &waitHarness{
		ctx: ctx, bus: bus, raw: raw, store: store, scopes: scopes,
		eventBus: bus, waitStore: store, exec: exec,
	}
}

// gatedCommitFaultStore blocks the first SaveWaitTransition until released and
// then fails it, while delegating every later transition. This forces one
// driver's rollback to happen after a peer has already committed a newer
// revision to the same shared *Execution.
type gatedCommitFaultStore struct {
	*executionmodel.ExecutionStore
	mu      sync.Mutex
	calls   int
	first   chan struct{}
	second  chan struct{}
	release chan struct{}
	err     error
}

func (s *gatedCommitFaultStore) SaveWaitTransition(ctx context.Context, req executionmodel.WaitTransitionRequest) (*executionmodel.WaitTransitionResult, error) {
	s.mu.Lock()
	s.calls++
	n := s.calls
	s.mu.Unlock()
	switch n {
	case 1:
		close(s.first)
		<-s.release
		return nil, s.err
	case 2:
		defer close(s.second)
	}
	return s.ExecutionStore.SaveWaitTransition(ctx, req)
}

// TestConcurrentWaitDriverRollbackCannotPoisonCommittedPeer reproduces T-041:
// one shared *Execution is driven by a signal resolution and a due-timer poll.
// The signal driver snapshots `before`, then the poller commits a newer
// revision, then the signal driver's commit fails and restores `before`. The
// live copy must not end up behind durable storage.
func TestConcurrentWaitDriverRollbackCannotPoisonCommittedPeer(t *testing.T) {
	h := newTwoWaitHarness(t)
	sched := h.scheduler(t)

	// Park both capabilities through the real store before installing the gate.
	if err := sched.EnterWait(h.ctx, WaitEntryRequest{
		Execution: h.exec, CapabilityID: "signal",
		Spec: executionmodel.WaitSpec{Type: executionmodel.WaitTypeSignal, ExpectedSignal: "approved"},
	}); err != nil {
		t.Fatalf("EnterWait(signal): %v", err)
	}
	deadline := time.Now().UTC().Add(-time.Minute)
	if err := sched.EnterWait(h.ctx, WaitEntryRequest{
		Execution: h.exec, CapabilityID: "deadline",
		Spec: executionmodel.WaitSpec{
			Type: executionmodel.WaitTypeSignal, ExpectedSignal: "approved",
			WaitUntil: &deadline, DeadlinePolicy: executionmodel.WaitDeadlinePolicyFail,
		},
	}); err != nil {
		t.Fatalf("EnterWait(deadline): %v", err)
	}

	fault := &gatedCommitFaultStore{
		ExecutionStore: h.store,
		first:          make(chan struct{}),
		second:         make(chan struct{}),
		release:        make(chan struct{}),
		err:            errors.New("injected signal commit failure"),
	}
	sched.waitStore = fault

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = sched.ResolveSignal(h.ctx, SignalResolutionRequest{
			Execution: h.exec, CapabilityID: "signal", DeliveryID: "del_1",
			SignalName: "approved", Payload: map[string]any{"ok": true},
		})
	}()
	select {
	case <-fault.first:
	case <-time.After(5 * time.Second):
		t.Fatal("signal driver never reached its commit")
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = sched.PollDueTimers(h.ctx, h.exec.InstanceID)
	}()
	select {
	case <-fault.second:
		// Pre-fix interleaving: the poller committed a newer revision while the
		// signal driver was gated.
	case <-time.After(100 * time.Millisecond):
		// Fixed interleaving: the poller is serialized behind the signal driver.
	}

	close(fault.release)
	wg.Wait()

	stored, err := h.store.Reload(h.ctx, h.exec.ID)
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if h.exec.Revision() != stored.Revision() {
		t.Fatalf("live revision %d != durable revision %d: stale rollback poisoned the shared execution", h.exec.Revision(), stored.Revision())
	}
	if h.exec.Status() != stored.Status() {
		t.Fatalf("live status %s != durable status %s: stale rollback poisoned the shared execution", h.exec.Status(), stored.Status())
	}
}

func countLogEntries(items []string, want string) int {
	n := 0
	for _, item := range items {
		if item == want {
			n++
		}
	}
	return n
}

// TestConcurrentSignalVsPollerCommitsOnce is the primary ADR 0004 regression:
// one shared *Execution is driven at a common barrier by both a signal delivery
// and a due-timer poll whose deadline policy resumes to CapabilityReady. The
// guard on the shared value must let exactly one driver commit and publish; the
// loser must fail its precheck without publishing a second CapabilityReady.
func TestConcurrentSignalVsPollerCommitsOnce(t *testing.T) {
	h := newWaitHarness(t)
	order := &orderLog{}
	h.waitStore = &recordingWaitStore{ExecutionStore: h.store, log: order}
	h.eventBus = &recordingBus{Bus: h.bus, log: order}
	sched := h.scheduler(t)

	// A signal wait with an elapsed deadline under the continue policy is a
	// candidate for both drivers: the signal resumes it and the poller's expiry
	// path also resumes it to CapabilityReady.
	past := time.Now().UTC().Add(-time.Minute)
	if err := sched.EnterWait(h.ctx, WaitEntryRequest{
		Execution: h.exec, CapabilityID: "cap",
		Spec: executionmodel.WaitSpec{
			Type: executionmodel.WaitTypeSignal, ExpectedSignal: "approved",
			WaitUntil: &past, DeadlinePolicy: executionmodel.WaitDeadlinePolicyContinue,
		},
	}); err != nil {
		t.Fatalf("EnterWait: %v", err)
	}
	order.reset()

	type outcome struct {
		name  string
		count int
		err   error
	}
	results := make(chan outcome, 2)
	start := make(chan struct{})

	go func() {
		<-start
		_, err := sched.ResolveSignal(h.ctx, signalRequest(h.exec, "del_race", map[string]any{"status": "approved"}))
		results <- outcome{name: "signal", err: err}
	}()
	go func() {
		<-start
		count, err := sched.PollDueTimers(h.ctx, h.exec.InstanceID)
		results <- outcome{name: "poller", count: count, err: err}
	}()
	close(start)

	signalWon, pollerWon := false, false
	for i := 0; i < 2; i++ {
		select {
		case result := <-results:
			if result.name == "signal" && result.err == nil {
				signalWon = true
			}
			if result.name == "poller" && result.count > 0 {
				pollerWon = true
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent wait drivers did not finish")
		}
	}
	if signalWon && pollerWon {
		t.Fatal("both drivers committed a transition from the shared execution")
	}

	got := order.snapshot()
	if commits := countLogEntries(got, "commit"); commits != 1 {
		t.Fatalf("committed transitions = %d, want exactly 1 (%v)", commits, got)
	}
	if ready := countLogEntries(got, "publish:capability.ready"); ready != 1 {
		t.Fatalf("CapabilityReady publishes = %d, want exactly 1 (%v)", ready, got)
	}
	for _, entry := range got {
		if entry == "publish:execution.failed" || entry == "publish:execution.cancelled" {
			t.Fatalf("unexpected terminal publish %q from a resume-resume race: %v", entry, got)
		}
	}
}

// TestConcurrentRecoveryVsSignalSerializesOnOneExecution proves the recovery
// driver shares the execution-boundary guard with a live signal delivery. The
// committer gate holds RecoverExecution inside its guard; a concurrent signal
// must not publish until recovery releases it, and only one CapabilityReady is
// announced. A signal wait is never resumed by recovery alone, so recovery
// commits a still-waiting snapshot and the signal is the sole publisher.
func TestConcurrentRecoveryVsSignalSerializesOnOneExecution(t *testing.T) {
	h := newWaitHarness(t)
	committer := &fakeCommitter{entered: make(chan struct{}, 1), release: make(chan struct{})}
	sched, err := New(h.bus, h.store, h.scopes,
		WithWaitStore(h.waitStore, TimerPollConfig{}), WithTransitionCommitter(committer))
	if err != nil {
		t.Fatalf("New scheduler: %v", err)
	}
	if err := sched.EnterWait(h.ctx, WaitEntryRequest{
		Execution: h.exec, CapabilityID: "cap",
		Spec: executionmodel.WaitSpec{Type: executionmodel.WaitTypeSignal, ExpectedSignal: "approved"},
	}); err != nil {
		t.Fatalf("EnterWait: %v", err)
	}

	ready, err := h.bus.Subscribe(event.CapabilityReady, 8)
	if err != nil {
		t.Fatalf("subscribe CapabilityReady: %v", err)
	}
	defer func() { _ = ready.Close() }()

	type outcome struct {
		name string
		err  error
	}
	results := make(chan outcome, 2)

	go func() {
		results <- outcome{name: "recover", err: sched.RecoverExecution(h.ctx, h.exec)}
	}()
	select {
	case <-committer.entered:
		// Recovery now holds the guard at its commit.
	case <-time.After(5 * time.Second):
		t.Fatal("recovery never reached its guarded commit")
	}

	signalStart := make(chan struct{})
	go func() {
		<-signalStart
		_, err := sched.ResolveSignal(h.ctx, signalRequest(h.exec, "del_recovery", map[string]any{"status": "approved"}))
		results <- outcome{name: "signal", err: err}
	}()
	close(signalStart)

	select {
	case evt := <-ready.Events():
		t.Fatalf("CapabilityReady %v published before recovery released the guard", evt.Type)
	case <-time.After(50 * time.Millisecond):
	}

	close(committer.release)
	for i := 0; i < 2; i++ {
		select {
		case <-results:
		case <-time.After(5 * time.Second):
			t.Fatal("recovery and signal did not finish")
		}
	}

	events := drainRecoveryEvents(t, ready)
	if len(events) != 1 || events[0].Type != event.CapabilityReady {
		t.Fatalf("published %d events, want exactly one CapabilityReady: %v", len(events), events)
	}
	if state := h.exec.CapabilityState("cap"); state.Status != executionmodel.CapabilityReady {
		t.Fatalf("capability status = %s, want ready", state.Status)
	}
}

// TestConcurrentRunLoopHandlerVsSignalSerializesOnOneExecution proves a Run-loop
// completion handler and a wait driver share the same execution-boundary guard.
// The gated completion holds the guard; the concurrent signal cannot publish
// until it is released, and only the signal announces CapabilityReady.
func TestConcurrentRunLoopHandlerVsSignalSerializesOnOneExecution(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bus := event.NewBus()
	store := executionmodel.NewExecutionStore(storage.NewMemoryStore())
	scopes := executionmodel.NewScopeRegistry(ctx)
	committer := &fakeCommitter{entered: make(chan struct{}, 1), release: make(chan struct{})}
	sched, err := New(bus, store, scopes,
		WithWaitStore(store, TimerPollConfig{}), WithTransitionCommitter(committer))
	if err != nil {
		t.Fatalf("New scheduler: %v", err)
	}

	bp := &types.ExecutionBlueprint{
		Metadata: core.Metadata{ID: "bp", Name: "runloop-race", Version: "1.0.0"},
		Nodes: map[core.ID]types.ExecutionNode{
			"runner": stubNode("runner", core.RuntimeExecutionModeWait),
			"waiter": stubNode("waiter", core.RuntimeExecutionModeWait),
		},
		EntryCapabilityIDs: []core.ID{"runner", "waiter"},
	}
	exec, err := executionmodel.NewExecution(bp, core.NewID("corr_"), "inst_1", "", "", "", "")
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	if err := store.Add(ctx, exec); err != nil {
		t.Fatalf("store.Add: %v", err)
	}
	if err := exec.Start(map[string]any{}, 2); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for _, id := range []core.ID{"runner", "waiter"} {
		if err := exec.MarkCapabilityReady(id, map[string]any{}); err != nil {
			t.Fatalf("MarkCapabilityReady(%s): %v", id, err)
		}
		if err := exec.MarkCapabilityRunning(id); err != nil {
			t.Fatalf("MarkCapabilityRunning(%s): %v", id, err)
		}
	}
	// Park only the waiter; the runner stays runnable for the completion handler.
	if err := sched.EnterWait(ctx, WaitEntryRequest{
		Execution: exec, CapabilityID: "waiter",
		Spec: executionmodel.WaitSpec{Type: executionmodel.WaitTypeSignal, ExpectedSignal: "approved"},
	}); err != nil {
		t.Fatalf("EnterWait: %v", err)
	}

	ready, err := bus.Subscribe(event.CapabilityReady, 8)
	if err != nil {
		t.Fatalf("subscribe CapabilityReady: %v", err)
	}
	defer func() { _ = ready.Close() }()

	type outcome struct {
		name string
		err  error
	}
	results := make(chan outcome, 2)

	go func() {
		err := sched.onCapabilityCompleted(ctx, event.New(
			event.CapabilityCompleted, exec.ID, exec.CorrelationID, "runner",
			event.CapabilityCompletedPayload{Result: map[string]any{}},
		))
		results <- outcome{name: "runloop", err: err}
	}()
	select {
	case <-committer.entered:
		// The Run-loop handler now holds the guard at its commit.
	case <-time.After(5 * time.Second):
		t.Fatal("Run-loop handler never reached its guarded commit")
	}

	signalStart := make(chan struct{})
	go func() {
		<-signalStart
		_, err := sched.ResolveSignal(ctx, SignalResolutionRequest{
			Execution: exec, CapabilityID: "waiter", DeliveryID: "del_runloop",
			SignalName: "approved", Payload: map[string]any{"status": "approved"},
		})
		results <- outcome{name: "signal", err: err}
	}()
	close(signalStart)

	select {
	case evt := <-ready.Events():
		t.Fatalf("CapabilityReady %v published while the Run-loop handler held the guard", evt.Type)
	case <-time.After(50 * time.Millisecond):
	}

	close(committer.release)
	for i := 0; i < 2; i++ {
		select {
		case <-results:
		case <-time.After(5 * time.Second):
			t.Fatal("Run-loop handler and signal did not finish")
		}
	}

	events := drainRecoveryEvents(t, ready)
	if len(events) != 1 || events[0].Type != event.CapabilityReady {
		t.Fatalf("published %d events, want exactly one CapabilityReady: %v", len(events), events)
	}
	if events[0].Metadata.CapabilityID != "waiter" {
		t.Fatalf("CapabilityReady for %s, want waiter", events[0].Metadata.CapabilityID)
	}
	if state := exec.CapabilityState("waiter"); state.Status != executionmodel.CapabilityReady {
		t.Fatalf("waiter status = %s, want ready", state.Status)
	}
}

// gatedFailCommitStore holds the deadline-failure commit — the one whose
// execution is already terminal after ExpireWait + MarkFailed — inside its
// transition guard until released. Starting a competing driver only after
// entered fires makes that driver the deterministic loser of the shared-value
// race, mirroring the commit gate used by the recovery and Run-loop tests.
type gatedFailCommitStore struct {
	*executionmodel.ExecutionStore
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *gatedFailCommitStore) SaveWaitTransition(ctx context.Context, req executionmodel.WaitTransitionRequest) (*executionmodel.WaitTransitionResult, error) {
	if req.Execution != nil && req.Execution.IsTerminal() {
		s.once.Do(func() { close(s.entered) })
		<-s.release
	}
	return s.ExecutionStore.SaveWaitTransition(ctx, req)
}

// TestConcurrentSignalVsDeadlineFailPublishesOnlyFailure is the ADR 0004
// same-capability variant. One shared *Execution is driven at a common barrier
// by a signal delivery and by expiry of that same capability's signal deadline
// under the fail policy. The poller is gated to win, so the signal must then
// find a failed capability, commit nothing, and publish no CapabilityReady.
func TestConcurrentSignalVsDeadlineFailPublishesOnlyFailure(t *testing.T) {
	h := newWaitHarness(t)
	gate := &gatedFailCommitStore{
		ExecutionStore: h.store,
		entered:        make(chan struct{}),
		release:        make(chan struct{}),
	}
	h.waitStore = gate
	sched := h.scheduler(t)

	past := time.Now().UTC().Add(-time.Minute)
	if err := sched.EnterWait(h.ctx, WaitEntryRequest{
		Execution: h.exec, CapabilityID: "cap",
		Spec: executionmodel.WaitSpec{
			Type: executionmodel.WaitTypeSignal, ExpectedSignal: "approved",
			WaitUntil: &past, DeadlinePolicy: executionmodel.WaitDeadlinePolicyFail,
		},
	}); err != nil {
		t.Fatalf("EnterWait: %v", err)
	}

	sub, err := h.bus.SubscribeExecution(h.exec.ID, 8)
	if err != nil {
		t.Fatalf("SubscribeExecution: %v", err)
	}
	defer func() { _ = sub.Close() }()

	type outcome struct {
		name  string
		count int
		err   error
	}
	results := make(chan outcome, 2)

	go func() {
		count, err := sched.PollDueTimers(h.ctx, h.exec.InstanceID)
		results <- outcome{name: "poller", count: count, err: err}
	}()
	select {
	case <-gate.entered:
		// The poller now holds the guard at its gated failure commit.
	case <-time.After(5 * time.Second):
		t.Fatal("poller never reached its gated fail commit")
	}

	go func() {
		_, err := sched.ResolveSignal(h.ctx, signalRequest(h.exec, "del_fail", map[string]any{"status": "approved"}))
		results <- outcome{name: "signal", err: err}
	}()

	select {
	case evt := <-sub.Events():
		t.Fatalf("event %s published while the failure commit was gated", evt.Type)
	case <-time.After(50 * time.Millisecond):
	}

	close(gate.release)

	var poll, signal outcome
	for i := 0; i < 2; i++ {
		select {
		case result := <-results:
			switch result.name {
			case "poller":
				poll = result
			case "signal":
				signal = result
			}
		case <-time.After(5 * time.Second):
			t.Fatal("signal and poller did not finish")
		}
	}
	if poll.err != nil || poll.count != 1 {
		t.Fatalf("poller = (%d, %v), want (1, nil)", poll.count, poll.err)
	}
	if signal.err == nil {
		t.Fatal("signal resumed a capability after the deadline failed the execution")
	}

	events := drainRecoveryEvents(t, sub)
	failed, ready := 0, 0
	for _, evt := range events {
		switch evt.Type {
		case event.ExecutionFailed:
			failed++
		case event.CapabilityReady:
			ready++
		}
	}
	if failed != 1 || ready != 0 {
		t.Fatalf("published ExecutionFailed=%d CapabilityReady=%d, want 1/0: %v", failed, ready, events)
	}
	if h.exec.Status() != executionmodel.StatusFailed {
		t.Fatalf("execution status = %s, want failed", h.exec.Status())
	}
	if state := h.exec.CapabilityState("cap"); state.Status != executionmodel.CapabilityFailed {
		t.Fatalf("capability status = %s, want failed", state.Status)
	}
	intent, ok, err := h.store.ClaimDispatchIntent(h.ctx, executionmodel.NewDispatchIntent(h.exec.ID, "cap", h.exec.AttemptID), "probe", time.Minute)
	if err != nil || ok || intent != nil {
		t.Fatalf("failed capability has a dispatch intent: intent=%v ok=%v err=%v", intent, ok, err)
	}
}

// TestConcurrentSignalVsDeadlineFailDistinctCapabilities is the ADR 0004
// different-capability variant. One shared *Execution is driven at a common
// barrier by a signal delivery for "signal" and by expiry of "deadline"'s
// signal deadline under the fail policy. The poller is gated to win, so the
// failure is the single terminal publish; the only ready event and dispatch
// intent belong to the resumed capability, never to the failed one.
func TestConcurrentSignalVsDeadlineFailDistinctCapabilities(t *testing.T) {
	h := newTwoWaitHarness(t)
	gate := &gatedFailCommitStore{
		ExecutionStore: h.store,
		entered:        make(chan struct{}),
		release:        make(chan struct{}),
	}
	h.waitStore = gate
	sched := h.scheduler(t)

	if err := sched.EnterWait(h.ctx, WaitEntryRequest{
		Execution: h.exec, CapabilityID: "signal",
		Spec: executionmodel.WaitSpec{Type: executionmodel.WaitTypeSignal, ExpectedSignal: "approved"},
	}); err != nil {
		t.Fatalf("EnterWait(signal): %v", err)
	}
	past := time.Now().UTC().Add(-time.Minute)
	if err := sched.EnterWait(h.ctx, WaitEntryRequest{
		Execution: h.exec, CapabilityID: "deadline",
		Spec: executionmodel.WaitSpec{
			Type: executionmodel.WaitTypeSignal, ExpectedSignal: "approved",
			WaitUntil: &past, DeadlinePolicy: executionmodel.WaitDeadlinePolicyFail,
		},
	}); err != nil {
		t.Fatalf("EnterWait(deadline): %v", err)
	}

	sub, err := h.bus.SubscribeExecution(h.exec.ID, 8)
	if err != nil {
		t.Fatalf("SubscribeExecution: %v", err)
	}
	defer func() { _ = sub.Close() }()

	type outcome struct {
		name  string
		count int
		err   error
	}
	results := make(chan outcome, 2)

	go func() {
		count, err := sched.PollDueTimers(h.ctx, h.exec.InstanceID)
		results <- outcome{name: "poller", count: count, err: err}
	}()
	select {
	case <-gate.entered:
		// The poller now holds the guard at its gated failure commit.
	case <-time.After(5 * time.Second):
		t.Fatal("poller never reached its gated fail commit")
	}

	go func() {
		_, err := sched.ResolveSignal(h.ctx, SignalResolutionRequest{
			Execution: h.exec, CapabilityID: "signal",
			DeliveryID: "del_distinct", SignalName: "approved",
			Payload: map[string]any{"status": "approved"},
		})
		results <- outcome{name: "signal", err: err}
	}()

	select {
	case evt := <-sub.Events():
		t.Fatalf("event %s published while the failure commit was gated", evt.Type)
	case <-time.After(50 * time.Millisecond):
	}

	close(gate.release)

	var poll, signal outcome
	for i := 0; i < 2; i++ {
		select {
		case result := <-results:
			switch result.name {
			case "poller":
				poll = result
			case "signal":
				signal = result
			}
		case <-time.After(5 * time.Second):
			t.Fatal("signal and poller did not finish")
		}
	}
	if poll.err != nil || poll.count != 1 {
		t.Fatalf("poller = (%d, %v), want (1, nil)", poll.count, poll.err)
	}
	if signal.err != nil {
		t.Fatalf("signal resume failed: %v", signal.err)
	}

	events := drainRecoveryEvents(t, sub)
	failed, ready := 0, 0
	readyCap := ""
	for _, evt := range events {
		switch evt.Type {
		case event.ExecutionFailed:
			failed++
		case event.CapabilityReady:
			ready++
			readyCap = string(evt.Metadata.CapabilityID)
		}
	}
	if failed != 1 || ready != 1 {
		t.Fatalf("published ExecutionFailed=%d CapabilityReady=%d, want 1/1: %v", failed, ready, events)
	}
	if readyCap != "signal" {
		t.Fatalf("CapabilityReady for %q, want signal", readyCap)
	}
	if h.exec.Status() != executionmodel.StatusFailed {
		t.Fatalf("execution status = %s, want failed", h.exec.Status())
	}

	resumed, ok, err := h.store.ClaimDispatchIntent(h.ctx, executionmodel.NewDispatchIntent(h.exec.ID, "signal", h.exec.AttemptID), "probe", time.Minute)
	if err != nil || !ok || resumed == nil {
		t.Fatalf("resumed capability intent missing: intent=%v ok=%v err=%v", resumed, ok, err)
	}
	failedIntent, ok, err := h.store.ClaimDispatchIntent(h.ctx, executionmodel.NewDispatchIntent(h.exec.ID, "deadline", h.exec.AttemptID), "probe", time.Minute)
	if err != nil || ok || failedIntent != nil {
		t.Fatalf("failed capability has a dispatch intent: intent=%v ok=%v err=%v", failedIntent, ok, err)
	}
}
