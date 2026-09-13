package yaml_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Muhammad-Jay/neuron/application/build/builder"
	yamlpkg "github.com/Muhammad-Jay/neuron/application/build/yaml"
	"github.com/Muhammad-Jay/neuron/application/compiler/manifest"
)

// writeProject lays out a minimal YAML Neuron project in dir.
func writeProject(t *testing.T, dir string) {
	t.Helper()

	writeFile(t, filepath.Join(dir, "systems/hello/system.yaml"), `apiVersion: neuron/v1
kind: System

metadata:
  name: hello
  version: 0.1.0

services:
  - ref: greet
    entry: ../../services/greet.yaml
`)

	writeFile(t, filepath.Join(dir, "services/greet.yaml"), `apiVersion: neuron/v1
kind: Service

metadata:
  name: greet
  version: 0.1.0

spec:
  executor:
    type: set

  mappings:
    - direction: input
      source: value
      target: value
`)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestYAMLBBuilderBuildProducesManifest(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeProject(t, dir)

	if err := yamlpkg.New().Build(ctx, builder.Options{
		Root:  dir,
		Entry: filepath.Join("systems", "hello", "system.yaml"),
	}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	manifestPath := filepath.Join(dir, ".neuron", "manifest.json")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("expected manifest at %s: %v", manifestPath, err)
	}

	m, err := manifest.LoadFromProjectRoot(dir)
	if err != nil {
		t.Fatalf("LoadFromProjectRoot: %v", err)
	}
	if m.Metadata.Name != "hello" {
		t.Errorf("manifest name = %q, want hello", m.Metadata.Name)
	}
	if len(m.Services) == 0 {
		t.Error("expected at least one service in the manifest")
	}
	// Project variables come from the config (Builder options), not the YAML.
	if len(m.Variables) != 0 {
		t.Errorf("variables = %#v, want none", m.Variables)
	}
}

func TestYAMLBBuilderCarriesVariables(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeProject(t, dir)

	if err := yamlpkg.New().Build(ctx, builder.Options{
		Root:      dir,
		Entry:     filepath.Join("systems", "hello", "system.yaml"),
		Variables: map[string]any{"environment": "staging"},
	}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	m, err := manifest.LoadFromProjectRoot(dir)
	if err != nil {
		t.Fatalf("LoadFromProjectRoot: %v", err)
	}
	if m.Variables["environment"] != "staging" {
		t.Errorf("variables = %#v, want environment=staging", m.Variables)
	}
}

func TestYAMLBBuilderRequiresRoot(t *testing.T) {
	b := yamlpkg.New()
	if err := b.Build(context.Background(), builder.Options{}); err == nil {
		t.Fatal("expected error when root is empty")
	}
}
