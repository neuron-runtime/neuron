package register

import (
	"testing"

	"github.com/Muhammad-Jay/neuron/application/compiler/manifest"
	"github.com/Muhammad-Jay/neuron/application/config"
)

func testConfig() config.Config {
	return config.Config{
		Executors: config.ExecutorsConfig{
			DefaultRegistries: []string{"local"},
			Registries: []config.ExecutorRegistry{
				{Name: "local", URL: "./executors"},
			},
		},
		Runtime: config.RuntimeConfig{
			Execution: config.ExecutionConfig{Mode: "wait", Timeout: "30m"},
			Workers:   config.WorkerConfig{Min: 1, Max: 4},
		},
		Inspector: config.InspectorConfig{Enabled: true, Address: "127.0.0.1:7433"},
	}
}

func testM() *manifest.System {
	return &manifest.System{
		Services: []manifest.Service{
			{
				Name: "hello",
				Executor: manifest.ExecutorSpec{
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

	if len(ec.ExecutorRegistries) != 1 || ec.ExecutorRegistries[0].Name != "local" {
		t.Fatalf("registries = %#v, want the configured local registry", ec.ExecutorRegistries)
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
	if len(ec.ExecutorRequirements) != 1 {
		t.Fatalf("requirements = %d, want 1 indexed from manifest services", len(ec.ExecutorRequirements))
	}
	if req := ec.ExecutorRequirements[0]; req.Name != "example:echo" || req.Services[0] != "hello" {
		t.Errorf("requirement = %#v, want example:echo for hello", req)
	}
}
