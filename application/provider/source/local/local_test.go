package local_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/neuron-runtime/neuron/application/provider"
	"github.com/neuron-runtime/neuron/application/provider/source/local"
	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
	providertypes "github.com/neuron-runtime/neuron/shared/types/provider"
)

func writeVersionDir(t *testing.T, root, name, version string, extra map[string]string) string {
	t.Helper()
	seg, err := provider.ProviderTypePath(name)
	if err != nil {
		t.Fatalf("provider type path: %v", err)
	}
	dir := filepath.Join(root, seg, version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	m := &providertypes.Manifest{
		APIVersion:   providertypes.APIVersion,
		Kind:         providertypes.Kind,
		Metadata:     providertypes.ManifestMetadata{Name: name, Version: version},
		Provider:     providertypes.ManifestProvider{ProtocolVersion: providertypes.ProtocolVersion},
		RuntimeKinds: []string{capabilityrt.RuntimeKindProcess},
		Platforms:    map[string]providertypes.Platform{"linux-amd64": {}},
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, providertypes.ManifestFile), data, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	for rel, content := range extra {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return dir
}

func TestVersionsAndPackageFromLayout(t *testing.T) {
	root := t.TempDir()
	writeVersionDir(t, root, "process", "1.0.0", map[string]string{"payload.bin": "v1"})
	writeVersionDir(t, root, "process", "1.1.0", map[string]string{"payload.bin": "v2"})

	src, err := local.New(root)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	ctx := context.Background()

	versions, err := src.Versions(ctx, "process")
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if len(versions) != 2 || versions[0] != "1.1.0" || versions[1] != "1.0.0" {
		t.Fatalf("Versions = %v, want [1.1.0 1.0.0]", versions)
	}

	pkg, err := src.Package(ctx, "process", "1.0.0")
	if err != nil {
		t.Fatalf("Package: %v", err)
	}
	if pkg.Registry != "local" || pkg.Source == "" || len(pkg.Manifest) == 0 {
		t.Fatalf("Package = %+v, want local provenance with source and manifest", pkg)
	}
}

func TestPackagePrefersCanonicalArchive(t *testing.T) {
	root := t.TempDir()
	dir := writeVersionDir(t, root, "process", "1.0.0", nil)
	archive := providertypes.PackageArchiveName("process", "1.0.0")
	if err := os.WriteFile(filepath.Join(dir, archive), []byte("fake-archive"), 0o644); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	// A second archive carrying the suffix must lose to the exact name.
	if err := os.WriteFile(filepath.Join(dir, "other-provider.neuron.tar.gz"), []byte("other"), 0o644); err != nil {
		t.Fatalf("write other archive: %v", err)
	}

	src, err := local.New(root)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	pkg, err := src.Package(context.Background(), "process", "1.0.0")
	if err != nil {
		t.Fatalf("Package: %v", err)
	}
	if filepath.Base(pkg.Source) != archive {
		t.Fatalf("Source = %q, want %q", pkg.Source, archive)
	}
}

func TestVersionsUnionAcrossRoots(t *testing.T) {
	rootA, rootB := t.TempDir(), t.TempDir()
	writeVersionDir(t, rootA, "process", "1.0.0", nil)
	writeVersionDir(t, rootB, "process", "1.1.0", nil)

	src, err := local.New(rootA, rootB)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	versions, err := src.Versions(context.Background(), "process")
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("Versions = %v, want both roots unioned", versions)
	}
}

func TestMissingProviderIsNotFound(t *testing.T) {
	src, err := local.New(t.TempDir())
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	if _, err := src.Versions(context.Background(), "missing"); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("Versions(missing) = %v, want ErrNotFound", err)
	}
	if _, err := src.Package(context.Background(), "process", "9.9.9"); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("Package(missing) = %v, want ErrNotFound", err)
	}
}

func TestNewRejectsMissingRoot(t *testing.T) {
	if _, err := local.New(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("missing root must be rejected")
	}
	if _, err := local.New(); err == nil {
		t.Fatal("empty root list must be rejected")
	}
}

func TestManifestIdentityMismatch(t *testing.T) {
	root := t.TempDir()
	writeVersionDir(t, root, "process", "1.0.0", nil)

	src, err := local.New(root)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	if _, err := src.Package(context.Background(), "other", "1.0.0"); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("Package(other) = %v, want ErrNotFound", err)
	}
}
