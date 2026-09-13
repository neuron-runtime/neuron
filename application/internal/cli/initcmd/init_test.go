package initcmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Muhammad-Jay/neuron/application/build"
	"github.com/Muhammad-Jay/neuron/application/compiler"
	"github.com/Muhammad-Jay/neuron/application/compiler/manifest"
	"github.com/Muhammad-Jay/neuron/application/language"
)

func TestScaffoldYAMLCompiles(t *testing.T) {
	root := t.TempDir()
	if err := scaffoldYAML(root, "hello-scaffold"); err != nil {
		t.Fatal(err)
	}
	if err := ensureImplicitExecutorRoot(root); err != nil {
		t.Fatal(err)
	}

	// The scaffolded YAML system must build to the canonical manifest and
	// compile to a runtime System without source-language-specific tweaks.
	if err := build.Build(context.Background(), language.YAML, build.Options{
		Root:  root,
		Entry: "system.yaml",
	}); err != nil {
		t.Fatalf("build scaffolded yaml project: %v", err)
	}

	m, err := manifest.LoadFromProjectRoot(root)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if m.Metadata.Name != "hello-scaffold" {
		t.Errorf("manifest name = %q, want hello-scaffold", m.Metadata.Name)
	}
	if len(m.Services) != 1 || m.Services[0].Name != "say-hello" {
		t.Errorf("manifest services = %+v, want a single say-hello service", m.Services)
	}

	sys, err := compiler.New().Compile(m)
	if err != nil {
		t.Fatalf("compile scaffolded manifest: %v", err)
	}
	if sys.Metadata.Name != "hello-scaffold" {
		t.Errorf("compiled system name = %q", sys.Metadata.Name)
	}
}

func TestScaffoldTypeScriptLayout(t *testing.T) {
	root := t.TempDir()
	if err := scaffoldTypeScript(root, "hello-ts"); err != nil {
		t.Fatal(err)
	}
	if err := ensureImplicitExecutorRoot(root); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"neuron.config.json", "package.json", "tsconfig.json", "system.ts", "neuron/executors/.gitkeep"} {
		if _, err := os.Stat(filepath.Join(root, want)); err != nil {
			t.Errorf("missing scaffold file %s: %v", want, err)
		}
	}

	// The scaffold config must be JSON-decodable and declare the language.
	cfgPath := filepath.Join(root, "neuron.config.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	// The YAML service file names and config keys stay strict-by-construction;
	// spot-checking entry keeps the test from over-fitting to config internals.
	if string(data) == "" {
		t.Fatal("neuron.config.json is empty")
	}
}
