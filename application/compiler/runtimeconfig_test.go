package compiler

import (
	"strings"
	"testing"

	"github.com/neuron-runtime/neuron/application/compiler/manifest"
	core "github.com/neuron-runtime/neuron/shared/types/core"
)

// The compiler validates what an author declared and carries it through
// unchanged. It never substitutes a value: deciding what an omission means
// belongs to N.O.R.E., not to compilation.

func TestConvertCapabilityCarriesDeclaredRuntimeConfig(t *testing.T) {
	capability, err := convertCapability(manifest.Capability{
		Name: "step",
		CapabilityRuntime: manifest.CapabilityRuntimeSpec{
			Name: "neuron:core:set",
			RuntimeConfig: &core.RuntimeConfig{
				Execution: &core.RuntimeExecution{Mode: core.RuntimeExecutionModeDetach, Timeout: "5s"},
				Retry:     &core.RuntimeRetry{Policy: core.RetryPolicyFixed, MaxAttempts: 3},
			},
		},
	})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	if capability.RuntimeConfig == nil {
		t.Fatal("the declared runtime configuration must reach core")
	}
	if capability.RuntimeConfig.Execution.Mode != core.RuntimeExecutionModeDetach ||
		capability.RuntimeConfig.Execution.Timeout != "5s" {
		t.Fatalf("execution = %+v, want the declared values", capability.RuntimeConfig.Execution)
	}
	if capability.RuntimeConfig.Retry.Policy != core.RetryPolicyFixed ||
		capability.RuntimeConfig.Retry.MaxAttempts != 3 {
		t.Fatalf("retry = %+v, want the declared values", capability.RuntimeConfig.Retry)
	}
}

// A capability with no declared runtime configuration must reach N.O.R.E.
// undeclared, so the engine can distinguish "nothing declared" from "an empty
// configuration". N.O.R.E. must never see a value the author did not write.
func TestConvertCapabilityLeavesUndeclaredRuntimeConfigNil(t *testing.T) {
	capability, err := convertCapability(manifest.Capability{
		Name:              "step",
		CapabilityRuntime: manifest.CapabilityRuntimeSpec{Name: "neuron:core:set"},
	})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	if capability.RuntimeConfig != nil {
		t.Fatalf("the compiler must not invent a runtime configuration, got %+v", capability.RuntimeConfig)
	}
}

func TestConvertCapabilityDoesNotAliasTheManifest(t *testing.T) {
	// The compiler must not hand N.O.R.E. a pointer into the manifest, or a
	// later mutation on one side would silently change the other.
	declared := &core.RuntimeConfig{
		Execution: &core.RuntimeExecution{Timeout: "5s"},
		Retry:     &core.RuntimeRetry{Policy: core.RetryPolicyFixed, MaxAttempts: 2},
	}
	capability, err := convertCapability(manifest.Capability{
		Name:              "step",
		CapabilityRuntime: manifest.CapabilityRuntimeSpec{Name: "neuron:core:set", RuntimeConfig: declared},
	})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	capability.RuntimeConfig.Execution.Timeout = "999s"
	capability.RuntimeConfig.Retry.MaxAttempts = 99

	if declared.Execution.Timeout != "5s" || declared.Retry.MaxAttempts != 2 {
		t.Fatalf("the manifest was mutated through the compiled capability: %+v", declared)
	}
}

func TestConvertCapabilityRejectsInvalidRuntimeConfig(t *testing.T) {
	cases := map[string]*core.RuntimeConfig{
		"unknown execution mode":  {Execution: &core.RuntimeExecution{Mode: "detatch"}},
		"unknown retry policy":    {Retry: &core.RuntimeRetry{Policy: "linear", MaxAttempts: 2}},
		"attempts without policy": {Retry: &core.RuntimeRetry{MaxAttempts: 3}},
		"attempts without retry":  {Retry: &core.RuntimeRetry{Policy: core.RetryPolicyNone, MaxAttempts: 3}},
		"negative max attempts":   {Retry: &core.RuntimeRetry{Policy: core.RetryPolicyFixed, MaxAttempts: -1}},
		"ceiling below floor": {Retry: &core.RuntimeRetry{
			Policy: core.RetryPolicyExponential, MaxAttempts: 2, InitialBackoff: "2s", MaxBackoff: "100ms",
		}},
		"unparseable timeout": {Execution: &core.RuntimeExecution{Timeout: "soon"}},
		"unparseable backoff": {Retry: &core.RuntimeRetry{
			Policy: core.RetryPolicyFixed, MaxAttempts: 2, InitialBackoff: "later",
		}},
	}

	for name, config := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := convertCapability(manifest.Capability{
				Name:              "step",
				CapabilityRuntime: manifest.CapabilityRuntimeSpec{Name: "neuron:core:set", RuntimeConfig: config},
			})
			if err == nil {
				t.Fatal("an invalid runtime configuration must not compile")
			}
			if !strings.Contains(err.Error(), "step") {
				t.Fatalf("the error must name the offending capability: %v", err)
			}
		})
	}
}

// An explicitly empty configuration is legal and means "no overrides": the
// engine supplies the defaults.
func TestConvertCapabilityAcceptsAnEmptyRuntimeConfig(t *testing.T) {
	capability, err := convertCapability(manifest.Capability{
		Name:              "step",
		CapabilityRuntime: manifest.CapabilityRuntimeSpec{Name: "neuron:core:set", RuntimeConfig: &core.RuntimeConfig{}},
	})
	if err != nil {
		t.Fatalf("an empty runtime configuration must compile: %v", err)
	}
	if capability.RuntimeConfig == nil {
		t.Fatal("an explicitly declared empty configuration must stay declared")
	}
}
