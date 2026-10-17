package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/neuron-runtime/neuron/application/internal/artifact"
)

// Installer installs provider artifacts into the store.
type Installer struct {
	Store Store

	// Downloader fetches Artifact URLs. It is required when a Package
	// carries no local Source; a nil Downloader rejects remote packages.
	Downloader Downloader
}

// Package describes a provider artifact to install. Exactly one distribution
// path must be present: a local Source (directory or archive), an Artifact
// URL the installer downloads, or raw Manifest bytes describing a Source
// tree that already contains the payload.
type Package struct {
	Name    string
	Version string
	// Registry records where the package came from (a registry name such as
	// "local" or "github", or a bootstrap marker). It is persisted in the
	// install record so a bootstrap install is never mistaken for a
	// registry install. It is not part of the artifact identity.
	Registry string
	// Manifest is the provider.runtime.json bytes. When empty, the
	// materialized source must already contain the manifest file.
	Manifest []byte
	// Source is the path to a directory or a .tar.gz archive to install.
	// It wins over Artifact when both are set.
	Source string
	// Artifact locates a remote or file:// package (usually the canonical
	// <name>-<version>-provider.neuron.tar.gz archive) the installer
	// downloads before installation.
	Artifact Artifact
	// Digest, when set, is the expected content digest of the staged tree.
	// A declared digest that does not match the staged content fails the
	// install; it is never trusted blindly.
	Digest string
}

// Artifact locates a downloadable provider package payload.
type Artifact struct {
	// URL is an http(s) or file:// location of the package payload.
	URL string
	// Name is the payload file name (e.g. the canonical archive name).
	Name string
	// SHA256, when set, is the expected digest of the downloaded bytes.
	SHA256 string
}

// InstallResult reports the result of installing a provider.
type InstallResult struct {
	Installed      *Installed
	AlreadyPresent bool
}

// Install materializes the provider package into the store.
//
// The flow is stage → materialize → reconcile identity → sanitize the
// persisted manifest → compute the content digest → verify a declared digest
// if one was supplied → reject a conflicting already-installed identity →
// write the install record → atomically commit. The content digest covers
// every staged file including the sanitized manifest, and excludes only the
// install record itself, which is written after the digest is computed; this
// makes the digest a verifiable property of the committed tree rather than of
// an intermediate staging state.
func (i *Installer) Install(ctx context.Context, pkg *Package) (*InstallResult, error) {
	if pkg == nil {
		return nil, fmt.Errorf("package is nil")
	}
	if i.Store == nil {
		return nil, fmt.Errorf("provider store is nil")
	}

	stage, err := i.Store.Stage()
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)

	if err := i.materialize(pkg, stage); err != nil {
		return nil, fmt.Errorf("install provider %s@%s: %w", pkg.Name, pkg.Version, err)
	}

	// A source tree may carry a stale install record — for example when the
	// source is an already-installed directory or an archive packed from
	// one. It must never be committed with the new install, so drop it
	// before the digest is computed; a fresh record is written after
	// verification.
	_ = os.Remove(filepath.Join(stage, InstallFile))

	manifestPath := filepath.Join(stage, ManifestFile)
	if len(pkg.Manifest) > 0 {
		if err := os.WriteFile(manifestPath, pkg.Manifest, 0o644); err != nil {
			return nil, err
		}
	} else if _, err := os.Stat(manifestPath); err != nil {
		return nil, fmt.Errorf("%w: provider.runtime.json not found", ErrManifestInvalid)
	}

	m, err := ReadManifest(manifestPath)
	if err != nil {
		return nil, err
	}
	if m.Metadata.Name != "" && m.Metadata.Name != pkg.Name {
		return nil, fmt.Errorf("%w: manifest name %q != %q", ErrManifestInvalid, m.Metadata.Name, pkg.Name)
	}
	if m.Metadata.Version != "" && m.Metadata.Version != pkg.Version {
		return nil, fmt.Errorf("%w: manifest version %q != %q", ErrManifestInvalid, m.Metadata.Version, pkg.Version)
	}

	san := m.Sanitized()
	data, err := json.MarshalIndent(san, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(manifestPath, data, 0o644); err != nil {
		return nil, err
	}

	digest, err := artifact.DigestTree(stage)
	if err != nil {
		return nil, err
	}
	if pkg.Digest != "" && pkg.Digest != digest {
		return nil, fmt.Errorf("declared digest %s does not match staged content %s: %w", pkg.Digest, digest, artifact.ErrChecksumMismatch)
	}
	pkg.Digest = digest

	existing, err := i.Store.Get(ctx, pkg.Name, pkg.Version)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if existing != nil {
		if existing.Digest == digest {
			return &InstallResult{Installed: existing, AlreadyPresent: true}, nil
		}
		return nil, fmt.Errorf("%w: %s@%s installed with digest %s, staged content is %s", ErrIntegrityConflict, pkg.Name, pkg.Version, existing.Digest, digest)
	}

	finalDir, err := i.Store.InstalledDir(pkg.Name, pkg.Version, digest)
	if err != nil {
		return nil, err
	}
	rec := InstallRecord{
		Provider:     pkg.Name,
		Version:      pkg.Version,
		Digest:       digest,
		Source:       pkg.Registry,
		RootDir:      finalDir,
		ManifestPath: filepath.Join(finalDir, ManifestFile),
		InstalledAt:  time.Now().UTC(),
	}
	if err := WriteInstallRecord(filepath.Join(stage, InstallFile), rec); err != nil {
		return nil, err
	}

	installed, err := i.Store.Commit(ctx, stage, pkg.Name, pkg.Version, digest)
	if err != nil {
		return nil, err
	}
	return &InstallResult{Installed: installed}, nil
}

func (i *Installer) materialize(pkg *Package, stage string) error {
	if pkg.Source != "" {
		info, err := os.Stat(pkg.Source)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return artifact.CopyDir(pkg.Source, stage)
		}
		if artifact.IsArchive(pkg.Source) {
			return artifact.ExtractTarGz(pkg.Source, stage)
		}
		return fmt.Errorf("unsupported provider source: %s", pkg.Source)
	}

	if strings.TrimSpace(pkg.Artifact.URL) != "" {
		if i.Downloader == nil {
			return fmt.Errorf("provider %s@%s: artifact %s requires a downloader", pkg.Name, pkg.Version, pkg.Artifact.URL)
		}
		downloaded, err := i.Downloader.Download(context.Background(), pkg.Artifact.URL, pkg.Artifact.SHA256)
		if err != nil {
			return err
		}
		defer os.RemoveAll(filepath.Dir(downloaded))
		if artifact.IsArchive(downloaded) {
			return artifact.ExtractTarGz(downloaded, stage)
		}
		name := pkg.Artifact.Name
		if name == "" {
			name = filepath.Base(downloaded)
		}
		data, err := os.ReadFile(downloaded)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(stage, name), data, 0o644)
	}

	return fmt.Errorf("provider source is required")
}
