package project

import (
	"strings"
	"testing"

	core "github.com/neuron-runtime/neuron/shared/types/core"
)

func capabilityFile(execution *ExecutionConfig, runtimeConfig *core.RuntimeConfig) CapabilityFile {
	return CapabilityFile{
		APIVersion: "neuron/v1",
		Kind:       "Capability",
		Metadata:   CapabilityMetadata{Name: "step", Version: "1.0.0"},
		Spec: CapabilitySpec{
			CapabilityRuntime: CapabilityRuntimeSpec{
				Type:          "neuron:core:set",
				RuntimeConfig: runtimeConfig,
			},
			Execution: execution,
		},
	}
}

func TestValidateAcceptsTheLegacyExecutionBlock(t *testing.T) {
	// The legacy block is deprecated, not removed: rejecting it outright would
	// break every existing YAML assembly with no migration path.
	err := validateCapabilityBasic(capabilityFile(&ExecutionConfig{Mode: "detach", Timeout: "30s", Retries: 2}, nil))
	if err != nil {
		t.Fatalf("the legacy execution block must still validate: %v", err)
	}
}

// Silently discarding concurrency or continueOnFail would tell an author their
// declaration had an effect when it had none, so they are rejected instead.
func TestValidateRejectsUnsupportedLegacyOptions(t *testing.T) {
	cases := map[string]*ExecutionConfig{
		"concurrency":    {Concurrency: 4},
		"continueOnFail": {ContinueOnFail: true},
	}
	for name, execution := range cases {
		t.Run(name, func(t *testing.T) {
			err := validateCapabilityBasic(capabilityFile(execution, nil))
			if err == nil {
				t.Fatalf("%s must be rejected rather than silently ignored", name)
			}
			if !strings.Contains(err.Error(), name) {
				t.Fatalf("the error must name the unsupported option: %v", err)
			}
		})
	}
}

// A malformed runtimeConfig must fail at build time, not at execution time,
// once it is a single supported field.
func TestValidateRejectsInvalidRuntimeConfig(t *testing.T) {
	cases := map[string]*core.RuntimeConfig{
		"unknown mode":   {Execution: &core.RuntimeExecution{Mode: "detatch"}},
		"bad timeout":    {Execution: &core.RuntimeExecution{Timeout: "soon"}},
		"unknown policy": {Retry: &core.RuntimeRetry{Policy: "linear", MaxAttempts: 2}},
		"attempts alone": {Retry: &core.RuntimeRetry{MaxAttempts: 2}},
	}
	for name, config := range cases {
		t.Run(name, func(t *testing.T) {
			if err := validateCapabilityBasic(capabilityFile(nil, config)); err == nil {
				t.Fatalf("%s must be rejected", name)
			}
		})
	}
}

func TestValidateAcceptsTheGroupedRuntimeConfig(t *testing.T) {
	err := validateCapabilityBasic(capabilityFile(nil, &core.RuntimeConfig{
		Execution: &core.RuntimeExecution{Mode: core.RuntimeExecutionModeDetach, Timeout: "5s"},
		Retry:     &core.RuntimeRetry{Policy: core.RetryPolicyFixed, MaxAttempts: 3, InitialBackoff: "100ms"},
	}))
	if err != nil {
		t.Fatalf("a grouped runtimeConfig must validate: %v", err)
	}
}
