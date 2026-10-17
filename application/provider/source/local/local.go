// Package local implements a directory-backed provider registry source. It
// exists so the full resolve → version-select → package → install pipeline
// works offline and in tests without a network.
//
// Layout (no implicit project root — providers are machine-global):
//
//	<root>/process/1.0.0/
//	    provider.runtime.json
//	    process-1.0.0-provider.neuron.tar.gz   <- optional canonical archive
//	    ...payload files...
//
// A source serves one or more roots. Multiple roots behave as one "local"
// registry: names and versions are the union across roots. Unlike the
// capability runtime local source, there is no project-relative implicit
// root; every root is explicit configuration.
package local

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/neuron-runtime/neuron/application/provider"
	providertypes "github.com/neuron-runtime/neuron/shared/types/provider"
)

// Registry serves provider packages laid out under one or more local roots.
type Registry struct {
	roots []string
}

// New validates and returns a local source serving every given root. Roots
// must exist and be directories; an empty root list is an error.
func New(roots ...string) (*Registry, error) {
	valid := make([]string, 0, len(roots))
	for _, root := range roots {
		if strings.TrimSpace(root) == "" {
			return nil, fmt.Errorf("local provider root is required")
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(abs)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("local provider root %q does not exist", root)
		}
		valid = append(valid, abs)
	}
	if len(valid) == 0 {
		return nil, fmt.Errorf("at least one local provider root is required")
	}
	return &Registry{roots: valid}, nil
}

func (r *Registry) Name() string {
	return "local"
}

// Versions lists the version directories under every root for a provider
// name, newest first.
func (r *Registry) Versions(ctx context.Context, name string) ([]string, error) {
	seen := make(map[string]bool)
	var versions []string
	var firstErr error

	for _, root := range r.roots {
		dir, err := typeDirFor(root, name)
		if err != nil {
			firstErr = err
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			if !os.IsNotExist(err) {
				firstErr = err
			}
			continue
		}
		for _, e := range entries {
			if e.IsDir() && !seen[e.Name()] {
				seen[e.Name()] = true
				versions = append(versions, e.Name())
			}
		}
	}

	if len(versions) == 0 {
		if firstErr != nil {
			return nil, firstErr
		}
		return nil, provider.ErrNotFound
	}

	sort.Slice(versions, func(i, j int) bool {
		return versions[i] > versions[j]
	})
	return versions, nil
}

// Package returns the immutable package for an exact version, preferring the
// canonical package archive and falling back to the version directory tree
// with the standalone manifest.
func (r *Registry) Package(ctx context.Context, name, version string) (*provider.Package, error) {
	for _, root := range r.roots {
		pkg, err := packageFromVersionDir(root, name, version)
		if err == nil {
			return pkg, nil
		}
		if !isNotFound(err) {
			return nil, err
		}
	}
	return nil, provider.ErrNotFound
}

func packageFromVersionDir(root, name, version string) (*provider.Package, error) {
	versionDir, err := versionDirFor(root, name, version)
	if err != nil {
		return nil, err
	}

	// The canonical package archive is the preferred payload; its inner
	// manifest is authoritative and reconciled at install time.
	if archive, ok := archivePayload(versionDir, name, version); ok {
		return &provider.Package{
			Name:     name,
			Version:  version,
			Registry: "local",
			Source:   archive,
		}, nil
	}

	manifestPath := filepath.Join(versionDir, providertypes.ManifestFile)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, provider.ErrNotFound
		}
		return nil, err
	}
	m, err := provider.ParseManifest(data)
	if err != nil {
		return nil, err
	}
	if m.Metadata.Name != name || m.Metadata.Version != version {
		return nil, fmt.Errorf(
			"%w: directory %s@%s carries manifest %s@%s",
			provider.ErrManifestInvalid, name, version, m.Metadata.Name, m.Metadata.Version,
		)
	}

	return &provider.Package{
		Name:     name,
		Version:  version,
		Registry: "local",
		Manifest: data,
		Source:   versionDir,
	}, nil
}

// archivePayload finds the canonical provider package archive in dir,
// preferring the exact <name>-<version> archive then any file carrying the
// canonical package archive suffix.
func archivePayload(dir, name, version string) (string, bool) {
	exact := filepath.Join(dir, providertypes.PackageArchiveName(name, version))
	if info, err := os.Stat(exact); err == nil && !info.IsDir() {
		return exact, true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), providertypes.PackageArchiveSuffix) {
			return filepath.Join(dir, e.Name()), true
		}
	}
	return "", false
}

func typeDirFor(root, name string) (string, error) {
	path, err := provider.ProviderTypePath(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, path), nil
}

func versionDirFor(root, name, version string) (string, error) {
	base, err := typeDirFor(root, name)
	if err != nil {
		return "", err
	}
	return filepath.Join(base, strings.TrimSpace(version)), nil
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	if err == provider.ErrNotFound {
		return true
	}
	return os.IsNotExist(err)
}
