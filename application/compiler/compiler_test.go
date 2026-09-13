package compiler

import (
	"testing"

	"github.com/Muhammad-Jay/neuron/application/compiler/manifest"
	"github.com/Muhammad-Jay/neuron/shared/types/core"
)

func testManifest() *manifest.System {
	return &manifest.System{
		APIVersion: "neuron/v1",
		Kind:       "System",
		Metadata: manifest.Metadata{
			Name:        "order-processing",
			Version:     "1.0.0",
			Description: "Order processing pipeline",
		},
		Services: []manifest.Service{
			{
				Name: "validate",
				Executor: manifest.ExecutorSpec{
					Name:     "set",
					Version:  "latest",
					Registry: "local",
				},
				Inputs:  []manifest.Port{{Name: "input", Type: "object", Required: true}},
				Outputs: []manifest.Port{{Name: "output", Type: "object", Required: true}},
				Config:  map[string]any{"foo": "bar"},
			},
			{
				Name: "process",
				Executor: manifest.ExecutorSpec{
					Name:     "set",
					Version:  "latest",
					Registry: "local",
				},
				Inputs:  []manifest.Port{{Name: "data", Type: "any", Required: true}},
				Outputs: []manifest.Port{{Name: "result", Type: "object", Required: true}},
			},
		},
		Connectors: []manifest.Connector{
			{
				From: "validate",
				To:   "process",
				Mappings: []manifest.ConnectorMapping{
					{Target: "data", Expression: "source.output"},
				},
			},
		},
		Definition: manifest.SystemNode{
			Kind: "sequence",
			Steps: []manifest.SystemNode{
				{Kind: "service", Service: "validate"},
				{Kind: "service", Service: "process"},
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
	if len(sys.Specification.Services) != 2 {
		t.Fatalf("services = %d, want 2", len(sys.Specification.Services))
	}
	if len(sys.Specification.Connectors) != 1 {
		t.Fatalf("connectors = %d, want 1", len(sys.Specification.Connectors))
	}

	svc := sys.Specification.Services[0]
	if svc.Metadata.ID != core.ID("validate") {
		t.Errorf("service ID = %q, want %q", svc.Metadata.ID, "validate")
	}
	if svc.Type != core.ExecutorType("set") {
		t.Errorf("service type = %q, want %q", svc.Type, "set")
	}
	if svc.ServiceConfigurations["foo"] != "bar" {
		t.Errorf("service config foo = %v, want bar", svc.ServiceConfigurations["foo"])
	}

	conn := sys.Specification.Connectors[0]
	if conn.From.ServiceID != core.ID("validate") {
		t.Errorf("connector from = %q, want validate", conn.From.ServiceID)
	}
	if conn.To.ServiceID != core.ID("process") {
		t.Errorf("connector to = %q, want process", conn.To.ServiceID)
	}
	if len(conn.Mappings) != 1 || conn.Mappings[0].Expression != "source.output" {
		t.Errorf("connector mappings = %#v", conn.Mappings)
	}
}

func TestCompileMissingService(t *testing.T) {
	m := testManifest()
	m.Connectors = []manifest.Connector{
		{From: "missing", To: "process"},
	}

	c := New()
	if _, err := c.Compile(m); err == nil {
		t.Fatal("expected error for missing from service")
	}
}

func TestInstanceKey(t *testing.T) {
	c := New()
	key, err := c.InstanceKey(testManifest(), "wait")
	if err != nil {
		t.Fatalf("instance key: %v", err)
	}

	if key.SystemID != "order-processing" {
		t.Errorf("system id = %q", key.SystemID)
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

func TestExecutorRequirements(t *testing.T) {
	reqs := ExecutorRequirements(testManifest().Services)

	// Two services share the same executor -> one requirement with both services.
	if len(reqs) != 1 {
		t.Fatalf("requirements = %d, want 1", len(reqs))
	}
	req := reqs[0]
	if len(req.Services) != 2 {
		t.Errorf("requirement services = %d, want 2", len(req.Services))
	}
}
