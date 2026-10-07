package core

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestParseMappingSource(t *testing.T) {
	tests := []struct {
		name       string
		from       string
		expression string
		want       ValueRef
	}{
		{
			name:       "capability result",
			from:       "validate-order",
			expression: "source.result.order",
			want:       ValueRef{Kind: ValueRefCapabilityResult, Capability: "validate-order", Path: []string{"order"}},
		},
		{
			name:       "capability result nested",
			from:       "validate-order",
			expression: "source.result.order.customer.id",
			want:       ValueRef{Kind: ValueRefCapabilityResult, Capability: "validate-order", Path: []string{"order", "customer", "id"}},
		},
		{
			name:       "capability result whole",
			from:       "capture-payment",
			expression: "source.result",
			want:       ValueRef{Kind: ValueRefCapabilityResult, Capability: "capture-payment"},
		},
		{
			name:       "capability result casing normalized to snake",
			from:       "parse-order",
			expression: "source.result.shippingAddress",
			want:       ValueRef{Kind: ValueRefCapabilityResult, Capability: "parse-order", Path: []string{"shipping_address"}},
		},
		{
			name:       "capability params",
			from:       "authorize-payment",
			expression: "source.params.amountCents",
			want:       ValueRef{Kind: ValueRefCapabilityParams, Capability: "authorize-payment", Path: []string{"amount_cents"}},
		},
		{
			name:       "assembly params",
			from:       "create-shipment",
			expression: "execution.params.order.shipping_address",
			want:       ValueRef{Kind: ValueRefAssemblyParams, Path: []string{"order", "shipping_address"}},
		},
		{
			name:       "assembly params casing normalized",
			from:       "create-shipment",
			expression: "execution.params.order.shippingAddress",
			want:       ValueRef{Kind: ValueRefAssemblyParams, Path: []string{"order", "shipping_address"}},
		},
		{
			name:       "number literal",
			from:       "x",
			expression: "1000",
			want:       ValueRef{Kind: ValueRefLiteral, Value: float64(1000)},
		},
		{
			name:       "negative number literal",
			from:       "x",
			expression: "-3.5",
			want:       ValueRef{Kind: ValueRefLiteral, Value: float64(-3.5)},
		},
		{
			name:       "string literal single quoted",
			from:       "x",
			expression: "'active'",
			want:       ValueRef{Kind: ValueRefLiteral, Value: "active"},
		},
		{
			name:       "string literal double quoted",
			from:       "x",
			expression: `"active"`,
			want:       ValueRef{Kind: ValueRefLiteral, Value: "active"},
		},
		{
			name:       "boolean literal",
			from:       "x",
			expression: "true",
			want:       ValueRef{Kind: ValueRefLiteral, Value: true},
		},
		{
			name:       "null literal",
			from:       "x",
			expression: "null",
			want:       ValueRef{Kind: ValueRefLiteral, Value: nil},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMappingSource(tt.from, tt.expression)
			if err != nil {
				t.Fatalf("ParseMappingSource(%q, %q): %v", tt.from, tt.expression, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseMappingSource(%q, %q) = %+v, want %+v", tt.from, tt.expression, got, tt.want)
			}
			if err := got.Validate(); err != nil {
				t.Errorf("ref failed validation: %v", err)
			}
		})
	}
}

func TestParseMappingSourceRejects(t *testing.T) {
	tests := []struct {
		name       string
		from       string
		expression string
	}{
		{"empty expression", "x", ""},
		{"whitespace only", "x", "   "},
		{"operator expression", "x", "source.result.amount_cents >= 1000"},
		{"computed expression", "x", "source.result.a + source.result.b"},
		{"bare identifier", "x", "order"},
		{"unknown root", "x", "system.params.order"},
		{"empty path segment", "x", "source.result.order..id"},
		{"trailing dot", "x", "source.result.order."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseMappingSource(tt.from, tt.expression); err == nil {
				t.Errorf("ParseMappingSource(%q) = nil error, want rejection", tt.expression)
			}
		})
	}
}

func TestParseMappingSourceRequiresIdentity(t *testing.T) {
	if _, err := ParseMappingSource("", "source.result.order"); err == nil {
		t.Error("source.* reference without a from capability must be rejected")
	}
}

func TestValueRefValidate(t *testing.T) {
	tests := []struct {
		name    string
		ref     ValueRef
		wantErr bool
	}{
		{"valid capability result", ValueRef{Kind: ValueRefCapabilityResult, Capability: "a", Path: []string{"b"}}, false},
		{"capability result missing identity", ValueRef{Kind: ValueRefCapabilityResult, Path: []string{"b"}}, true},
		{"assembly params with identity", ValueRef{Kind: ValueRefAssemblyParams, Capability: "a"}, true},
		{"unknown kind", ValueRef{Kind: "mystery"}, true},
		{"empty path segment", ValueRef{Kind: ValueRefAssemblyParams, Path: []string{"", "b"}}, true},
		{"operator in path", ValueRef{Kind: ValueRefAssemblyParams, Path: []string{"a >= b"}}, true},
		{"null literal", ValueRef{Kind: ValueRefLiteral, Value: nil}, false},
		{"non-null literal", ValueRef{Kind: ValueRefLiteral, Value: "x"}, false},
		{"variable with identity", ValueRef{Kind: ValueRefVariable, Capability: "a"}, true},
		{"valid variable", ValueRef{Kind: ValueRefVariable, Path: []string{"project"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.ref.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValueRefRoundTripJSON(t *testing.T) {
	ref := ValueRef{
		Kind:       ValueRefCapabilityResult,
		Capability: "validate-order",
		Path:       []string{"order", "customer_id"},
	}
	data, err := json.Marshal(ref)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var restored ValueRef
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(restored, ref) {
		t.Errorf("round trip = %+v, want %+v", restored, ref)
	}
}

func TestMappingRuleLoadsLegacyString(t *testing.T) {
	legacy := `{"TargetPath":"order","Expression":"source.result.order"}`
	var rule MappingRule
	if err := json.Unmarshal([]byte(legacy), &rule); err != nil {
		t.Fatalf("unmarshal legacy: %v", err)
	}
	ref, err := rule.SourceRef("validate-order")
	if err != nil {
		t.Fatalf("SourceRef: %v", err)
	}
	want := ValueRef{Kind: ValueRefCapabilityResult, Capability: "validate-order", Path: []string{"order"}}
	if !reflect.DeepEqual(ref, want) {
		t.Errorf("SourceRef = %+v, want %+v", ref, want)
	}
}

func TestMappingRuleLoadsStructured(t *testing.T) {
	structured := `{"targetPath":"order","source":{"kind":"assemblyParams","path":["order"]}}`
	var rule MappingRule
	if err := json.Unmarshal([]byte(structured), &rule); err != nil {
		t.Fatalf("unmarshal structured: %v", err)
	}
	ref, err := rule.SourceRef("validate-order")
	if err != nil {
		t.Fatalf("SourceRef: %v", err)
	}
	want := ValueRef{Kind: ValueRefAssemblyParams, Path: []string{"order"}}
	if !reflect.DeepEqual(ref, want) {
		t.Errorf("SourceRef = %+v, want %+v", ref, want)
	}
}

func TestMappingRuleMarshalDropsLegacyExpression(t *testing.T) {
	legacy := `{"TargetPath":"order","Expression":"source.result.order"}`
	var rule MappingRule
	if err := json.Unmarshal([]byte(legacy), &rule); err != nil {
		t.Fatalf("unmarshal legacy: %v", err)
	}
	data, err := json.Marshal(rule)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if contains := stringContains(string(data), "Expression") || stringContains(string(data), "expression"); contains {
		t.Errorf("marshal still serializes the legacy expression: %s", data)
	}
}

func stringContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
