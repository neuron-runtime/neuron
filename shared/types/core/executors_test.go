package core

import "testing"

func TestCoreName(t *testing.T) {
	cases := map[string]ExecutorType{
		"set":     "neuron:core:set",
		"ai":      "neuron:core:ai",
		"command": "neuron:core:command",
	}
	for in, want := range cases {
		if got := CoreName(in); got != want {
			t.Errorf("CoreName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsCoreExecutorType(t *testing.T) {
	core := []ExecutorType{
		"neuron:core:set",
		"neuron:core:ai",
		"neuron:core:log",
		"neuron:core:command",
		"neuron:core:http",
		"neuron:core:delay",
		"neuron:core:anything-new",
		"set",
		"ai",
		"command",
	}
	for _, tt := range core {
		if !IsCoreExecutorType(tt) {
			t.Errorf("IsCoreExecutorType(%q) = false, want true", tt)
		}
	}

	notCore := []ExecutorType{
		"neuron:set",
		"neuron:ai",
		"github:read",
		"Muhammad-Jay:github:read",
		"example:echo",
		"",
		"setx",
		"core",
		"neuron:core",
	}
	for _, tt := range notCore {
		if IsCoreExecutorType(tt) {
			t.Errorf("IsCoreExecutorType(%q) = true, want false", tt)
		}
	}
}