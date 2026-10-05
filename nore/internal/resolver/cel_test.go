package resolver

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func testCompiler(t *testing.T) Compiler {
	t.Helper()
	compiler, err := NewCELCompiler(DefaultCELConfig())
	if err != nil {
		t.Fatalf("NewCELCompiler: %v", err)
	}
	return compiler
}

// The transition environment is the dialect every Binding expression is
// authored against. These tests pin the exact variable names, because a
// mismatch between the declared environment and the activation bindings fails
// only at evaluation time, not at compile time.
func TestTransitionEnvironmentVariables(t *testing.T) {
	compiler := testCompiler(t)
	environment := Environment{
		Source: map[string]any{
			"id":   "cap_1",
			"name": "order.validate",
			"type": "core:set",
			"params": map[string]any{
				"order": map[string]any{"total": 2250},
			},
			"result": map[string]any{"valid": true},
			"metadata": map[string]any{
				"id": "cap_1", "name": "order.validate",
			},
		},
		Execution: map[string]any{
			"id":             "exec_1",
			"correlation_id": "corr_1",
			"params": map[string]any{
				"order": map[string]any{"total": 2250},
			},
			"blueprint": map[string]any{
				"id": "bp_1", "name": "order-processing",
			},
		},
	}

	cases := []struct {
		name       string
		expression string
		want       any
	}{
		{"source result scalar", "source.result.valid", true},
		// Every number leaves the resolver as float64, the representation
		// encoding/json produces, regardless of how CEL computed it.
		{"source result nested", "source.params.order.total", float64(2250)},
		{"source identity", "source.name", "order.validate"},
		{"source metadata", "source.metadata.name", "order.validate"},
		{"execution initial params", "execution.params.order.total", float64(2250)},
		{"execution identity", "execution.id", "exec_1"},
		{"execution correlation", "execution.correlation_id", "corr_1"},
		{"blueprint identity", "execution.blueprint.name", "order-processing"},
		{
			"predicate over source result",
			"source.result.valid == true && source.params.order.total >= 1000",
			true,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			program, err := compiler.CompileTransitionExpression(testCase.expression)
			if err != nil {
				t.Fatalf("CompileTransitionExpression(%q): %v", testCase.expression, err)
			}
			got, err := program.Evaluate(context.Background(), environment)
			if err != nil {
				t.Fatalf("Evaluate(%q): %v", testCase.expression, err)
			}
			if !reflect.DeepEqual(got, testCase.want) {
				t.Errorf("Evaluate(%q) = %#v, want %#v", testCase.expression, got, testCase.want)
			}
			if program.Expression() != testCase.expression {
				t.Errorf("Expression() = %q, want %q", program.Expression(), testCase.expression)
			}
		})
	}
}

// The capability environment declares `params`, not `input`. A configuration
// template that reached for `input` compiled successfully and then failed at
// evaluation, so the declared name and the bound name are pinned here.
func TestCapabilityEnvironmentDeclaresParams(t *testing.T) {
	compiler := testCompiler(t)
	program, err := compiler.CompileCapabilityConfigurations(map[string]any{
		"resolved": "{{ params.order.total }}",
	})
	if err != nil {
		t.Fatalf("CompileCapabilityConfigurations: %v", err)
	}

	got, err := program.Resolve(context.Background(), CapabilityEnvironment{
		Params:     map[string]any{"order": map[string]any{"total": 2250}},
		Execution:  map[string]any{"id": "exec_1"},
		Capability: map[string]any{"name": "payment.authorize"},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got["resolved"] != float64(2250) {
		t.Errorf("resolved = %#v, want 2250", got["resolved"])
	}
}

// `input` must not exist in the capability environment. If a future change
// re-introduces it, the two names would silently diverge again.
func TestCapabilityEnvironmentRejectsInput(t *testing.T) {
	compiler := testCompiler(t)
	if _, err := compiler.CompileCapabilityConfigurations(map[string]any{
		"resolved": "{{ input.order.total }}",
	}); err == nil {
		t.Fatal("expected compilation to reject `input`; the capability environment exposes `params`")
	}
}

// The execution environment inside a Capability configuration template must use
// the same namnding transition environment, otherwise a template and
// a binding that both want the assembly's input disagree about how to spell it.
func TestCapabilityExecutionEnvMatchesTransitionEnv(t *testing.T) {
	compiler := testCompiler(t)
	program, err := compiler.CompileCapabilityConfigurations(map[string]any{
		"resolved": "{{ execution.params.order.total }}",
	})
	if err != nil {
		t.Fatalf("CompileCapabilityConfigurations: %v", err)
	}
	got, err := program.Resolve(context.Background(), CapabilityEnvironment{
		Execution: map[string]any{
			"params": map[string]any{"order": map[string]any{"total": 2250}},
		},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got["resolved"] != float64(2250) {
		t.Errorf("resolved = %#v, want 2250", got["resolved"])
	}
}

func TestCapabilityEnvironmentIdentity(t *testing.T) {
	compiler := testCompiler(t)
	program, err := compiler.CompileCapabilityConfigurations(map[string]any{
		"resolved": "{{ capability.name }}@{{ execution.correlation_id }}",
	})
	if err != nil {
		t.Fatalf("CompileCapabilityConfigurations: %v", err)
	}
	got, err := program.Resolve(context.Background(), CapabilityEnvironment{
		Execution:  map[string]any{"correlation_id": "corr_1"},
		Capability: map[string]any{"name": "payment.authorize"},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got["resolved"] != "payment.authorize@corr_1" {
		t.Errorf("resolved = %#v", got["resolved"])
	}
}

func TestCompileTransitionExpressionRejectsEmpty(t *testing.T) {
	compiler := testCompiler(t)
	if _, err := compiler.CompileTransitionExpression("   "); err == nil {
		t.Fatal("expected an empty expression to be rejected")
	}
}

// Transition expressions must not reach capability-configuration variables and
// vice versa. The two environments are separate contracts; sharing them would
// let a Binding expression depend on a Capability's configuration context.
func TestEnvironmentsAreIsolated(t *testing.T) {
	compiler := testCompiler(t)
	if _, err := compiler.CompileTransitionExpression("capability.name"); err == nil {
		t.Error("transition expressions must not see `capability`")
	}
	if _, err := compiler.CompileTransitionExpression("params.order"); err == nil {
		t.Error("transition expressions must not see `params`")
	}
}

func TestCompileExpressionLimits(t *testing.T) {
	t.Run("expression size", func(t *testing.T) {
		compiler, err := NewCELCompiler(CELConfig{MaxExpressionSize: 32})
		if err != nil {
			t.Fatalf("NewCELCompiler: %v", err)
		}
		_, err = compiler.CompileTransitionExpression("source.result.value == 'a string far longer than thirty two bytes'")
		if err == nil {
			t.Fatal("expected the expression size limit to be enforced")
		}
	})

	t.Run("ast nodes", func(t *testing.T) {
		compiler, err := NewCELCompiler(CELConfig{MaxASTNodes: 8})
		if err != nil {
			t.Fatalf("NewCELCompiler: %v", err)
		}
		if _, err := compiler.CompileTransitionExpression("source.result.a.b.c.d.e.f.g.h.i.j.k"); err == nil {
			t.Fatal("expected the AST node limit to be enforced")
		}
	})
}

func TestCostLimitIsEnforced(t *testing.T) {
	compiler, err := NewCELCompiler(CELConfig{CostLimit: 1})
	if err != nil {
		t.Fatalf("NewCELCompiler: %v", err)
	}
	program, err := compiler.CompileTransitionExpression("source.result.nested.deep.value.leaf")
	if err != nil {
		t.Fatalf("CompileTransitionExpression: %v", err)
	}
	if _, err := program.Evaluate(context.Background(), Environment{
		Source: map[string]any{"result": map[string]any{}},
	}); err == nil {
		t.Fatal("expected the cost limit to reject an evaluation")
	}
}

func TestEvaluateRequiresContext(t *testing.T) {
	compiler := testCompiler(t)
	program, err := compiler.CompileTransitionExpression("source.result.valid")
	if err != nil {
		t.Fatalf("CompileTransitionExpression: %v", err)
	}
	//nolint:staticcheck // deliberately passing nil to assert the guard holds
	if _, err := program.Evaluate(nil, Environment{}); err == nil { //nolint:staticcheck
		t.Fatal("expected evaluation without a context to fail")
	}
}

// A JSON null is modelled by CEL as types.NullValue, whose Value() is a
// structpb.NullValue. Passing that through turned a null field into the number
// 0 on its way to a capability runtime, silently and without error. Every null
// must leave the resolver as a real Go nil so it encodes back to JSON null.
func TestNullIsNeverSilentlyZero(t *testing.T) {
	compiler := testCompiler(t)
	environment := Environment{
		Source: map[string]any{
			"result": map[string]any{"value": nil},
		},
	}

	t.Run("direct reference", func(t *testing.T) {
		program, err := compiler.CompileTransitionExpression("source.result.value")
		if err != nil {
			t.Fatalf("CompileTransitionExpression: %v", err)
		}
		got, err := program.Evaluate(context.Background(), environment)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if got != nil {
			t.Errorf("Evaluate = %#v (%T), want nil", got, got)
		}
	})

	t.Run("null survives json encoding", func(t *testing.T) {
		program, err := compiler.CompileTransitionExpression("source.result.value")
		if err != nil {
			t.Fatalf("CompileTransitionExpression: %v", err)
		}
		got, err := program.Evaluate(context.Background(), environment)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		encoded, err := json.Marshal(map[string]any{"value": got})
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		if string(encoded) != `{"value":null}` {
			t.Errorf("encoded = %s, want {\"value\":null}", encoded)
		}
	})

	t.Run("nested null", func(t *testing.T) {
		program, err := compiler.CompileTransitionExpression("source.result")
		if err != nil {
			t.Fatalf("CompileTransitionExpression: %v", err)
		}
		got, err := program.Evaluate(context.Background(), environment)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		object, ok := got.(map[string]any)
		if !ok {
			t.Fatalf("Evaluate = %#v (%T), want a map", got, got)
		}
		if object["value"] != nil {
			t.Errorf("value = %#v (%T), want nil", object["value"], object["value"])
		}
	})

	t.Run("null is still detectable in cel", func(t *testing.T) {
		program, err := compiler.CompileTransitionExpression("source.result.value == null")
		if err != nil {
			t.Fatalf("CompileTransitionExpression: %v", err)
		}
		got, err := program.Evaluate(context.Background(), environment)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if got != true {
			t.Errorf("Evaluate = %#v, want true", got)
		}
	})
}
