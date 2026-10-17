package provider_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/neuron-runtime/neuron/application/internal/artifact"
	"github.com/neuron-runtime/neuron/application/provider"
	providerstore "github.com/neuron-runtime/neuron/application/provider/store"
	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
	providertypes "github.com/neuron-runtime/neuron/shared/types/provider"
)

func newInstaller(t *testing.T) (*provider.Installer, *providerstore.FileSystemStore) {
	t.Helper()
	store, err := providerstore.NewFileSystemStore(filepath.Join(t.TempDir(), "providers"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return &provider.Installer{Store: store, Downloader: provider.NewHTTPDownloader()}, store
}

func writeSource(t *testing.T, name, version string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	m := &providertypes.Manifest{
		APIVersion:   providertypes.APIVersion,
		Kind:         providertypes.Kind,
		Metadata:     providertypes.ManifestMetadata{Name: name, Version: version},
		Provider:     providertypes.ManifestProvider{ProtocolVersion: providertypes.ProtocolVersion},
		RuntimeKinds: []string{capabilityrt.RuntimeKindProcess},
		Protocols:    []string{capabilityrt.ProtocolV1, capabilityrt.ProtocolJSONV1},
		Platforms:    map[string]providertypes.Platform{"linux-amd64": {}},
		Build:        &providertypes.ManifestBuild{Command: "go build"},
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	files[providertypes.ManifestFile] = string(data)
	for rel, content := range files {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return dir
}

func TestInstallCommitsVerifiedArtifact(t *testing.T) {
	installer, _ := newInstaller(t)
	src := writeSource(t, "acme:proc", "1.0.0", map[string]string{"payload.bin": "hello"})

	res, err := installer.Install(context.Background(), &provider.Package{
		Name:     "acme:proc",
		Version:  "1.0.0",
		Registry: "local",
		Source:   src,
	})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if res.AlreadyPresent {
		t.Fatal("fresh install must not report already present")
	}
	installed := res.Installed
	if installed.Digest == "" {
		t.Fatal("digest is required")
	}
	if _, err := os.Stat(filepath.Join(installed.RootDir, provider.InstallFile)); err != nil {
		t.Fatalf("install record missing: %v", err)
	}
	rec, err := provider.ReadInstallRecord(filepath.Join(installed.RootDir, provider.InstallFile))
	if err != nil {
		t.Fatalf("read install record: %v", err)
	}
	if rec.Source != "local" {
		t.Fatalf("install record source = %q, want %q", rec.Source, "local")
	}
	if rec.Digest != installed.Digest {
		t.Fatalf("record digest %q != installed digest %q", rec.Digest, installed.Digest)
	}
	if installed.Manifest == nil || installed.Manifest.Build != nil {
		t.Fatalf("installed manifest must be sanitized (Build cleared), got %+v", installed.Manifest)
	}
	// The digest covers the committed tree minus the install record, so a
	// recomputation over the same exclusions must reproduce it.
	if err := os.Remove(filepath.Join(installed.RootDir, provider.InstallFile)); err != nil {
		t.Fatalf("remove install record: %v", err)
	}
	got, err := artifact.DigestTree(installed.RootDir)
	if err != nil {
		t.Fatalf("digest committed tree: %v", err)
	}
	if got != installed.Digest {
		t.Fatalf("committed tree digest %q != recorded digest %q", got, installed.Digest)
	}
}

func TestInstallIsIdempotentForIdenticalContent(t *testing.T) {
	installer, _ := newInstaller(t)
	src := writeSource(t, "acme:proc", "1.0.0", map[string]string{"payload.bin": "hello"})

	first, err := installer.Install(context.Background(), &provider.Package{Name: "acme:proc", Version: "1.0.0", Source: src})
	if err != nil {
		t.Fatalf("first install: %v", err)
	}
	second, err := installer.Install(context.Background(), &provider.Package{Name: "acme:proc", Version: "1.0.0", Source: src})
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if !second.AlreadyPresent {
		t.Fatal("identical reinstall must report already present")
	}
	if second.Installed.Digest != first.Installed.Digest {
		t.Fatalf("digest changed: %s != %s", second.Installed.Digest, first.Installed.Digest)
	}
}

func TestInstallRejectsConflictingContent(t *testing.T) {
	installer, _ := newInstaller(t)
	src := writeSource(t, "acme:proc", "1.0.0", map[string]string{"payload.bin": "hello"})
	if _, err := installer.Install(context.Background(), &provider.Package{Name: "acme:proc", Version: "1.0.0", Source: src}); err != nil {
		t.Fatalf("first install: %v", err)
	}

	conflicting := writeSource(t, "acme:proc", "1.0.0", map[string]string{"payload.bin": "goodbye"})
	_, err := installer.Install(context.Background(), &provider.Package{Name: "acme:proc", Version: "1.0.0", Source: conflicting})
	if !errors.Is(err, provider.ErrIntegrityConflict) {
		t.Fatalf("conflicting reinstall error = %v, want ErrIntegrityConflict", err)
	}
}

func TestInstallVerifiesDeclaredDigest(t *testing.T) {
	installer, _ := newInstaller(t)
	src := writeSource(t, "acme:proc", "1.0.0", map[string]string{"payload.bin": "hello"})

	_, err := installer.Install(context.Background(), &provider.Package{
		Name:    "acme:proc",
		Version: "1.0.0",
		Source:  src,
		Digest:  "sha256:0000000000000000000000000000000000000000000000000000000000000000",
	})
	if !errors.Is(err, artifact.ErrChecksumMismatch) {
		t.Fatalf("declared digest mismatch error = %v, want ErrChecksumMismatch", err)
	}

	first, err := installer.Install(context.Background(), &provider.Package{Name: "acme:proc", Version: "1.0.0", Source: src})
	if err != nil {
		t.Fatalf("install to learn digest: %v", err)
	}
	verified, err := installer.Install(context.Background(), &provider.Package{
		Name:    "acme:proc",
		Version: "1.0.0",
		Source:  src,
		Digest:  first.Installed.Digest,
	})
	if err != nil {
		t.Fatalf("install with correct declared digest: %v", err)
	}
	if !verified.AlreadyPresent {
		t.Fatal("expected already-present for matching declared digest")
	}
}

func TestInstallDropsStaleInstallRecordFromSource(t *testing.T) {
	installer, _ := newInstaller(t)
	src := writeSource(t, "acme:proc", "1.0.0", map[string]string{
		"payload.bin":        "hello",
		provider.InstallFile: `{"provider":"acme:proc","version":"0.9.0","digest":"sha256:stale"}`,
	})

	res, err := installer.Install(context.Background(), &provider.Package{Name: "acme:proc", Version: "1.0.0", Source: src})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	rec, err := provider.ReadInstallRecord(filepath.Join(res.Installed.RootDir, provider.InstallFile))
	if err != nil {
		t.Fatalf("read install record: %v", err)
	}
	if rec.Version != "1.0.0" || rec.Digest == "sha256:stale" {
		t.Fatalf("stale install record leaked into committed install: %+v", rec)
	}
}

func TestInstallRejectsManifestIdentityMismatch(t *testing.T) {
	installer, _ := newInstaller(t)
	src := writeSource(t, "acme:proc", "1.0.0", map[string]string{"payload.bin": "hello"})

	_, err := installer.Install(context.Background(), &provider.Package{Name: "acme:other", Version: "1.0.0", Source: src})
	if !errors.Is(err, provider.ErrManifestInvalid) {
		t.Fatalf("identity mismatch error = %v, want ErrManifestInvalid", err)
	}
	_, err = installer.Install(context.Background(), &provider.Package{Name: "acme:proc", Version: "2.0.0", Source: src})
	if !errors.Is(err, provider.ErrManifestInvalid) {
		t.Fatalf("version mismatch error = %v, want ErrManifestInvalid", err)
	}
}

func TestInstallCoexistsAcrossVersions(t *testing.T) {
	installer, store := newInstaller(t)
	ctx := context.Background()
	srcV1 := writeSource(t, "acme:proc", "1.0.0", map[string]string{"payload.bin": "v1"})
	srcV2 := writeSource(t, "acme:proc", "1.1.0", map[string]string{"payload.bin": "v2"})

	if _, err := installer.Install(ctx, &provider.Package{Name: "acme:proc", Version: "1.0.0", Source: srcV1}); err != nil {
		t.Fatalf("install 1.0.0: %v", err)
	}
	if _, err := installer.Install(ctx, &provider.Package{Name: "acme:proc", Version: "1.1.0", Source: srcV2}); err != nil {
		t.Fatalf("install 1.1.0: %v", err)
	}

	listed, err := store.List(ctx, "acme:proc")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("listed %d versions, want 2", len(listed))
	}
	if _, err := store.Get(ctx, "acme:proc", "1.0.0"); err != nil {
		t.Fatalf("get 1.0.0: %v", err)
	}
	if _, err := store.Get(ctx, "acme:proc", "1.1.0"); err != nil {
		t.Fatalf("get 1.1.0: %v", err)
	}
	if err := store.Remove(ctx, "acme:proc", "1.0.0"); err != nil {
		t.Fatalf("remove 1.0.0: %v", err)
	}
	if _, err := store.Get(ctx, "acme:proc", "1.0.0"); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("get removed version error = %v, want ErrNotFound", err)
	}
	if _, err := store.Get(ctx, "acme:proc", "1.1.0"); err != nil {
		t.Fatalf("sibling version must survive removal: %v", err)
	}
}

func TestTamperedInstallNoLongerMatchesDigest(t *testing.T) {
	installer, _ := newInstaller(t)
	src := writeSource(t, "acme:proc", "1.0.0", map[string]string{"payload.bin": "hello"})
	res, err := installer.Install(context.Background(), &provider.Package{Name: "acme:proc", Version: "1.0.0", Source: src})
	if err != nil {
		t.Fatalf("install: %v", err)
	}

	payload := filepath.Join(res.Installed.RootDir, "payload.bin")
	if err := os.WriteFile(payload, []byte("tampered"), 0o644); err != nil {
		t.Fatalf("tamper payload: %v", err)
	}
	if err := os.Remove(filepath.Join(res.Installed.RootDir, provider.InstallFile)); err != nil {
		t.Fatalf("remove install record: %v", err)
	}
	got, err := artifact.DigestTree(res.Installed.RootDir)
	if err != nil {
		t.Fatalf("digest tree: %v", err)
	}
	if got == res.Installed.Digest {
		t.Fatal("tampered tree must not reproduce the recorded digest")
	}
}
