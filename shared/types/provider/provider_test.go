package provider

import (
	"errors"
	"testing"

	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
)

func TestDescriptorSupportsRuntimeKind(t *testing.T) {
	d := Descriptor{RuntimeKinds: []string{capabilityrt.RuntimeKindProcess}}
	if !d.SupportsRuntimeKind(capabilityrt.RuntimeKindProcess) {
		t.Error("process should be supported")
	}
	if d.SupportsRuntimeKind(capabilityrt.RuntimeKindWasm) {
		t.Error("wasm should not be supported")
	}
}

func TestDescriptorProtocolSupport(t *testing.T) {
	unconstrained := Descriptor{}
	if !unconstrained.SupportsCapabilityRuntimeProtocol(capabilityrt.ProtocolV1) {
		t.Error("a provider that declares no protocols should constrain nothing")
	}

	jsonOnly := Descriptor{CapabilityRuntimeProtocols: []string{capabilityrt.ProtocolJSONV1}}
	if !jsonOnly.SupportsCapabilityRuntimeProtocol(capabilityrt.ProtocolJSONV1) {
		t.Error("json protocol should be supported")
	}
	if jsonOnly.SupportsCapabilityRuntimeProtocol(capabilityrt.ProtocolV1) {
		t.Error("gRPC protocol should not be supported")
	}
}

func TestDescriptorPlatformSupport(t *testing.T) {
	portable := Descriptor{}
	if !portable.SupportsPlatform("anything-at-all") {
		t.Error("a provider that declares no platforms should run everywhere")
	}

	wasm := Descriptor{Platforms: []string{capabilityrt.PlatformWasm}}
	if !wasm.SupportsPlatform(capabilityrt.PlatformWasm) {
		t.Error("wasm32-wasi should be supported")
	}
	if wasm.SupportsPlatform("linux-amd64") {
		t.Error("a native platform should not be supported by a WASI provider")
	}
}

// Match derives the platform from the runtime kind when the caller does not
// supply one, so a WASI provider is checked against the portable platform rather
// than the host's.
func TestMatchDerivesPlatformFromRuntimeKind(t *testing.T) {
	wasm := Descriptor{
		Name:                       capabilityrt.RuntimeKindWasm,
		RuntimeKinds:               []string{capabilityrt.RuntimeKindWasm},
		CapabilityRuntimeProtocols: []string{capabilityrt.ProtocolJSONV1},
		Platforms:                  []string{capabilityrt.PlatformWasm},
	}
	if err := wasm.Match(capabilityrt.RuntimeKindWasm, capabilityrt.ProtocolJSONV1, ""); err != nil {
		t.Fatalf("Match = %v, want nil", err)
	}
}

func TestMatchReportsTheRequirementNotMet(t *testing.T) {
	d := Descriptor{
		Name:                       "p",
		RuntimeKinds:               []string{capabilityrt.RuntimeKindProcess},
		CapabilityRuntimeProtocols: []string{capabilityrt.ProtocolJSONV1},
		Platforms:                  []string{capabilityrt.PlatformWasm},
	}

	tests := []struct {
		name     string
		kind     string
		protocol string
		want     ErrorKind
	}{
		{"wrong kind", capabilityrt.RuntimeKindWasm, "", ErrUnsupportedRuntimeKind},
		{"wrong protocol", capabilityrt.RuntimeKindProcess, capabilityrt.ProtocolV1, ErrUnsupportedProtocol},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := d.Match(tt.kind, tt.protocol, "")
			var perr *Error
			if !errors.As(err, &perr) {
				t.Fatalf("Match = %v, want a *provider.Error", err)
			}
			if perr.Kind != tt.want {
				t.Errorf("Kind = %q, want %q", perr.Kind, tt.want)
			}
		})
	}
}

func validManifest() *Manifest {
	return &Manifest{
		APIVersion:   APIVersion,
		Kind:         Kind,
		Metadata:     ManifestMetadata{Name: "process", Version: "1.0.0"},
		RuntimeKinds: []string{capabilityrt.RuntimeKindProcess},
		Protocols:    []string{capabilityrt.ProtocolV1},
		Platforms: map[string]Platform{
			"linux-amd64": {Artifact: "bin/process"},
		},
	}
}

func TestManifestValidate(t *testing.T) {
	if err := validManifest().Validate(); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{"nil", nil},
		{"bad apiVersion", func(m *Manifest) { m.APIVersion = "v9" }},
		{"bad kind", func(m *Manifest) { m.Kind = "Other" }},
		{"missing name", func(m *Manifest) { m.Metadata.Name = "" }},
		{"missing version", func(m *Manifest) { m.Metadata.Version = "" }},
		{"no runtime kinds", func(m *Manifest) { m.RuntimeKinds = nil }},
		{"unknown runtime kind", func(m *Manifest) { m.RuntimeKinds = []string{"quantum"} }},
		{"unknown protocol", func(m *Manifest) { m.Protocols = []string{"neuron/capability-runtime-v99"} }},
		{"no platforms", func(m *Manifest) { m.Platforms = nil }},
		{"empty platform key", func(m *Manifest) { m.Platforms = map[string]Platform{" ": {}} }},
		{"incompatible provider protocol", func(m *Manifest) { m.Provider.ProtocolVersion = "neuron/provider-v99" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m *Manifest
			if tt.mutate != nil {
				m = validManifest()
				tt.mutate(m)
			}
			if err := m.Validate(); err == nil {
				t.Fatalf("want error, got nil")
			}
		})
	}
}

func TestManifestToDescriptorCarriesPlatforms(t *testing.T) {
	m := validManifest()
	m.Platforms = map[string]Platform{
		"linux-amd64":  {},
		"darwin-arm64": {},
		"linux-arm64":  {},
	}
	d := m.ToDescriptor()
	want := []string{"darwin-arm64", "linux-amd64", "linux-arm64"}
	if len(d.Platforms) != len(want) {
		t.Fatalf("platforms = %v, want %v", d.Platforms, want)
	}
	for i := range want {
		if d.Platforms[i] != want[i] {
			t.Fatalf("platforms = %v, want sorted %v", d.Platforms, want)
		}
	}
	if d.Name != "process" || d.Version != "1.0.0" {
		t.Fatalf("descriptor identity = %q@%q", d.Name, d.Version)
	}
	if d.ProtocolVersion != ProtocolVersion {
		t.Fatalf("protocol version = %q, want default %q", d.ProtocolVersion, ProtocolVersion)
	}
}

func TestCheckProviderProtocol(t *testing.T) {
	for _, version := range []string{"", ProtocolVersion} {
		d := Descriptor{Name: "p", ProtocolVersion: version}
		if err := d.CheckProviderProtocol(); err != nil {
			t.Errorf("CheckProviderProtocol(%q) = %v, want nil", version, err)
		}
	}

	d := Descriptor{Name: "p", ProtocolVersion: "neuron/provider-v99"}
	err := d.CheckProviderProtocol()
	var perr *Error
	if !errors.As(err, &perr) || perr.Kind != ErrIncompatibleProviderProtocol {
		t.Errorf("CheckProviderProtocol = %v, want ErrIncompatibleProviderProtocol", err)
	}
}
