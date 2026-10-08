package manifest

import (
	"reflect"
	"testing"

	"github.com/neuron-runtime/neuron/shared/types/core"
)

func TestCanonicalize(t *testing.T) {
	// These fixtures use the dialect the engine actually binds: `source.result`
	// for the upstream capability's result and `execution.params` for the
	// Assembly's initial parameters. See
	// nore/internal/execution/scheduler/transition.go.
	m := &Assembly{
		Bindings: []Binding{
			{
				From: "validate-order", To: "parse-order",
				Mappings: []BindingMapping{
					{Target: "order", Expression: "source.result.order"},
					{Target: "validationData", Expression: "source.result"},
				},
			},
			{
				From: "authorize-payment", To: "capture-payment",
				Mappings: []BindingMapping{
					{Target: "amountCents", Expression: "source.result.amountCents"},
				},
				Validations: []BindingValidation{
					{Expression: "source.result.amountCents >= 1000", Message: "not authed"},
				},
			},
			{
				From: "capture-payment", To: "create-shipment",
				Mappings: []BindingMapping{
					{Target: "shippingAddress", Expression: "execution.params.order.shippingAddress"},
				},
				Validations: []BindingValidation{
					{Expression: "source.result.code == 'OK'", Message: "no"},
				},
			},
		},
	}

	canonical, err := Canonicalize(m)
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}

	c0 := canonical.Bindings[0]
	if c0.Mappings[1].Target != "validation_data" {
		t.Errorf("target = %q, want validation_data", c0.Mappings[1].Target)
	}
	// Legacy expressions are replaced by structured sources.
	if got := c0.Mappings[0].Source; got == nil || !reflect.DeepEqual(*got,
		core.ValueRef{Kind: core.ValueRefCapabilityResult, Capability: "validate-order", Path: []string{"order"}}) {
		t.Errorf("mapping[0].source = %+v, want capabilityResult validate-order", got)
	}
	if got := c0.Mappings[1].Source; got == nil || !reflect.DeepEqual(*got,
		core.ValueRef{Kind: core.ValueRefCapabilityResult, Capability: "validate-order"}) {
		t.Errorf("mapping[1].source = %+v, want unqualified capabilityResult", got)
	}

	c1 := canonical.Bindings[1]
	if c1.Mappings[0].Target != "amount_cents" {
		t.Errorf("target = %q, want amount_cents", c1.Mappings[0].Target)
	}
	if got := c1.Mappings[0].Source; got == nil || !reflect.DeepEqual(*got,
		core.ValueRef{Kind: core.ValueRefCapabilityResult, Capability: "authorize-payment", Path: []string{"amount_cents"}}) {
		t.Errorf("mapping[0].source = %+v, want normalized capabilityResult", got)
	}
	if c1.Mappings[0].Expression != "" {
		t.Errorf("expression = %q, want cleared after conversion", c1.Mappings[0].Expression)
	}
	if c1.Validations[0].Expression != "source.result.amount_cents >= 1000" {
		t.Errorf("validation = %q, want source.result.amount_cents >= 1000", c1.Validations[0].Expression)
	}

	// Quoted literals and operators survive untouched in validations.
	c2 := canonical.Bindings[2]
	if c2.Mappings[0].Target != "shipping_address" {
		t.Errorf("target = %q, want shipping_address", c2.Mappings[0].Target)
	}
	if got := c2.Mappings[0].Source; got == nil || !reflect.DeepEqual(*got,
		core.ValueRef{Kind: core.ValueRefAssemblyParams, Path: []string{"order", "shipping_address"}}) {
		t.Errorf("mapping[0].source = %+v, want assemblyParams", got)
	}
	if c2.Validations[0].Expression != "source.result.code == 'OK'" {
		t.Errorf("validation = %q, want source.result.code == 'OK'", c2.Validations[0].Expression)
	}

	// Idempotent on already-canonical manifests.
	before := *canonical
	again, err := Canonicalize(canonical)
	if err != nil {
		t.Fatalf("Canonicalize(second pass): %v", err)
	}
	if !reflect.DeepEqual(again.Bindings[1].Mappings[0].Source, before.Bindings[1].Mappings[0].Source) {
		t.Errorf("canonicalize is not idempotent over sources")
	}
}

func TestCanonicalizeStructuredSourceIsPreserved(t *testing.T) {
	m := &Assembly{
		Bindings: []Binding{{
			From: "validate-order", To: "create-shipment",
			Mappings: []BindingMapping{{
				Target: "order",
				Source: &core.ValueRef{
					Kind:       core.ValueRefCapabilityResult,
					Capability: "validate-order",
					Path:       []string{"order", "customerId"},
				},
			}},
		}},
	}
	canonical, err := Canonicalize(m)
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	got := canonical.Bindings[0].Mappings[0].Source
	want := core.ValueRef{Kind: core.ValueRefCapabilityResult, Capability: "validate-order", Path: []string{"order", "customer_id"}}
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("source = %+v, want %+v", *got, want)
	}
}

func TestCanonicalizeRejectsEmptyMapping(t *testing.T) {
	m := &Assembly{
		Bindings: []Binding{{
			From: "a", To: "b",
			Mappings: []BindingMapping{{Target: "order"}},
		}},
	}
	if _, err := Canonicalize(m); err == nil {
		t.Error("a mapping with neither source nor expression must be rejected")
	}
}

func TestCanonicalizeNil(t *testing.T) {
	canonical, err := Canonicalize(nil)
	if err != nil {
		t.Errorf("Canonicalize(nil) unexpectedly errored: %v", err)
	}
	if canonical != nil {
		t.Errorf("Canonicalize(nil) should return nil")
	}
}
