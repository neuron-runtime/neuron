package protocol

import (
	"testing"

	"github.com/neuron-runtime/neuron/shared/types/core"
)

func bindingAssembly(bindings ...core.Binding) core.Assembly {
	return core.Assembly{
		Metadata: core.Metadata{Name: "assembly", Version: "1.0.0"},
		Specification: core.AssemblySpec{
			Capabilities: []core.Capability{capability("a"), capability("b")},
			Bindings:     bindings,
		},
	}
}

func TestHashAssemblyNormalizesJoinPolicy(t *testing.T) {
	omitted, err := HashAssembly(bindingAssembly(core.Binding{
		From: core.Endpoint{CapabilityID: "a"},
		To:   core.Endpoint{CapabilityID: "b"},
	}))
	if err != nil {
		t.Fatalf("hash assembly: %v", err)
	}
	explicit, err := HashAssembly(bindingAssembly(core.Binding{
		From:       core.Endpoint{CapabilityID: "a"},
		To:         core.Endpoint{CapabilityID: "b"},
		JoinPolicy: core.JoinPolicyAll,
	}))
	if err != nil {
		t.Fatalf("hash assembly: %v", err)
	}
	if omitted != explicit {
		t.Fatalf("an omitted join policy and explicit all must hash identically: %s != %s", omitted, explicit)
	}
}

func TestHashAssemblyIncludesJoinPolicy(t *testing.T) {
	all, err := HashAssembly(bindingAssembly(core.Binding{
		From:       core.Endpoint{CapabilityID: "a"},
		To:         core.Endpoint{CapabilityID: "b"},
		JoinPolicy: core.JoinPolicyAll,
	}))
	if err != nil {
		t.Fatalf("hash assembly: %v", err)
	}
	other, err := HashAssembly(bindingAssembly(core.Binding{
		From:       core.Endpoint{CapabilityID: "a"},
		To:         core.Endpoint{CapabilityID: "b"},
		JoinPolicy: core.JoinPolicy("any"),
	}))
	if err != nil {
		t.Fatalf("hash assembly: %v", err)
	}
	if all == other {
		t.Fatal("a different join policy must change the assembly hash")
	}
}

func TestHashAssemblyBindingOrderIndependentWithTieBreakers(t *testing.T) {
	first := core.Binding{
		From: core.Endpoint{CapabilityID: "a"},
		To:   core.Endpoint{CapabilityID: "b"},
		Mappings: []core.MappingRule{
			{TargetPath: "left", Source: &core.ValueRef{Kind: core.ValueRefCapabilityResult, Capability: "a", Path: []string{"left"}}},
		},
		Validations: []core.ValidationRule{{Expression: "source.result.left != null", Message: "left required"}},
	}
	second := core.Binding{
		From: core.Endpoint{CapabilityID: "a"},
		To:   core.Endpoint{CapabilityID: "b"},
		Mappings: []core.MappingRule{
			{TargetPath: "right", Source: &core.ValueRef{Kind: core.ValueRefCapabilityResult, Capability: "a", Path: []string{"right"}}},
		},
		Validations: []core.ValidationRule{{Expression: "source.result.right != null", Message: "right required"}},
	}

	forward, err := HashAssembly(bindingAssembly(first, second))
	if err != nil {
		t.Fatalf("hash assembly: %v", err)
	}
	reversed, err := HashAssembly(bindingAssembly(second, first))
	if err != nil {
		t.Fatalf("hash assembly: %v", err)
	}
	if forward != reversed {
		t.Fatalf("hash must not depend on parallel-edge declaration order: %s != %s", forward, reversed)
	}
}

func TestHashAssemblyMappingOrderIndependentWithTieBreakers(t *testing.T) {
	left := core.MappingRule{TargetPath: "value", Source: &core.ValueRef{Kind: core.ValueRefCapabilityResult, Capability: "a", Path: []string{"left"}}}
	right := core.MappingRule{TargetPath: "value", Source: &core.ValueRef{Kind: core.ValueRefCapabilityResult, Capability: "a", Path: []string{"right"}}}

	forward, err := HashAssembly(bindingAssembly(core.Binding{
		From:     core.Endpoint{CapabilityID: "a"},
		To:       core.Endpoint{CapabilityID: "b"},
		Mappings: []core.MappingRule{left, right},
	}))
	if err != nil {
		t.Fatalf("hash assembly: %v", err)
	}
	reversed, err := HashAssembly(bindingAssembly(core.Binding{
		From:     core.Endpoint{CapabilityID: "a"},
		To:       core.Endpoint{CapabilityID: "b"},
		Mappings: []core.MappingRule{right, left},
	}))
	if err != nil {
		t.Fatalf("hash assembly: %v", err)
	}
	if forward != reversed {
		t.Fatalf("hash must not depend on same-target mapping order: %s != %s", forward, reversed)
	}
}
