package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/neuron-runtime/neuron/application/internal/artifact"
)

// Installer installs provider artifacts into the store.
type Installer struct {
	Store Store
}

// Package describes a provider artifact to install.
type Package struct {
	Name     string
	Version  string
	Manifest []byte
	Source   string // path to dir or archive
	Digest   string
}

// InstallResult reports the result of installing a provider.
type InstallResult struct {
	Installed      *Installed
	AlreadyPresent bool
}

// Install materializes the provider package into the store.
func (i *Installer) Install(ctx context.Context, pkg *Package) (*InstallResult, error) {
	if pkg == nil {
		return nil, fmt.Errorf("package is nil")
	}
	if i.Store == nil {
		return nil, fmt.Errorf("provider store is nil")
	}

	if existing, err := i.Store.Get(ctx, pkg.Name, pkg.Version); err == nil && existing != nil {
		if existing.Digest == pkg.Digest {
			return &InstallResult{Installed: existing, AlreadyPresent: true}, nil
		}
		return nil, fmt.Errorf("%w: %s@%s", ErrIntegrityConflict, pkg.Name, pkg.Version)
	}

	stage, err := i.Store.Stage()
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)

	if err := materialize(pkg, stage); err != nil {
		return nil, fmt.Errorf("install provider %s@%s: %w", pkg.Name, pkg.Version, err)
	}

	if pkg.Digest == "" {
		digest, err := artifact.DigestTree(stage)
		if err != nil {
			return nil, err
		}
		pkg.Digest = digest
	}

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

	finalDir, err := i.Store.InstalledDir(pkg.Name, pkg.Version, pkg.Digest)
	if err != nil {
		return nil, err
	}
	rec := InstallRecord{
		Provider:     pkg.Name,
		Version:      pkg.Version,
		Digest:       pkg.Digest,
		RootDir:      finalDir,
		ManifestPath: filepath.Join(finalDir, ManifestFile),
		InstalledAt:  time.Now().UTC(),
	}
	if err := WriteInstallRecord(filepath.Join(stage, InstallFile), rec); err != nil {
		return nil, err
	}

	installed, err := i.Store.Commit(ctx, stage, pkg.Name, pkg.Version, pkg.Digest)
	if err != nil {
		return nil, err
	}
	return &InstallResult{Installed: installed}, nil
}

func materialize(pkg *Package, stage string) error {
	if pkg.Source == "" {
		return fmt.Errorf("provider source is required")
	}
	info, err := os.Stat(pkg.Source)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return copyDir(pkg.Source, stage)
	}
	if artifact.IsArchive(pkg.Source) {
		return artifact.ExtractTarGz(pkg.Source, stage)
	}
	return fmt.Errorf("unsupported provider source: %s", pkg.Source)
}

func copyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := os.MkdirAll(dstPath, 0o755); err != nil {
				return err
			}
			if err := copyDir(srcPath, dstPath); err != nil {
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
