package planner

import (
	"encoding/json"
	"reflect"
	"testing"

	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

func planningAssembly(mappings []shared.MappingRule) shared.Assembly {
	return shared.Assembly{
		Metadata: shared.Metadata{Name: "planning", Version: "1.0.0"},
		Specification: shared.AssemblySpec{
			Capabilities: []shared.Capability{
				{Metadata: shared.Metadata{ID: "a", Name: "a", Version: "1"}, Type: "neuron:core:set"},
				{Metadata: shared.Metadata{ID: "b", Name: "b", Version: "1"}, Type: "neuron:core:set"},
			},
			Bindings: []shared.Binding{{
				Metadata: shared.Metadata{ID: shared.NewID("binding_")},
				From:     shared.Endpoint{CapabilityID: "a"},
				To:       shared.Endpoint{CapabilityID: "b"},
				Mappings: mappings,
			}},
		},
	}
}

func legacyRule(t *testing.T, expression string) shared.MappingRule {
	t.Helper()
	var rule shared.MappingRule
	if err := json.Unmarshal([]byte(`{"TargetPath":"amount","Expression":"`+expression+`"}`), &rule); err != nil {
		t.Fatalf("unmarshal legacy rule: %v", err)
	}
	return rule
}

func TestCompileResolvesStructuredMapping(t *testing.T) {
	compiler, err := NewCompiler(stubExpressionCompiler{})
	if err != nil {
		t.Fatalf("new compiler: %v", err)
	}
	assembly := planningAssembly([]shared.MappingRule{
		{TargetPath: "amount", Source: &shared.ValueRef{Kind: shared.ValueRefCapabilityResult, Capability: "a", Path: []string{"amount_cents"}}},
	})
	blueprint, err := compiler.Compile(assembly)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	node := blueprint.Nodes["a"]
	if len(node.Next) != 1 || len(node.Next[0].Mappings) != 1 {
		t.Fatalf("expected one transition with one mapping, got %+v", node.Next)
	}
	mapping := node.Next[0].Mappings[0]
	want := shared.ValueRef{Kind: shared.ValueRefCapabilityResult, Capability: "a", Path: []string{"amount_cents"}}
	if !reflect.DeepEqual(mapping.Source, want) {
		t.Errorf("mapping source = %+v, want %+v", mapping.Source, want)
	}
	if mapping.TargetPath != "amount" {
		t.Errorf("mapping target = %q, want amount", mapping.TargetPath)
	}
}

func TestCompileResolvesLegacyExpressionMapping(t *testing.T) {
	compiler, err := NewCompiler(stubExpressionCompiler{})
	if err != nil {
		t.Fatalf("new compiler: %v", err)
	}
	// A mapping from a pre-structure artifact carries the legacy expression
	// string and no Source; the planner must freeze the parsed reference into
	// the plan so the scheduler never parses.
	assembly := planningAssembly([]shared.MappingRule{legacyRule(t, "source.result.amountCents")})
	blueprint, err := compiler.Compile(assembly)
	if err != nil {
		t.Fatalf("compile legacy: %v", err)
	}
	mapping := blueprint.Nodes["a"].Next[0].Mappings[0]
	want := shared.ValueRef{Kind: shared.ValueRefCapabilityResult, Capability: "a", Path: []string{"amount_cents"}}
	if !reflect.DeepEqual(mapping.Source, want) {
		t.Errorf("legacy mapping source = %+v, want %+v", mapping.Source, want)
	}
}

func TestCompileRejectsMappingWithoutSource(t *testing.T) {
	compiler, err := NewCompiler(stubExpressionCompiler{})
	if err != nil {
		t.Fatalf("new compiler: %v", err)
	}
	assembly := planningAssembly([]shared.MappingRule{{TargetPath: "amount"}})
	if _, err := compiler.Compile(assembly); err == nil {
		t.Fatal("a binding mapping with no source must be rejected at plan time")
	}
}

func TestCompileRejectsOperatorExpressionAtPlanTime(t *testing.T) {
	compiler, err := NewCompiler(stubExpressionCompiler{})
	if err != nil {
		t.Fatalf("new compiler: %v", err)
	}
	// A legacy expression that was actually an operator condition is not a
	// reference; parsing it must fail when the plan is built, not silently
	// produce a malformed path.
	assembly := planningAssembly([]shared.MappingRule{legacyRule(t, "source.result.amount_cents >= 1000")})
	if _, err := compiler.Compile(assembly); err == nil {
		t.Fatal("an operator expression on a mapping must be rejected at plan time")
	}
}
