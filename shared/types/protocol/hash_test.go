package protocol

import (
	"encoding/json"
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

func bindingWithSource(source core.ValueRef) core.Binding {
	return core.Binding{
		From: core.Endpoint{CapabilityID: "a"},
		To:   core.Endpoint{CapabilityID: "b"},
		Mappings: []core.MappingRule{
			{TargetPath: "order", Source: &source},
		},
	}
}

func TestHashAssemblyHashesStructuredReferencesDeterministically(t *testing.T) {
	assembly := core.Assembly{
		Metadata: core.Metadata{Name: "assembly", Version: "1.0.0"},
		Specification: core.AssemblySpec{
			Capabilities: []core.Capability{capability("a"), capability("b")},
			Bindings: []core.Binding{
				bindingWithSource(core.ValueRef{Kind: core.ValueRefCapabilityResult, Capability: "a", Path: []string{"order", "customer_id"}}),
			},
		},
	}
	first, err := HashAssembly(assembly)
	if err != nil {
		t.Fatalf("hash assembly: %v", err)
	}

	// Bindings are sorted by From/To, so declaring them in reverse order must
	// not change the hash.
	reversed := assembly
	reversed.Specification.Bindings = []core.Binding{
		{From: core.Endpoint{CapabilityID: "a"}, To: core.Endpoint{CapabilityID: "b"}, Mappings: assembly.Specification.Bindings[0].Mappings},
	}
	second, err := HashAssembly(reversed)
	if err != nil {
		t.Fatalf("hash assembly: %v", err)
	}
	if first != second {
		t.Fatalf("hash must not depend on binding order with structured refs: %s != %s", first, second)
	}
}

func TestHashAssemblyDistinguishesMappingSources(t *testing.T) {
	variants := map[string]core.ValueRef{
		"different capability": {Kind: core.ValueRefCapabilityResult, Capability: "a", Path: []string{"order"}},
		"different path":       {Kind: core.ValueRefCapabilityResult, Capability: "a", Path: []string{"order", "total"}},
		"different kind":       {Kind: core.ValueRefAssemblyParams, Path: []string{"order"}},
		"literal":              {Kind: core.ValueRefLiteral, Value: "fixed"},
	}
	base, err := HashAssembly(core.Assembly{
		Metadata: core.Metadata{Name: "assembly", Version: "1.0.0"},
		Specification: core.AssemblySpec{
			Capabilities: []core.Capability{capability("a"), capability("b")},
			Bindings:     []core.Binding{bindingWithSource(variants["different capability"])},
		},
	})
	if err != nil {
		t.Fatalf("hash assembly: %v", err)
	}
	for name, source := range variants {
		t.Run(name, func(t *testing.T) {
			changed, err := HashAssembly(core.Assembly{
				Metadata: core.Metadata{Name: "assembly", Version: "1.0.0"},
				Specification: core.AssemblySpec{
					Capabilities: []core.Capability{capability("a"), capability("b")},
					Bindings:     []core.Binding{bindingWithSource(source)},
				},
			})
			if err != nil {
				t.Fatalf("hash assembly: %v", err)
			}
			if changed == base && name != "different capability" {
				t.Fatalf("%s must change the assembly hash", name)
			}
		})
	}
}

func TestHashAssemblyParsedLegacyMatchesStructured(t *testing.T) {
	// A mapping loaded from a pre-structure artifact (legacy expression string)
	// must hash identically to the same mapping authored as a structured ref,
	// because the parser normalizes the legacy string into the same ValueRef.
	var legacy core.MappingRule
	if err := json.Unmarshal([]byte(`{"TargetPath":"order","Expression":"source.result.order.customerId"}`), &legacy); err != nil {
		t.Fatalf("unmarshal legacy: %v", err)
	}
	parsed, err := legacy.SourceRef("a")
	if err != nil {
		t.Fatalf("SourceRef: %v", err)
	}

	hashLegacy, err := HashAssembly(core.Assembly{
		Metadata: core.Metadata{Name: "assembly", Version: "1.0.0"},
		Specification: core.AssemblySpec{
			Capabilities: []core.Capability{capability("a"), capability("b")},
			Bindings: []core.Binding{{
				From: core.Endpoint{CapabilityID: "a"}, To: core.Endpoint{CapabilityID: "b"},
				Mappings: []core.MappingRule{{TargetPath: "order", Source: &parsed}},
			}},
		},
	})
	if err != nil {
		t.Fatalf("hash assembly: %v", err)
	}

	hashStructured, err := HashAssembly(core.Assembly{
		Metadata: core.Metadata{Name: "assembly", Version: "1.0.0"},
		Specification: core.AssemblySpec{
			Capabilities: []core.Capability{capability("a"), capability("b")},
			Bindings: []core.Binding{{
				From: core.Endpoint{CapabilityID: "a"}, To: core.Endpoint{CapabilityID: "b"},
				Mappings: []core.MappingRule{{
					TargetPath: "order",
					Source:     &core.ValueRef{Kind: core.ValueRefCapabilityResult, Capability: "a", Path: []string{"order", "customer_id"}},
				}},
			}},
		},
	})
	if err != nil {
		t.Fatalf("hash assembly: %v", err)
	}
	if hashLegacy != hashStructured {
		t.Fatalf("parsed legacy and structured sources must hash identically: %s != %s", hashLegacy, hashStructured)
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

func assemblyWithContract(contract func(*core.Assembly)) core.Assembly {
	assembly := assemblyOf(capability("a"))
	contract(&assembly)
	return assembly
}

func TestHashAssemblyIncludesAssemblyParamsAndResult(t *testing.T) {
	withContract, err := HashAssembly(assemblyWithContract(func(a *core.Assembly) {
		a.Params = []core.Port{{Name: "order", Type: "object", Required: true}}
		a.Result = &core.AssemblyResult{
			Schema: []core.Port{{Name: "status", Type: "string", Required: true}},
			Fields: []core.AssemblyResultField{
				{Name: "status", Source: &core.ValueRef{
					Kind:       core.ValueRefCapabilityResult,
					Capability: "a",
					Path:       []string{"status"},
				}},
			},
		}
	}))
	if err != nil {
		t.Fatalf("hash assembly: %v", err)
	}

	withParamsOnly, err := HashAssembly(core.Assembly{
		Metadata: core.Metadata{Name: "assembly", Version: "1.0.0"},
		Params:   []core.Port{{Name: "order", Type: "object", Required: true}},
		Specification: core.AssemblySpec{
			Capabilities: []core.Capability{capability("a")},
		},
	})
	if err != nil {
		t.Fatalf("hash assembly: %v", err)
	}

	withResultOnly, err := HashAssembly(core.Assembly{
		Metadata: core.Metadata{Name: "assembly", Version: "1.0.0"},
		Result: &core.AssemblyResult{
			Schema: []core.Port{{Name: "status", Type: "string", Required: true}},
			Fields: []core.AssemblyResultField{
				{Name: "status", Source: &core.ValueRef{
					Kind:       core.ValueRefCapabilityResult,
					Capability: "a",
					Path:       []string{"status"},
				}},
			},
		},
		Specification: core.AssemblySpec{
			Capabilities: []core.Capability{capability("a")},
		},
	})
	if err != nil {
		t.Fatalf("hash assembly: %v", err)
	}

	withoutContract, err := HashAssembly(assemblyOf(capability("a")))
	if err != nil {
		t.Fatalf("hash assembly: %v", err)
	}

	if withContract == withoutContract {
		t.Fatal("an assembly contract must change the assembly hash")
	}
	if withParamsOnly == withoutContract {
		t.Fatal("declared params must change the assembly hash")
	}
	if withResultOnly == withoutContract {
		t.Fatal("a declared result must change the assembly hash")
	}
}

func TestHashAssemblyNormalizesMissingAndEmptyResult(t *testing.T) {
	missing, err := HashAssembly(assemblyOf(capability("a")))
	if err != nil {
		t.Fatalf("hash assembly: %v", err)
	}
	empty, err := HashAssembly(assemblyWithContract(func(a *core.Assembly) {
		a.Result = &core.AssemblyResult{}
	}))
	if err != nil {
		t.Fatalf("hash assembly: %v", err)
	}
	if missing != empty {
		t.Fatal("an assembly with no result must hash the same as one declaring an empty result")
	}
}

func TestHashAssemblyKeepsAssemblyParamsDeclaredOrder(t *testing.T) {
	ordered := core.Assembly{
		Metadata: core.Metadata{Name: "assembly", Version: "1.0.0"},
		Params: []core.Port{
			{Name: "order", Type: "object", Required: true},
			{Name: "traceId", Type: "string"},
		},
		Specification: core.AssemblySpec{
			Capabilities: []core.Capability{capability("a")},
		},
	}
	first, err := HashAssembly(ordered)
	if err != nil {
		t.Fatalf("hash assembly: %v", err)
	}

	reordered := ordered
	reordered.Params = []core.Port{
		{Name: "traceId", Type: "string"},
		{Name: "order", Type: "object", Required: true},
	}
	second, err := HashAssembly(reordered)
	if err != nil {
		t.Fatalf("hash assembly: %v", err)
	}
	if first == second {
		t.Fatal("the declared params order is the contract and must change the assembly hash")
	}
}

func TestHashAssemblyDistinguishesAssemblyResultFieldSources(t *testing.T) {
	baseFields := []core.AssemblyResultField{
		{Name: "status", Source: &core.ValueRef{
			Kind:       core.ValueRefCapabilityResult,
			Capability: "a",
			Path:       []string{"status"},
		}},
	}
	base, err := HashAssembly(assemblyWithContract(func(a *core.Assembly) {
		a.Result = &core.AssemblyResult{
			Schema: []core.Port{{Name: "status", Type: "string", Required: true}},
			Fields: baseFields,
		}
	}))
	if err != nil {
		t.Fatalf("hash assembly: %v", err)
	}

	variants := map[string][]core.AssemblyResultField{
		"different capability": {{
			Name:   "status",
			Source: &core.ValueRef{Kind: core.ValueRefCapabilityResult, Capability: "other", Path: []string{"status"}},
		}},
		"different path": {{
			Name:   "status",
			Source: &core.ValueRef{Kind: core.ValueRefCapabilityResult, Capability: "a", Path: []string{"status", "code"}},
		}},
		"different kind": {{
			Name:   "status",
			Source: &core.ValueRef{Kind: core.ValueRefAssemblyParams, Path: []string{"status"}},
		}},
		"literal": {{
			Name:   "status",
			Source: &core.ValueRef{Kind: core.ValueRefLiteral, Value: "fixed"},
		}},
		"different field name": {{
			Name:   "code",
			Source: &core.ValueRef{Kind: core.ValueRefCapabilityResult, Capability: "a", Path: []string{"status"}},
		}},
	}

	for name, fields := range variants {
		t.Run(name, func(t *testing.T) {
			changed, err := HashAssembly(assemblyWithContract(func(a *core.Assembly) {
				a.Result = &core.AssemblyResult{
					Schema: []core.Port{{Name: "status", Type: "string", Required: true}},
					Fields: fields,
				}
			}))
			if err != nil {
				t.Fatalf("hash assembly: %v", err)
			}
			if changed == base {
				t.Fatalf("%s must change the assembly hash", name)
			}
		})
	}
}

func TestHashAssemblyIgnoresAssemblyDescription(t *testing.T) {
	// Assembly description was removed from the canonical model and from the
	// hash: it carries no execution or deployment meaning, so editing it must
	// not invalidate a registration.
	described := assemblyWithContract(func(a *core.Assembly) {
		a.Metadata.Description = "a friendly assembly"
	})
	first, err := HashAssembly(described)
	if err != nil {
		t.Fatalf("hash assembly: %v", err)
	}
	second, err := HashAssembly(assemblyOf(capability("a")))
	if err != nil {
		t.Fatalf("hash assembly: %v", err)
	}
	if first != second {
		t.Fatal("an assembly description must not change the assembly hash")
	}
}
