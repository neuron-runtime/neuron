package core

import "testing"

func TestCoreName(t *testing.T) {
	cases := map[string]CapabilityRuntimeType{
		"set":        "neuron:core:set",
		"command":    "neuron:core:command",
		"filesystem": "neuron:core:filesystem",
	}
	for in, want := range cases {
		if got := CoreName(in); got != want {
			t.Errorf("CoreName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsCoreRuntimeType(t *testing.T) {
	core := []CapabilityRuntimeType{
		"neuron:core:set",
		"neuron:core:http",
		"neuron:core:command",
		"neuron:core:anything-new",
		"set",
		"command",
		"http",
	}
	for _, tt := range core {
		if !IsCoreRuntimeType(tt) {
			t.Errorf("IsCoreRuntimeType(%q) = false, want true", tt)
		}
	}

	notCore := []CapabilityRuntimeType{
		"neuron:set",
		"neuron:http",
		"github:read",
		"neuron-runtime:github:read",
		"example:echo",
		"",
		"setx",
		"core",
		"neuron:core",
	}
	for _, tt := range notCore {
		if IsCoreRuntimeType(tt) {
			t.Errorf("IsCoreRuntimeType(%q) = true, want false", tt)
		}
	}
}

// A bare name of a retired built-in is not a core runtime type any more. It
// was never an external package either, so a reference to it is reported as
// a mistake in the assembly rather than sent to the package resolver.
func TestIsCoreRuntimeTypeRejectsRetiredBareNames(t *testing.T) {
	for _, name := range []CapabilityRuntimeType{"ai", "delay", "log"} {
		if IsCoreRuntimeType(name) {
			t.Errorf("IsCoreRuntimeType(%q) = true, want false", name)
		}
	}
}

func TestCanonicalCoreType(t *testing.T) {
	cases := map[CapabilityRuntimeType]CapabilityRuntimeType{
		"set":             "neuron:core:set",
		"command":         "neuron:core:command",
		"neuron:core:set": "neuron:core:set",
	}
	for in, want := range cases {
		got := CanonicalCoreType(in)
		if got != want {
			t.Errorf("CanonicalCoreType(%q) = %q, want %q", in, got, want)
		}
		if !IsCoreRuntimeType(got) {
			t.Errorf("CanonicalCoreType(%q) = %q is not a core runtime type", in, got)
		}
	}
}
