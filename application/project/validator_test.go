package project

import (
	"strings"
	"testing"
)

// TestValidateAssemblyBasicAllowsAbsentIdentity pins the P2-6 resolution:
// neuron.config.* owns the Assembly identity, so a YAML assembly without
// metadata.name / metadata.version validates. manifest.ApplyIdentity fills
// the values from configuration at the shared build seam, or fails there
// with a configuration-named error.
func TestValidateAssemblyBasicAllowsAbsentIdentity(t *testing.T) {
	assembly := AssemblyFile{
		APIVersion: "neuron/v1",
		Kind:       "Assembly",
		Capabilities: []CapabilityReference{
			{Ref: "greet", Entry: "capabilities/greet.yaml"},
		},
	}

	if err := validateAssemblyBasic(assembly); err != nil {
		t.Fatalf("validateAssemblyBasic: %v", err)
	}
}

// TestValidateAssemblyBasicAllowsPartialIdentity verifies that a YAML
// assembly may author only part of the identity (or none). The config
// conflict detection lives in manifest.ApplyIdentity, not here.
func TestValidateAssemblyBasicAllowsPartialIdentity(t *testing.T) {
	assembly := AssemblyFile{
		APIVersion: "neuron/v1",
		Kind:       "Assembly",
		Metadata: AssemblyMetadata{
			Version: "1.0.0",
		},
		Capabilities: []CapabilityReference{
			{Ref: "greet", Entry: "capabilities/greet.yaml"},
		},
	}

	if err := validateAssemblyBasic(assembly); err != nil {
		t.Fatalf("validateAssemblyBasic: %v", err)
	}
}

// TestValidateAssemblyBasicStillRequiresStructuralFields pins that removing
// the identity checks did not weaken the structural validation: apiVersion,
// kind, and at least one capability remain required.
func TestValidateAssemblyBasicStillRequiresStructuralFields(t *testing.T) {
	valid := AssemblyFile{
		APIVersion: "neuron/v1",
		Kind:       "Assembly",
		Capabilities: []CapabilityReference{
			{Ref: "greet", Entry: "capabilities/greet.yaml"},
		},
	}

	tests := []struct {
		name string
		body func(a AssemblyFile) AssemblyFile
		want string
	}{
		{"missing apiVersion", func(a AssemblyFile) AssemblyFile { a.APIVersion = ""; return a }, "apiVersion is required"},
		{"wrong kind", func(a AssemblyFile) AssemblyFile { a.Kind = "Service"; return a }, `kind must be Assembly`},
		{"no capabilities", func(a AssemblyFile) AssemblyFile { a.Capabilities = nil; return a }, "assemblies.capabilities must contain at least one capability"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAssemblyBasic(tt.body(valid))
			if err == nil {
				t.Fatal("expected validation to fail")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to contain %q", err, tt.want)
			}
		})
	}
}

// TestValidateCapabilityBasicStillRequiresIdentity pins that capability
// metadata stays required at this boundary: this change relaxes only the
// Assembly identity (owned by neuron.config.*), never the per-capability
// metadata that still identifies a capability definition.
func TestValidateCapabilityBasicStillRequiresIdentity(t *testing.T) {
	capability := CapabilityFile{
		APIVersion: "neuron/v1",
		Kind:       "Capability",
		Spec: CapabilitySpec{
			CapabilityRuntime: CapabilityRuntimeSpec{
				Type: "neuron:core:set",
			},
		},
	}

	err := validateCapabilityBasic(capability)
	if err == nil {
		t.Fatal("expected capability validation to fail without metadata")
	}
	for _, want := range []string{"metadata.name is required", "metadata.version is required"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}
