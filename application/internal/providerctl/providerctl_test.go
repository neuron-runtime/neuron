package providerctl_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/neuron-runtime/neuron/application/config"
	"github.com/neuron-runtime/neuron/application/internal/providerctl"
)

func TestBuildCatalogDefaults(t *testing.T) {
	dir := t.TempDir()
	catalog, err := providerctl.BuildCatalog(providerctl.CatalogConfig{
		ProvidersConfig: config.ProvidersConfig{
			StoreDir:          filepath.Join(dir, "providers"),
			DefaultRegistries: []string{"local"},
		},
	})
	if err != nil {
		t.Fatalf("BuildCatalog: %v", err)
	}
	if catalog.StoreDir() != filepath.Join(dir, "providers") {
		t.Fatalf("StoreDir = %q", catalog.StoreDir())
	}
	if got := catalog.Require("process", "", nil); len(got.Registries) != 1 || got.Registries[0] != "local" {
		t.Fatalf("Require defaults = %v, want [local]", got.Registries)
	}
}

func TestBuildCatalogConfiguredRootMissingErrors(t *testing.T) {
	_, err := providerctl.BuildCatalog(providerctl.CatalogConfig{
		ProvidersConfig: config.ProvidersConfig{
			StoreDir:   filepath.Join(t.TempDir(), "providers"),
			LocalRoots: []string{filepath.Join(t.TempDir(), "absent")},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("BuildCatalog with missing root = %v, want configuration error", err)
	}
}

func TestBuildCatalogUnknownRegistryErrors(t *testing.T) {
	_, err := providerctl.BuildCatalog(providerctl.CatalogConfig{
		ProvidersConfig: config.ProvidersConfig{
			StoreDir:   filepath.Join(t.TempDir(), "providers"),
			Registries: []config.ProviderRegistry{{Name: "mystery"}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "unknown provider registry") {
		t.Fatalf("BuildCatalog with unknown registry = %v, want unknown-registry error", err)
	}
}

func TestBuildCatalogRegistersLocalRoots(t *testing.T) {
	root := t.TempDir()
	catalog, err := providerctl.BuildCatalog(providerctl.CatalogConfig{
		ProvidersConfig: config.ProvidersConfig{
			StoreDir:   filepath.Join(t.TempDir(), "providers"),
			LocalRoots: []string{root},
		},
	})
	if err != nil {
		t.Fatalf("BuildCatalog: %v", err)
	}
	if _, ok := catalog.Registry.Get("local"); !ok {
		t.Fatal("local source must be registered when localRoots exist")
	}
}

func TestRequireExplicitRegistriesWin(t *testing.T) {
	catalog, err := providerctl.BuildCatalog(providerctl.CatalogConfig{
		ProvidersConfig: config.ProvidersConfig{
			StoreDir:          filepath.Join(t.TempDir(), "providers"),
			DefaultRegistries: []string{"local"},
		},
	})
	if err != nil {
		t.Fatalf("BuildCatalog: %v", err)
	}
	got := catalog.Require("process", "1.0.0", []string{"github"})
	if len(got.Registries) != 1 || got.Registries[0] != "github" {
		t.Fatalf("Require registries = %v, want [github]", got.Registries)
	}
	if got.Name != "process" || got.Version != "1.0.0" {
		t.Fatalf("Require = %+v", got)
	}
}
