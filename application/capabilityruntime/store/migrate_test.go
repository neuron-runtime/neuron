package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neuron-runtime/neuron/application/capabilityruntime"
	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
)

const (
	migrateType    = "example:echo"
	migrateVersion = "1.0.0"
	migrateDigest  = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

// newStore returns a FileassemblyStore rooted in a fresh temporary directory.
func newStore(t *testing.T) *FileassemblyStore {
	t.Helper()
	s, err := NewFileassemblyStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileassemblyStore: %v", err)
	}
	return s
}

// writeFlatInstall creates a pre-content-addressed flat install at
// <root>/<typePath>/<version>/: the record and the artifact files sit directly
// under the version directory, exactly as older installers wrote them.
func writeFlatInstall(t *testing.T, root string, withDigest bool) string {
	t.Helper()
	typePath, err := capabilityrt.TypePath(migrateType)
	if err != nil {
		t.Fatalf("TypePath: %v", err)
	}
	versionDir := filepath.Join(root, typePath, migrateVersion)
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatalf("mkdir flat install: %v", err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, "echo"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, "pkg.tar.gz"), []byte("archive"), 0o644); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	rec := capabilityruntime.InstallRecord{
		Type:         migrateType,
		Version:      migrateVersion,
		Registry:     "local",
		RootDir:      versionDir,
		ManifestPath: filepath.Join(versionDir, capabilityrt.ManifestFile),
		ArtifactPath: versionDir,
		Runtime: capabilityruntime.RuntimeSpec{
			Type:       "process",
			Entrypoint: "echo",
			Protocol:   "neuron/capability-runtime-v1-json",
		},
	}
	if withDigest {
		rec.Digest = migrateDigest
	}
	if err := capabilityruntime.WriteInstallRecord(filepath.Join(versionDir, capabilityrt.InstallFile), rec); err != nil {
		t.Fatalf("write install record: %v", err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, capabilityrt.ManifestFile), []byte(`{"metadata":{"name":"example:echo"}}`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return versionDir
}

// assertMigrated fails the test unless versionDir holds a complete
// content-addressed install: record inside <digest>/, artifact files moved,
// record paths rewritten, and no flat leftovers.
func assertMigrated(t *testing.T, versionDir string) {
	t.Helper()
	digestDir := filepath.Join(versionDir, migrateDigest)
	rec, err := capabilityruntime.ReadInstallRecord(filepath.Join(digestDir, capabilityrt.InstallFile))
	if err != nil {
		t.Fatalf("read migrated record: %v", err)
	}
	if rec.RootDir != digestDir {
		t.Errorf("RootDir = %q, want %q", rec.RootDir, digestDir)
	}
	if rec.ArtifactPath != digestDir {
		t.Errorf("ArtifactPath = %q, want %q", rec.ArtifactPath, digestDir)
	}
	wantManifest := filepath.Join(digestDir, capabilityrt.ManifestFile)
	if rec.ManifestPath != wantManifest {
		t.Errorf("ManifestPath = %q, want %q", rec.ManifestPath, wantManifest)
	}
	for _, name := range []string{"echo", "pkg.tar.gz", capabilityrt.ManifestFile} {
		if _, err := os.Stat(filepath.Join(digestDir, name)); err != nil {
			t.Errorf("artifact file %q missing after migration: %v", name, err)
		}
	}
	entries, err := os.ReadDir(versionDir)
	if err != nil {
		t.Fatalf("read version dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			if e.Name() != migrateDigest {
				t.Errorf("unexpected directory %q under version dir", e.Name())
			}
			continue
		}
		t.Errorf("flat leftover file %q was not migrated away", e.Name())
	}
}

func TestGetMigratesFlatInstall(t *testing.T) {
	s := newStore(t)
	versionDir := writeFlatInstall(t, s.Root(), true)

	installed, err := s.Get(context.Background(), migrateType, migrateVersion)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if installed.Digest != migrateDigest {
		t.Errorf("Digest = %q, want %q", installed.Digest, migrateDigest)
	}
	assertMigrated(t, versionDir)

	// The migrated install must resolve again without further changes.
	again, err := s.Get(context.Background(), migrateType, migrateVersion)
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if again.RootDir != installed.RootDir {
		t.Errorf("second Get RootDir = %q, want %q", again.RootDir, installed.RootDir)
	}
}

func TestGetStaleFlatWithoutDigestSurfacesActionableError(t *testing.T) {
	s := newStore(t)
	writeFlatInstall(t, s.Root(), false)

	_, err := s.Get(context.Background(), migrateType, migrateVersion)
	if err == nil {
		t.Fatal("Get on a digestless flat install must fail")
	}
	if !errors.Is(err, capabilityruntime.ErrNotFound) && !strings.Contains(err.Error(), "reinstall") {
		t.Errorf("error = %q, want an actionable reinstall message", err)
	}
	if !strings.Contains(err.Error(), "neuron add --force") {
		t.Errorf("error = %q, want it to mention `neuron add --force`", err)
	}
}

func TestGetMissingReturnsNotFound(t *testing.T) {
	s := newStore(t)
	_, err := s.Get(context.Background(), migrateType, migrateVersion)
	if !errors.Is(err, capabilityruntime.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestListMigratesFlatInstall(t *testing.T) {
	s := newStore(t)
	versionDir := writeFlatInstall(t, s.Root(), true)

	installed, err := s.List(context.Background(), migrateType)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(installed) != 1 {
		t.Fatalf("len = %d, want 1", len(installed))
	}
	if installed[0].Digest != migrateDigest {
		t.Errorf("Digest = %q, want %q", installed[0].Digest, migrateDigest)
	}
	assertMigrated(t, versionDir)
}

func TestListAllMigratesFlatInstallAndDedupes(t *testing.T) {
	s := newStore(t)
	versionDir := writeFlatInstall(t, s.Root(), true)

	// Simulate a reinstall performed after the content-addressed migration
	// while the stale flat files were still present: a complete digest copy
	// exists next to the flat files.
	digestDir := filepath.Join(versionDir, migrateDigest)
	if err := os.MkdirAll(digestDir, 0o755); err != nil {
		t.Fatalf("mkdir digest dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(digestDir, "echo"), []byte("new"), 0o755); err != nil {
		t.Fatalf("write digest artifact: %v", err)
	}
	complete := capabilityruntime.InstallRecord{
		Type:         migrateType,
		Version:      migrateVersion,
		Digest:       migrateDigest,
		RootDir:      digestDir,
		ManifestPath: filepath.Join(digestDir, capabilityrt.ManifestFile),
		ArtifactPath: digestDir,
	}
	if err := capabilityruntime.WriteInstallRecord(filepath.Join(digestDir, capabilityrt.InstallFile), complete); err != nil {
		t.Fatalf("write digest record: %v", err)
	}

	installed, err := s.List(context.Background(), "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(installed) != 1 {
		t.Fatalf("len = %d, want 1 (flat copy and digest copy must dedupe)", len(installed))
	}
	if installed[0].RootDir != digestDir {
		t.Errorf("RootDir = %q, want %q", installed[0].RootDir, digestDir)
	}
	if _, err := os.Stat(filepath.Join(versionDir, capabilityrt.InstallFile)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("flat leftover install.json still present: %v", err)
	}
}

func TestRemoveDeletesFlatInstall(t *testing.T) {
	s := newStore(t)
	versionDir := writeFlatInstall(t, s.Root(), true)

	if err := s.Remove(context.Background(), migrateType, migrateVersion); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(versionDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("version directory still present after Remove: %v", err)
	}
}

func TestMigrateRejectsUnusableDigest(t *testing.T) {
	versionDir := writeFlatInstall(t, newStore(t).Root(), true)
	rec, err := capabilityruntime.ReadInstallRecord(filepath.Join(versionDir, capabilityrt.InstallFile))
	if err != nil {
		t.Fatalf("read record: %v", err)
	}
	rec.Digest = "../escape"
	if err := capabilityruntime.WriteInstallRecord(filepath.Join(versionDir, capabilityrt.InstallFile), *rec); err != nil {
		t.Fatalf("write record: %v", err)
	}

	if _, err := migrateFlatInstall(versionDir); err == nil {
		t.Fatal("migrateFlatInstall must reject a digest that is not a single path segment")
	} else if !strings.Contains(err.Error(), "reinstall") {
		t.Errorf("error = %q, want an actionable reinstall message", err)
	}
}

// TestMigrateConvergesAfterInterruption pins the crash-recovery contract: a
// migration that moved files but never published the rewritten record must
// finish on the next attempt instead of stranding the install.
func TestMigrateConvergesAfterInterruption(t *testing.T) {
	s := newStore(t)
	versionDir := writeFlatInstall(t, s.Root(), true)

	// Interrupted state: files moved into the digest directory, but the flat
	// record was never rewritten or removed.
	digestDir := filepath.Join(versionDir, migrateDigest)
	if err := os.MkdirAll(digestDir, 0o755); err != nil {
		t.Fatalf("mkdir digest dir: %v", err)
	}
	for _, name := range []string{"echo", "pkg.tar.gz", capabilityrt.ManifestFile} {
		if err := os.Rename(filepath.Join(versionDir, name), filepath.Join(digestDir, name)); err != nil {
			t.Fatalf("move %s: %v", name, err)
		}
	}

	finalDir, err := migrateFlatInstall(versionDir)
	if err != nil {
		t.Fatalf("migrateFlatInstall: %v", err)
	}
	if finalDir != digestDir {
		t.Errorf("finalDir = %q, want %q", finalDir, digestDir)
	}
	assertMigrated(t, versionDir)
}
