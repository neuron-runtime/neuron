package resolver

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func resolveConfig(t *testing.T, config map[string]any, environment CapabilityEnvironment) map[string]any {
	t.Helper()
	compiler := testCompiler(t)
	program, err := compiler.CompileCapabilityConfigurations(config)
	if err != nil {
		t.Fatalf("CompileCapabilityConfigurations: %v", err)
	}
	resolved, err := program.Resolve(context.Background(), environment)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return resolved
}

func TestConfigurationLiteralsArePassedThrough(t *testing.T) {
	environment := CapabilityEnvironment{Params: map[string]any{"order": map[string]any{"total": 2250}}}

	t.Run("string without delimiters is a literal", func(t *testing.T) {
		got := resolveConfig(t, map[string]any{"endpoint": "https://api.example.com"}, environment)
		if got["endpoint"] != "https://api.example.com" {
			t.Errorf("endpoint = %#v", got["endpoint"])
		}
	})

	t.Run("non string scalars are canonicalized", func(t *testing.T) {
		// A literal 3 authored in a manifest reaches the capability as
		// float64(3), matching the representation an execution's params
		// already carry after being decoded from a request body.
		got := resolveConfig(t, map[string]any{"retries": 3, "timeout": 1.5, "enabled": true}, environment)
		if got["retries"] != float64(3) || got["timeout"] != 1.5 || got["enabled"] != true {
			t.Errorf("scalars = %#v", got)
		}
	})

	t.Run("null is preserved", func(t *testing.T) {
		got := resolveConfig(t, map[string]any{"trace": nil}, environment)
		value, present := got["trace"]
		if !present || value != nil {
			t.Errorf("trace = %#v (present=%v), want an explicit null", value, present)
		}
	})
}

// An exact expression -- a string that is entirely one `{{ ... }}` -- must
// return the resolved value itself, not a stringified form. Its numbers are
// canonicalized, but a number must not become a string and an object must not
// become text.
func TestConfigurationExactExpressionPreservesShape(t *testing.T) {
	environment := CapabilityEnvironment{Params: map[string]any{
		"order": map[string]any{"total": 2250, "items": []any{map[string]any{"sku": "A"}}},
	}}

	t.Run("number", func(t *testing.T) {
		got := resolveConfig(t, map[string]any{"amount": "{{ params.order.total }}"}, environment)
		if got["amount"] != float64(2250) {
			t.Errorf("amount = %#v (%T), want float64(2250)", got["amount"], got["amount"])
		}
		if _, isString := got["amount"].(string); isString {
			t.Error("an exact expression must not stringify a number")
		}
	})

	t.Run("object", func(t *testing.T) {
		got := resolveConfig(t, map[string]any{"order": "{{ params.order }}"}, environment)
		order, ok := got["order"].(map[string]any)
		if !ok {
			t.Fatalf("order = %#v (%T), want a map", got["order"], got["order"])
		}
		if order["total"] != float64(2250) {
			t.Errorf("order.total = %#v", order["total"])
		}
	})

	t.Run("array", func(t *testing.T) {
		got := resolveConfig(t, map[string]any{"items": "{{ params.order.items }}"}, environment)
		items, ok := got["items"].([]any)
		if !ok {
			t.Fatalf("items = %#v (%T), want a slice", got["items"], got["items"])
		}
		if len(items) != 1 {
			t.Errorf("len(items) = %d, want 1", len(items))
		}
	})
}

// A partially templated string interpolates into text and stringifies whatever
// the expression produced.
func TestConfigurationInterpolatedTemplateStringifies(t *testing.T) {
	environment := CapabilityEnvironment{
		Params:     map[string]any{"order": map[string]any{"total": 2250}},
		Capability: map[string]any{"name": "payment.authorize"},
	}

	got := resolveConfig(t, map[string]any{
		"message": "authorize {{ params.order.total }} for {{ capability.name }}",
		"number":  "port-{{ params.order.total }}",
		"boolean": "flag-{{ capability.name == 'payment.authorize' }}",
	}, environment)

	if got["message"] != "authorize 2250 for payment.authorize" {
		t.Errorf("message = %#v", got["message"])
	}
	if got["number"] != "port-2250" {
		t.Errorf("number = %#v", got["number"])
	}
	if got["boolean"] != "flag-true" {
		t.Errorf("boolean = %#v", got["boolean"])
	}
}

// An interpolated object must be encoded as JSON, not rendered with Go's
// default map formatting.
func TestConfigurationInterpolatedObjectUsesJSON(t *testing.T) {
	got := resolveConfig(t, map[string]any{
		"payload": "body={{ params.order }}",
	}, CapabilityEnvironment{Params: map[string]any{
		"order": map[string]any{"id": "ord_1"},
	}})

	want := `body={"id":"ord_1"}`
	if got["payload"] != want {
		t.Errorf("payload = %#v, want %#v", got["payload"], want)
	}
}

func TestConfigurationNestsRecursively(t *testing.T) {
	got := resolveConfig(t, map[string]any{
		"outer": map[string]any{
			"inner": "{{ params.token }}",
			"list":  []any{"literal", "{{ params.token }}"},
		},
	}, CapabilityEnvironment{Params: map[string]any{"token": "abc"}})

	want := map[string]any{
		"outer": map[string]any{
			"inner": "abc",
			"list":  []any{"literal", "abc"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("resolved\ngot:  %#v\nwant: %#v", got, want)
	}
}

// Resolution must not alias the caller's environment. A capability runtime
// that mutated the execution's params would corrupt the execution's own state.
//
// The environment carries canonical float64 numbers because the engine
// canonicalizes an execution's params before they reach a configuration
// template, which is what makes the resolver's output comparable to it.
func TestConfigurationResolutionDoesNotAliasEnvironment(t *testing.T) {
	environment := CapabilityEnvironment{Params: map[string]any{
		"order": map[string]any{"total": float64(2250)},
	}}
	got := resolveConfig(t, map[string]any{"order": "{{ params.order }}"}, environment)

	order := got["order"].(map[string]any)
	order["total"] = float64(9999)

	if environment.Params["order"].(map[string]any)["total"] != float64(2250) {
		t.Error("resolved configuration aliases the execution environment; the engine's own state can be mutated by a capability runtime")
	}
}

// Resolution must not alias the authored configuration either, or a second
// resolution of the same program observes the first one's writes.
func TestConfigurationResolutionIsRepeatable(t *testing.T) {
	compiler := testCompiler(t)
	program, err := compiler.CompileCapabilityConfigurations(map[string]any{
		"order": "{{ params.order }}",
	})
	if err != nil {
		t.Fatalf("CompileCapabilityConfigurations: %v", err)
	}
	environment := CapabilityEnvironment{Params: map[string]any{
		"order": map[string]any{"total": 2250},
	}}

	first, err := program.Resolve(context.Background(), environment)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	first["order"].(map[string]any)["total"] = float64(9999)

	second, err := program.Resolve(context.Background(), environment)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if second["order"].(map[string]any)["total"] != float64(2250) {
		t.Errorf("second resolution = %#v; the compiled program retained state from the first", second)
	}
}

func TestConfigurationTemplateErrors(t *testing.T) {
	environment := CapabilityEnvironment{Params: map[string]any{
		"a":       "1",
		"nullish": nil,
	}}

	cases := []struct {
		name    string
		config  map[string]any
		wantSub string
	}{
		{"unclosed expression", map[string]any{"k": "{{ params.a"}, "unclosed expression"},
		{"unmatched closing delimiter", map[string]any{"k": "params.a }}"}, "unmatched closing delimiter"},
		{"empty expression", map[string]any{"k": "{{  }}"}, "empty expression"},
		{"nested delimiters", map[string]any{"k": "{{ {{ params.a }} }}"}, "nested template delimiters"},
		{"unknown variable", map[string]any{"k": "{{ missing.a }}"}, "undeclared reference"},
		// A missing key is reported by CEL itself, before the resolver's own
		// null check can run.
		{"absent key", map[string]any{"k": "{{ params.absent }}"}, "no such key"},
		{"explicit null", map[string]any{"k": "{{ params.nullish }}"}, "resolved to null"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			compiler := testCompiler(t)
			program, err := compiler.CompileCapabilityConfigurations(testCase.config)
			if err != nil {
				if !strings.Contains(err.Error(), testCase.wantSub) {
					t.Fatalf("compile error = %v, want it to mention %q", err, testCase.wantSub)
				}
				return
			}
			if _, err := program.Resolve(context.Background(), environment); err == nil {
				t.Fatalf("expected resolution to fail with %q", testCase.wantSub)
			} else if !strings.Contains(err.Error(), testCase.wantSub) {
				t.Fatalf("resolve error = %v, want it to mention %q", err, testCase.wantSub)
			}
		})
	}
}

// Every limit in CELConfig exists to bound work an author -- or a hostile
// configuration -- can demand of the engine. Each one must be enforced at the
// right stage: structural limits at compile time, resolution limits at
// evaluation time.
func TestConfigurationLimits(t *testing.T) {
	t.Run("template size", func(t *testing.T) {
		compiler, err := NewCELCompiler(CELConfig{MaxTemplateSize: 16})
		if err != nil {
			t.Fatalf("NewCELCompiler: %v", err)
		}
		if _, err := compiler.CompileCapabilityConfigurations(map[string]any{
			"k": strings.Repeat("x", 64),
		}); err == nil {
			t.Fatal("expected the template size limit to be enforced")
		}
	})

	t.Run("expression count", func(t *testing.T) {
		compiler, err := NewCELCompiler(CELConfig{MaxTemplateExpressions: 2})
		if err != nil {
			t.Fatalf("NewCELCompiler: %v", err)
		}
		if _, err := compiler.CompileCapabilityConfigurations(map[string]any{
			"k": "{{ params.a }}{{ params.b }}{{ params.c }}",
		}); err == nil {
			t.Fatal("expected the expression count limit to be enforced")
		}
	})

	t.Run("resolved string size", func(t *testing.T) {
		compiler, err := NewCELCompiler(CELConfig{MaxResolvedStringSize: 8})
		if err != nil {
			t.Fatalf("NewCELCompiler: %v", err)
		}
		program, err := compiler.CompileCapabilityConfigurations(map[string]any{
			"k": "prefix-{{ params.value }}",
		})
		if err != nil {
			t.Fatalf("CompileCapabilityConfigurations: %v", err)
		}
		if _, err := program.Resolve(context.Background(), CapabilityEnvironment{
			Params: map[string]any{"value": strings.Repeat("y", 64)},
		}); err == nil {
			t.Fatal("expected the resolved size limit to be enforced")
		}
	})
}

// A capability configuration whose root compiles to something other than an
// object can never be delivered as a configuration map.
func TestConfigurationRootMustBeObject(t *testing.T) {
	compiler := testCompiler(t)
	program, err := compiler.CompileCapabilityConfigurations(map[string]any{"k": "v"})
	if err != nil {
		t.Fatalf("CompileCapabilityConfigurations: %v", err)
	}
	if _, err := program.Resolve(context.Background(), CapabilityEnvironment{}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
}

func TestUnsupportedConfigurationValueTypeIsRejected(t *testing.T) {
	compiler := testCompiler(t)
	if _, err := compiler.CompileCapabilityConfigurations(map[string]any{
		"k": make(chan int),
	}); err == nil {
		t.Fatal("expected an unsupported value type to be rejected at compile time")
	}
}
