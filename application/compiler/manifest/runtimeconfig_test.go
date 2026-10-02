package manifest

import (
	"testing"

	"github.com/neuron-runtime/neuron/application/project"
	core "github.com/neuron-runtime/neuron/shared/types/core"
)

func specWith(declared *core.RuntimeConfig, legacy *project.ExecutionConfig) project.CapabilitySpec {
	return project.CapabilitySpec{
		CapabilityRuntime: project.CapabilityRuntimeSpec{
			Type:          "neuron:core:set",
			RuntimeConfig: declared,
		},
		Execution: legacy,
	}
}

func TestRuntimeConfigFromNothingDeclared(t *testing.T) {
	if got := runtimeConfigFrom(specWith(nil, nil)); got != nil {
		t.Fatalf("nothing declared must yield no runtimeConfig, got %+v", got)
	}
}

// The legacy YAML block is authoring sugar only: it must never survive into the
// canonical manifest as its own field, so there is exactly one place a
// capability's runtime configuration can live.
func TestRuntimeConfigFromLegacyBlockAlone(t *testing.T) {
	got := runtimeConfigFrom(specWith(nil, &project.ExecutionConfig{Mode: "detach", Timeout: "30s"}))

	if got == nil {
		t.Fatal("the legacy execution block must be translated into runtimeConfig")
	}
	if got.Execution == nil {
		t.Fatal("expected an execution group")
	}
	if got.Execution.Mode != core.RuntimeExecutionModeDetach {
		t.Fatalf("mode = %q, want detach", got.Execution.Mode)
	}
	if got.Execution.Timeout != "30s" {
		t.Fatalf("timeout = %q, want 30s", got.Execution.Timeout)
	}
}

func TestRuntimeConfigFromNestedBlockWinsOverLegacy(t *testing.T) {
	// An author migrating both surfaces at once must get the nested block, not
	// a silent merge that resurrects a stale legacy value.
	declared := &core.RuntimeConfig{
		Execution: &core.RuntimeExecution{Mode: core.RuntimeExecutionModeWait, Timeout: "5s"},
	}
	got := runtimeConfigFrom(specWith(declared, &project.ExecutionConfig{Mode: "detach", Timeout: "30s"}))

	if got.Execution.Mode != core.RuntimeExecutionModeWait {
		t.Fatalf("mode = %q, want the nested wait", got.Execution.Mode)
	}
	if got.Execution.Timeout != "5s" {
		t.Fatalf("timeout = %q, want the nested 5s", got.Execution.Timeout)
	}
}

func TestRuntimeConfigFromLegacyFillsUndeclaredNestedFields(t *testing.T) {
	declared := &core.RuntimeConfig{
		Execution: &core.RuntimeExecution{Mode: core.RuntimeExecutionModeWait},
	}
	got := runtimeConfigFrom(specWith(declared, &project.ExecutionConfig{Timeout: "30s"}))

	if got.Execution.Mode != core.RuntimeExecutionModeWait {
		t.Fatalf("mode = %q, want the nested value preserved", got.Execution.Mode)
	}
	if got.Execution.Timeout != "30s" {
		t.Fatalf("timeout = %q, want the legacy value filling the gap", got.Execution.Timeout)
	}
}

func TestRuntimeConfigFromConvertsRetriesToMaxAttempts(t *testing.T) {
	// The legacy field counted retries beyond the first attempt; maxAttempts
	// counts total attempts. Getting this wrong silently doubles invocations.
	got := runtimeConfigFrom(specWith(nil, &project.ExecutionConfig{Retries: 3}))

	if got.Retry == nil {
		t.Fatal("expected a retry group")
	}
	if got.Retry.MaxAttempts != 4 {
		t.Fatalf("maxAttempts = %d, want 4 total attempts for 3 retries", got.Retry.MaxAttempts)
	}
	if got.Retry.Policy != core.RetryPolicyExponential {
		t.Fatalf("policy = %q, want exponential", got.Retry.Policy)
	}
}

func TestRuntimeConfigFromNestedRetryWinsOverLegacyRetries(t *testing.T) {
	declared := &core.RuntimeConfig{
		Retry: &core.RuntimeRetry{Policy: core.RetryPolicyNone, MaxAttempts: 1},
	}
	got := runtimeConfigFrom(specWith(declared, &project.ExecutionConfig{Retries: 3}))

	if got.Retry.MaxAttempts != 1 {
		t.Fatalf("maxAttempts = %d, want the nested single attempt", got.Retry.MaxAttempts)
	}
}

func TestRuntimeConfigFromDoesNotMutateTheDeclaredValue(t *testing.T) {
	// Folding must not write into the parsed project document, or a second
	// capability sharing the same declaration would inherit a legacy value.
	declared := &core.RuntimeConfig{
		Execution: &core.RuntimeExecution{Mode: core.RuntimeExecutionModeWait},
	}
	if err := declared.Validate(); err != nil {
		t.Fatalf("declared must be valid: %v", err)
	}

	got := runtimeConfigFrom(specWith(declared, &project.ExecutionConfig{Timeout: "30s"}))
	if got.Execution.Timeout != "30s" {
		t.Fatalf("timeout = %q, want 30s", got.Execution.Timeout)
	}
	if declared.Execution.Timeout != "" {
		t.Fatalf("the declared block was mutated: timeout = %q", declared.Execution.Timeout)
	}
}
