package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
	"github.com/neuron-runtime/neuron/nore/internal/event"
	executionmodel "github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/storage"
	core "github.com/neuron-runtime/neuron/shared/types/core"
)

type waitHarness struct {
	ctx       context.Context
	bus       *event.Bus
	raw       *storage.MemoryStore
	store     *executionmodel.ExecutionStore
	scopes    *executionmodel.ScopeRegistry
	eventBus  contracts.EventBus
	waitStore WaitStore
	exec      *executionmodel.Execution
}

// newWaitHarness builds a durable execution parked in a runnable capability
// state, ready to enter a wait. The scheduler's Run loop is deliberately absent:
// these tests drive the wait API directly so commit and publish order is exact.
func newWaitHarness(t *testing.T) *waitHarness {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	bus := event.NewBus()
	raw := storage.NewMemoryStore()
	store := executionmodel.NewExecutionStore(raw)
	scopes := executionmodel.NewScopeRegistry(ctx)
	exec, err := executionmodel.NewExecution(singleCapabilityBlueprint("cap"), core.NewID("corr_"), "inst_1", "", "", "", "")
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	if err := store.Add(ctx, exec); err != nil {
		t.Fatalf("store.Add: %v", err)
	}
	if err := exec.Start(map[string]any{}, 1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := exec.MarkCapabilityReady("cap", map[string]any{"seed": "old"}); err != nil {
		t.Fatalf("MarkCapabilityReady: %v", err)
	}
	if err := exec.MarkCapabilityRunning("cap"); err != nil {
		t.Fatalf("MarkCapabilityRunning: %v", err)
	}
	return &waitHarness{
		ctx:       ctx,
		bus:       bus,
		raw:       raw,
		store:     store,
		scopes:    scopes,
		eventBus:  bus,
		waitStore: store,
		exec:      exec,
	}
}

func (h *waitHarness) scheduler(t *testing.T, extra ...Option) *Scheduler {
	t.Helper()
	opts := append([]Option{WithWaitStore(h.waitStore, TimerPollConfig{})}, extra...)
	sched, err := New(h.eventBus, h.store, h.scopes, opts...)
	if err != nil {
		t.Fatalf("New scheduler: %v", err)
	}
	return sched
}

func signalRequest(exec *executionmodel.Execution, deliveryID string, payload map[string]any) SignalResolutionRequest {
	return SignalResolutionRequest{
		Execution:    exec,
		CapabilityID: "cap",
		DeliveryID:   deliveryID,
		SignalName:   "approved",
		Payload:      payload,
	}
}

type orderLog struct {
	mu    sync.Mutex
	items []string
}

func (l *orderLog) add(item string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.items = append(l.items, item)
}

func (l *orderLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.items...)
}

func (l *orderLog) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.items = nil
}

type recordingWaitStore struct {
	*executionmodel.ExecutionStore
	log *orderLog
}

func (s *recordingWaitStore) SaveWaitTransition(ctx context.Context, req executionmodel.WaitTransitionRequest) (*executionmodel.WaitTransitionResult, error) {
	s.log.add("commit")
	return s.ExecutionStore.SaveWaitTransition(ctx, req)
}

type recordingBus struct {
	*event.Bus
	log *orderLog
}

func (b *recordingBus) Publish(ctx context.Context, evt event.Event) error {
	b.log.add("publish:" + evt.Type.String())
	return b.Bus.Publish(ctx, evt)
}

type failingBus struct {
	*event.Bus
	fail map[event.Type]error
}

func (b *failingBus) Publish(ctx context.Context, evt event.Event) error {
	if err, ok := b.fail[evt.Type]; ok {
		return err
	}
	return b.Bus.Publish(ctx, evt)
}

func TestTimerPollConfigDefaultsAndValidation(t *testing.T) {
	defaults, err := TimerPollConfig{}.validated()
	if err != nil {
		t.Fatalf("validate zero config: %v", err)
	}
	if defaults != DefaultTimerPollConfig() {
		t.Fatalf("zero config defaults = %+v, want %+v", defaults, DefaultTimerPollConfig())
	}
	if defaults.Interval != time.Second || defaults.BatchSize != 256 || defaults.Lease != 30*time.Second {
		t.Fatalf("Phase 5 defaults = %+v, want 1s/256/30s", defaults)
	}

	bad := map[string]TimerPollConfig{
		"negative interval":    {Interval: -time.Second},
		"sub-minimum interval": {Interval: core.MinTimerPollInterval - time.Nanosecond},
		"huge interval":        {Interval: 2 * time.Minute},
		"negative batch":       {BatchSize: -1},
		"huge batch":           {BatchSize: maxTimerPollBatchSize + 1},
		"short lease":          {Lease: time.Second},
		"long lease":           {Lease: 10 * time.Minute},
	}
	for name, cfg := range bad {
		t.Run(name, func(t *testing.T) {
			if _, err := cfg.validated(); err == nil {
				t.Fatalf("config %+v was accepted, want rejection", cfg)
			}
		})
	}
}

// TestTimerPollConfigEnforcesCoreMinimumInterval proves the scheduler does not
// keep a second authority for the poll-interval bounds: the core contract is
// the floor and ceiling, inclusive values are valid, and the 1s default is safe.
func TestTimerPollConfigEnforcesCoreMinimumInterval(t *testing.T) {
	if core.MinTimerPollInterval != 100*time.Millisecond {
		t.Fatalf("core minimum = %s, want 100ms", core.MinTimerPollInterval)
	}
	validated, err := (TimerPollConfig{Interval: core.MinTimerPollInterval}).validated()
	if err != nil || validated.Interval != core.MinTimerPollInterval {
		t.Fatalf("config at the minimum = (%+v, %v), want it accepted", validated, err)
	}
	if _, err := (TimerPollConfig{Interval: core.MinTimerPollInterval - time.Nanosecond}).validated(); err == nil {
		t.Fatal("config one nanosecond below the core minimum was accepted")
	}
	if _, err := (TimerPollConfig{Interval: core.MaxTimerPollInterval + time.Nanosecond}).validated(); err == nil {
		t.Fatal("config one nanosecond above the core maximum was accepted")
	}
	if defaults, err := (TimerPollConfig{}).validated(); err != nil || defaults.Interval != core.DefaultTimerPollInterval {
		t.Fatalf("default config = (%+v, %v), want core default %s", defaults, err, core.DefaultTimerPollInterval)
	}
}

func TestNonDurableSchedulerRefusesWaits(t *testing.T) {
	h := newWaitHarness(t)
	sched, err := New(h.bus, h.store, h.scopes)
	if err != nil {
		t.Fatalf("New scheduler: %v", err)
	}
	deadline := time.Now().UTC().Add(time.Minute)
	if err := sched.EnterWait(h.ctx, WaitEntryRequest{
		Execution: h.exec, CapabilityID: "cap", Spec: executionmodel.WaitSpec{Type: executionmodel.WaitTypeTimer, WaitUntil: &deadline},
	}); !errors.Is(err, ErrWaitStoreUnavailable) {
		t.Fatalf("EnterWait error = %v, want %v", err, ErrWaitStoreUnavailable)
	}
	if _, err := sched.ResolveSignal(h.ctx, signalRequest(h.exec, "del_1", map[string]any{"ok": true})); !errors.Is(err, ErrWaitStoreUnavailable) {
		t.Fatalf("ResolveSignal error = %v, want %v", err, ErrWaitStoreUnavailable)
	}
	count, err := sched.PollDueTimers(h.ctx, h.exec.InstanceID)
	if err != nil || count != 0 {
		t.Fatalf("PollDueTimers = (%d, %v), want (0, nil)", count, err)
	}
}

func TestEnterWaitWritesTimerOnlyWithDeadline(t *testing.T) {
	deadline := time.Now().UTC().Add(-time.Minute)
	cases := []struct {
		name      string
		spec      executionmodel.WaitSpec
		wantTimer bool
	}{
		{
			name: "signal without deadline",
			spec: executionmodel.WaitSpec{Type: executionmodel.WaitTypeSignal, ExpectedSignal: "approved"},
		},
		{
			name:      "timer wait",
			spec:      executionmodel.WaitSpec{Type: executionmodel.WaitTypeTimer, WaitUntil: &deadline},
			wantTimer: true,
		},
		{
			name: "signal wait with deadline",
			spec: executionmodel.WaitSpec{
				Type: executionmodel.WaitTypeSignal, ExpectedSignal: "approved",
				WaitUntil: &deadline, DeadlinePolicy: executionmodel.WaitDeadlinePolicyContinue,
			},
			wantTimer: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newWaitHarness(t)
			sched := h.scheduler(t)
			if err := sched.EnterWait(h.ctx, WaitEntryRequest{Execution: h.exec, CapabilityID: "cap", Spec: tc.spec}); err != nil {
				t.Fatalf("EnterWait: %v", err)
			}
			if state := h.exec.CapabilityState("cap"); state.Status != executionmodel.CapabilityWaiting {
				t.Fatalf("capability status = %s, want waiting", state.Status)
			}
			timers, err := h.store.DueWaitTimersForInstance(h.ctx, h.exec.InstanceID, time.Now().UTC().Add(time.Hour), 16)
			if err != nil {
				t.Fatalf("DueWaitTimers: %v", err)
			}
			if got := len(timers) == 1; got != tc.wantTimer {
				t.Fatalf("timer count = %d, want timer=%v", len(timers), tc.wantTimer)
			}
			if tc.wantTimer {
				payload, err := executionmodel.DecodeWaitTimer(timers[0].Data)
				if err != nil {
					t.Fatalf("DecodeWaitTimer: %v", err)
				}
				if payload.ExecutionID != h.exec.ID || payload.CapabilityID != "cap" || payload.WaitType != tc.spec.Type {
					t.Fatalf("timer payload = %+v, want %s/%s/%s", payload, h.exec.ID, "cap", tc.spec.Type)
				}
				if !timers[0].DueAt.Equal(deadline) {
					t.Fatalf("timer due = %s, want %s", timers[0].DueAt, deadline)
				}
			}
		})
	}
}

func TestResolveSignalCommitsBeforePublishing(t *testing.T) {
	h := newWaitHarness(t)
	order := &orderLog{}
	h.waitStore = &recordingWaitStore{ExecutionStore: h.store, log: order}
	h.eventBus = &recordingBus{Bus: h.bus, log: order}
	sched := h.scheduler(t)
	if err := sched.EnterWait(h.ctx, WaitEntryRequest{
		Execution: h.exec, CapabilityID: "cap",
		Spec: executionmodel.WaitSpec{Type: executionmodel.WaitTypeSignal, ExpectedSignal: "approved"},
	}); err != nil {
		t.Fatalf("EnterWait: %v", err)
	}
	order.reset()

	ready, err := h.bus.SubscribeExecution(h.exec.ID, 8)
	if err != nil {
		t.Fatalf("SubscribeExecution: %v", err)
	}
	defer func() { _ = ready.Close() }()

	req := signalRequest(h.exec, "del_1", map[string]any{"status": "approved"})
	result, err := sched.ResolveSignal(h.ctx, req)
	if err != nil {
		t.Fatalf("ResolveSignal: %v", err)
	}
	if result.Replay || result.Delivery == nil || result.Delivery.DeliveryID != "del_1" {
		t.Fatalf("resolution = %+v, want a committed delivery", result)
	}

	got := order.snapshot()
	if len(got) != 2 || got[0] != "commit" || got[1] != "publish:capability.ready" {
		t.Fatalf("commit/publish order = %v, want [commit publish:capability.ready]", got)
	}
	if params := h.exec.Params("cap"); params["status"] != "approved" {
		t.Fatalf("resume params = %v, want the delivered payload", params)
	}
	stored, err := h.store.Reload(h.ctx, h.exec.ID)
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if state := stored.CapabilityState("cap"); state.Status != executionmodel.CapabilityReady {
		t.Fatalf("durable capability status = %s, want ready", state.Status)
	}
	if _, err := h.store.GetSignalDelivery(h.ctx, h.exec.ID, "del_1"); err != nil {
		t.Fatalf("durable delivery missing: %v", err)
	}
	intent, ok, err := h.store.ClaimDispatchIntent(h.ctx, executionmodel.NewDispatchIntent(h.exec.ID, "cap", h.exec.AttemptID), "probe", time.Minute)
	if err != nil || !ok || intent == nil {
		t.Fatalf("committed dispatch intent not claimable: intent=%v ok=%v err=%v", intent, ok, err)
	}
}

func TestResolveSignalPersistsResumeDescriptor(t *testing.T) {
	h := newWaitHarness(t)
	sched := h.scheduler(t)
	if err := sched.EnterWait(h.ctx, WaitEntryRequest{
		Execution: h.exec, CapabilityID: "cap",
		Spec: executionmodel.WaitSpec{Type: executionmodel.WaitTypeSignal, ExpectedSignal: "approved"},
	}); err != nil {
		t.Fatalf("EnterWait: %v", err)
	}

	if _, err := sched.ResolveSignal(h.ctx, signalRequest(h.exec, "del_1", map[string]any{"status": "approved"})); err != nil {
		t.Fatalf("ResolveSignal: %v", err)
	}
	want := &executionmodel.CapabilityResume{
		Cause:      executionmodel.ResumeCauseSignal,
		SignalName: "approved",
		DeliveryID: "del_1",
	}
	live := h.exec.CapabilityState("cap").Resume
	if live == nil || *live != *want {
		t.Fatalf("live resume = %+v, want %+v", live, want)
	}
	stored, err := h.store.Reload(h.ctx, h.exec.ID)
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	durable := stored.CapabilityState("cap").Resume
	if durable == nil || *durable != *want {
		t.Fatalf("durable resume = %+v, want %+v", durable, want)
	}
}

// TestResolveSignalUsesPreRotationTimerKeyAndPostRotationIntent proves ADR 0010
// D3: a signal resolution deletes the deadline timer under the generation that
// wrote it, but the dispatch it commits is claimable under the fresh generation
// produced by the same transition.
func TestResolveSignalUsesPreRotationTimerKeyAndPostRotationIntent(t *testing.T) {
	h := newWaitHarness(t)
	sched := h.scheduler(t)
	prior := h.exec.AttemptID
	logical := h.exec.LogicalOperationID
	deadline := time.Now().UTC().Add(time.Hour)
	if err := sched.EnterWait(h.ctx, WaitEntryRequest{
		Execution: h.exec, CapabilityID: "cap",
		Spec: executionmodel.WaitSpec{
			Type: executionmodel.WaitTypeSignal, ExpectedSignal: "approved",
			WaitUntil: &deadline, DeadlinePolicy: executionmodel.WaitDeadlinePolicyContinue,
		},
	}); err != nil {
		t.Fatalf("EnterWait: %v", err)
	}
	oldKey, err := executionmodel.WaitTimerKey(h.exec.ID, "cap", prior)
	if err != nil {
		t.Fatalf("WaitTimerKey: %v", err)
	}
	if _, err := h.raw.GetTimer(h.ctx, oldKey); err != nil {
		t.Fatalf("test setup: signal deadline timer is missing: %v", err)
	}

	if _, err := sched.ResolveSignal(h.ctx, signalRequest(h.exec, "del_1", map[string]any{"ok": true})); err != nil {
		t.Fatalf("ResolveSignal: %v", err)
	}
	if h.exec.AttemptID == prior {
		t.Fatal("resolution did not rotate the attempt ID")
	}
	if h.exec.LogicalOperationID != logical {
		t.Fatalf("logical operation ID = %q, want %q", h.exec.LogicalOperationID, logical)
	}
	if _, err := h.raw.GetTimer(h.ctx, oldKey); err == nil {
		t.Fatal("pre-rotation deadline timer survived resolution")
	}
	intent, ok, err := h.store.ClaimDispatchIntent(h.ctx, executionmodel.NewDispatchIntent(h.exec.ID, "cap", h.exec.AttemptID), "probe", time.Minute)
	if err != nil || !ok || intent == nil {
		t.Fatalf("post-rotation intent not claimable: intent=%v ok=%v err=%v", intent, ok, err)
	}
	stale, ok, err := h.store.ClaimDispatchIntent(h.ctx, executionmodel.NewDispatchIntent(h.exec.ID, "cap", prior), "probe", time.Minute)
	if err != nil || ok {
		t.Fatalf("stale-generation intent remained claimable: intent=%v ok=%v err=%v", stale, ok, err)
	}
}

func TestResolveSignalReplayReloadsDurableState(t *testing.T) {
	h := newWaitHarness(t)
	sched := h.scheduler(t)
	if err := sched.EnterWait(h.ctx, WaitEntryRequest{
		Execution: h.exec, CapabilityID: "cap",
		Spec: executionmodel.WaitSpec{Type: executionmodel.WaitTypeSignal, ExpectedSignal: "approved"},
	}); err != nil {
		t.Fatalf("EnterWait: %v", err)
	}
	req := signalRequest(h.exec, "del_1", map[string]any{"status": "approved"})
	if _, err := sched.ResolveSignal(h.ctx, req); err != nil {
		t.Fatalf("first ResolveSignal: %v", err)
	}

	// Simulate a caller that mutated its execution after the first delivery.
	// Replaying the same delivery must restore durable state, not keep it.
	stale := time.Now().UTC().Add(time.Hour)
	h.exec.SetExecutionDeadline(&stale)
	if h.exec.Snapshot().ExecutionDeadline == nil {
		t.Fatal("test setup: mutation did not take effect")
	}

	second, err := sched.ResolveSignal(h.ctx, req)
	if err != nil {
		t.Fatalf("replayed ResolveSignal: %v", err)
	}
	if !second.Replay {
		t.Fatal("duplicate delivery was not reported as a replay")
	}
	if h.exec.Snapshot().ExecutionDeadline != nil {
		t.Fatal("replay did not reload durable state before returning")
	}
}

func TestResolveSignalConflictingPayload(t *testing.T) {
	h := newWaitHarness(t)
	sched := h.scheduler(t)
	if err := sched.EnterWait(h.ctx, WaitEntryRequest{
		Execution: h.exec, CapabilityID: "cap",
		Spec: executionmodel.WaitSpec{Type: executionmodel.WaitTypeSignal, ExpectedSignal: "approved"},
	}); err != nil {
		t.Fatalf("EnterWait: %v", err)
	}
	if _, err := sched.ResolveSignal(h.ctx, signalRequest(h.exec, "del_1", map[string]any{"status": "approved"})); err != nil {
		t.Fatalf("first ResolveSignal: %v", err)
	}
	if _, err := sched.ResolveSignal(h.ctx, signalRequest(h.exec, "del_1", map[string]any{"status": "denied"})); !errors.Is(err, executionmodel.ErrSignalDeliveryConflict) {
		t.Fatalf("conflicting replay error = %v, want %v", err, executionmodel.ErrSignalDeliveryConflict)
	}
}

func TestResolveSignalPublishFailureLeavesDurableIntent(t *testing.T) {
	h := newWaitHarness(t)
	errPublish := errors.New("event bus unavailable")
	h.eventBus = &failingBus{Bus: h.bus, fail: map[event.Type]error{event.CapabilityReady: errPublish}}
	sched := h.scheduler(t)
	if err := sched.EnterWait(h.ctx, WaitEntryRequest{
		Execution: h.exec, CapabilityID: "cap",
		Spec: executionmodel.WaitSpec{Type: executionmodel.WaitTypeSignal, ExpectedSignal: "approved"},
	}); err != nil {
		t.Fatalf("EnterWait: %v", err)
	}
	_, err := sched.ResolveSignal(h.ctx, signalRequest(h.exec, "del_1", map[string]any{"status": "approved"}))
	var publishErr *DispatchPublishError
	if !errors.As(err, &publishErr) {
		t.Fatalf("ResolveSignal error = %v, want *DispatchPublishError", err)
	}
	if !errors.Is(err, errPublish) || publishErr.CapabilityID != "cap" {
		t.Fatalf("publish error = %+v, want wrapped bus failure for cap", publishErr)
	}
	if _, err := h.store.GetSignalDelivery(h.ctx, h.exec.ID, "del_1"); err != nil {
		t.Fatalf("delivery was rolled back with the publish failure: %v", err)
	}
	intent, ok, err := h.store.ClaimDispatchIntent(h.ctx, executionmodel.NewDispatchIntent(h.exec.ID, "cap", h.exec.AttemptID), "probe", time.Minute)
	if err != nil || !ok || intent == nil {
		t.Fatalf("durable intent was lost: intent=%v ok=%v err=%v", intent, ok, err)
	}
}
