package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/event"
	executionmodel "github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/nore/internal/types"
	core "github.com/neuron-runtime/neuron/shared/types/core"
)

func deliveryRequest(exec *executionmodel.Execution, deliveryID, signalName, correlationKey string, payload map[string]any) SignalDeliveryRequest {
	return SignalDeliveryRequest{
		Execution:      exec,
		DeliveryID:     deliveryID,
		SignalName:     signalName,
		CorrelationKey: correlationKey,
		Payload:        payload,
	}
}

func enterSignalWait(t *testing.T, sched *Scheduler, h *waitHarness, capabilityID core.ID, spec executionmodel.WaitSpec) {
	t.Helper()
	if err := sched.EnterWait(h.ctx, WaitEntryRequest{Execution: h.exec, CapabilityID: capabilityID, Spec: spec}); err != nil {
		t.Fatalf("EnterWait(%s): %v", capabilityID, err)
	}
}

func signalWaitSpec(name, schema string) executionmodel.WaitSpec {
	return executionmodel.WaitSpec{
		Type:           executionmodel.WaitTypeSignal,
		ExpectedSignal: name,
		PayloadSchema:  schema,
	}
}

// newMultiWaitHarness builds a durable execution with several capabilities that
// can each enter a wait, so lookup ambiguity and correlation-key narrowing can
// be exercised through persisted wait state.
func newMultiWaitHarness(t *testing.T, ids ...core.ID) *waitHarness {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	bus := event.NewBus()
	raw := storage.NewMemoryStore()
	store := executionmodel.NewExecutionStore(raw)
	scopes := executionmodel.NewScopeRegistry(ctx)
	bp := &types.ExecutionBlueprint{
		Metadata:           core.Metadata{ID: "bp", Name: "multi", Version: "1.0.0"},
		Nodes:              make(map[core.ID]types.ExecutionNode, len(ids)),
		EntryCapabilityIDs: ids,
	}
	for _, id := range ids {
		bp.Nodes[id] = stubNode(id, core.RuntimeExecutionModeWait)
	}
	exec, err := executionmodel.NewExecution(bp, core.NewID("corr_"), "inst_1", "", "", "", "")
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	if err := store.Add(ctx, exec); err != nil {
		t.Fatalf("store.Add: %v", err)
	}
	if err := exec.Start(map[string]any{}, 1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for _, id := range ids {
		if err := exec.MarkCapabilityReady(id, map[string]any{}); err != nil {
			t.Fatalf("MarkCapabilityReady(%s): %v", id, err)
		}
		if err := exec.MarkCapabilityRunning(id); err != nil {
			t.Fatalf("MarkCapabilityRunning(%s): %v", id, err)
		}
	}
	return &waitHarness{ctx: ctx, bus: bus, raw: raw, store: store, scopes: scopes, eventBus: bus, waitStore: store, exec: exec}
}

func TestDeliverSignalCommitsBeforePublishing(t *testing.T) {
	h := newWaitHarness(t)
	order := &orderLog{}
	h.waitStore = &recordingWaitStore{ExecutionStore: h.store, log: order}
	h.eventBus = &recordingBus{Bus: h.bus, log: order}
	sched := h.scheduler(t)
	enterSignalWait(t, sched, h, "cap", signalWaitSpec("approved", ""))
	order.reset()

	result, err := sched.DeliverSignal(h.ctx, deliveryRequest(h.exec, "del_1", "approved", "", map[string]any{"status": "approved"}))
	if err != nil {
		t.Fatalf("DeliverSignal: %v", err)
	}
	if result.Replay || result.Delivery == nil || result.Delivery.DeliveryID != "del_1" {
		t.Fatalf("result = %+v, want a committed delivery", result)
	}
	got := order.snapshot()
	if len(got) != 2 || got[0] != "commit" || got[1] != "publish:capability.ready" {
		t.Fatalf("commit/publish order = %v, want [commit publish:capability.ready]", got)
	}
	if state := h.exec.CapabilityState("cap"); state.Status != executionmodel.CapabilityReady {
		t.Fatalf("capability status = %s, want ready", state.Status)
	}
	if params := h.exec.Params("cap"); params["status"] != "approved" {
		t.Fatalf("resume params = %v, want the delivered payload", params)
	}
	if _, err := h.store.GetSignalDelivery(h.ctx, h.exec.ID, "del_1"); err != nil {
		t.Fatalf("durable delivery missing: %v", err)
	}
}

func TestDeliverSignalReplayAndConflict(t *testing.T) {
	h := newWaitHarness(t)
	sched := h.scheduler(t)
	enterSignalWait(t, sched, h, "cap", signalWaitSpec("approved", ""))
	payload := map[string]any{"status": "approved"}
	if _, err := sched.DeliverSignal(h.ctx, deliveryRequest(h.exec, "del_1", "approved", "", payload)); err != nil {
		t.Fatalf("first DeliverSignal: %v", err)
	}

	stale := time.Now().UTC().Add(time.Hour)
	h.exec.SetExecutionDeadline(&stale)
	replay, err := sched.DeliverSignal(h.ctx, deliveryRequest(h.exec, "del_1", "approved", "", map[string]any{"status": "approved"}))
	if err != nil {
		t.Fatalf("replayed DeliverSignal: %v", err)
	}
	if !replay.Replay {
		t.Fatal("duplicate delivery was not reported as a replay")
	}
	if h.exec.Snapshot().ExecutionDeadline != nil {
		t.Fatal("replay did not reload durable state before returning")
	}

	if _, err := sched.DeliverSignal(h.ctx, deliveryRequest(h.exec, "del_1", "approved", "", map[string]any{"status": "denied"})); !errors.Is(err, executionmodel.ErrSignalDeliveryConflict) {
		t.Fatalf("conflicting delivery error = %v, want %v", err, executionmodel.ErrSignalDeliveryConflict)
	}
}

func TestDeliverSignalNoMatchingWait(t *testing.T) {
	h := newWaitHarness(t)
	sched := h.scheduler(t)
	enterSignalWait(t, sched, h, "cap", signalWaitSpec("approved", ""))
	_, err := sched.DeliverSignal(h.ctx, deliveryRequest(h.exec, "del_1", "unrelated", "", map[string]any{}))
	if !errors.Is(err, ErrSignalNoMatchingWait) {
		t.Fatalf("error = %v, want %v", err, ErrSignalNoMatchingWait)
	}
}

func TestDeliverSignalAmbiguousWait(t *testing.T) {
	h := newMultiWaitHarness(t, "first", "second")
	sched := h.scheduler(t)
	enterSignalWait(t, sched, h, "first", signalWaitSpec("approved", ""))
	enterSignalWait(t, sched, h, "second", signalWaitSpec("approved", ""))

	_, err := sched.DeliverSignal(h.ctx, deliveryRequest(h.exec, "del_1", "approved", "", map[string]any{}))
	if !errors.Is(err, ErrSignalAmbiguousWait) {
		t.Fatalf("error = %v, want %v", err, ErrSignalAmbiguousWait)
	}

	// A correlation key narrows the same persisted state to one wait.
	firstState := h.exec.CapabilityState("first")
	result, err := sched.DeliverSignal(h.ctx, deliveryRequest(h.exec, "del_2", "approved", firstState.CorrelationKey, map[string]any{}))
	if err != nil {
		t.Fatalf("correlation-keyed DeliverSignal: %v", err)
	}
	if result.Replay {
		t.Fatal("correlated delivery was reported as a replay")
	}
	if state := h.exec.CapabilityState("first"); state.Status != executionmodel.CapabilityReady {
		t.Fatalf("first capability status = %s, want ready", state.Status)
	}
	if state := h.exec.CapabilityState("second"); state.Status != executionmodel.CapabilityWaiting {
		t.Fatalf("second capability status = %s, want still waiting", state.Status)
	}
}

func TestDeliverSignalDeadlineExpired(t *testing.T) {
	h := newWaitHarness(t)
	sched := h.scheduler(t)
	future := time.Now().UTC().Add(-time.Minute)
	spec := signalWaitSpec("approved", "")
	spec.WaitUntil = &future
	enterSignalWait(t, sched, h, "cap", spec)

	_, err := sched.DeliverSignal(h.ctx, deliveryRequest(h.exec, "del_1", "approved", "", map[string]any{}))
	if !errors.Is(err, ErrSignalWaitExpired) {
		t.Fatalf("error = %v, want %v", err, ErrSignalWaitExpired)
	}
}

func TestDeliverSignalTerminalExecution(t *testing.T) {
	h := newWaitHarness(t)
	sched := h.scheduler(t)
	enterSignalWait(t, sched, h, "cap", signalWaitSpec("approved", ""))
	if !h.exec.MarkFailed(errors.New("settled before delivery")) {
		t.Fatal("test setup: could not mark execution terminal")
	}
	_, err := sched.DeliverSignal(h.ctx, deliveryRequest(h.exec, "del_1", "approved", "", map[string]any{}))
	if !errors.Is(err, ErrSignalWaitTerminal) {
		t.Fatalf("error = %v, want %v", err, ErrSignalWaitTerminal)
	}
}

func TestDeliverSignalInvalidPayload(t *testing.T) {
	h := newWaitHarness(t)
	sched := h.scheduler(t)
	enterSignalWait(t, sched, h, "cap", signalWaitSpec("approved", `{"required":["status"]}`))
	_, err := sched.DeliverSignal(h.ctx, deliveryRequest(h.exec, "del_1", "approved", "", map[string]any{"other": 1}))
	if !errors.Is(err, ErrSignalInvalidPayload) {
		t.Fatalf("error = %v, want %v", err, ErrSignalInvalidPayload)
	}
	if state := h.exec.CapabilityState("cap"); state.Status != executionmodel.CapabilityWaiting {
		t.Fatalf("invalid delivery changed capability status to %s", state.Status)
	}
	if _, err := h.store.GetSignalDelivery(h.ctx, h.exec.ID, "del_1"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("invalid delivery was persisted: %v", err)
	}
}

func TestDeliverSignalWithoutWaitStore(t *testing.T) {
	h := newWaitHarness(t)
	sched, err := New(h.bus, h.store, h.scopes)
	if err != nil {
		t.Fatalf("New scheduler: %v", err)
	}
	_, err = sched.DeliverSignal(h.ctx, deliveryRequest(h.exec, "del_1", "approved", "", map[string]any{}))
	if !errors.Is(err, ErrWaitStoreUnavailable) {
		t.Fatalf("error = %v, want %v", err, ErrWaitStoreUnavailable)
	}
}
