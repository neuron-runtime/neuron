// Package local implements a directory-backed executor registry. It exists so
// the full resolve → version-select → package → install pipeline works offline
// and in tests without a network, and it demonstrates that registries are
// pluggable: "local" is just another source.Registry.
//
// Layout (mirrors the installed store, minus install.json):
//
//	<root>/github/read/1.2.0/
//	    executor.json
//	    github-read-linux-amd64        <- optional platform binary/archive
//
// A Registry serves one or more roots. Multiple roots behave as one "local"
// registry: types and versions are the union across roots, so the project's
// own ./neuron/executors and any configured localRoots share a single
// resolution space.
package local

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Muhammad-Jay/neuron/application/executor"
	shadexec "github.com/Muhammad-Jay/neuron/shared/types/executor"
)

// Registry serves executor packages laid out under one or more local roots.
type Registry struct {
	roots []string
}

// New validates and returns a local registry rooted at root.
func New(root string) (*Registry, error) {
	return NewMulti(root)
}

// NewMulti validates and returns a local registry serving every given root.
// Roots must exist and be directories; an empty root list is an error.
func NewMulti(roots ...string) (*Registry, error) {
	valid := make([]string, 0, len(roots))
	for _, root := range roots {
		if strings.TrimSpace(root) == "" {
			return nil, executor.ErrNotFound // malformed config: no packages available
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		if info, err := os.Stat(abs); err != nil || !info.IsDir() {
			return nil, executor.ErrNotFound
		}
		valid = append(valid, abs)
	}
	if len(valid) == 0 {
		return nil, executor.ErrNotFound
	}
	return &Registry{roots: valid}, nil
}

func (r *Registry) Name() string {
	return "local"
}

func (r *Registry) Types(ctx context.Context) ([]string, error) {
	seen := make(map[string]bool)
	var types []string
	for _, root := range r.roots {
		for _, manifestPath := range r.findManifests(root) {
			rel, _ := filepath.Rel(root, manifestPath)
			dir := filepath.Dir(rel)
			segments := strings.Split(filepath.ToSlash(dir), "/")
			if len(segments) < 2 {
				continue
			}
			typ := strings.Join(segments[:len(segments)-1], ":")
			if !seen[typ] {
				seen[typ] = true
				types = append(types, typ)
			}
		}
	}
	sort.Strings(types)
	return types, nil
}

func (r *Registry) Versions(ctx context.Context, typ string) ([]string, error) {
	seen := make(map[string]bool)
	var versions []string
	var firstErr error

	for _, root := range r.roots {
		dir, err := typeDirFor(root, typ)
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
		return nil, executor.ErrNotFound
	}

	sort.Slice(versions, func(i, j int) bool {
		return versions[i] > versions[j]
	})
	return versions, nil
}

func (r *Registry) Package(ctx context.Context, typ, version string) (*executor.Package, error) {
	for _, root := range r.roots {
		pkg, err := packageFromVersionDir(root, typ, version)
		if err == nil {
			return pkg, nil
		}
		if !isNotFound(err) {
			return nil, err
		}
	}
	return nil, executor.ErrNotFound
}

// packageFromVersionDir builds the Package for typ@version stored under root.
func packageFromVersionDir(root, typ, version string) (*executor.Package, error) {
	versionDir, err := versionDirFor(root, typ, version)
	if err != nil {
		return nil, err
	}

	// The standalone executor.json is optional when the version directory
	// ships an executor package archive; the archive's inner manifest is then
	// authoritative and reconciled at install time.
	manifestPath := filepath.Join(versionDir, shadexec.ManifestFile)
	data, err := os.ReadFile(manifestPath)
	var m *shadexec.Manifest
	if err == nil {
		m, err = executor.ParseManifest(data)
		if err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	pkg := &executor.Package{Type: typ, Version: version, Registry: "local"}
	if m != nil {
		pkg.Manifest = data
		pkg.Description = m.Metadata.Description
		pkg.Runtime = executor.RuntimeSpec{
			Type:       m.Runtime.Type,
			Entrypoint: m.Runtime.Entrypoint,
			Protocol:   m.Runtime.Protocol,
			MaxWorkers: m.Runtime.MaxWorkers,
		}
		pkg.Capabilities = m.Capabilities
		pkg.Services = m.Services
		pkg.Platforms = m.Platforms
	}

	// The executor package archive is the preferred payload: one file that
	// installs the whole executor. Prefer the exact <type>-<version> name,
	// then any canonical archive in the version directory.
	if archive, ok := archivePayload(versionDir, typ, version); ok {
		pkg.Artifact = executor.Artifact{
			URL:  "file://" + filepath.ToSlash(archive),
			Name: filepath.Base(archive),
		}
		return pkg, nil
	}

	if m == nil {
		return nil, executor.ErrNotFound
	}

	// No archive: bind the platform artifact for the executor's runtime kind.
	if platform, ok := m.Platforms[executor.PlatformForRuntime(m.Runtime.Type)]; ok && platform.Artifact != "" {
		artifactPath := filepath.Join(versionDir, platform.Artifact)
		if info, err := os.Stat(artifactPath); err == nil && !info.IsDir() {
			pkg.Artifact = executor.Artifact{
				URL:    "file://" + filepath.ToSlash(artifactPath),
				Name:   platform.Artifact,
				SHA256: platform.SHA256,
			}
		}
	}

	return pkg, nil
}

// archivePayload finds the canonical executor package archive in dir,
// preferring <type>-<version>-executor.neuron.tar.gz then any asset carrying
// the canonical package archive suffix.
func archivePayload(dir, typ, version string) (string, bool) {
	exact := filepath.Join(dir, shadexec.PackageArchiveName(typ, version))
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
		if strings.HasSuffix(e.Name(), shadexec.PackageArchiveSuffix) {
			return filepath.Join(dir, e.Name()), true
		}
	}
	return "", false
}

func typeDirFor(root, typ string) (string, error) {
	path, err := executor.TypePath(typ)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, path), nil
}

func versionDirFor(root, typ, version string) (string, error) {
	base, err := typeDirFor(root, typ)
	if err != nil {
		return "", err
	}
	return filepath.Join(base, version), nil
}

// findManifests walks root and returns every executor.json path with at least
// two parent segments (owner/.../version/executor.json).
func (r *Registry) findManifests(root string) []string {
	var out []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if info.Name() != shadexec.ManifestFile {
			return nil
		}
		out = append(out, path)
		return nil
	})
	return out
}

// isNotFound reports whether err represents a missing executor path.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	if err == executor.ErrNotFound {
		return true
	}
	if os.IsNotExist(err) {
		return true
	}
	return strings.Contains(err.Error(), executor.ErrNotFound.Error())
}
