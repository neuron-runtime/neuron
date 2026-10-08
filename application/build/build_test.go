package build_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neuron-runtime/neuron/application/build"
	"github.com/neuron-runtime/neuron/application/compiler/manifest"
	"github.com/neuron-runtime/neuron/application/language"
)

// writeYAMLProject lays out a minimal YAML Neuron project at dir using the
// default assembly.yaml entry.
func writeYAMLProject(t *testing.T, dir string) {
	t.Helper()

	files := map[string]string{
		"assembly.yaml": `apiVersion: neuron/v1
kind: Assembly

metadata:
  name: hello
  version: 0.1.0
  description: A friendly hello assembly

capabilities:
  - ref: greet
    entry: capabilities/greet.yaml
`,
		"capabilities/greet.yaml": `apiVersion: neuron/v1
kind: Capability

metadata:
  name: greet
  version: 0.1.0

spec:
  capability runtime:
    type: set

  mappings:
    - direction: input
      source: value
      target: value
`,
	}

	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestBuildPersistsIdentityInjectedManifest pins the shared injection
// boundary: build.Build applies the project-configured identity, then
// persists the manifest for every authoring language.
func TestBuildPersistsIdentityInjectedManifest(t *testing.T) {
	dir := t.TempDir()
	writeYAMLProject(t, dir)

	if err := build.Build(context.Background(), language.YAML, build.Options{
		Root:      dir,
		Name:      "hello",
		Version:   "0.1.0",
		Variables: map[string]any{"environment": "staging"},
	}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	m, err := manifest.LoadFromProjectRoot(dir)
	if err != nil {
		t.Fatalf("LoadFromProjectRoot: %v", err)
	}
	if m.Metadata.Name != "hello" {
		t.Errorf("persisted name = %q, want hello", m.Metadata.Name)
	}
	if m.Metadata.Version != "0.1.0" {
		t.Errorf("persisted version = %q, want 0.1.0", m.Metadata.Version)
	}
	// Description stays authored; configuration owns only name and version.
	if m.Metadata.Description != "A friendly hello assembly" {
		t.Errorf("persisted description = %q, want the authored description", m.Metadata.Description)
	}
	if len(m.Capabilities) != 1 || m.Capabilities[0].Name != "greet" {
		t.Errorf("persisted capabilities = %+v, want a single greet capability", m.Capabilities)
	}
	if m.Variables["environment"] != "staging" {
		t.Errorf("persisted variables = %#v, want environment=staging", m.Variables)
	}
}

// TestBuildRequiresConfiguredIdentity verifies the seam rejects a build
// when the project configuration carries no Assembly identity.
func TestBuildRequiresConfiguredIdentity(t *testing.T) {
	dir := t.TempDir()
	writeYAMLProject(t, dir)

	err := build.Build(context.Background(), language.YAML, build.Options{
		Root:    dir,
		Version: "0.1.0",
	})
	if err == nil {
		t.Fatal("expected an error when the configured name is empty")
	}
	if !strings.Contains(err.Error(), `assembly name is required: add "name" to neuron.config.* (project configuration)`) {
		t.Errorf("error = %q, want the required-name message", err)
	}
}

// TestBuildRejectsIdentityConflict verifies the seam surfaces the
// configuration-versus-authored conflict instead of silently picking one.
func TestBuildRejectsIdentityConflict(t *testing.T) {
	dir := t.TempDir()
	writeYAMLProject(t, dir)

	err := build.Build(context.Background(), language.YAML, build.Options{
		Root:    dir,
		Name:    "other",
		Version: "0.1.0",
	})
	if err == nil {
		t.Fatal("expected an error when the configured name conflicts with the authored one")
	}
	for _, want := range []string{"assembly name conflict", `"other"`, `"hello"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %s", err, want)
		}
	}
}
