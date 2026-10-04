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
