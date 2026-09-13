package executor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	shadexec "github.com/Muhammad-Jay/neuron/shared/types/executor"
)

// Installer turns a resolved Package into an Installed executor using the
// store. Installation is atomic: everything happens in a staging directory
// inside the store, and the executor only becomes visible after a final
// rename.
//
//	stage        → download → verify → extract → validate executor.json → write install.json
//	rename       → <root>/<owner>/<...segments>/<version>/   (only now installed)
type Installer struct {
	Store      Store
	Downloader Downloader

	// Observer receives installation progress events. A nil observer keeps
	// installation silent.
	Observer Observer
}

// InstallResult reports what happened.
type InstallResult struct {
	Installed *Installed
	// AlreadyPresent is true when the exact version was already installed and
	// the call was idempotent (no download occurred).
	AlreadyPresent bool
}

// Install materializes pkg into the store, returning the installed record.
// The exact version already being installed is not an error.
func (i *Installer) Install(ctx context.Context, pkg *Package) (*InstallResult, error) {
	if pkg == nil {
		return nil, fmt.Errorf("package is nil")
	}

	if existing, err := i.Store.Get(ctx, pkg.Type, pkg.Version); err == nil && existing != nil {
		notify(i.Observer, func(o Observer) {
			o.Installed(InstallResult{Installed: existing, AlreadyPresent: true})
		})
		return &InstallResult{Installed: existing, AlreadyPresent: true}, nil
	}

	notify(i.Observer, func(o Observer) { o.Installing(*pkg) })

	stage, err := i.Store.Stage()
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)

	// 1. Materialize the artifact into the staging directory.
	if err := i.materialize(ctx, pkg, stage); err != nil {
		return nil, fmt.Errorf("install executor %s@%s: %w", pkg.Type, pkg.Version, err)
	}

	// 2. executor.json must be present and authoritative.
	manifestDir := stage
	manifestPath := filepath.Join(stage, shadexec.ManifestFile)

	if pkg.Manifest != nil {
		if err := os.WriteFile(manifestPath, pkg.Manifest, 0o644); err != nil {
			return nil, err
		}
	} else if _, err := os.Stat(manifestPath); err != nil {
		return nil, fmt.Errorf("%w: executor.json not found in package payload", ErrManifestInvalid)
	}

	m, err := ReadManifest(manifestPath)
	if err != nil {
		return nil, err
	}

	// When the payload is a package archive and no separate manifest was
	// fetched from the registry, the executor.json inside the archive is
	// authoritative. Reconcile it with the identity resolved from the
	// registry so a mismatched asset cannot masquerade as another executor.
	if pkg.Manifest == nil {
		if m.Metadata.Name != "" && m.Metadata.Name != pkg.Type {
			return nil, fmt.Errorf("%w: package archive carries executor %q, require %q", ErrManifestInvalid, m.Metadata.Name, pkg.Type)
		}
		if m.Metadata.Version != "" && !versionMatches(m.Metadata.Version, pkg.Version) {
			return nil, fmt.Errorf("%w: package archive declares version %q, require %q", ErrManifestInvalid, m.Metadata.Version, pkg.Version)
		}
	}

	// 3. Resolve the concrete entrypoint (may differ from the package manifest
	// when the payload is a single binary rather than a pre-extracted dir).
	entrypoint, err := i.resolveEntrypoint(pkg, stage, m.Runtime.Entrypoint)
	if err != nil {
		return nil, err
	}

	// 3.5. The materialized entrypoint must agree with the declared runtime
	// type so a mismatched package is rejected before any execution.
	if err := assertRuntimeConsistency(stage, m.Runtime.Type, entrypoint); err != nil {
		return nil, err
	}

	// 4. Persist the install record.
	rec := RecordFor(&Installed{
		Type:         pkg.Type,
		Version:      pkg.Version,
		Digest:       pkg.Digest,
		Registry:     pkg.Registry,
		Platform:     PlatformForRuntime(m.Runtime.Type),
		RootDir:      stage,
		ManifestPath: manifestPath,
		ArtifactPath: stage,
		Runtime: RuntimeSpec{
			Type:       m.Runtime.Type,
			Entrypoint: entrypoint,
			Protocol:   m.Runtime.Protocol,
			MaxWorkers: m.Runtime.MaxWorkers,
		},
		Capabilities: m.Capabilities,
		Services:     m.Services,
	})
	if err := WriteInstallRecord(filepath.Join(manifestDir, InstallFile), rec); err != nil {
		return nil, err
	}

	// 5. Atomic commit.
	installed, err := i.Store.Commit(ctx, stage, pkg.Type, pkg.Version)
	if err != nil {
		return nil, err
	}
	installed.Runtime.Entrypoint = entrypoint

	notify(i.Observer, func(o Observer) {
		o.Installed(InstallResult{Installed: installed})
	})

	return &InstallResult{Installed: installed}, nil
}

// notify invokes f against the observer, if any. It lives here as a package
// helper so both Installer and Resolver can stay readable.
func notify(o Observer, f func(Observer)) {
	if o != nil {
		f(o)
	}
}

// materialize downloads (or copies) the artifact and lays it into stage.
func (i *Installer) materialize(ctx context.Context, pkg *Package, stage string) error {
	if pkg.Artifact.URL == "" {
		// A package with no artifact (e.g. a source-only or local layout)
		// must carry the executor.json itself via pkg.Manifest.
		return nil
	}

	dst := filepath.Join(stage, ".artifact")

	if i.Downloader == nil {
		return fmt.Errorf("no downloader configured")
	}

	if err := i.Downloader.Download(ctx, pkg.Artifact.URL, dst); err != nil {
		return err
	}

	info, err := os.Stat(dst)
	if err != nil {
		return err
	}

	// Checksum verification: mandatory for single-file artifacts.
	if !info.IsDir() {
		if pkg.Artifact.SHA256 != "" {
			if err := VerifySHA256(dst, pkg.Artifact.SHA256); err != nil {
				return err
			}
		}
		if pkg.Digest == "" {
			digest, err := DigestFile(dst)
			if err != nil {
				return err
			}
			pkg.Digest = digest
		}

		if IsArchive(dst) {
			if err := ExtractTarGz(dst, stage); err != nil {
				return fmt.Errorf("extract artifact archive: %w", err)
			}
			return nil
		}

		// Single binary: place it next to the manifest.
		return placeBinary(dst, stage, fileNameOf(pkg))
	}

	// Directory payload: copy its contents into the staging root.
	return copyDirStage(dst, stage)
}

func (i *Installer) resolveEntrypoint(pkg *Package, stage, manifestEntrypoint string) (string, error) {
	if manifestEntrypoint != "" {
		p := filepath.Join(stage, filepath.FromSlash(manifestEntrypoint))
		if _, err := os.Stat(p); err == nil {
			return filepath.ToSlash(manifestEntrypoint), nil
		}
	}

	// Fall back to the artifact's own binary name (covers single-binary
	// packages whose manifest entrypoint did not match the file name).
	name := fileNameOf(pkg)
	if name != "" {
		if _, err := os.Stat(filepath.Join(stage, name)); err == nil {
			return name, nil
		}
	}

	return "", fmt.Errorf("%w: entrypoint %q not materialized", ErrManifestInvalid, manifestEntrypoint)
}

func placeBinary(src, stage, name string) error {
	if name == "" {
		return fmt.Errorf("cannot place binary artifact: no file name")
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(stage, name), data, 0o755)
}

func copyDirStage(src, stage string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(stage, entry.Name())
		if entry.IsDir() {
			if err := copyDirRecursive(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		mode := 0o644
		if entry.Type()&0o111 != 0 {
			mode = 0o755
		}
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dstPath, data, os.FileMode(mode)); err != nil {
			return err
		}
	}
	return nil
}

func copyDirRecursive(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := copyDirRecursive(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return err
		}
		mode := 0o644
		if entry.Type()&0o111 != 0 {
			mode = 0o755
		}
		if err := os.WriteFile(dstPath, data, os.FileMode(mode)); err != nil {
			return err
		}
	}
	return nil
}

func fileNameOf(pkg *Package) string {
	if name := strings.TrimSpace(pkg.Artifact.Name); name != "" {
		return name
	}
	return FileName(pkg.Artifact.URL)
}

// wasmMagic is the 4-byte WebAssembly module header (\0asm).
var wasmMagic = []byte{0x00, 'a', 's', 'm'}

// versionMatches compares two semantic versions tolerating a leading "v"
// (GitHub release tags conventionally carry one).
func versionMatches(a, b string) bool {
	trim := func(s string) string { return strings.TrimPrefix(strings.TrimSpace(s), "v") }
	return trim(a) == trim(b)
}

// assertRuntimeConsistency fast-fails an install when the materialized
// entrypoint contradicts the manifest's runtime type: a WASM runtime must
// back a WASM module and a process runtime must not. The check is advisory
// (a module's header is not proof of correct behavior) but rejects the
// overwhelmingly common mismatch of shipping the wrong artifact kind. Kinds
// whose artifacts are not files (container, remote) are skipped.
func assertRuntimeConsistency(root, runtimeType, entrypoint string) error {
	if runtimeType != shadexec.RuntimeKindProcess && runtimeType != shadexec.RuntimeKindWasm {
		return nil
	}

	f, err := os.Open(filepath.Join(root, filepath.FromSlash(entrypoint)))
	if err != nil {
		return fmt.Errorf("%w: entrypoint %q not materialized: %v", ErrManifestInvalid, entrypoint, err)
	}
	defer f.Close()

	head := make([]byte, len(wasmMagic))
	n, _ := io.ReadFull(f, head)
	isWasm := n == len(wasmMagic) && bytes.Equal(head, wasmMagic)

	switch {
	case runtimeType == shadexec.RuntimeKindWasm && !isWasm:
		return fmt.Errorf("%w: runtime type is %q but entrypoint %q is not a WebAssembly module", ErrManifestInvalid, runtimeType, entrypoint)
	case runtimeType == shadexec.RuntimeKindProcess && isWasm:
		return fmt.Errorf("%w: runtime type is %q but entrypoint %q is a WebAssembly module; declare runtime type %q or ship a native binary", ErrManifestInvalid, runtimeType, entrypoint, shadexec.RuntimeKindWasm)
	}
	return nil
}
