package scheduler

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/neuron-runtime/neuron/nore/internal/resolver"
	"github.com/neuron-runtime/neuron/nore/internal/types"
	core "github.com/neuron-runtime/neuron/shared/types/core"
)

func reference(kind core.ValueRefKind, capability string, path ...string) core.ValueRef {
	return core.ValueRef{Kind: kind, Capability: capability, Path: path}
}

func TestResolveReference(t *testing.T) {
	environment := resolver.Environment{
		Source: map[string]any{
			"id": "validate-order",
			"result": map[string]any{
				"order":     map[string]any{"customer_id": "c-1"},
				"validated": true,
			},
			"params": map[string]any{"order_id": "o-1"},
		},
		Execution: map[string]any{
			"params": map[string]any{"order": map[string]any{"total_cents": 2250}},
		},
	}

	tests := []struct {
		name string
		ref  core.ValueRef
		want any
	}{
		{"capability result whole", reference(core.ValueRefCapabilityResult, "validate-order"), map[string]any{"order": map[string]any{"customer_id": "c-1"}, "validated": true}},
		{"capability result nested", reference(core.ValueRefCapabilityResult, "validate-order", "order", "customer_id"), "c-1"},
		{"capability params", reference(core.ValueRefCapabilityParams, "validate-order", "order_id"), "o-1"},
		{"assembly params nested", reference(core.ValueRefAssemblyParams, "", "order", "total_cents"), 2250},
		{"literal", core.ValueRef{Kind: core.ValueRefLiteral, Value: "fixed"}, "fixed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveReference(environment, tt.ref)
			if err != nil {
				t.Fatalf("resolveReference: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("resolveReference = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestResolveReferenceFailures(t *testing.T) {
	environment := resolver.Environment{
		Source: map[string]any{
			"id":     "validate-order",
			"result": map[string]any{"order": "not-an-object"},
		},
		Execution: map[string]any{"params": map[string]any{}},
	}

	tests := []struct {
		name string
		ref  core.ValueRef
	}{
		{"missing field", reference(core.ValueRefCapabilityResult, "validate-order", "absent")},
		{"non-object intermediate", reference(core.ValueRefCapabilityResult, "validate-order", "order", "customer_id")},
		{"identity mismatch", reference(core.ValueRefCapabilityResult, "other-capability", "order")},
		{"variable has no provider", reference(core.ValueRefVariable, "", "project")},
		{"unknown kind", reference(core.ValueRefKind("mystery"), "")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := resolveReference(environment, tt.ref); err == nil {
				t.Errorf("resolveReference(%s) = nil error, want rejection", tt.ref)
			}
		})
	}
}

func TestApplyTransitionStructuralMappings(t *testing.T) {
	environment := resolver.Environment{
		Source: map[string]any{
			"id":     "auth",
			"result": map[string]any{"payment": map[string]any{"intent_id": "pi_123"}},
		},
		Execution: map[string]any{"params": map[string]any{"currency": "usd"}},
	}
	transition := types.ExecutionTransition{
		BindingID: "a-b",
		Mappings: []types.CompiledMapping{
			{TargetPath: "intent_id", Source: reference(core.ValueRefCapabilityResult, "auth", "payment", "intent_id")},
			{TargetPath: "currency", Source: reference(core.ValueRefAssemblyParams, "", "currency")},
		},
	}
	input, err := applyTransition(context.Background(), environment, transition)
	if err != nil {
		t.Fatalf("applyTransition: %v", err)
	}
	want := map[string]any{
		"intent_id": "pi_123",
		"currency":  "usd",
	}
	if !reflect.DeepEqual(input, want) {
		t.Errorf("applyTransition = %#v, want %#v", input, want)
	}
}

func TestApplyTransitionLiteral(t *testing.T) {
	transition := types.ExecutionTransition{
		BindingID: "a-b",
		Mappings: []types.CompiledMapping{
			{TargetPath: "status", Source: core.ValueRef{Kind: core.ValueRefLiteral, Value: "authorized"}},
			{TargetPath: "tries", Source: core.ValueRef{Kind: core.ValueRefLiteral, Value: 3}},
		},
	}
	input, err := applyTransition(context.Background(), resolver.Environment{}, transition)
	if err != nil {
		t.Fatalf("applyTransition: %v", err)
	}
	want := map[string]any{"status": "authorized", "tries": 3}
	if !reflect.DeepEqual(input, want) {
		t.Errorf("applyTransition = %#v, want %#v", input, want)
	}
}

func TestApplyTransitionFailureMessageNamesReference(t *testing.T) {
	environment := resolver.Environment{
		Source:    map[string]any{"id": "auth", "result": map[string]any{}},
		Execution: map[string]any{"params": map[string]any{}},
	}
	transition := types.ExecutionTransition{
		BindingID: "a-b",
		Mappings: []types.CompiledMapping{
			{TargetPath: "intent", Source: reference(core.ValueRefCapabilityResult, "auth", "payment")},
		},
	}
	_, err := applyTransition(context.Background(), environment, transition)
	if err == nil {
		t.Fatal("expected a missing-field error")
	}
	if !strings.Contains(err.Error(), "source.result.payment") {
		t.Errorf("error should name the reference, got %q", err.Error())
	}
}