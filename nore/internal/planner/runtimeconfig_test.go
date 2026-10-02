package planner

import (
	"context"
	"testing"

	"github.com/neuron-runtime/neuron/nore/internal/resolver"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

// stubExpressionCompiler stands in for the CEL compiler. Runtime configuration
// resolution is independent of expression compilation, so the planner only
// needs the interface satisfied.
type stubExpressionCompiler struct{}

func (stubExpressionCompiler) CompileTransitionExpression(string) (resolver.Program, error) {
	return nil, nil
}

func (stubExpressionCompiler) CompileCapabilityConfigurations(map[string]any) (resolver.ConfigurationProgram, error) {
	return stubConfigurationProgram{}, nil
}

type stubConfigurationProgram struct{}

func (stubConfigurationProgram) Resolve(context.Context, resolver.CapabilityEnvironment) (map[string]any, error) {
	return map[string]any{}, nil
}

func assemblyWith(declared *shared.RuntimeConfig) shared.Assembly {
	return shared.Assembly{
		Metadata: shared.Metadata{Name: "runtime-config", Version: "1.0.0"},
		Specification: shared.AssemblySpec{
			Capabilities: []shared.Capability{{
				Metadata:      shared.Metadata{ID: shared.ID("step"), Name: "step", Version: "1.0.0"},
				Type:          "neuron:core:set",
				RuntimeConfig: declared,
			}},
		},
	}
}

func compileSingle(t *testing.T, declared *shared.RuntimeConfig) *shared.Capability {
	t.Helper()
	compiler, err := NewCompiler(stubExpressionCompiler{})
	if err != nil {
		t.Fatalf("new compiler: %v", err)
	}
	blueprint, err := compiler.Compile(assemblyWith(declared))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	node, ok := blueprint.Nodes[shared.ID("step")]
	if !ok {
		t.Fatal("compiled plan is missing the capability node")
	}
	capability := node.Capability
	return &capability
}

func TestPlanSuppliesDefaultsWhenNoneDeclared(t *testing.T) {
	// An author does not have to declare a runtimeConfig at all.
	capability := compileSingle(t, nil)

	config := capability.RuntimeConfig
	if config == nil {
		t.Fatal("the plan must carry an effective runtime configuration even when none was declared")
	}
	if config.Execution == nil || config.Retry == nil || config.Resources == nil {
		t.Fatalf("every group must be defaulted, got %+v", config)
	}
	if config.Execution.Mode != shared.RuntimeExecutionModeWait {
		t.Fatalf("mode = %q, want %q", config.Execution.Mode, shared.RuntimeExecutionModeWait)
	}
	if config.Retry.Policy != shared.RetryPolicyNone || config.Retry.MaxAttempts != 1 {
		t.Fatalf("retry = %+v, want no retry with a single attempt", config.Retry)
	}
}

func TestPlanKeepsDeclaredRuntimeConfig(t *testing.T) {
	declared := &shared.RuntimeConfig{
		Execution: &shared.RuntimeExecution{Mode: shared.RuntimeExecutionModeDetach, Timeout: "45s"},
		Retry:     &shared.RuntimeRetry{Policy: shared.RetryPolicyExponential, MaxAttempts: 4, InitialBackoff: "100ms"},
	}
	config := compileSingle(t, declared).RuntimeConfig

	if config.Execution.Mode != shared.RuntimeExecutionModeDetach || config.Execution.Timeout != "45s" {
		t.Fatalf("execution = %+v, want the declared values", config.Execution)
	}
	if config.Retry.Policy != shared.RetryPolicyExponential || config.Retry.MaxAttempts != 4 {
		t.Fatalf("retry = %+v, want the declared values", config.Retry)
	}
	// Declaring one field must not silently reset the others to zero.
	if config.Resources == nil {
		t.Fatal("resources must still be defaulted when only execution and retry were declared")
	}
}

func TestPlanDoesNotAliasTheRegisteredAssembly(t *testing.T) {
	declared := &shared.RuntimeConfig{
		Execution: &shared.RuntimeExecution{Timeout: "5s"},
		Retry:     &shared.RuntimeRetry{Policy: shared.RetryPolicyFixed, MaxAttempts: 2},
	}
	capability := compileSingle(t, declared)

	// The plan holds its own copy, so resolving defaults per plan cannot corrupt
	// the registered assembly that every instance shares.
	if capability.RuntimeConfig == declared {
		t.Fatal("the plan must not share the assembly's runtimeConfig pointer")
	}
	capability.RuntimeConfig.Execution.Timeout = "999s"
	capability.RuntimeConfig.Retry.MaxAttempts = 99
	if declared.Execution.Timeout != "5s" || declared.Retry.MaxAttempts != 2 {
		t.Fatalf("the declared runtimeConfig was mutated through the plan: %+v", declared)
	}
}

func TestPlanLeavesCapabilityConfigurationsUntouched(t *testing.T) {
	// Capability configurations are user input; they are not runtime
	// configuration and must never be merged into it.
	compiler, err := NewCompiler(stubExpressionCompiler{})
	if err != nil {
		t.Fatalf("new compiler: %v", err)
	}
	assembly := assemblyWith(nil)
	assembly.Specification.Capabilities[0].CapabilityConfigurations = shared.CapabilityConfigurations{
		"prompt": "do the thing",
	}
	blueprint, err := compiler.Compile(assembly)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	node := blueprint.Nodes[shared.ID("step")]

	if node.Capability.RuntimeConfig.Execution == nil {
		t.Fatal("runtime configuration must still be defaulted")
	}
	for _, group := range []any{node.Capability.RuntimeConfig.Execution, node.Capability.RuntimeConfig.Retry} {
		if group == nil {
			t.Fatal("runtime configuration group was dropped")
		}
	}
	if node.Configurations == nil {
		t.Fatal("capability configurations must still compile into their own program")
	}
}
