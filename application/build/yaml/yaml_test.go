package yaml_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/neuron-runtime/neuron/application/build/builder"
	yamlpkg "github.com/neuron-runtime/neuron/application/build/yaml"
)

// writeProject lays out a minimal YAML Neuron project in dir.
func writeProject(t *testing.T, dir string) {
	t.Helper()

	writeFile(t, filepath.Join(dir, "assemblies/hello/assembly.yaml"), `apiVersion: neuron/v1
kind: Assembly

metadata:
  name: hello
  version: 0.1.0

capabilities:
  - ref: greet
    entry: ../../capabilities/greet.yaml
`)

	writeFile(t, filepath.Join(dir, "capabilities/greet.yaml"), `apiVersion: neuron/v1
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

func TestYAMLBuilderBuildProducesManifest(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeProject(t, dir)

	sys, err := yamlpkg.New().Build(ctx, builder.Options{
		Root:  dir,
		Entry: filepath.Join("assemblies", "hello", "assembly.yaml"),
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if sys == nil {
		t.Fatal("Build returned a nil manifest")
	}

	// The builder returns the authored manifest; identity injection,
	// canonicalization and persistence are owned by the build package.
	if sys.Metadata.Name != "hello" {
		t.Errorf("manifest name = %q, want hello", sys.Metadata.Name)
	}
	if sys.Metadata.Version != "0.1.0" {
		t.Errorf("manifest version = %q, want 0.1.0", sys.Metadata.Version)
	}
	if len(sys.Capabilities) == 0 {
		t.Error("expected at least one capability in the manifest")
	}
	// Project variables come from the config (Builder options), not the YAML.
	if len(sys.Variables) != 0 {
		t.Errorf("variables = %#v, want none", sys.Variables)
	}
}

// TestYAMLBuilderDoesNotApplyVariables pins that the builder returns the
// authored manifest without touching project variables: application/build.Build
// writes them at the shared seam, and a builder that applied them itself would
// let the YAML frontend diverge from the TypeScript frontend.
func TestYAMLBuilderDoesNotApplyVariables(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeProject(t, dir)

	sys, err := yamlpkg.New().Build(ctx, builder.Options{
		Root:      dir,
		Entry:     filepath.Join("assemblies", "hello", "assembly.yaml"),
		Variables: map[string]any{"environment": "staging"},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if sys == nil {
		t.Fatal("Build returned a nil manifest")
	}
	if sys.Variables != nil {
		t.Errorf("variables = %#v, want none from the builder", sys.Variables)
	}
}

func TestYAMLBuilderRequiresRoot(t *testing.T) {
	b := yamlpkg.New()
	if _, err := b.Build(context.Background(), builder.Options{}); err == nil {
		t.Fatal("expected error when root is empty")
	}
}
