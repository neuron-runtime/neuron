package compiler

import (
	"testing"

	"github.com/neuron-runtime/neuron/application/compiler/manifest"
	"github.com/neuron-runtime/neuron/shared/types/core"
)

func testManifest() *manifest.Assembly {
	return &manifest.Assembly{
		APIVersion: "neuron/v1",
		Kind:       "Assembly",
		Metadata: manifest.Metadata{
			Name:        "order-processing",
			Version:     "1.0.0",
			Description: "Order processing pipeline",
		},
		Capabilities: []manifest.Capability{
			{
				Name: "validate",
				CapabilityRuntime: manifest.CapabilityRuntimeSpec{
					Name:     "set",
					Version:  "latest",
					Registry: "local",
				},
				Params:  []manifest.Port{{Name: "input", Type: "object", Required: true}},
				Results: []manifest.Port{{Name: "output", Type: "object", Required: true}},
				Config:  map[string]any{"foo": "bar"},
			},
			{
				Name: "process",
				CapabilityRuntime: manifest.CapabilityRuntimeSpec{
					Name:     "set",
					Version:  "latest",
					Registry: "local",
				},
				Params:  []manifest.Port{{Name: "data", Type: "any", Required: true}},
				Results: []manifest.Port{{Name: "result", Type: "object", Required: true}},
			},
		},
		Bindings: []manifest.Binding{
			{
				From: "validate",
				To:   "process",
				Mappings: []manifest.BindingMapping{
					{Target: "data", Expression: "source.output"},
				},
			},
		},
	}
}

func TestCompileBasic(t *testing.T) {
	c := New()
	sys, err := c.Compile(testManifest())
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	if sys.Metadata.Name != "order-processing" {
		t.Errorf("name = %q, want %q", sys.Metadata.Name, "order-processing")
	}
	if len(sys.Specification.Capabilities) != 2 {
		t.Fatalf("capabilities = %d, want 2", len(sys.Specification.Capabilities))
	}
	if len(sys.Specification.Bindings) != 1 {
		t.Fatalf("bindings = %d, want 1", len(sys.Specification.Bindings))
	}

	svc := sys.Specification.Capabilities[0]
	if svc.Metadata.ID != core.ID("validate") {
		t.Errorf("capability ID = %q, want %q", svc.Metadata.ID, "validate")
	}
	if svc.Type != core.CapabilityRuntimeType("set") {
		t.Errorf("capability type = %q, want %q", svc.Type, "set")
	}
	if svc.CapabilityConfigurations["foo"] != "bar" {
		t.Errorf("capability config foo = %v, want bar", svc.CapabilityConfigurations["foo"])
	}

	conn := sys.Specification.Bindings[0]
	if conn.From.CapabilityID != core.ID("validate") {
		t.Errorf("binding from = %q, want validate", conn.From.CapabilityID)
	}
	if conn.To.CapabilityID != core.ID("process") {
		t.Errorf("binding to = %q, want process", conn.To.CapabilityID)
	}
	if len(conn.Mappings) != 1 || conn.Mappings[0].Expression != "source.output" {
		t.Errorf("binding mappings = %#v", conn.Mappings)
	}
}

func TestCompileMissingCapability(t *testing.T) {
	m := testManifest()
	m.Bindings = []manifest.Binding{
		{From: "missing", To: "process"},
	}

	c := New()
	if _, err := c.Compile(m); err == nil {
		t.Fatal("expected error for missing from capability")
	}
}

func TestInstanceKey(t *testing.T) {
	c := New()
	key, err := c.InstanceKey(testManifest(), "wait")
	if err != nil {
		t.Fatalf("instance key: %v", err)
	}

	if key.AssemblyID != "order-processing" {
		t.Errorf("assembly id = %q", key.AssemblyID)
	}
	if key.Version != "1.0.0" {
		t.Errorf("version = %q", key.Version)
	}
	if key.Hash == "" {
		t.Error("hash is empty")
	}
	if key.Env != "wait" {
		t.Errorf("env = %q, want wait", key.Env)
	}
}

func TestInstanceKeyDefaultEnv(t *testing.T) {
	c := New()
	key, err := c.InstanceKey(testManifest(), "")
	if err != nil {
		t.Fatalf("instance key: %v", err)
	}
	if key.Env != "development" {
		t.Errorf("env = %q, want development default", key.Env)
	}
}

func TestCapabilityRuntimeRequirements(t *testing.T) {
	reqs := CapabilityRuntimeRequirements(testManifest().Capabilities)

	// Two capabilities share the same capability runtime -> one requirement with both capabilities.
	if len(reqs) != 1 {
		t.Fatalf("requirements = %d, want 1", len(reqs))
	}
	req := reqs[0]
	if len(req.Capabilities) != 2 {
		t.Errorf("requirement capabilities = %d, want 2", len(req.Capabilities))
	}
}
