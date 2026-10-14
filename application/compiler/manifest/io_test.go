package manifest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAssemblyManifestPathIsPerAssembly(t *testing.T) {
	root := "/proj"
	a := filepath.ToSlash(AssemblyManifestPath(root, "orders"))
	b := filepath.ToSlash(AssemblyManifestPath(root, "billing"))
	if a == b {
		t.Fatalf("distinct assembly names produced the same path: %s", a)
	}
	want := "/proj/.neuron/assemblies/orders/manifest.json"
	if a != want {
		t.Fatalf("path = %q, want %q", a, want)
	}
	if filepath.ToSlash(SDKManifestPath(root)) != "/proj/.neuron/manifest.json" {
		t.Fatalf("SDK manifest path = %q", SDKManifestPath(root))
	}
}

func TestSaveLoadAssemblyRoundTrips(t *testing.T) {
	root := t.TempDir()
	m := &Assembly{APIVersion: "neuron/v1", Kind: "Assembly"}
	m.Metadata.Name = "orders"
	m.Metadata.Version = "1.2.3"

	if err := SaveAssembly(root, "orders", m); err != nil {
		t.Fatalf("SaveAssembly: %v", err)
	}
	if _, err := os.Stat(AssemblyManifestPath(root, "orders")); err != nil {
		t.Fatalf("manifest artifact not written: %v", err)
	}

	got, err := LoadAssembly(root, "orders")
	if err != nil {
		t.Fatalf("LoadAssembly: %v", err)
	}
	if got.Metadata.Name != "orders" || got.Metadata.Version != "1.2.3" {
		t.Errorf("round-tripped metadata = %q@%q", got.Metadata.Name, got.Metadata.Version)
	}
}
