package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/event"
	executionmodel "github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/execution/engine"
	executionmodelTypes "github.com/neuron-runtime/neuron/nore/internal/types"
	"github.com/neuron-runtime/neuron/shared/types/core"
)

func TestFanInJoinExecution(t *testing.T) {
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

	a := stubNode("a", core.RuntimeExecutionModeWait)
	a.Next = []executionmodelTypes.ExecutionTransition{{BindingID: "a-t", TargetCapabilityID: "t"}}
	b := stubNode("b", core.RuntimeExecutionModeWait)
	b.Next = []executionmodelTypes.ExecutionTransition{{BindingID: "b-t", TargetCapabilityID: "t"}}
	tNode := executionmodelTypes.ExecutionNode{
		Capability: core.Capability{
			Metadata: core.Metadata{ID: "t", Name: "t", Version: "1.0.0"},
			Type:     "test:stub",
		},
		Configurations: staticConfigurations{},
		Predecessors: []executionmodelTypes.ExecutionPredecessor{
			{BindingID: "a-t", CapabilityID: core.ID("a")},
			{BindingID: "b-t", CapabilityID: core.ID("b")},
		},
		Next: []executionmodelTypes.ExecutionTransition{},
	}
	d := stubNode("d", core.RuntimeExecutionModeWait)

	blueprint := &executionmodelTypes.ExecutionBlueprint{
		Metadata: core.Metadata{ID: "bp", Name: "fanin", Version: "1.0.0"},
		Nodes: map[core.ID]executionmodelTypes.ExecutionNode{
			core.ID("a"): a,
			core.ID("b"): b,
			core.ID("t"): tNode,
			core.ID("d"): d,
		},
		EntryCapabilityIDs: []core.ID{core.ID("a")},
	}

	parent, err := execution.NewExecution(NewExecution(blueprint, "corr_1", "inst_1")NewExecution(blueprint, "corr_1", "inst_1")nbsp;NewExecution(blueprint, "corr_1", "inst_1")NewExecution(blueprint, "corr_1", "inst_1")nbsp;NewExecution(blueprint, "corr_1", "inst_1")NewExecution(blueprint, "corr_1", "inst_1")nbsp;NewExecution(blueprint, "corr_1", "inst_1")NewExecution(blueprint, "corr_1", "inst_1")nbsp;NewExecution(blueprint, "corr_1", "inst_1")NewExecution(blueprint, "corr_1", "inst_1")nbsp;NewExecution(blueprint, "corr_1", "inst_1")NewExecution(blueprint, "corr_1", "inst_1")nbsp;)
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	if err := store.Add(ctx, parent); err != nil {
		t.Fatalf("store.Add(ctx, parent): %v", err)
	}
	if err := bus.Publish(ctx, event.New(event.ExecutionStarted, parent.ID, parent.CorrelationID, "", event.ExecutionStartedPayload{Params: map[string]any{}})); err != nil {
		t.Fatalf("publish ExecutionStarted: %v", err)
	}

	waitFor(t, "execution to complete", func() bool {
		current, err := store.Get(context.Background(), parent.ID)
		return err == nil && current.IsTerminal()
	})

	if got := runtime.count(parent.ID, core.ID("t")); got != 1 {
		t.Fatalf("capability t ran %d times, want 1, got %d", got, 1)
	}
	expectedParams := map[string]any{"x": "from-a", "y": "from-b"}
	if got := parent.Params("t"); !deepEqualParams(t, got, expectedParams) {
		t.Errorf("exec.Params(t) = %v, want %v", got, expectedParams)
	}

	stored, err := store.Get(context.Background(), parent.ID)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if !stored.IsTerminal() {
		t.Error("execution is not terminal")
	}
}

func deepEqualParams(t *testing.T, got, want map[string]any) bool {
	t.Helper()
	if len(got) != len(want) {
		return false
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}
