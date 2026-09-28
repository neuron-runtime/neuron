package build

import (
	"testing"

	"github.com/Muhammad-Jay/neuron/application/compiler/manifest"
	"github.com/Muhammad-Jay/neuron/application/config"
)

func testConfig() config.Config {
	return config.Config{
		CapabilityRuntimes: config.CapabilityRuntimesConfig{
			DefaultRegistries: []string{"local"},
			Registries: []config.CapabilityRuntimeRegistry{
				{Name: "local", URL: "./capabilityRuntimes"},
			},
		},
		Runtime: config.RuntimeConfig{
			Execution: config.ExecutionConfig{Mode: "wait", Timeout: "30m"},
			Workers:   config.WorkerConfig{Min: 1, Max: 4},
		},
		Inspector: config.InspectorConfig{Enabled: true, Address: "127.0.0.1:7433"},
	}
}

func testM() *manifest.Assembly {
	return &manifest.Assembly{
		Capabilities: []manifest.Capability{
			{
				Name: "hello",
				CapabilityRuntime: manifest.CapabilityRuntimeSpec{
					Name:     "example:echo",
					Version:  "^1.0.0",
					Registry: "local",
				},
			},
		},
	}
}

func TestBuildExecutionConfigurations(t *testing.T) {
	ec := buildExecutionConfigurations(testConfig(), testM())

	if len(ec.CapabilityRuntimeRegistries) != 1 || ec.CapabilityRuntimeRegistries[0].Name != "local" {
		t.Fatalf("registries = %#v, want the configured local registry", ec.CapabilityRuntimeRegistries)
	}
	if ec.Runtime.Execution.Mode != "wait" || ec.Runtime.Execution.Timeout != "30m" {
		t.Errorf("runtime = %#v, want mode/execution from config", ec.Runtime.Execution)
	}
	if ec.Runtime.Workers.Max != 4 {
		t.Errorf("workers = %#v, want config max 4", ec.Runtime.Workers)
	}
	if !ec.Inspector.Enabled {
		t.Errorf("inspector = %#v, want enabled from config", ec.Inspector)
	}
	if len(ec.CapabilityRuntimeRequirements) != 1 {
		t.Fatalf("requirements = %d, want 1 indexed from manifest capabilities", len(ec.CapabilityRuntimeRequirements))
	}
	if req := ec.CapabilityRuntimeRequirements[0]; req.Name != "example:echo" || req.Capabilities[0] != "hello" {
		t.Errorf("requirement = %#v, want example:echo for hello", req)
	}
}
