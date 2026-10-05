package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
	"github.com/neuron-runtime/neuron/nore/internal/event"
	executionmodel "github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/execution/engine"
	"github.com/neuron-runtime/neuron/nore/internal/resolver"
	"github.com/neuron-runtime/neuron/nore/internal/runtimeconfig"
	"github.com/neuron-runtime/neuron/nore/internal/types"
	core "github.com/neuron-runtime/neuron/shared/types/core"
)

const stubRuntimeType core.CapabilityRuntimeType = "test:stub"

// recordingRuntime is a capability runtime that succeeds and remembers which
// execution asked it to run which capability. Recording the execution is what
// lets the test tell the root execution's work apart from the detached task's,
// since both scopes contain a capability with the same ID. It is safe for
// concurrent invocations because the engine hands capabilities to workers.
type recordingRuntime struct {
	mu    sync.Mutex
	calls []runtimeCall
}

type runtimeCall struct {
	execution  core.ID
	capability core.ID
}

func (r *recordingRuntime) Execute(_ context.Context, execution contracts.ExecutionContext) (map[string]any, error) {
	call := runtimeCall{execution: execution.ExecutionID, capability: execution.Capability.Metadata.ID}
	r.mu.Lock()
	r.calls = append(r.calls, call)
	r.mu.Unlock()
	return map[string]any{"ran": string(call.capability)}, nil
}

func (r *recordingRuntime) count(executionID, capabilityID core.ID) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, call := range r.calls {
		if call.execution == executionID && call.capability == capabilityID {
			count++
		}
	}
	return count
}

type stubRegistry struct{ runtime contracts.CapabilityRuntime }

func (r stubRegistry) Register(core.CapabilityRuntimeType, contracts.CapabilityRuntime) error {
	return nil
}

func (r stubRegistry) Resolve(core.CapabilityRuntimeType) (contracts.CapabilityRuntime, error) {
	return r.runtime, nil
}

// staticConfigurations stands in for the planner-compiled configuration program
// of a capability that declares no configuration templates.
type staticConfigurations struct{}

func (staticConfigurations) Resolve(context.Context, resolver.CapabilityEnvironment) (map[string]any, error) {
	return map[string]any{}, nil
}

func stubNode(id core.ID, mode core.RuntimeExecutionMode) types.ExecutionNode {
	var declared *core.RuntimeConfig
	if mode == core.RuntimeExecutionModeDetach {
		declared = &core.RuntimeConfig{Execution: &core.RuntimeExecution{Mode: mode}}
	}
	return types.ExecutionNode{
		Capability: core.Capability{
			Metadata:      core.Metadata{ID: id, Name: string(id), Version: "1.0.0"},
			Type:          stubRuntimeType,
			RuntimeConfig: runtimeconfig.Resolve(declared),
		},
		Configurations: staticConfigurations{},
	}
}

// detachBlueprint builds `a -> b(detach) -> c`. The work for `b` and everything
// downstream belongs to a task, while `a` is awaited by the root execution.
func detachBlueprint() *types.ExecutionBlueprint {
	a := stubNode("a", core.RuntimeExecutionModeWait)
	a.Next = []types.ExecutionTransition{{BindingID: "a-b", TargetCapabilityID: "b"}}
	b := stubNode("b", core.RuntimeExecutionModeDetach)
	b.Next = []types.ExecutionTransition{{BindingID: "b-c", TargetCapabilityID: "c"}}
	c := stubNode("c", core.RuntimeExecutionModeWait)

	// Inside its own scope the detached capability runs; only the enclosing
	// plan describes it as detached.
	scopeB := stubNode("b", core.RuntimeExecutionModeWait)
	scopeB.Next = []types.ExecutionTransition{{BindingID: "b-c", TargetCapabilityID: "c"}}
	scope := &types.ExecutionBlueprint{
		Metadata:           core.Metadata{ID: "bp", Name: "detach", Version: "1.0.0"},
		Nodes:              map[core.ID]types.ExecutionNode{"b": scopeB, "c": c},
		EntryCapabilityIDs: []core.ID{"b"},
	}

	return &types.ExecutionBlueprint{
		Metadata:           core.Metadata{ID: "bp", Name: "detach", Version: "1.0.0"},
		Nodes:              map[core.ID]types.ExecutionNode{"a": a, "b": b},
		EntryCapabilityIDs: []core.ID{"a"},
		Detached:           map[core.ID]*types.ExecutionBlueprint{"b": scope},
	}
}

func TestSchedulerHandsDetachedWorkToATrackedChildExecution(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus := event.NewBus()
	store := executionmodel.NewMemoryStore()
	scopes := executionmodel.NewScopeRegistry(ctx)
	runtime := &recordingRuntime{}

	scheduler, err := New(bus, store, scopes)
	if err != nil {
		t.Fatalf("New scheduler: %v", err)
	}
	engineInstance, err := engine.NewCapabilityRuntimeEngine(bus, stubRegistry{runtime: runtime}, store, scopes, 4, time.Second)
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}
	go func() { _ = scheduler.Run(ctx) }()
	go func() { _ = engineInstance.Run(ctx) }()

	parent, err := executionmodel.NewExecution(detachBlueprint(), "corr_1", "inst_1")
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	if err := store.Add(parent); err != nil {
		t.Fatalf("store.Add(parent): %v", err)
	}
	if err := bus.Publish(ctx, event.New(event.ExecutionStarted, parent.ID, parent.CorrelationID, "", event.ExecutionStartedPayload{Params: map[string]any{}})); err != nil {
		t.Fatalf("publish ExecutionStarted: %v", err)
	}

	waitFor(t, "parent execution to finish", func() bool {
		current, ok := store.Get(parent.ID)
		return ok && current.IsTerminal()
	})

	// The parent must never run the detached capability or its descendants.
	if got := runtime.count(parent.ID, "b"); got != 0 {
		t.Fatalf("the root execution ran the detached capability %d times, want 0", got)
	}
	if got := runtime.count(parent.ID, "c"); got != 0 {
		t.Fatalf("the root execution ran the detached descendant %d times, want 0", got)
	}

	task := waitForChild(t, store, parent.ID)
	waitFor(t, "detached task to finish", func() bool {
		current, ok := store.Get(task.ID)
		return ok && current.IsTerminal()
	})

	// The work actually happened exactly once, inside the task.
	if got := runtime.count(task.ID, "b"); got != 1 {
		t.Fatalf("the detached capability ran %d times, want 1", got)
	}
	if got := runtime.count(task.ID, "c"); got != 1 {
		t.Fatalf("the detached descendant ran %d times, want 1", got)
	}

	storedTask, ok := store.Get(task.ID)
	if !ok {
		t.Fatal("detached task disappeared")
	}
	if storedTask.ParentExecutionID != parent.ID {
		t.Fatalf("task parent = %q, want %q", storedTask.ParentExecutionID, parent.ID)
	}
	if storedTask.InstanceID != parent.InstanceID || storedTask.CorrelationID != parent.CorrelationID {
		t.Fatalf("task identity %+v does not match parent", storedTask)
	}
}

// waitForChild blocks until a task that points at parent appears in the store.
func waitForChild(t *testing.T, store *executionmodel.MemoryStore, parent core.ID) *executionmodel.Execution {
	t.Helper()
	var found *executionmodel.Execution
	waitFor(t, "detached child execution to be created", func() bool {
		for _, candidate := range store.List() {
			if candidate.ParentExecutionID == parent {
				found = candidate
				return true
			}
		}
		return false
	})
	return found
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// outcome records how one invocation ended: whether the runtime was allowed to
// finish its work or had its context cancelled underneath it.
type outcome string

const (
	outcomeCompleted outcome = "completed"
	outcomeCancelled outcome = "cancelled"
)

// releasableRuntime stays inside an invocation of the capability named by
// blockFor until the test releases it or the invocation's context dies, and
// records which of the two happened. Every other capability returns immediately,
// so a test can let an execution reach its detached capability before shutdown
// lands. It stands in for a capability doing real work that must survive the
// caller going away.
type releasableRuntime struct {
	blockFor core.ID
	release  chan struct{}
	started  chan core.ID
	mu       sync.Mutex
	outcomes map[core.ID]outcome
}

func newReleasableRuntime(blockFor core.ID) *releasableRuntime {
	return &releasableRuntime{
		blockFor: blockFor,
		release:  make(chan struct{}),
		started:  make(chan core.ID, 8),
		outcomes: map[core.ID]outcome{},
	}
}

func (r *releasableRuntime) Execute(ctx context.Context, execution contracts.ExecutionContext) (map[string]any, error) {
	id := execution.ExecutionID
	capability := execution.Capability.Metadata.ID
	if capability != r.blockFor {
		return map[string]any{"ok": true}, nil
	}
	select {
	case r.started <- id:
	default:
	}
	select {
	case <-r.release:
		r.record(id, outcomeCompleted)
		return map[string]any{"ok": true}, nil
	case <-ctx.Done():
		r.record(id, outcomeCancelled)
		return nil, ctx.Err()
	}
}

func (r *releasableRuntime) record(id core.ID, result outcome) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.outcomes[id] = result
}

func (r *releasableRuntime) outcomeOf(id core.ID) (outcome, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result, ok := r.outcomes[id]
	return result, ok
}

// Detaching exists so a capability's work can outlive the execution that started
// it, including outliving the instance shutting down. Releasing every scope at
// shutdown used to cancel detached work at the very instant shutdown began, so the
// engine's drain budget could never apply and a detached capability was aborted
// rather than finished.
func TestDetachedWorkSurvivesInstanceShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus := event.NewBus()
	store := executionmodel.NewMemoryStore()
	scopes := executionmodel.NewScopeRegistry(ctx)
	runtime := newReleasableRuntime("b")

	scheduler, err := New(bus, store, scopes)
	if err != nil {
		t.Fatalf("New scheduler: %v", err)
	}
	// A drain budget long enough that only an early cancellation could cut it
	// short, so a passing result cannot come from the timeout expiring.
	engineInstance, err := engine.NewCapabilityRuntimeEngine(bus, stubRegistry{runtime: runtime}, store, scopes, 4, 30*time.Second)
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}

	schedulerDone := make(chan error, 1)
	go func() { schedulerDone <- scheduler.Run(ctx) }()
	engineDone := make(chan error, 1)
	go func() { engineDone <- engineInstance.Run(ctx) }()

	parent, err := executionmodel.NewExecution(detachBlueprint(), "corr_1", "inst_1")
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	if err := store.Add(parent); err != nil {
		t.Fatalf("store.Add(parent): %v", err)
	}
	if err := bus.Publish(ctx, event.New(event.ExecutionStarted, parent.ID, parent.CorrelationID, "", event.ExecutionStartedPayload{Params: map[string]any{}})); err != nil {
		t.Fatalf("publish ExecutionStarted: %v", err)
	}

	task := waitForChild(t, store, parent.ID)

	// Wait until the detached capability is genuinely inside the runtime, so
	// shutdown lands while its work is in flight.
	waitFor(t, "the detached capability to start", func() bool {
		select {
		case id := <-runtime.started:
			return id == task.ID
		default:
			return false
		}
	})

	// Shutdown. Cancelling the instance context stops the scheduler, which
	// releases every scope it owns.
	cancel()
	if err := <-schedulerDone; err != nil {
		t.Fatalf("scheduler.Run: %v", err)
	}

	// The detached invocation must still be alive after every scope was released.
	// Releasing it here is what the drain budget would otherwise have to wait for.
	select {
	case id := <-runtime.started:
		t.Fatalf("capability %s started unexpectedly while the detached task was in flight", id)
	case <-time.After(50 * time.Millisecond):
	}
	close(runtime.release)

	// The engine waits for in-flight work, so it can only finish once the
	// detached capability returned.
	select {
	case <-engineDone:
	case <-time.After(10 * time.Second):
		t.Fatal("engine.Run did not return; the detached invocation was never released")
	}

	result, recorded := runtime.outcomeOf(task.ID)
	if !recorded {
		t.Fatalf("the detached capability recorded no outcome")
	}
	if result != outcomeCompleted {
		t.Fatalf("detached work outcome = %q, want %q; shutdown cancelled the work that detach exists to preserve", result, outcomeCompleted)
	}
}

// An ordinary execution must not gain the same immunity. Otherwise a fix for
// detached work would leak work nobody asked to keep running.
func TestOrdinaryWorkIsStillCancelledAtShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus := event.NewBus()
	store := executionmodel.NewMemoryStore()
	scopes := executionmodel.NewScopeRegistry(ctx)
	runtime := newReleasableRuntime("slow")

	scheduler, err := New(bus, store, scopes)
	if err != nil {
		t.Fatalf("New scheduler: %v", err)
	}
	engineInstance, err := engine.NewCapabilityRuntimeEngine(bus, stubRegistry{runtime: runtime}, store, scopes, 4, 30*time.Second)
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}

	schedulerDone := make(chan error, 1)
	go func() { schedulerDone <- scheduler.Run(ctx) }()
	engineDone := make(chan error, 1)
	go func() { engineDone <- engineInstance.Run(ctx) }()

	// One capability and no detached scopes, so the only work that exists is
	// ordinary work the instance owns.
	blueprint := singleCapabilityBlueprint("slow")
	root, err := executionmodel.NewExecution(blueprint, "corr_1", "inst_1")
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	if err := store.Add(root); err != nil {
		t.Fatalf("store.Add(root): %v", err)
	}
	if err := bus.Publish(ctx, event.New(event.ExecutionStarted, root.ID, root.CorrelationID, "", event.ExecutionStartedPayload{Params: map[string]any{}})); err != nil {
		t.Fatalf("publish ExecutionStarted: %v", err)
	}

	waitFor(t, "the root capability to start", func() bool {
		select {
		case id := <-runtime.started:
			return id == root.ID
		default:
			return false
		}
	})

	cancel()
	if err := <-schedulerDone; err != nil {
		t.Fatalf("scheduler.Run: %v", err)
	}
	select {
	case <-engineDone:
	case <-time.After(10 * time.Second):
		t.Fatal("engine.Run did not return; ordinary work was never cancelled")
	}

	result, recorded := runtime.outcomeOf(root.ID)
	if !recorded {
		t.Fatal("the root capability recorded no outcome")
	}
	if result != outcomeCancelled {
		t.Fatalf("root work outcome = %q, want %q; shutdown must still abort ordinary work", result, outcomeCancelled)
	}
}
