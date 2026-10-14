package planner

import (
	"strings"
	"testing"

	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

func resultField(name string, kind shared.ValueRefKind, capability string) shared.AssemblyResultField {
	return shared.AssemblyResultField{
		Name:   name,
		Source: &shared.ValueRef{Kind: kind, Capability: capability, Path: []string{"value"}},
	}
}

func withResult(assembly shared.Assembly, fields ...shared.AssemblyResultField) shared.Assembly {
	assembly.Result = &shared.AssemblyResult{Fields: fields}
	return assembly
}

func compileErr(t *testing.T, assembly shared.Assembly) error {
	t.Helper()
	compiler, err := NewCompiler(stubExpressionCompiler{})
	if err != nil {
		t.Fatalf("new compiler: %v", err)
	}
	_, err = compiler.Compile(assembly)
	return err
}

func TestResultReferencingDetachedCapabilityFails(t *testing.T) {
	assembly := withResult(chainAssembly("second"),
		resultField("amount", shared.ValueRefCapabilityResult, "second"))

	err := compileErr(t, assembly)
	if err == nil {
		t.Fatal("a result referencing a detached capability must fail compilation")
	}
	for _, want := range []string{`"amount"`, `"second"`} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q must name %s", err.Error(), want)
		}
	}
}

func TestResultReferencingDetachedDescendantFails(t *testing.T) {
	// `third` is not itself detached, but it runs inside `second`'s detached
	// branch, so the parent never records its result.
	assembly := withResult(chainAssembly("second"),
		resultField("amount", shared.ValueRefCapabilityResult, "third"))

	err := compileErr(t, assembly)
	if err == nil {
		t.Fatal("a result referencing a descendant of a detached capability must fail compilation")
	}
	for _, want := range []string{`"third"`, `owned by "second"`} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q must mention %s", err.Error(), want)
		}
	}
}

func TestResultReferencingNestedDetachedNamesOutermostOwner(t *testing.T) {
	// first -> second(detach) -> third(detach): `third` is reachable from both
	// detached roots. The enclosing `second` is the branch that actually leaves
	// the root execution, so it is the owner reported to the author.
	assembly := withResult(chainAssembly("second", "third"),
		resultField("amount", shared.ValueRefCapabilityResult, "third"))

	err := compileErr(t, assembly)
	if err == nil {
		t.Fatal("a result referencing a nested detached descendant must fail compilation")
	}
	if !strings.Contains(err.Error(), `owned by "second"`) {
		t.Fatalf("error %q must name the outermost detached owner", err.Error())
	}
}

func TestResultReferencingDetachedParamsFails(t *testing.T) {
	// The detached branch records neither params nor results in the parent, so
	// a params reference into it is equally unresolvable.
	assembly := withResult(chainAssembly("second"),
		resultField("amount", shared.ValueRefCapabilityParams, "third"))

	if err := compileErr(t, assembly); err == nil {
		t.Fatal("a result referencing a detached descendant's params must fail compilation")
	}
}

func TestResultReferencingUpstreamOfDetachedCapabilityCompiles(t *testing.T) {
	// `first` completes in the root execution before `second` detaches; reading
	// it is legitimate.
	assembly := withResult(chainAssembly("second"),
		resultField("amount", shared.ValueRefCapabilityResult, "first"))

	if err := compileErr(t, assembly); err != nil {
		t.Fatalf("a result referencing an upstream capability must compile, got %v", err)
	}
}

func TestResultReferencingNonDetachedCapabilityCompiles(t *testing.T) {
	assembly := withResult(chainAssembly(),
		resultField("amount", shared.ValueRefCapabilityResult, "third"))

	if err := compileErr(t, assembly); err != nil {
		t.Fatalf("a result referencing a non-detached capability must compile, got %v", err)
	}
}

func TestResultDetachmentRejectsOnlyTheDetachedField(t *testing.T) {
	assembly := withResult(chainAssembly("second"),
		resultField("source_value", shared.ValueRefCapabilityResult, "first"),
		resultField("branch_value", shared.ValueRefCapabilityResult, "third"))

	err := compileErr(t, assembly)
	if err == nil {
		t.Fatal("a result with one detached field must fail compilation")
	}
	if !strings.Contains(err.Error(), `"branch_value"`) {
		t.Fatalf("error %q must name the offending field", err.Error())
	}
	if strings.Contains(err.Error(), `"source_value"`) {
		t.Fatalf("error %q must not blame the valid field", err.Error())
	}
}

func TestResultDetachmentErrorIsDeterministic(t *testing.T) {
	assembly := withResult(chainAssembly("second", "third"),
		resultField("amount", shared.ValueRefCapabilityResult, "third"))

	first := compileErr(t, assembly)
	second := compileErr(t, assembly)
	if first == nil || second == nil {
		t.Fatal("expected compilation to fail consistently")
	}
	if first.Error() != second.Error() {
		t.Fatalf("error is not deterministic:\n%v\n%v", first, second)
	}
}
