package build

import (
	"reflect"
	"testing"

	"github.com/neuron-runtime/neuron/application/compiler/manifest"
	"github.com/neuron-runtime/neuron/application/config"
	"github.com/neuron-runtime/neuron/application/project"
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

// A selective `neuron build --assembly X` must reuse the previously built entry
// for an unrelated Assembly Y rather than rebuild it, so Y's manifest and frozen
// capability runtime set are preserved unchanged. This is the decision that
// makes independent rebuilds possible.
func TestCarryForwardReusesUnselectedAssembly(t *testing.T) {
	prior := project.AssemblyBuild{
		Name:         "y",
		ManifestPath: ".neuron/assemblies/y/manifest.json",
		Fingerprint:  "fp-y",
	}
	record := &project.BuildRecord{Assemblies: []project.AssemblyBuild{prior}}

	got, ok := carryForward(record, "y")
	if !ok {
		t.Fatal("carryForward did not reuse the previously built Assembly")
	}
	if !reflect.DeepEqual(got, prior) {
		t.Errorf("carryForward = %#v, want the prior entry unchanged", got)
	}
}

// With no prior build, or no prior entry for the Assembly, there is nothing to
// carry forward and the Assembly must be built so the record stays complete.
func TestCarryForwardMissesAreRebuilt(t *testing.T) {
	if _, ok := carryForward(nil, "y"); ok {
		t.Error("carryForward reused an entry from a nil record")
	}

	record := &project.BuildRecord{Assemblies: []project.AssemblyBuild{{Name: "x"}}}
	if _, ok := carryForward(record, "y"); ok {
		t.Error("carryForward reused an entry for an Assembly with no prior build")
	}
}
