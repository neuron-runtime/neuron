package config

import (
	"fmt"
	"strings"
	"testing"
)

// loadProjectConfig writes a project configuration file named name into a
// fresh temp directory and loads it with no global configuration.
func loadProjectConfig(t *testing.T, name, content string) (Config, error) {
	t.Helper()
	path := write(t, t.TempDir(), name, content)
	return Load(Options{GlobalPath: "/nonexistent/global.yaml", ProjectPath: path})
}

// loadGlobalConfig writes the machine-level global configuration into a fresh
// temp directory and loads it against an empty project directory.
func loadGlobalConfig(t *testing.T, content string) (Config, error) {
	t.Helper()
	path := write(t, t.TempDir(), "global.yaml", content)
	return Load(Options{GlobalPath: path, ProjectDir: t.TempDir()})
}

func TestUnknownKeysRejected(t *testing.T) {
	tests := []struct {
		name    string
		scope   string
		file    string
		content string
		want    string
	}{
		{
			name:    "project top-level typo",
			scope:   "project",
			file:    "neuron.config.json",
			content: `{"typoKey": true}`,
			want:    "typoKey",
		},
		{
			name:    "project nested runtime typo",
			scope:   "project",
			file:    "neuron.config.yaml",
			content: "runtime:\n  portt: auto\n",
			want:    "runtime.portt",
		},
		{
			name:    "project deep runtime typo",
			scope:   "project",
			file:    "neuron.config.yaml",
			content: "runtime:\n  execution:\n    typoKey: 1\n",
			want:    "runtime.execution.typoKey",
		},
		{
			name:    "project capabilityRuntimes typo",
			scope:   "project",
			file:    "neuron.config.yaml",
			content: "capabilityRuntimes:\n  registreez: []\n",
			want:    "capabilityRuntimes.registreez",
		},
		{
			name:    "global top-level typo",
			scope:   "global",
			file:    "global.yaml",
			content: "typoKey: true\n",
			want:    "typoKey",
		},
		{
			name:    "global nested runtime typo",
			scope:   "global",
			file:    "global.yaml",
			content: "runtime:\n  portt: auto\n",
			want:    "runtime.portt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			if tt.scope == "project" {
				_, err = loadProjectConfig(t, tt.file, tt.content)
			} else {
				_, err = loadGlobalConfig(t, tt.content)
			}
			if err == nil {
				t.Fatalf("Load succeeded, want unknown key %q to be rejected", tt.want)
			}
			if !strings.Contains(err.Error(), "unknown key") {
				t.Errorf("error = %v, want it to say `unknown key`", err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want it to contain the dotted path %q", err, tt.want)
			}
		})
	}
}

func TestVariablesRemainFreeForm(t *testing.T) {
	cfg, err := loadProjectConfig(t, "neuron.config.yaml", `
variables:
  anything:
    nested: [1, 2]
`)
	if err != nil {
		t.Fatalf("Load failed: %v (variables must be free-form)", err)
	}

	anything, ok := cfg.Variables["anything"].(map[string]any)
	if !ok {
		t.Fatalf("variables.anything = %#v, want nested map", cfg.Variables["anything"])
	}
	nested, ok := anything["nested"].([]any)
	if !ok {
		t.Fatalf("variables.anything.nested = %#v, want list", anything["nested"])
	}
	if len(nested) != 2 || fmt.Sprint(nested[0]) != "1" || fmt.Sprint(nested[1]) != "2" {
		t.Errorf("variables.anything.nested = %v, want [1 2]", nested)
	}
}

// TestTypedRecordListsValidated pins that a list of typed configuration records
// (registries, assemblies) is validated element by element. These are records,
// not free-form value containers, so an unknown key inside one is a typo and
// must fail the load rather than be silently dropped.
func TestTypedRecordListsValidated(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "registry item typo",
			content: "capabilityRuntimes:\n  registries:\n    - name: x\n      url: y\n      mirror: z\n",
			want:    "capabilityRuntimes.registries[0].mirror",
		},
		{
			name:    "assembly item typo",
			content: "assemblies:\n  - name: orders\n    entry: a.ts\n    defualt: true\n",
			want:    "assemblies[0].defualt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadProjectConfig(t, "neuron.config.yaml", tt.content)
			if err == nil {
				t.Fatalf("Load succeeded, want unknown key %q rejected", tt.want)
			}
			if !strings.Contains(err.Error(), "unknown key") {
				t.Errorf("error = %v, want it to say `unknown key`", err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want it to contain the dotted path %q", err, tt.want)
			}
		})
	}
}

func TestProjectFileDeniesDaemon(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"whole subtree", "daemon:\n  socket: /tmp/x.sock\n  pidFile: /tmp/x.pid\n"},
		{"endpoint only", "daemon:\n  endpoint: unix:///tmp/nore.sock\n"},
		{"single leaf", "daemon:\n  norePath: /usr/local/bin/neuron\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadProjectConfig(t, "neuron.config.yaml", tt.content)
			if err == nil {
				t.Fatal("Load succeeded, want `daemon` denied in project config")
			}
			if !strings.Contains(err.Error(), "daemon") {
				t.Errorf("error = %v, want it to mention `daemon`", err)
			}
		})
	}
}

func TestGlobalFileAllowsDaemon(t *testing.T) {
	// The machine-level global file still carries engine plumbing during the
	// transition to the project runtime; daemon must remain accepted there.
	cfg, err := loadGlobalConfig(t, "daemon:\n  endpoint: unix:///tmp/nore.sock\n")
	if err != nil {
		t.Fatalf("Load failed: %v (daemon belongs to the global file during the transition)", err)
	}
	if cfg.Daemon.Endpoint != "unix:///tmp/nore.sock" {
		t.Errorf("daemon.endpoint = %q, want unix:///tmp/nore.sock", cfg.Daemon.Endpoint)
	}
}

func TestGlobalFileDeniesProjectIdentity(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"name", "name: my-proj\n", "name"},
		{"version", "version: 1.2.3\n", "version"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadGlobalConfig(t, tt.content)
			if err == nil {
				t.Fatalf("Load succeeded, want `%s` denied in global config", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want it to mention `%s`", err, tt.want)
			}
			if !strings.Contains(err.Error(), "assembly identity") {
				t.Errorf("error = %v, want it to explain that assembly identity is project configuration", err)
			}
		})
	}
}

func TestGlobalFileDeniesStoreDir(t *testing.T) {
	_, err := loadGlobalConfig(t, "capabilityRuntimes:\n  storeDir: /somewhere\n")
	if err == nil {
		t.Fatal("Load succeeded, want `capabilityRuntimes.storeDir` denied in global config")
	}
	if !strings.Contains(err.Error(), "capabilityRuntimes.storeDir") {
		t.Errorf("error = %v, want it to mention `capabilityRuntimes.storeDir`", err)
	}
}

func TestGlobalFileDeniesProviderStoreDir(t *testing.T) {
	_, err := loadGlobalConfig(t, "providers:\n  storeDir: /somewhere\n")
	if err == nil {
		t.Fatal("Load succeeded, want `providers.storeDir` denied in global config")
	}
	if !strings.Contains(err.Error(), "providers.storeDir") {
		t.Errorf("error = %v, want it to mention `providers.storeDir`", err)
	}
}
