package registry

import (
	"testing"

	"github.com/neuron-runtime/neuron/shared/types/builtins"
	"github.com/neuron-runtime/neuron/shared/types/core"
)

func TestRegisterCoreRuntimesRegistersTheCanonicalAndLegacyNames(t *testing.T) {
	reg := New()
	if err := reg.RegisterCoreRuntimes(builtins.Policy{}); err != nil {
		t.Fatalf("RegisterCoreRuntimes: %v", err)
	}

	for _, name := range core.BuiltinTypeNames() {
		canonical := core.CoreName(name)
		if _, err := reg.Resolve(canonical); err != nil {
			t.Errorf("resolve %q: %v", canonical, err)
		}
		if _, err := reg.Resolve(core.CapabilityRuntimeType(name)); err != nil {
			t.Errorf("resolve %q: %v", name, err)
		}
	}
}

// A policy that reaches the runtime from outside must not be able to widen
// what a capability may reach: the registry resolves it against the ceilings
// compiled into the runtime.
func TestRegisterCoreRuntimesRefusesAPolicyAboveTheCeiling(t *testing.T) {
	reg := New()
	err := reg.RegisterCoreRuntimes(builtins.Policy{MaxCommandOutputBytes: 1 << 40})
	if err == nil {
		t.Fatal("RegisterCoreRuntimes accepted a limit above the compiled-in ceiling")
	}
}

// A runtime registered under two names is one runtime, and it is closed once.
func TestCloseClosesARuntimeRegisteredUnderTwoNamesOnce(t *testing.T) {
	reg := New()
	if err := reg.RegisterCoreRuntimes(builtins.Policy{}); err != nil {
		t.Fatalf("RegisterCoreRuntimes: %v", err)
	}
	if err := reg.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
