package executor_test

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Muhammad-Jay/neuron/application/executor"
	"github.com/Muhammad-Jay/neuron/application/executor/source/local"
	execstore "github.com/Muhammad-Jay/neuron/application/executor/store"
	shadexec "github.com/Muhammad-Jay/neuron/shared/types/executor"
)

// wasmMagicHeader is the byte prefix every WebAssembly module starts with.
var wasmMagicHeader = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

// writeWasmExecutorPackage lays out a wasm executor package in the local
// registry layout:
//
//	<root>/example/echo/1.0.0/executor.json
//	<root>/example/echo/1.0.0/echo.wasm
func writeWasmExecutorPackage(t *testing.T, root, typ, version string) string {
	t.Helper()

	split, err := executor.ParseType(typ)
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(root, filepath.FromSlash(split.ToLocalPath()), version)
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}

	wasmPath := filepath.Join(base, "echo.wasm")
	if err := os.WriteFile(wasmPath, wasmMagicHeader, 0o644); err != nil {
		t.Fatal(err)
	}

	m := &shadexec.Manifest{
		APIVersion: shadexec.APIVersion,
		Kind:       shadexec.Kind,
		Metadata: shadexec.ManifestMetadata{
			Name:        typ,
			Version:     version,
			Description: "test wasm executor",
		},
		Runtime: shadexec.ManifestRuntime{
			Type:       shadexec.RuntimeKindWasm,
			Entrypoint: "echo.wasm",
			Protocol:   shadexec.ProtocolJSONV1,
		},
		Services:     []string{"echo"},
		Capabilities: []string{"io.echo"},
		Platforms: map[string]shadexec.Platform{
			shadexec.ExecutorPlatformWasm: {Artifact: "echo.wasm"},
		},
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, shadexec.ManifestFile), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return base
}

// writePackageArchive wraps a set of root-level files (executor.json plus the
// entrypoint) into the canonical <name>-<version>-executor.neuron.tar.gz and
// returns the archive path. The archive lacks the standalone executor.json in
// the version directory, mirroring a released GitHub package: the inner
// manifest is authoritative.
func writePackageArchive(t *testing.T, rootDir, typ, version string, files map[string][]byte) string {
	t.Helper()
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(rootDir, shadexec.PackageArchiveName(typ, version))

	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: name,
			Mode: 0o755,
			Size: int64(len(content)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return archivePath
}

// writePackageArchiveExecutor writes a canonical package archive (no
// standalone executor.json) for an executor that ships both a wasm and a
// native artifact. entrypoint and payload follow runtimeKind.
func writePackageArchiveExecutor(t *testing.T, rootDir, typ, version, runtimeKind string) string {
	t.Helper()
	entrypoint := "echo.wasm"
	files := map[string][]byte{shadexec.ManifestFile: nil}
	if runtimeKind == shadexec.RuntimeKindProcess {
		entrypoint = "executor.sh"
		files["executor.sh"] = []byte("#!/bin/sh\necho '{\"output\":{}}'\n")
	} else {
		files["echo.wasm"] = wasmMagicHeader
	}

	m := &shadexec.Manifest{
		APIVersion: shadexec.APIVersion,
		Kind:       shadexec.Kind,
		Metadata: shadexec.ManifestMetadata{
			Name:        typ,
			Version:     version,
			Description: "package archive executor",
		},
		Runtime: shadexec.ManifestRuntime{
			Type:       runtimeKind,
			Entrypoint: entrypoint,
			Protocol:   shadexec.ProtocolJSONV1,
		},
		Services: []string{"echo"},
		Platforms: map[string]shadexec.Platform{
			shadexec.ExecutorPlatformWasm: {Artifact: "echo.wasm"},
			executor.HostPlatform():       {Artifact: "executor.sh"},
		},
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	files[shadexec.ManifestFile] = data
	return writePackageArchive(t, rootDir, typ, version, files)
}

// installViaLocal resolves+installs typ from a local registry and returns the
// installed record.
func installViaLocal(t *testing.T, regRoot, storeRoot string, req executor.Requirement) (*executor.Installed, error) {
	t.Helper()
	reg, err := local.New(regRoot)
	if err != nil {
		t.Fatal(err)
	}
	fsStore, err := execstore.NewFilesystemStore(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	catalog := executor.NewRegistry()
	if err := catalog.Add(reg); err != nil {
		t.Fatal(err)
	}
	installer := &executor.Installer{Store: fsStore, Downloader: executor.NewHTTPDownloader()}
	resolver := executor.NewResolver(catalog, fsStore, installer)
	return resolver.Resolve(context.Background(), req)
}

// writeExecutorPackage lays out a local registry package:
//
//	<root>/github/read/1.2.0/executor.json
//	<root>/github/read/1.2.0/executor.sh
func writeExecutorPackage(t *testing.T, root, typ, version string) string {
	t.Helper()

	split, err := executor.ParseType(typ)
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(root, filepath.FromSlash(split.ToLocalPath()), version)
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}

	entrypoint := "executor.sh"
	executorPath := filepath.Join(base, entrypoint)
	if err := os.WriteFile(executorPath, []byte("#!/bin/sh\necho '{\"output\":{}}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	m := &shadexec.Manifest{
		APIVersion: shadexec.APIVersion,
		Kind:       shadexec.Kind,
		Metadata: shadexec.ManifestMetadata{
			Name:        typ,
			Version:     version,
			Description: "test executor",
		},
		Runtime: shadexec.ManifestRuntime{
			Type:       "process",
			Entrypoint: entrypoint,
			Protocol:   shadexec.ProtocolJSONV1,
		},
		Services:     []string{"read"},
		Capabilities: []string{"io.read"},
		Platforms: map[string]shadexec.Platform{
			executor.HostPlatform(): {Artifact: entrypoint},
		},
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, shadexec.ManifestFile), data, 0o644); err != nil {
		t.Fatal(err)
	}

	return base
}

func TestLocalRegistryPipelineTest(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	writeExecutorPackage(t, root, "github:read", "1.2.0")
	writeExecutorPackage(t, root, "github:read", "2.0.0")
	writeExecutorPackage(t, root, "Muhammad-Jay:github:read", "1.0.0")

	reg, err := local.New(root)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}

	types, err := reg.Types(ctx)
	if err != nil {
		t.Fatalf("Types: %v", err)
	}
	if len(types) != 2 {
		t.Fatalf("Types = %v, want 2", types)
	}

	versions, err := reg.Versions(ctx, "github:read")
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("Versions = %v, want 2", versions)
	}

	pkg, err := reg.Package(ctx, "github:read", "2.0.0")
	if err != nil {
		t.Fatalf("Package: %v", err)
	}
	if pkg.Version != "2.0.0" {
		t.Errorf("Package.Version = %q", pkg.Version)
	}
	if pkg.Artifact.URL == "" {
		t.Error("Package.Artifact should be bound to the local file")
	}
}

func TestResolveInstallPipeline(t *testing.T) {
	ctx := context.Background()

	regRoot := t.TempDir()
	storeRoot := filepath.Join(t.TempDir(), "store")
	writeExecutorPackage(t, regRoot, "github:read", "1.2.0")
	writeExecutorPackage(t, regRoot, "github:read", "2.0.0")

	reg, err := local.New(regRoot)
	if err != nil {
		t.Fatal(err)
	}

	fsStore, err := execstore.NewFilesystemStore(storeRoot)
	if err != nil {
		t.Fatal(err)
	}

	catalog := executor.NewRegistry()
	if err := catalog.Add(reg); err != nil {
		t.Fatal(err)
	}

	installer := &executor.Installer{Store: fsStore, Downloader: executor.NewHTTPDownloader()}
	resolver := executor.NewResolver(catalog, fsStore, installer)

	// Constrained: ^1 resolves to 1.2.0 only.
	req := executor.Requirement{Type: "github:read", Version: "^1.0.0", Registries: []string{"local"}}
	installed, err := resolver.Resolve(ctx, req)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if installed.Version != "1.2.0" {
		t.Errorf("resolved version = %q, want 1.2.0", installed.Version)
	}
	if installed.Registry != "local" {
		t.Errorf("registry = %q, want local", installed.Registry)
	}

	// Installed-first: resolving again returns the same artifact.
	again, err := resolver.Resolve(ctx, req)
	if err != nil {
		t.Fatalf("Resolve again: %v", err)
	}
	if again.Version != installed.Version || again.RootDir != installed.RootDir {
		t.Errorf("installed-first violated: %+v vs %+v", installed, again)
	}

	// Latest floating resolves to 2.0.0 and is installed.
	if _, err := resolver.Resolve(ctx, executor.Requirement{Type: "github:read", Registries: []string{"local"}}); err != nil {
		t.Fatalf("Resolve latest: %v", err)
	}

	list, err := fsStore.List(ctx, "github:read")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("store list = %d, want 2", len(list))
	}

	// Get + remove.
	got, err := fsStore.Get(ctx, "github:read", "1.2.0")
	if err != nil {
		t.Fatal(err)
	}
	if got.RootDir == "" {
		t.Error("installed RootDir should be set")
	}
	if err := fsStore.Remove(ctx, "github:read", "1.2.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := fsStore.Get(ctx, "github:read", "1.2.0"); err == nil {
		t.Error("expected ErrNotFound after remove")
	} else if !errors.Is(err, executor.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestResolveUnconfiguredRegistry(t *testing.T) {
	ctx := context.Background()

	fsStore, err := execstore.NewFilesystemStore(filepath.Join(t.TempDir(), "store"))
	if err != nil {
		t.Fatal(err)
	}

	catalog := executor.NewRegistry()
	installer := &executor.Installer{Store: fsStore, Downloader: executor.NewHTTPDownloader()}
	resolver := executor.NewResolver(catalog, fsStore, installer)

	req := executor.Requirement{Type: "github:read", Registries: []string{"nope"}}
	_, err = resolver.Resolve(ctx, req)
	if err == nil {
		t.Fatal("expected error for unconfigured registry")
	}
	if !errors.Is(err, executor.ErrRegistryNotConfigured) {
		t.Errorf("want ErrRegistryNotConfigured, got %v", err)
	}
}

func TestResolveForceReinstalls(t *testing.T) {
	// ResolveForce bypasses the installed-store fast path and reintalls the
	// selected version even when it is already present: the registry is
	// consulted and the artifact is fetched, verified, and committed fresh.
	ctx := context.Background()

	regRoot := t.TempDir()
	storeRoot := filepath.Join(t.TempDir(), "store")
	writeExecutorPackage(t, regRoot, "github:read", "1.2.0")

	reg, err := local.New(regRoot)
	if err != nil {
		t.Fatal(err)
	}
	fsStore, err := execstore.NewFilesystemStore(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	catalog := executor.NewRegistry()
	if err := catalog.Add(reg); err != nil {
		t.Fatal(err)
	}
	installer := &executor.Installer{Store: fsStore, Downloader: executor.NewHTTPDownloader()}
	resolver := executor.NewResolver(catalog, fsStore, installer)

	req := executor.Requirement{Type: "github:read", Version: "1.2.0", Registries: []string{"local"}}

	if _, err := resolver.Resolve(ctx, req); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// An exact-pin resolve is idempotent: the store fast path answers without
	// installing anything.
	cached := &recordingObserver{}
	resolver.Observer = cached
	if _, err := resolver.Resolve(ctx, req); err != nil {
		t.Fatalf("Resolve cached: %v", err)
	}
	if len(cached.events) != 2 || cached.events[1] != "already:github:read@1.2.0" {
		t.Fatalf("cached resolve events = %v, want [resolve:github:read already:github:read@1.2.0]", cached.events)
	}

	// Force reinstalls from the registry: the installer runs again and the
	// store keeps exactly one entry for the type/version.
	forced := &recordingObserver{}
	installer.Observer = forced
	resolver.Observer = forced
	if _, err := resolver.ResolveForce(ctx, req); err != nil {
		t.Fatalf("ResolveForce: %v", err)
	}
	if len(forced.events) != 3 || forced.events[1] != "install:github:read@1.2.0" {
		t.Fatalf("force resolve events = %v, want [resolve:github:read install:github:read@1.2.0 installed:github:read@1.2.0]", forced.events)
	}

	list, err := fsStore.List(ctx, "github:read")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("store entries = %d, want exactly 1 after force reinstall", len(list))
	}
}

func TestResolveForceFloating(t *testing.T) {
	// A floating requirement under force still consults the registries and
	// reinstalls the newest matching version.
	ctx := context.Background()

	regRoot := t.TempDir()
	storeRoot := filepath.Join(t.TempDir(), "store")
	writeExecutorPackage(t, regRoot, "github:read", "1.2.0")
	writeExecutorPackage(t, regRoot, "github:read", "2.0.0")

	reg, err := local.New(regRoot)
	if err != nil {
		t.Fatal(err)
	}
	fsStore, err := execstore.NewFilesystemStore(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	catalog := executor.NewRegistry()
	if err := catalog.Add(reg); err != nil {
		t.Fatal(err)
	}
	installer := &executor.Installer{Store: fsStore, Downloader: executor.NewHTTPDownloader()}
	resolver := executor.NewResolver(catalog, fsStore, installer)

	req := executor.Requirement{Type: "github:read", Registries: []string{"local"}}

	if _, err := resolver.Resolve(ctx, req); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	forced, err := resolver.ResolveForce(ctx, req)
	if err != nil {
		t.Fatalf("ResolveForce: %v", err)
	}
	if forced.Version != "2.0.0" {
		t.Errorf("forced version = %q, want 2.0.0", forced.Version)
	}
	if forced.RootDir == "" {
		t.Error("forced install has no RootDir")
	}
}

func TestFrozenJSONRoundTrip(t *testing.T) {
	ctx := context.Background()

	regRoot := t.TempDir()
	storeRoot := filepath.Join(t.TempDir(), "store")
	writeExecutorPackage(t, regRoot, "Muhammad-Jay:github:read", "1.2.0")

	reg, err := local.New(regRoot)
	if err != nil {
		t.Fatal(err)
	}
	fsStore, err := execstore.NewFilesystemStore(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	catalog := executor.NewRegistry()
	if err := catalog.Add(reg); err != nil {
		t.Fatal(err)
	}
	installer := &executor.Installer{Store: fsStore, Downloader: executor.NewHTTPDownloader()}
	resolver := executor.NewResolver(catalog, fsStore, installer)

	installed, err := resolver.Resolve(ctx, executor.Requirement{Type: "Muhammad-Jay:github:read", Version: "^1.0.0", Registries: []string{"local"}})
	if err != nil {
		t.Fatal(err)
	}

	frozen := installed.Frozen("^1.0.0")
	if frozen.Type != "Muhammad-Jay:github:read" {
		t.Errorf("Frozen.Type = %q", frozen.Type)
	}
	if frozen.ResolvedVersion != "1.2.0" {
		t.Errorf("Frozen.ResolvedVersion = %q", frozen.ResolvedVersion)
	}
	if frozen.RequestedVersion != "^1.0.0" {
		t.Errorf("Frozen.RequestedVersion = %q", frozen.RequestedVersion)
	}
	if frozen.Registry != "local" {
		t.Errorf("Frozen.Registry = %q", frozen.Registry)
	}

	// Wire round-trip through JSON keeps all fields.
	data, err := json.Marshal(frozen)
	if err != nil {
		t.Fatal(err)
	}
	var back shadexec.ResolvedExecutor
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.ResolvedVersion != frozen.ResolvedVersion || back.Type != frozen.Type {
		t.Errorf("JSON round-trip mismatch: %+v", back)
	}
}

func TestPackageArchiveResolveInstall(t *testing.T) {
	// A version directory containing ONLY the canonical package archive (no
	// standalone executor.json) resolves and installs. This is the GitHub
	// distribution shape: the manifest inside the archive is authoritative.
	regRoot := t.TempDir()
	versionDir := filepath.Join(regRoot, "example", "echo", "1.0.0")
	writePackageArchiveExecutor(t, versionDir, "example:echo", "1.0.0", shadexec.RuntimeKindWasm)

	installed, err := installViaLocal(t, regRoot, filepath.Join(t.TempDir(), "store"),
		executor.Requirement{Type: "example:echo", Version: "1.0.0", Registries: []string{"local"}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if installed.Runtime.Type != shadexec.RuntimeKindWasm {
		t.Errorf("installed runtime type = %q, want %q", installed.Runtime.Type, shadexec.RuntimeKindWasm)
	}
	if installed.Platform != shadexec.ExecutorPlatformWasm {
		t.Errorf("installed platform = %q, want %q", installed.Platform, shadexec.ExecutorPlatformWasm)
	}
	if installed.Runtime.Entrypoint != "echo.wasm" {
		t.Errorf("installed entrypoint = %q, want echo.wasm", installed.Runtime.Entrypoint)
	}
}

func TestPackageArchiveVersionMismatch(t *testing.T) {
	// The archive's inner manifest claims type 9.9.9 under the 1.0.0 package
	// version: the install must reject the foreign artifact.
	regRoot := t.TempDir()
	versionDir := filepath.Join(regRoot, "example", "echo", "1.0.0")
	data, err := json.MarshalIndent(&shadexec.Manifest{
		APIVersion: shadexec.APIVersion,
		Kind:       shadexec.Kind,
		Metadata:   shadexec.ManifestMetadata{Name: "example:echo", Version: "9.9.9"},
		Runtime: shadexec.ManifestRuntime{
			Type:       shadexec.RuntimeKindWasm,
			Entrypoint: "echo.wasm",
			Protocol:   shadexec.ProtocolJSONV1,
		},
		Services: []string{"echo"},
		Platforms: map[string]shadexec.Platform{
			shadexec.ExecutorPlatformWasm: {Artifact: "echo.wasm"},
		},
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writePackageArchive(t, versionDir, "example:echo", "1.0.0", map[string][]byte{
		shadexec.ManifestFile: data,
		"echo.wasm":           wasmMagicHeader,
	})

	_, err = installViaLocal(t, regRoot, filepath.Join(t.TempDir(), "store"),
		executor.Requirement{Type: "example:echo", Version: "1.0.0", Registries: []string{"local"}})
	if err == nil {
		t.Fatal("expected install to reject mismatched archive version")
	}
	if !errors.Is(err, executor.ErrManifestInvalid) {
		t.Errorf("want ErrManifestInvalid, got %v", err)
	}
}

func TestStandaloneManifestIdentityReconciled(t *testing.T) {
	// B4: the identity reconciliation runs even when the registry supplied the
	// manifest (pkg.Manifest != nil). A standalone executor.json whose
	// metadata.name claims another executor must not install under the
	// resolved type path.
	regRoot := t.TempDir()
	versionDir := filepath.Join(regRoot, "example", "echo", "1.0.0")
	data, err := json.MarshalIndent(&shadexec.Manifest{
		APIVersion: shadexec.APIVersion,
		Kind:       shadexec.Kind,
		Metadata:   shadexec.ManifestMetadata{Name: "other:echo", Version: "1.0.0"},
		Runtime: shadexec.ManifestRuntime{
			Type:       shadexec.RuntimeKindWasm,
			Entrypoint: "echo.wasm",
			Protocol:   shadexec.ProtocolJSONV1,
		},
		Services: []string{"echo"},
		Platforms: map[string]shadexec.Platform{
			shadexec.ExecutorPlatformWasm: {Artifact: "echo.wasm"},
		},
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, shadexec.ManifestFile), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, "echo.wasm"), wasmMagicHeader, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = installViaLocal(t, regRoot, filepath.Join(t.TempDir(), "store"),
		executor.Requirement{Type: "example:echo", Version: "1.0.0", Registries: []string{"local"}})
	if err == nil {
		t.Fatal("expected install to reject mismatched standalone manifest name")
	}
	if !errors.Is(err, executor.ErrManifestInvalid) {
		t.Errorf("want ErrManifestInvalid, got %v", err)
	}
}

func TestStandaloneWasmPlatformRecorded(t *testing.T) {
	// A wasm executor spread across a version directory (no archive) binds
	// the wasm32-wasi platform key and records it at install time.
	regRoot := t.TempDir()
	writeWasmExecutorPackage(t, regRoot, "example:echo-wasm", "1.0.0")

	installed, err := installViaLocal(t, regRoot, filepath.Join(t.TempDir(), "store"),
		executor.Requirement{Type: "example:echo-wasm", Version: "1.0.0", Registries: []string{"local"}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if installed.Platform != shadexec.ExecutorPlatformWasm {
		t.Errorf("installed platform = %q, want %q", installed.Platform, shadexec.ExecutorPlatformWasm)
	}
}

func TestInstallRejectsRuntimeArtifactMismatch(t *testing.T) {
	cases := []struct {
		name       string
		runtime    string
		entrypoint string
		payload    []byte
		platform   string
	}{
		{
			name:       "wasm runtime with shell entrypoint",
			runtime:    shadexec.RuntimeKindWasm,
			entrypoint: "echo.sh",
			payload:    []byte("#!/bin/sh\n"),
			platform:   shadexec.ExecutorPlatformWasm,
		},
		{
			name:       "process runtime with wasm entrypoint",
			runtime:    shadexec.RuntimeKindProcess,
			entrypoint: "evil.wasm",
			payload:    wasmMagicHeader,
			platform:   executor.HostPlatform(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			regRoot := t.TempDir()
			split, err := executor.ParseType("example:echo")
			if err != nil {
				t.Fatal(err)
			}
			versionDir := filepath.Join(regRoot, filepath.FromSlash(split.ToLocalPath()), "1.0.0")
			if err := os.MkdirAll(versionDir, 0o755); err != nil {
				t.Fatal(err)
			}
			data, err := json.MarshalIndent(&shadexec.Manifest{
				APIVersion: shadexec.APIVersion,
				Kind:       shadexec.Kind,
				Metadata:   shadexec.ManifestMetadata{Name: "example:echo", Version: "1.0.0"},
				Runtime: shadexec.ManifestRuntime{
					Type:       tc.runtime,
					Entrypoint: tc.entrypoint,
					Protocol:   shadexec.ProtocolJSONV1,
				},
				Services: []string{"echo"},
				Platforms: map[string]shadexec.Platform{
					tc.platform: {Artifact: tc.entrypoint},
				},
			}, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(versionDir, shadexec.ManifestFile), data, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(versionDir, tc.entrypoint), tc.payload, 0o755); err != nil {
				t.Fatal(err)
			}

			_, err = installViaLocal(t, regRoot, filepath.Join(t.TempDir(), "store"),
				executor.Requirement{Type: "example:echo", Version: "1.0.0", Registries: []string{"local"}})
			if err == nil {
				t.Fatal("expected install to reject runtime/artifact mismatch")
			}
			if !errors.Is(err, executor.ErrManifestInvalid) {
				t.Errorf("want ErrManifestInvalid, got %v", err)
			}
		})
	}
}

func TestPackageArchiveResolveInstallProcess(t *testing.T) {
	// A process package archive installs under the host platform key.
	regRoot := t.TempDir()
	versionDir := filepath.Join(regRoot, "example", "echo", "1.0.0")
	writePackageArchiveExecutor(t, versionDir, "example:echo", "1.0.0", shadexec.RuntimeKindProcess)

	installed, err := installViaLocal(t, regRoot, filepath.Join(t.TempDir(), "store"),
		executor.Requirement{Type: "example:echo", Version: "1.0.0", Registries: []string{"local"}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if installed.Runtime.Type != shadexec.RuntimeKindProcess {
		t.Errorf("installed runtime type = %q, want %q", installed.Runtime.Type, shadexec.RuntimeKindProcess)
	}
	if installed.Platform != executor.HostPlatform() {
		t.Errorf("installed platform = %q, want %q", installed.Platform, executor.HostPlatform())
	}
	want := "executor.sh"
	if installed.Runtime.Entrypoint != want {
		t.Errorf("installed entrypoint = %q, want %q", installed.Runtime.Entrypoint, want)
	}
}
