package compiler

import (
	"testing"

	"github.com/neuron-runtime/neuron/shared/types/core"
)

func TestCompileCarriesJoinPolicy(t *testing.T) {
	m := testManifest()
	m.Bindings[0].JoinPolicy = core.JoinPolicyAll

	sys, err := New().Compile(m)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if got := sys.Specification.Bindings[0].JoinPolicy; got != core.JoinPolicyAll {
		t.Fatalf("binding join policy = %q, want %q", got, core.JoinPolicyAll)
	}
}

func TestCompileRejectsUnsupportedJoinPolicy(t *testing.T) {
	m := testManifest()
	m.Bindings[0].JoinPolicy = core.JoinPolicy("any")

	if _, err := New().Compile(m); err == nil {
		t.Fatal("an unsupported join policy must be rejected at build time")
	}
}
