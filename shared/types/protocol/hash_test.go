package protocol

import (
	"testing"

	"github.com/neuron-runtime/neuron/shared/types/core"
)

func capability(id string, runtimeConfig ...*core.RuntimeConfig) core.Capability {
	var config *core.RuntimeConfig
	if len(runtimeConfig) > 0 {
		config = runtimeConfig[0]
	}
	return core.Capability{
		Metadata:      core.Metadata{ID: core.ID(id), Name: id, Version: "1.0.0"},
		Type:          "neuron:core:set",
		RuntimeConfig: config,
	}
}

func assemblyOf(capabilities ...core.Capability) core.Assembly {
	return core.Assembly{
		Metadata: core.Metadata{Name: "assembly", Version: "1.0.0"},
		Specification: core.AssemblySpec{
			Capabilities: capabilities,
		},
	}
}

func TestHashAssemblyIsDeterministic(t *testing.T) {
	first, err := HashAssembly(assemblyOf(capability("a"), capability("b")))
	if err != nil {
		t.Fatalf("hash assembly: %v", err)
	}
	second, err := HashAssembly(assemblyOf(capability("b"), capability("a")))
	if err != nil {
		t.Fatalf("hash assembly: %v", err)
	}
	if first != second {
		t.Fatalf("hash must not depend on capability order: %s != %s", first, second)
	}
}

func TestHashAssemblyIgnoresGeneratedIdentifiers(t *testing.T) {
	withGenerated := core.Assembly{
		Metadata: core.Metadata{ID: core.NewID("assembly_"), Name: "assembly", Version: "1.0.0"},
		Specification: core.AssemblySpec{
			Capabilities: []core.Capability{capability("a")},
		},
	}
	withoutGenerated := core.Assembly{
		Metadata: core.Metadata{Name: "assembly", Version: "1.0.0"},
		Specification: core.AssemblySpec{
			Capabilities: []core.Capability{capability("a")},
		},
	}

	first, err := HashAssembly(withGenerated)
	if err != nil {
		t.Fatalf("hash assembly: %v", err)
	}
	second, err := HashAssembly(withoutGenerated)
	if err != nil {
		t.Fatalf("hash assembly: %v", err)
	}
	if first != second {
		t.Fatalf("generated identifiers must not affect the hash: %s != %s", first, second)
	}
}

func TestHashAssemblyIncludesDeclaredRuntimeConfig(t *testing.T) {
	declared := &core.RuntimeConfig{
		Execution: &core.RuntimeExecution{Mode: core.RuntimeExecutionModeDetach, Timeout: "45s"},
		Retry: &core.RuntimeRetry{
			Policy:         core.RetryPolicyExponential,
			MaxAttempts:    4,
			InitialBackoff: "100ms",
			MaxBackoff:     "2s",
		},
	}

	withConfig, err := HashAssembly(assemblyOf(capability("a", declared)))
	if err != nil {
		t.Fatalf("hash assembly: %v", err)
	}
	withoutConfig, err := HashAssembly(assemblyOf(capability("a")))
	if err != nil {
		t.Fatalf("hash assembly: %v", err)
	}
	if withConfig == withoutConfig {
		t.Fatal("a declared runtimeConfig must change the assembly hash")
	}
}

func TestHashAssemblyDistinguishesRuntimeConfigFields(t *testing.T) {
	baseline := assemblyOf(capability("a", &core.RuntimeConfig{
		Execution: &core.RuntimeExecution{Mode: core.RuntimeExecutionModeWait, Timeout: "5s"},
		Retry:     &core.RuntimeRetry{Policy: core.RetryPolicyFixed, MaxAttempts: 2, InitialBackoff: "50ms"},
	}))
	base, err := HashAssembly(baseline)
	if err != nil {
		t.Fatalf("hash assembly: %v", err)
	}

	variants := map[string]*core.RuntimeConfig{
		"different mode": {
			Execution: &core.RuntimeExecution{Mode: core.RuntimeExecutionModeDetach, Timeout: "5s"},
			Retry:     &core.RuntimeRetry{Policy: core.RetryPolicyFixed, MaxAttempts: 2, InitialBackoff: "50ms"},
		},
		"different timeout": {
			Execution: &core.RuntimeExecution{Mode: core.RuntimeExecutionModeWait, Timeout: "6s"},
			Retry:     &core.RuntimeRetry{Policy: core.RetryPolicyFixed, MaxAttempts: 2, InitialBackoff: "50ms"},
		},
		"different retry policy": {
			Execution: &core.RuntimeExecution{Mode: core.RuntimeExecutionModeWait, Timeout: "5s"},
			Retry:     &core.RuntimeRetry{Policy: core.RetryPolicyExponential, MaxAttempts: 2, InitialBackoff: "50ms"},
		},
		"different max attempts": {
			Execution: &core.RuntimeExecution{Mode: core.RuntimeExecutionModeWait, Timeout: "5s"},
			Retry:     &core.RuntimeRetry{Policy: core.RetryPolicyFixed, MaxAttempts: 3, InitialBackoff: "50ms"},
		},
		"different backoff": {
			Execution: &core.RuntimeExecution{Mode: core.RuntimeExecutionModeWait, Timeout: "5s"},
			Retry:     &core.RuntimeRetry{Policy: core.RetryPolicyFixed, MaxAttempts: 2, InitialBackoff: "75ms"},
		},
	}

	for name, runtimeConfig := range variants {
		t.Run(name, func(t *testing.T) {
			changed, err := HashAssembly(assemblyOf(capability("a", runtimeConfig)))
			if err != nil {
				t.Fatalf("hash assembly: %v", err)
			}
			if changed == base {
				t.Fatalf("%s must change the assembly hash", name)
			}
		})
	}
}

func TestHashAssemblyKeepsPerCapabilityRuntimeConfigDistinct(t *testing.T) {
	// The same runtime artifact may be shared by several capabilities, but each
	// capability's runtimeConfig is its own. Two capabilities using the same
	// runtime with different configurations must not collapse into one another.
	shared := assemblyOf(
		capability("a", &core.RuntimeConfig{Execution: &core.RuntimeExecution{Mode: core.RuntimeExecutionModeWait}}),
		capability("b", &core.RuntimeConfig{Execution: &core.RuntimeExecution{Mode: core.RuntimeExecutionModeDetach}}),
	)
	first, err := HashAssembly(shared)
	if err != nil {
		t.Fatalf("hash assembly: %v", err)
	}

	swapped := assemblyOf(
		capability("a", &core.RuntimeConfig{Execution: &core.RuntimeExecution{Mode: core.RuntimeExecutionModeDetach}}),
		capability("b", &core.RuntimeConfig{Execution: &core.RuntimeExecution{Mode: core.RuntimeExecutionModeWait}}),
	)
	second, err := HashAssembly(swapped)
	if err != nil {
		t.Fatalf("hash assembly: %v", err)
	}
	if first == second {
		t.Fatal("per-capability runtimeConfig must be attributed to the capability that declares it")
	}
}

func TestHashAssemblyIgnoresNilAndEmptyRuntimeConfig(t *testing.T) {
	nilConfig, err := HashAssembly(assemblyOf(capability("a")))
	if err != nil {
		t.Fatalf("hash assembly: %v", err)
	}
	emptyConfig, err := HashAssembly(assemblyOf(capability("a", &core.RuntimeConfig{})))
	if err != nil {
		t.Fatalf("hash assembly: %v", err)
	}
	if nilConfig != emptyConfig {
		t.Fatal("an author who declares no runtimeConfig must hash the same as one declaring an empty group")
	}
}

func TestHashAssemblyIncludesTriggerRuntimeConfig(t *testing.T) {
	withConfig := core.Assembly{
		Metadata: core.Metadata{Name: "assembly", Version: "1.0.0"},
		Specification: core.AssemblySpec{
			Triggers: []core.Trigger{{
				Capability: capability("entry", &core.RuntimeConfig{
					Execution: &core.RuntimeExecution{Timeout: "20s"},
				}),
			}},
		},
	}
	withoutConfig := core.Assembly{
		Metadata: core.Metadata{Name: "assembly", Version: "1.0.0"},
		Specification: core.AssemblySpec{
			Triggers: []core.Trigger{{Capability: capability("entry")}},
		},
	}

	first, err := HashAssembly(withConfig)
	if err != nil {
		t.Fatalf("hash assembly: %v", err)
	}
	second, err := HashAssembly(withoutConfig)
	if err != nil {
		t.Fatalf("hash assembly: %v", err)
	}
	if first == second {
		t.Fatal("a trigger's declared runtimeConfig must change the assembly hash")
	}
}
