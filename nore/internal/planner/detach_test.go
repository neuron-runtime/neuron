package planner

import (
	"testing"

	"github.com/neuron-runtime/neuron/nore/internal/types"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

func compileAssembly(t *testing.T, assembly shared.Assembly) *types.ExecutionBlueprint {
	t.Helper()
	compiler, err := NewCompiler(stubExpressionCompiler{})
	if err != nil {
		t.Fatalf("new compiler: %v", err)
	}
	blueprint, err := compiler.Compile(assembly)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return blueprint
}

// chainAssembly builds `first -> second -> third` with the detached mode applied
// to every capability named in detached.
func chainAssembly(detached ...shared.ID) shared.Assembly {
	isDetached := make(map[shared.ID]bool, len(detached))
	for _, id := range detached {
		isDetached[id] = true
	}
	capability := func(id shared.ID) shared.Capability {
		var runtimeConfig *shared.RuntimeConfig
		if isDetached[id] {
			runtimeConfig = &shared.RuntimeConfig{
				Execution: &shared.RuntimeExecution{Mode: shared.RuntimeExecutionModeDetach},
			}
		}
		return shared.Capability{
			Metadata:      shared.Metadata{ID: id, Name: string(id), Version: "1.0.0"},
			Type:          "neuron:core:set",
			RuntimeConfig: runtimeConfig,
		}
	}
	return shared.Assembly{
		Metadata: shared.Metadata{Name: "detach", Version: "1.0.0"},
		Specification: shared.AssemblySpec{
			Capabilities: []shared.Capability{capability("first"), capability("second"), capability("third")},
			Bindings: []shared.Binding{
				{
					Metadata: shared.Metadata{ID: "first-second"},
					From:     shared.Endpoint{CapabilityID: "first"},
					To:       shared.Endpoint{CapabilityID: "second"},
				},
				{
					Metadata: shared.Metadata{ID: "second-third"},
					From:     shared.Endpoint{CapabilityID: "second"},
					To:       shared.Endpoint{CapabilityID: "third"},
				},
			},
		},
	}
}

func TestDetachCompilesScopeForDetachedCapability(t *testing.T) {
	blueprint := compileAssembly(t, chainAssembly("second"))

	scope, ok := blueprint.Detached[shared.ID("second")]
	if !ok {
		t.Fatalf("no detached scope compiled for `second`: %+v", blueprint.Detached)
	}
	if len(scope.EntryCapabilityIDs) != 1 || scope.EntryCapabilityIDs[0] != "second" {
		t.Fatalf("scope entry = %v, want [second]", scope.EntryCapabilityIDs)
	}
	if _, exists := scope.Nodes["second"]; !exists {
		t.Fatal("the scope must contain the detached capability itself")
	}
	if _, exists := scope.Nodes["third"]; !exists {
		t.Fatal("the scope must contain everything downstream of the detached capability")
	}
	if _, exists := scope.Nodes["first"]; exists {
		t.Fatal("the scope must not contain capabilities upstream of the detached capability")
	}
	if scope.Detached != nil {
		t.Fatalf("a single detached capability has no nested scopes, got %+v", scope.Detached)
	}
	// The detached capability must actually run inside its scope; if its own
	// mode were left as detach the task would detach itself forever.
	if mode := scope.Nodes["second"].Capability.RuntimeConfig.Execution.Mode; mode != shared.RuntimeExecutionModeWait {
		t.Fatalf("detached capability mode inside its scope = %q, want wait", mode)
	}
}

func TestNestedDetachCompilesNestedScopes(t *testing.T) {
	// first -> second(detach) -> third(detach): third becomes its own scope
	// inside second's scope.
	blueprint := compileAssembly(t, chainAssembly("second", "third"))

	second := blueprint.Detached["second"]
	if second == nil {
		t.Fatal("no scope compiled for `second`")
	}
	if _, exists := second.Nodes["third"]; !exists {
		t.Fatal("`third` must still belong to `second`'s reachable nodes")
	}
	third, ok := second.Detached["third"]
	if !ok {
		t.Fatalf("`third` must own a nested scope inside `second`, got %+v", second.Detached)
	}
	if len(third.EntryCapabilityIDs) != 1 || third.EntryCapabilityIDs[0] != "third" {
		t.Fatalf("nested scope entry = %v, want [third]", third.EntryCapabilityIDs)
	}
	if _, exists := third.Nodes["second"]; exists {
		t.Fatal("a nested scope must not include its enclosing capability")
	}
	if mode := blueprint.Nodes["second"].Capability.RuntimeConfig.Execution.Mode; mode != shared.RuntimeExecutionModeDetach {
		t.Fatalf("the enclosing plan must keep `second` detached, got %q", mode)
	}
	if mode := second.Nodes["third"].Capability.RuntimeConfig.Execution.Mode; mode != shared.RuntimeExecutionModeDetach {
		t.Fatalf("`third` must stay detached inside `second`'s scope, got %q", mode)
	}
	if mode := third.Nodes["third"].Capability.RuntimeConfig.Execution.Mode; mode != shared.RuntimeExecutionModeWait {
		t.Fatalf("nested scope entry mode = %q, want wait", mode)
	}
}

func TestNoDetachLeavesScopesEmpty(t *testing.T) {
	blueprint := compileAssembly(t, chainAssembly())
	if len(blueprint.Detached) != 0 {
		t.Fatalf("expected no detached scopes, got %+v", blueprint.Detached)
	}
}

func TestAssertSingleOwnerPerScopeRejectsTwoOwners(t *testing.T) {
	// The planner rejects fan-in today, so a scope can only be reached from one
	// enclosing boundary. This asserts the guard directly, because it is the
	// thing that would catch a future relaxation of that rule.
	roots := []shared.ID{"left", "right"}
	reachable := map[shared.ID]map[shared.ID]struct{}{
		"left":  {"left": {}, "shared": {}},
		"right": {"right": {}, "shared": {}},
	}
	// `shared` is reachable from both boundaries, but only detached roots count
	// as owners; make it detached so the guard has two candidates.
	roots = append(roots, "shared")
	if err := assertSingleOwnerPerScope(roots, reachable); err == nil {
		t.Fatal("a scope reachable from two detached roots must be rejected")
	}
}

func TestAssertSingleOwnerPerScopeAcceptsNesting(t *testing.T) {
	// A nested detached capability is reachable from its enclosing root; that
	// overlap is legitimate and must not be mistaken for two owners.
	roots := []shared.ID{"outer", "inner"}
	reachable := map[shared.ID]map[shared.ID]struct{}{
		"outer": {"outer": {}, "inner": {}},
		"inner": {"inner": {}},
	}
	if err := assertSingleOwnerPerScope(roots, reachable); err != nil {
		t.Fatalf("nesting must be allowed, got %v", err)
	}
}

func TestInvalidRuntimeConfigFailsCompilation(t *testing.T) {
	// The compiler is the last line of defence: a declaration that reaches a
	// plan without passing authoring validation must still be rejected.
	assembly := assemblyWith(&shared.RuntimeConfig{
		Execution: &shared.RuntimeExecution{Timeout: "not-a-duration"},
	})
	compiler, err := NewCompiler(stubExpressionCompiler{})
	if err != nil {
		t.Fatalf("new compiler: %v", err)
	}
	if _, err := compiler.Compile(assembly); err == nil {
		t.Fatal("compiling a capability with an unparsable timeout must fail")
	}
}

func TestUnknownRetryPolicyFailsCompilation(t *testing.T) {
	assembly := assemblyWith(&shared.RuntimeConfig{
		Retry: &shared.RuntimeRetry{Policy: "sometimes", MaxAttempts: 2},
	})
	compiler, err := NewCompiler(stubExpressionCompiler{})
	if err != nil {
		t.Fatalf("new compiler: %v", err)
	}
	if _, err := compiler.Compile(assembly); err == nil {
		t.Fatal("compiling a capability with an unknown retry policy must fail")
	}
}
