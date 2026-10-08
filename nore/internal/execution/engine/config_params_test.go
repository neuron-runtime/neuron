package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
	"github.com/neuron-runtime/neuron/nore/internal/event"
	exec "github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/resolver"
	"github.com/neuron-runtime/neuron/nore/internal/runtimeconfig"
	"github.com/neuron-runtime/neuron/nore/internal/types"
	core "github.com/neuron-runtime/neuron/shared/types/core"
)

// configCapturingRuntime records the resolved capability configuration it was
// invoked with, so a test can observe what the engine resolved from templates.
type configCapturingRuntime struct {
	mu         sync.Mutex
	config     map[string]any
	started    chan struct{}
	startOnce  sync.Once
	executions chan contracts.ExecutionContext
}

func newConfigCapturingRuntime() *configCapturingRuntime {
	return &configCapturingRuntime{started: make(chan struct{}, 4), executions: make(chan contracts.ExecutionContext, 4)}
}

func (r *configCapturingRuntime) Execute(_ context.Context, execution contracts.ExecutionContext) (map[string]any, error) {
	r.mu.Lock()
	r.config = execution.CapabilityConfigurations
	r.mu.Unlock()
	r.startOnce.Do(func() { close(r.started) })
	r.executions <- execution
	return map[string]any{"done": true}, nil
}

func (r *configCapturingRuntime) awaitStart(t *testing.T) {
	t.Helper()
	select {
	case <-r.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the capability was never invoked")
	}
}

func (r *configCapturingRuntime) resolvedConfig() map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.config
}

// p0ConfigBlueprint gives its single capability a configuration template that
// reads the capability's own `params` variable. The input is authored with
// camelCase keys, the shape a binding mapping or entry param can carry before
// it has been normalized.
func p0ConfigBlueprint(t *testing.T) *types.ExecutionBlueprint {
	t.Helper()
	compiler, err := resolver.NewCELCompiler(resolver.CELConfig{})
	if err != nil {
		t.Fatalf("new configuration compiler: %v", err)
	}
	configProgram, err := compiler.CompileCapabilityConfigurations(map[string]any{
		"message": "customer {{ params.customer_id }} authorized",
	})
	if err != nil {
		t.Fatalf("compile configuration: %v", err)
	}
	return &types.ExecutionBlueprint{
		Metadata: core.Metadata{ID: "bp", Name: "p0-config", Version: "1.0.0"},
		Nodes: map[core.ID]types.ExecutionNode{"capture": {
			Capability: core.Capability{
				Metadata:      core.Metadata{ID: "capture", Name: "capture", Version: "1.0.0"},
				Type:          core.CapabilityRuntimeType("test:config"),
				RuntimeConfig: runtimeconfig.Resolve(&core.RuntimeConfig{}),
			},
			Configurations: configProgram,
		}},
		EntryCapabilityIDs: []core.ID{"capture"},
	}
}

// TestCapabilityConfigParamsVariableExposesSnakeCaseKeys pins P0-23: the
// `params` variable a configuration template reads must expose the canonical
// snake_case keys even when the capability's input was authored with camelCase
// keys, so one spelling works across configuration templates and binding
// expressions.
func TestCapabilityConfigParamsVariableExposesSnakeCaseKeys(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runtime := newConfigCapturingRuntime()
	bus := event.NewBus()
	store := exec.NewMemoryStore()
	scopes := exec.NewScopeRegistry(ctx)

	engineInstance, err := NewCapabilityRuntimeEngine(bus, singleRuntimeRegistry{runtime: runtime}, store, scopes, 2, time.Second)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	go func() { _ = engineInstance.Run(ctx) }()

	execution, err := exec.NewExecution(p0ConfigBlueprint(t), core.NewID("corr_"), "inst_1")
	if err != nil {
		t.Fatalf("new execution: %v", err)
	}
	if err := store.Add(execution); err != nil {
		t.Fatalf("store.Add: %v", err)
	}

	if err := execution.Start(map[string]any{}, 1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	input := map[string]any{"customerId": "c-7"}
	if err := execution.MarkCapabilityReady("capture", input); err != nil {
		t.Fatalf("MarkCapabilityReady: %v", err)
	}
	if err := bus.Publish(ctx, event.New(event.ExecutionStarted, execution.ID, execution.CorrelationID, "", event.ExecutionStartedPayload{Params: map[string]any{}})); err != nil {
		t.Fatalf("publish ExecutionStarted: %v", err)
	}
	if err := bus.Publish(ctx, event.New(event.CapabilityReady, execution.ID, execution.CorrelationID, "capture", event.CapabilityReadyPayload{Params: input})); err != nil {
		t.Fatalf("publish CapabilityReady: %v", err)
	}

	runtime.awaitStart(t)
	config := runtime.resolvedConfig()
	if got := config["message"]; got != "customer c-7 authorized" {
		t.Errorf("resolved configuration = %q, want %q", got, "customer c-7 authorized")
	}
}
