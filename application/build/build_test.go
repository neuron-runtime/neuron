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

// writeYAMLProjectWithoutIdentity lays out a minimal YAML Neuron project
// whose assembly authors no identity at all: neuron.config.* owns the
// Assembly name/version, so nothing authored may duplicate it.
func writeYAMLProjectWithoutIdentity(t *testing.T, dir string) {
	t.Helper()

	files := map[string]string{
		"assembly.yaml": `apiVersion: neuron/v1
kind: Assembly

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
	if len(m.Capabilities) != 1 || m.Capabilities[0].Name != "greet" {
		t.Errorf("persisted capabilities = %+v, want a single greet capability", m.Capabilities)
	}
	if m.Variables["environment"] != "staging" {
		t.Errorf("persisted variables = %#v, want environment=staging", m.Variables)
	}
}

// TestBuildYAMLWithoutAuthoredIdentity proves a YAML assembly with no
// authored metadata.name/version builds through the shared seam: config
// supplies the identity and ApplyIdentity injects it before canonicalization.
func TestBuildYAMLWithoutAuthoredIdentity(t *testing.T) {
	dir := t.TempDir()
	writeYAMLProjectWithoutIdentity(t, dir)

	if err := build.Build(context.Background(), language.YAML, build.Options{
		Root:    dir,
		Name:    "hello",
		Version: "0.1.0",
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
}

// TestBuildYAMLWithoutAuthoredIdentityRequiresConfig pins that a build whose
// configuration carries no identity still fails — at ApplyIdentity, which
// names neuron.config.*, not at the project resolver.
func TestBuildYAMLWithoutAuthoredIdentityRequiresConfig(t *testing.T) {
	dir := t.TempDir()
	writeYAMLProjectWithoutIdentity(t, dir)

	err := build.Build(context.Background(), language.YAML, build.Options{Root: dir})
	if err == nil {
		t.Fatal("expected an error when configuration carries no identity")
	}
	if !strings.Contains(err.Error(), `assembly name is required: add "name" to neuron.config.* (project configuration)`) {
		t.Errorf("error = %q, want the required-name message", err)
	}
}

// stubBuilder is a minimal Builder registered in the build registry to prove
// the shared seam applies project variables to every authoring language, not
// just the shipped ones.
type stubBuilder struct{}

func (stubBuilder) Language() language.Language { return language.Language("test-stub") }

func (stubBuilder) Build(_ context.Context, opts build.Options) (*manifest.Assembly, error) {
	return &manifest.Assembly{
		APIVersion: "neuron/v1",
		Kind:       "Assembly",
		Metadata:   manifest.Metadata{Name: opts.Name, Version: opts.Version},
	}, nil
}

// TestBuildAppliesVariablesAtTheSharedSeam runs a freshly registered stub
// builder through build.Build with project variables, proving the seam — and
// not any individual builder — writes variables into the persisted manifest.
// This is the boundary the YAML frontend used to own alone, which is exactly
// how the TypeScript path lost them.
func TestBuildAppliesVariablesAtTheSharedSeam(t *testing.T) {
	if err := build.Register(stubBuilder{}); err != nil {
		t.Fatalf("register stub builder: %v", err)
	}

	dir := t.TempDir()
	if err := build.Build(context.Background(), stubBuilder{}.Language(), build.Options{
		Root:      dir,
		Name:      "hello",
		Version:   "0.1.0",
		Variables: map[string]any{"environment": "staging", "limits": map[string]any{"cpu": "1"}},
	}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	m, err := manifest.LoadFromProjectRoot(dir)
	if err != nil {
		t.Fatalf("LoadFromProjectRoot: %v", err)
	}
	if m.Metadata.Name != "hello" || m.Metadata.Version != "0.1.0" {
		t.Errorf("persisted identity = %q@%q, want hello@0.1.0", m.Metadata.Name, m.Metadata.Version)
	}
	if m.Variables["environment"] != "staging" {
		t.Errorf("persisted variables = %#v, want environment=staging", m.Variables)
	}
	limits, ok := m.Variables["limits"].(map[string]any)
	if !ok || limits["cpu"] != "1" {
		t.Errorf("persisted nested variable limits = %#v, want cpu=1", m.Variables["limits"])
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
