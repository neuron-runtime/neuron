package planner

import (
	"testing"

	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

func TestCompileBuildsPredecessorsAndJoinPolicy(t *testing.T) {
	assembly := planningAssembly(nil)
	assembly.Specification.Bindings[0].Metadata.ID = "a-b"
	assembly.Specification.Bindings[0].JoinPolicy = shared.JoinPolicyAll

	blueprint := compileAssembly(t, assembly)

	a := blueprint.Nodes["a"]
	if a.JoinPolicy != shared.JoinPolicyAll {
		t.Fatalf("entry join policy = %q, want %q", a.JoinPolicy, shared.JoinPolicyAll)
	}
	if len(a.Predecessors) != 0 {
		t.Fatalf("entry predecessors = %+v, want none", a.Predecessors)
	}

	b := blueprint.Nodes["b"]
	if b.JoinPolicy != shared.JoinPolicyAll {
		t.Fatalf("target join policy = %q, want %q", b.JoinPolicy, shared.JoinPolicyAll)
	}
	if len(b.Predecessors) != 1 {
		t.Fatalf("target predecessors = %+v, want one", b.Predecessors)
	}
	predecessor := b.Predecessors[0]
	if predecessor.BindingID != "a-b" || predecessor.CapabilityID != "a" {
		t.Fatalf("predecessor = %+v, want binding a-b from a", predecessor)
	}
}

func TestCompileDefaultsJoinPolicyToAll(t *testing.T) {
	blueprint := compileAssembly(t, planningAssembly(nil))

	if got := blueprint.Nodes["b"].JoinPolicy; got != shared.JoinPolicyAll {
		t.Fatalf("default join policy = %q, want %q", got, shared.JoinPolicyAll)
	}
}

func TestCompileRejectsUnsupportedJoinPolicy(t *testing.T) {
	assembly := planningAssembly(nil)
	assembly.Specification.Bindings[0].JoinPolicy = shared.JoinPolicy("any")

	compiler, err := NewCompiler(stubExpressionCompiler{})
	if err != nil {
		t.Fatalf("new compiler: %v", err)
	}
	if _, err := compiler.Compile(assembly); err == nil {
		t.Fatal("an unsupported join policy must be rejected at plan time")
	}
}
