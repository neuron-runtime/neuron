package manifest

import "testing"

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

	Canonicalize(m)

	c0 := m.Bindings[0]
	if c0.Mappings[1].Target != "validation_data" {
		t.Errorf("target = %q, want validation_data", c0.Mappings[1].Target)
	}
	c1 := m.Bindings[1]
	if c1.Mappings[0].Target != "amount_cents" {
		t.Errorf("target = %q, want amount_cents", c1.Mappings[0].Target)
	}
	if c1.Mappings[0].Expression != "source.result.amount_cents" {
		t.Errorf("expression = %q, want source.result.amount_cents", c1.Mappings[0].Expression)
	}
	if c1.Validations[0].Expression != "source.result.amount_cents >= 1000" {
		t.Errorf("validation = %q, want source.result.amount_cents >= 1000", c1.Validations[0].Expression)
	}

	// Quoted literals and operators survive untouched.
	c2 := m.Bindings[2]
	if c2.Mappings[0].Target != "shipping_address" {
		t.Errorf("target = %q, want shipping_address", c2.Mappings[0].Target)
	}
	if c2.Mappings[0].Expression != "execution.params.order.shipping_address" {
		t.Errorf("expression = %q, want execution.params.order.shipping_address", c2.Mappings[0].Expression)
	}
	if c2.Validations[0].Expression != "source.result.code == 'OK'" {
		t.Errorf("validation = %q, want source.result.code == 'OK'", c2.Validations[0].Expression)
	}

	// Idempotent on already-canonical manifests.
	before := *m
	Canonicalize(m)
	if m.Bindings[1].Mappings[0].Expression != before.Bindings[1].Mappings[0].Expression {
		t.Errorf("canonicalize is not idempotent")
	}
}

func TestCanonicalizeNil(t *testing.T) {
	if Canonicalize(nil) != nil {
		t.Errorf("Canonicalize(nil) should return nil")
	}
}
