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
	"github.com/neuron-runtime/neuron/nore/internal/execution/engine"
	"github.com/neuron-runtime/neuron/nore/internal/types"
	core "github.com/neuron-runtime/neuron/shared/types/core"
)

// blockingRuntime never returns on its own. It reports when it started and when it
// observed its context being cancelled, so a test can prove that cancelling an
// execution actually reaches the code doing the work rather than only changing a
// status string.
type blockingRuntime struct {
	started   chan struct{}
	cancelled chan struct{}

	once sync.Once
}

func newBlockingRuntime() *blockingRuntime {
	return &blockingRuntime{
		started:   make(chan struct{}, 8),
		cancelled: make(chan struct{}, 8),
	}
}

func (r *blockingRuntime) Execute(ctx context.Context, _ contracts.ExecutionContext) (map[string]any, error) {
	r.once.Do(func() { close(r.started) })
	<-ctx.Done()
	select {
	case r.cancelled <- struct{}{}:
	default:
	}
	return nil, ctx.Err()
}

// awaitStart waits until the runtime is actually inside Execute. Asserting on a
// status string instead would let this test pass without cancellation ever
// reaching the invocation.
func (r *blockingRuntime) awaitStart(t *testing.T) {
	t.Helper()
	select {
	case <-r.started:
	case <-time.After(5 * time.Second):
		t.Fatal("capability was never invoked")
	}
}

func (r *blockingRuntime) awaitCancellation(t *testing.T) {
	t.Helper()
	select {
	case <-r.cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("the running capability was never cancelled")
	}
}

// singleCapabilityBlueprint is one capability with no successors, so the only
// thing cancellation can affect is that one invocation.
func singleCapabilityBlueprint(capabilityID core.ID) *types.ExecutionBlueprint {
	return &types.ExecutionBlueprint{
		Metadata:           core.Metadata{ID: "bp", Name: "cancel", Version: "1.0.0"},
		Nodes:              map[core.ID]types.ExecutionNode{capabilityID: stubNode(capabilityID, core.RuntimeExecutionModeWait)},
		EntryCapabilityIDs: []core.ID{capabilityID},
	}
}

// startExecution registers an execution and announces it the way the instance
// does, which is the scheduler's only entry point into an execution's plan.
func startExecution(t *testing.T, ctx context.Context, bus *event.Bus, store *executionmodel.MemoryStore, blueprint *types.ExecutionBlueprint) *executionmodel.Execution {
	t.Helper()
	exec, err := executionmodel.NewExecution(blueprint, core.NewID("corr_"), "inst_1")
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	if err := store.Add(exec); err != nil {
		t.Fatalf("store.Add: %v", err)
	}
	if err := bus.Publish(ctx, event.New(event.ExecutionStarted, exec.ID, exec.CorrelationID, "", event.ExecutionStartedPayload{Params: map[string]any{}})); err != nil {
		t.Fatalf("publish ExecutionStarted: %v", err)
	}
	return exec
}

// subscribeToExecution returns a buffered subscription for one execution's events.
func subscribeToExecution(t *testing.T, bus *event.Bus, executionID core.ID) event.Subscription {
	t.Helper()
	sub, err := bus.SubscribeExecution(executionID, 16)
	if err != nil {
		t.Fatalf("SubscribeExecution: %v", err)
	}
	return sub
}

// newCancelHarness wires a scheduler and engine over one blocking runtime, which
// is the smallest arrangement in which a cancellation can be observed end to end.
func newCancelHarness(t *testing.T, ctx context.Context, blueprint *types.ExecutionBlueprint) (*Scheduler, *executionmodel.Execution, *blockingRuntime) {
	t.Helper()
	bus := event.NewBus()
	store := executionmodel.NewMemoryStore()
	scopes := executionmodel.NewScopeRegistry(ctx)
	runtime := newBlockingRuntime()

	scheduler, err := New(bus, store, scopes)
	if err != nil {
		t.Fatalf("New scheduler: %v", err)
	}
	engineInstance, err := engine.NewCapabilityRuntimeEngine(bus, stubRegistry{runtime: runtime}, store, scopes, 2, time.Second)
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}
	go func() { _ = scheduler.Run(ctx) }()
	go func() { _ = engineInstance.Run(ctx) }()

	return scheduler, startExecution(t, ctx, bus, store, blueprint), runtime
}

// TestCancelExecutionStopsTheRunningCapability is the behaviour the whole feature
// exists for: an operator stops a slow execution and the work actually stops.
func TestCancelExecutionStopsTheRunningCapability(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	scheduler, exec, runtime := newCancelHarness(t, ctx, singleCapabilityBlueprint("slow"))
	runtime.awaitStart(t)

	if !scheduler.CancelExecution(ctx, exec.ID, errors.New("stopped by operator")) {
		t.Fatal("CancelExecution() = false, want true for a running execution")
	}
	runtime.awaitCancellation(t)

	if got := exec.Status(); got != executionmodel.StatusCancelled {
		t.Errorf("Status() = %s, want %s", got, executionmodel.StatusCancelled)
	}
	if exec.Error() != "stopped by operator" {
		t.Errorf("Error() = %q, want the cancellation reason", exec.Error())
	}
	if got := exec.CapabilityState("slow").Status; got != executionmodel.CapabilityCancelled {
		t.Errorf("capability status = %s, want %s", got, executionmodel.CapabilityCancelled)
	}
}

// The cancellation must be announced, or a client watching the event stream waits
// forever for a terminal event that never arrives.
func TestCancelExecutionAnnouncesTheCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus := event.NewBus()
	store := executionmodel.NewMemoryStore()
	scopes := executionmodel.NewScopeRegistry(ctx)
	runtime := newBlockingRuntime()

	scheduler, err := New(bus, store, scopes)
	if err != nil {
		t.Fatalf("New scheduler: %v", err)
	}
	engineInstance, err := engine.NewCapabilityRuntimeEngine(bus, stubRegistry{runtime: runtime}, store, scopes, 2, time.Second)
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}
	go func() { _ = scheduler.Run(ctx) }()
	go func() { _ = engineInstance.Run(ctx) }()

	exec := startExecution(t, ctx, bus, store, singleCapabilityBlueprint("slow"))
	events := subscribeToExecution(t, bus, exec.ID)
	runtime.awaitStart(t)

	if !scheduler.CancelExecution(ctx, exec.ID, errors.New("stopped by operator")) {
		t.Fatal("CancelExecution() = false, want true")
	}

	// The subscription carries the execution's whole event stream, so the
	// cancellation has to be picked out of it rather than read off the head.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case received, open := <-events.Events():
			if !open {
				t.Fatal("subscription closed before the cancellation event arrived")
			}
			if received.Type != event.ExecutionCancelled {
				continue
			}
			payload, ok := received.Payload.(event.ExecutionCancelledPayload)
			if !ok {
				t.Fatalf("payload type = %T, want ExecutionCancelledPayload", received.Payload)
			}
			if payload.Message != "stopped by operator" {
				t.Errorf("payload message = %q, want the cancellation reason", payload.Message)
			}
			return
		case <-deadline:
			t.Fatal("no ExecutionCancelled event was published")
		}
	}
}

// The engine's late error for the aborted invocation must not turn the cancelled
// capability into a failed one. This is the end-to-end form of the state-guard
// test: only a real scheduler and engine running together can produce the race.
func TestTheAbortedInvocationDoesNotReportTheCapabilityAsFailed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	scheduler, exec, runtime := newCancelHarness(t, ctx, singleCapabilityBlueprint("slow"))
	runtime.awaitStart(t)
	scheduler.CancelExecution(ctx, exec.ID, errors.New("stopped by operator"))
	runtime.awaitCancellation(t)

	// The engine needs a moment to unwind and report the aborted invocation.
	// Asserting immediately would pass even with the bug present, because the
	// cancelled state is already set when CancelExecution returns.
	time.Sleep(200 * time.Millisecond)

	if got := exec.CapabilityState("slow").Status; got != executionmodel.CapabilityCancelled {
		t.Errorf("capability status = %s, want %s (an abort is not a capability failure)", got, executionmodel.CapabilityCancelled)
	}
	if got := exec.Status(); got != executionmodel.StatusCancelled {
		t.Errorf("Status() = %s, want %s", got, executionmodel.StatusCancelled)
	}
}

// A cancelled execution is terminal, so the scheduler must not advance it. Any
// capability scheduled after the cancellation would be work nobody asked to stop.
func TestCancelExecutionStopsThePlanFromAdvancing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// a -> b, so cancelling while a runs proves b is never scheduled.
	a := stubNode("a", core.RuntimeExecutionModeWait)
	a.Next = []types.ExecutionTransition{{BindingID: "a-b", TargetCapabilityID: "b"}}
	plan := &types.ExecutionBlueprint{
		Metadata:           core.Metadata{ID: "bp", Name: "cancel", Version: "1.0.0"},
		Nodes:              map[core.ID]types.ExecutionNode{"a": a, "b": stubNode("b", core.RuntimeExecutionModeWait)},
		EntryCapabilityIDs: []core.ID{"a"},
	}

	scheduler, exec, runtime := newCancelHarness(t, ctx, plan)
	runtime.awaitStart(t)
	if !scheduler.CancelExecution(ctx, exec.ID, errors.New("stop")) {
		t.Fatal("CancelExecution() = false, want true")
	}
	runtime.awaitCancellation(t)

	// Give a scheduler that ignored the terminal state a chance to misbehave.
	time.Sleep(200 * time.Millisecond)
	if got := exec.CapabilityState("b").Status; got == executionmodel.CapabilityCompleted {
		t.Error("capability b ran after the execution was cancelled")
	}
	if got := exec.Status(); got != executionmodel.StatusCancelled {
		t.Errorf("Status() = %s, want %s", got, executionmodel.StatusCancelled)
	}
}

// Cancelling something that is not running must not be reported as success.
func TestCancelExecutionRefusesUnknownAndTerminalExecutions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus := event.NewBus()
	store := executionmodel.NewMemoryStore()
	scopes := executionmodel.NewScopeRegistry(ctx)

	scheduler, err := New(bus, store, scopes)
	if err != nil {
		t.Fatalf("New scheduler: %v", err)
	}

	if scheduler.CancelExecution(ctx, "exec_missing", errors.New("stop")) {
		t.Error("CancelExecution() = true for an unknown execution, want false")
	}

	exec, err := executionmodel.NewExecution(singleCapabilityBlueprint("slow"), "corr_1", "inst_1")
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	if err := store.Add(exec); err != nil {
		t.Fatalf("store.Add: %v", err)
	}
	if err := exec.Start(map[string]any{}, 1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !exec.MarkCompleted() {
		t.Fatal("MarkCompleted() = false, want true")
	}

	if scheduler.CancelExecution(ctx, exec.ID, errors.New("stop")) {
		t.Error("CancelExecution() = true for a completed execution, want false")
	}
	if got := exec.Status(); got != executionmodel.StatusCompleted {
		t.Errorf("Status() = %s, want %s (a refused cancellation must not rewrite the outcome)", got, executionmodel.StatusCompleted)
	}
	if exec.Error() != "" {
		t.Errorf("Error() = %q, want empty", exec.Error())
	}
}
