package core

import (
	"strings"
	"testing"
)

func TestBuiltinDescriptorsAreTheSourceOfCoreTypes(t *testing.T) {
	descriptors := BuiltinDescriptors()
	if len(descriptors) == 0 {
		t.Fatal("BuiltinDescriptors returned no descriptors")
	}

	for _, descriptor := range descriptors {
		if descriptor.Name == "" {
			t.Error("a descriptor has no name")
		}
		if descriptor.Summary == "" {
			t.Errorf("built-in %q has no summary", descriptor.Name)
		}
		if descriptor.Description == "" {
			t.Errorf("built-in %q has no description", descriptor.Name)
		}
	}

	want := []string{"command", "filesystem", "http", "set"}
	got := BuiltinTypeNames()
	if len(got) != len(want) {
		t.Fatalf("BuiltinTypeNames() = %v, want %v", got, want)
	}
	for i, name := range want {
		if got[i] != name {
			t.Errorf("BuiltinTypeNames()[%d] = %q, want %q", i, got[i], name)
		}
	}
}

func TestCoreTypesMatchesTheDeclaredBuiltins(t *testing.T) {
	names := BuiltinTypeNames()
	if len(CoreTypes) != len(names) {
		t.Fatalf("CoreTypes = %v, want %v", CoreTypes, names)
	}
	for i, name := range names {
		if CoreTypes[i] != name {
			t.Errorf("CoreTypes[%d] = %q, want %q", i, CoreTypes[i], name)
		}
	}
}

func TestLookupBuiltinAcceptsBothForms(t *testing.T) {
	descriptor, ok := LookupBuiltin(CoreName("http"))
	if !ok {
		t.Fatal("LookupBuiltin(neuron:core:http) did not find the built-in")
	}
	if descriptor.Name != "http" {
		t.Errorf("descriptor name = %q, want %q", descriptor.Name, "http")
	}

	if _, ok := LookupBuiltin(CapabilityRuntimeType("http")); !ok {
		t.Error("LookupBuiltin(http) did not find the built-in")
	}
}

func TestLookupBuiltinRejectsUnknownNames(t *testing.T) {
	for _, name := range []string{"delay", "neuron:core:delay", "neuron:other:http", "example:http", "neuron:core:http:extra"} {
		if _, ok := LookupBuiltin(CapabilityRuntimeType(name)); ok {
			t.Errorf("LookupBuiltin(%q) found a built-in", name)
		}
	}
}

func TestIsBuiltinRuntimeType(t *testing.T) {
	for _, name := range []string{"set", "http", "command", "filesystem", "neuron:core:set", "neuron:core:filesystem"} {
		if !IsBuiltinRuntimeType(CapabilityRuntimeType(name)) {
			t.Errorf("IsBuiltinRuntimeType(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"ai", "delay", "log", "neuron:core:delay", "neuron:core:ai", "neuron:core:log", "neuron:core:storage"} {
		if IsBuiltinRuntimeType(CapabilityRuntimeType(name)) {
			t.Errorf("IsBuiltinRuntimeType(%q) = true, want false", name)
		}
	}
}

func TestUnknownBuiltinErrorNamesTheAvailableBuiltins(t *testing.T) {
	err := UnknownBuiltinError(CoreName("delay"))
	if err == nil {
		t.Fatal("UnknownBuiltinError(neuron:core:delay) returned no error")
	}
	if !strings.Contains(err.Error(), "neuron:core:delay") {
		t.Errorf("error %q does not name the unknown runtime", err)
	}
	for _, name := range BuiltinTypeNames() {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not list the available built-in %q", err, name)
		}
	}
}

func TestUnknownBuiltinErrorExplainsARemoval(t *testing.T) {
	err := UnknownBuiltinError(CoreName("log"))
	if err == nil {
		t.Fatal("UnknownBuiltinError(neuron:core:log) returned no error")
	}
	if !strings.Contains(err.Error(), "was removed") {
		t.Errorf("error %q does not report the removal", err)
	}
	if !strings.Contains(err.Error(), "instead") {
		t.Errorf("error %q does not name a replacement", err)
	}
}

func TestUnknownBuiltinErrorIgnoresExternalTypes(t *testing.T) {
	if err := UnknownBuiltinError("example:http"); err != nil {
		t.Errorf("UnknownBuiltinError(example:http) = %v, want nil", err)
	}
}

func TestIsCoreNamespaceType(t *testing.T) {
	if !IsCoreNamespaceType(CoreName("http")) {
		t.Error("IsCoreNamespaceType(neuron:core:http) = false, want true")
	}
	if IsCoreNamespaceType("example:http") {
		t.Error("IsCoreNamespaceType(example:http) = true, want false")
	}
	if IsCoreNamespaceType("http") {
		t.Error("IsCoreNamespaceType(http) = true, want false")
	}
}

func TestRemovedBuiltinsAreRecordedWithAnExplanation(t *testing.T) {
	removed := RemovedBuiltins()
	if len(removed) == 0 {
		t.Fatal("RemovedBuiltins returned no records")
	}
	for _, record := range removed {
		if record.Name == "" {
			t.Error("a removal record has no name")
		}
		if record.Reason == "" {
			t.Errorf("removal record %q has no reason", record.Name)
		}
		if record.Replacement == "" {
			t.Errorf("removal record %q has no replacement", record.Name)
		}
	}
}

func TestBuiltinDescriptorsAreCopied(t *testing.T) {
	descriptors := BuiltinDescriptors()
	descriptors[0].Name = "mutated"

	if BuiltinDescriptors()[0].Name == "mutated" {
		t.Error("BuiltinDescriptors returned a view of the declared descriptors")
	}
}
