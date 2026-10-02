package runtimeconfig

import (
	"testing"

	"github.com/neuron-runtime/neuron/shared/types/core"
)

func TestDefaultPopulatesEveryField(t *testing.T) {
	effective := Default()

	if effective.Execution == nil || effective.Retry == nil || effective.Resources == nil {
		t.Fatal("every group must be populated so a capability never needs a declared runtimeConfig")
	}
	if effective.Execution.Mode != core.RuntimeExecutionModeWait {
		t.Fatalf("mode default = %q, want %q", effective.Execution.Mode, core.RuntimeExecutionModeWait)
	}
	if effective.Execution.Timeout != "" {
		t.Fatalf("timeout default must stay unset so the runtime backend owns its invocation bound, got %q", effective.Execution.Timeout)
	}
	if effective.Retry.Policy != core.RetryPolicyNone {
		t.Fatalf("retry policy default = %q, want %q", effective.Retry.Policy, core.RetryPolicyNone)
	}
	if effective.Retry.MaxAttempts != 1 {
		t.Fatalf("maxAttempts default = %d, want 1 total attempt", effective.Retry.MaxAttempts)
	}
}

func TestResolveOfNothingYieldsDefaults(t *testing.T) {
	declared := Default()
	effective := Resolve(nil)
	if *effective.Execution != *declared.Execution {
		t.Fatalf("nil declaration must yield the defaults: got %+v", effective.Execution)
	}
	if *effective.Retry != *declared.Retry {
		t.Fatalf("nil declaration must yield the defaults: got %+v", effective.Retry)
	}
}

func TestResolveTreatsEmptyDeclarationAsNoDeclaration(t *testing.T) {
	// An author who writes `runtimeConfig: {}` has declared nothing, so the
	// defaults must stand rather than leaving a group zero-valued.
	effective := Resolve(&core.RuntimeConfig{
		Execution: &core.RuntimeExecution{},
		Retry:     &core.RuntimeRetry{},
		Resources: &core.RuntimeResources{},
	})

	if effective.Execution.Mode != core.RuntimeExecutionModeWait {
		t.Fatalf("mode = %q, want the default %q", effective.Execution.Mode, core.RuntimeExecutionModeWait)
	}
	if effective.Retry.Policy != core.RetryPolicyNone {
		t.Fatalf("policy = %q, want the default %q", effective.Retry.Policy, core.RetryPolicyNone)
	}
	if effective.Retry.MaxAttempts != 1 {
		t.Fatalf("maxAttempts = %d, want the default 1", effective.Retry.MaxAttempts)
	}
}

func TestResolveAppliesAuthoredValues(t *testing.T) {
	effective := Resolve(&core.RuntimeConfig{
		Execution: &core.RuntimeExecution{Mode: core.RuntimeExecutionModeDetach, Timeout: "45s"},
		Retry: &core.RuntimeRetry{
			Policy:         core.RetryPolicyExponential,
			MaxAttempts:    4,
			InitialBackoff: "100ms",
			MaxBackoff:     "2s",
		},
	})

	if effective.Execution.Mode != core.RuntimeExecutionModeDetach || effective.Execution.Timeout != "45s" {
		t.Fatalf("execution = %+v, want the declared values", effective.Execution)
	}
	if effective.Retry.Policy != core.RetryPolicyExponential || effective.Retry.MaxAttempts != 4 ||
		effective.Retry.InitialBackoff != "100ms" || effective.Retry.MaxBackoff != "2s" {
		t.Fatalf("retry = %+v, want the declared values", effective.Retry)
	}
}

func TestResolveFillsOnlyUndeclaredFields(t *testing.T) {
	// An author may declare one field and inherit the rest.
	effective := Resolve(&core.RuntimeConfig{
		Execution: &core.RuntimeExecution{Timeout: "90s"},
	})

	if effective.Execution.Timeout != "90s" {
		t.Fatalf("timeout = %q, want the declared 90s", effective.Execution.Timeout)
	}
	if effective.Execution.Mode != core.RuntimeExecutionModeWait {
		t.Fatalf("mode = %q, want the default %q", effective.Execution.Mode, core.RuntimeExecutionModeWait)
	}
	if effective.Retry.Policy != core.RetryPolicyNone || effective.Retry.MaxAttempts != 1 {
		t.Fatalf("retry = %+v, want the defaults", effective.Retry)
	}
}

func TestResolveDoesNotAliasTheDeclaredValue(t *testing.T) {
	// Resolving once per plan is only safe if the result is independent of the
	// declared configuration, which is shared by every instance of an assembly.
	declared := &core.RuntimeConfig{
		Execution: &core.RuntimeExecution{Timeout: "5s"},
		Retry:     &core.RuntimeRetry{Policy: core.RetryPolicyFixed, MaxAttempts: 2},
	}
	effective := Resolve(declared)

	effective.Execution.Timeout = "999s"
	effective.Retry.MaxAttempts = 99

	if declared.Execution.Timeout != "5s" {
		t.Fatalf("declared execution.timeout was mutated to %q", declared.Execution.Timeout)
	}
	if declared.Retry.MaxAttempts != 2 {
		t.Fatalf("declared retry.maxAttempts was mutated to %d", declared.Retry.MaxAttempts)
	}
}

func TestResolveIsIndependentPerCapability(t *testing.T) {
	// Two capabilities may share one runtime artifact while configuring their
	// invocations differently.
	declared := &core.RuntimeConfig{Execution: &core.RuntimeExecution{Mode: core.RuntimeExecutionModeWait}}
	declaredAlt := &core.RuntimeConfig{Execution: &core.RuntimeExecution{Mode: core.RuntimeExecutionModeDetach}}

	first := Resolve(declared)
	second := Resolve(declaredAlt)

	if first.Execution.Mode != core.RuntimeExecutionModeWait {
		t.Fatalf("first capability mode = %q", first.Execution.Mode)
	}
	if second.Execution.Mode != core.RuntimeExecutionModeDetach {
		t.Fatalf("second capability mode = %q", second.Execution.Mode)
	}
}
