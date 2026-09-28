package runtimectl

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Muhammad-Jay/neuron/application/config"
)

func TestRequireUsesDefaultRegistriesWhenRegistryEmpty(t *testing.T) {
	catalog := &Catalog{
		cfg: config.CapabilityRuntimesConfig{
			DefaultRegistries: []string{"github", "local"},
		},
	}

	got := catalog.Require("github:read", "^1.0.0", []string{""})
	if len(got.Registries) != 2 {
		t.Fatalf("Registries = %v, want default registries", got.Registries)
	}
	if got.Registries[0] != "github" || got.Registries[1] != "local" {
		t.Errorf("Registries = %v, want [github local]", got.Registries)
	}
}

func TestRequireUsesExplicitRegistry(t *testing.T) {
	catalog := &Catalog{
		cfg: config.CapabilityRuntimesConfig{
			DefaultRegistries: []string{"github", "local"},
		},
	}

	got := catalog.Require("github:read", "^1.0.0", []string{"local"})
	if len(got.Registries) != 1 || got.Registries[0] != "local" {
		t.Errorf("Registries = %v, want [local]", got.Registries)
	}
}

func TestBuildCatalogImplicitRootAndLocalRoots(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "neuron", "capabilityRuntimes", "example", "echo", "1.0.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"apiVersion":"neuron/v1","kind":"CapabilityRuntime","metadata":{"name":"example:echo","version":"1.0.0"},"runtime":{"type":"process","entrypoint":"./echo"},"capabilities":["example:echo"]}`
	if err := os.WriteFile(filepath.Join(root, "neuron", "capabilityRuntimes", "example", "echo", "1.0.0", "runtime.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	custom := filepath.Join(root, "custom")
	if err := os.MkdirAll(filepath.Join(custom, "other", "tool", "2.0.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(custom, "other", "tool", "2.0.0", "runtime.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	catalog, err := BuildCatalog(CatalogConfig{
		CapabilityRuntimesConfig: config.CapabilityRuntimesConfig{
			StoreDir:          filepath.Join(root, "store"),
			DefaultRegistries: []string{"local"},
			LocalRoots:        []string{custom},
		},
		ProjectRoot: root,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := catalog.Registry.Get("local"); !ok {
		t.Fatal("expected a `local` registry wired from the implicit + configured roots")
	}

	var allTypes []string
	for _, name := range catalog.Registry.Names() {
		provider, _ := catalog.Registry.Get(name)
		types, err := provider.Types(context.Background())
		if err != nil {
			t.Fatalf("types from %s: %v", name, err)
		}
		allTypes = append(allTypes, types...)
	}
	joined := strings.Join(allTypes, ",")
	if !strings.Contains(joined, "example:echo") {
		t.Errorf("types = %v, want example:echo from the implicit neuron/capabilityRuntimes root", allTypes)
	}
	if !strings.Contains(joined, "other:tool") {
		t.Errorf("types = %v, want other:tool from the configured localRoot", allTypes)
	}
}

func TestBuildCatalogImplicitRootMissingIsOK(t *testing.T) {
	root := t.TempDir()
	catalog, err := BuildCatalog(CatalogConfig{
		CapabilityRuntimesConfig: config.CapabilityRuntimesConfig{StoreDir: filepath.Join(root, "store")},
		ProjectRoot:              root,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := catalog.Registry.Get("local"); ok {
		t.Fatal("no local registry expected when the implicit root does not exist")
	}
}

func TestBuildCatalogConfiguredRootMissingErrors(t *testing.T) {
	root := t.TempDir()
	_, err := BuildCatalog(CatalogConfig{
		CapabilityRuntimesConfig: config.CapabilityRuntimesConfig{
			StoreDir:   filepath.Join(root, "store"),
			LocalRoots: []string{filepath.Join(root, "does-not-exist")},
		},
		ProjectRoot: root,
	})
	if err == nil {
		t.Fatal("BuildCatalog succeeded, want error for a missing configured local root")
	}
}

func TestBuildCatalogUnknownRegistryErrors(t *testing.T) {
	root := t.TempDir()
	_, err := BuildCatalog(CatalogConfig{
		CapabilityRuntimesConfig: config.CapabilityRuntimesConfig{
			StoreDir: filepath.Join(root, "store"),
			Registries: []config.CapabilityRuntimeRegistry{
				{Name: "megacorp", URL: "https://registry.megacorp.dev"},
			},
		},
		ProjectRoot: root,
	})
	if err == nil || !strings.Contains(err.Error(), "megacorp") {
		t.Fatalf("BuildCatalog err = %v, want it to name the unknown registry", err)
	}
}
