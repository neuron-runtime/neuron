// Package local implements a directory-backed capability runtime registry. It exists so
// the full resolve → version-select → package → install pipeline works offline
// and in tests without a network, and it demonstrates that registries are
// pluggable: "local" is just another source.Registry.
//
// Layout (mirrors the installed store, minus install.json):
//
//	<root>/github/read/1.2.0/
//	    runtime.json
//	    github-read-linux-amd64        <- optional platform binary/archive
//
// A Registry serves one or more roots. Multiple roots behave as one "local"
// registry: types and versions are the union across roots, so the project's
// own ./neuron/capabilityRuntimes and any configured localRoots share a single
// resolution space.
package local

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/neuron-runtime/neuron/application/capabilityruntime"
	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
)

// VersionSpec is a resolved view of one version directory under a local root.
// It binds the manifest to the concrete artifact payload the directory ships,
// and carries the build hint the CLI uses to materialize the payload when it
// does not exist yet.
type VersionSpec struct {
	// Root is the version directory (the build/mandate working directory).
	Root string

	// ManifData is the raw runtime.json bytes, nil when the directory ships
	// only a canonical package archive (whose inner manifest is then
	// authoritative and reconciled at install time).
	ManifestData []byte

	// Manifest is the parsed runtime.json, nil when only an archive exists.
	Manifest *capabilityrt.Manifest

	// Type is the logical capability runtime type (e.g. "example:echo").
	Type string

	// Version is the package version (e.g. "1.0.0").
	Version string

	// Artifact is the absolute path to the resolved artifact payload: the
	// canonical package archive, manifest artifact.path, or the platform
	// artifact for the runtime kind. Empty when the directory declares no
	// material artifact.
	Artifact string

	// BuildCommand is the manifest's build.command ("" when none).
	BuildCommand string
}

// DiscoverVersion reads one version directory under root and reports what it
// ships. It never runs builds; it only describes. Payload discovery order:
//
//  1. canonical package archive
//  2. manifest artifact.path (file or directory)
//  3. platform artifact for the manifest's runtime kind
//
// A directory with no manifest and no archive is ErrNotFound.
func DiscoverVersion(root, typ, version string) (*VersionSpec, error) {
	versionDir, err := versionDirFor(root, typ, version)
	if err != nil {
		return nil, err
	}

	spec := &VersionSpec{Root: versionDir, Type: typ, Version: version}

	// The standalone runtime.json is optional when the version directory
	// ships an capability runtime package archive; the archive's inner manifest is then
	// authoritative and reconciled at install time.
	manifestPath := filepath.Join(versionDir, capabilityrt.ManifestFile)
	data, err := os.ReadFile(manifestPath)
	if err == nil {
		m, err := capabilityruntime.ParseManifest(data)
		if err != nil {
			return nil, err
		}
		spec.ManifestData = data
		spec.Manifest = m
		spec.BuildCommand = m.BuildCommand()
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	// The capability runtime package archive is the preferred payload.
	if archive, ok := archivePayload(versionDir, typ, version); ok {
		spec.Artifact = archive
		return spec, nil
	}

	if spec.Manifest == nil {
		return nil, capabilityruntime.ErrNotFound
	}

	// artifact.path from the manifest, if declared and present.
	if spec.Manifest.Artifact != nil && strings.TrimSpace(spec.Manifest.Artifact.Path) != "" {
		p := filepath.Join(versionDir, filepath.FromSlash(spec.Manifest.Artifact.Path))
		spec.Artifact = p
		return spec, nil
	}

	// Platform artifact for the runtime kind.
	if platform, ok := spec.Manifest.Platforms[capabilityruntime.PlatformForRuntime(spec.Manifest.Runtime.Type)]; ok && platform.Artifact != "" {
		spec.Artifact = filepath.Join(versionDir, filepath.FromSlash(platform.Artifact))
	}

	return spec, nil
}

// Registry serves capability runtime packages laid out under one or more local roots.
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
			return nil, capabilityruntime.ErrNotFound // malformed config: no packages available
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		if info, err := os.Stat(abs); err != nil || !info.IsDir() {
			return nil, capabilityruntime.ErrNotFound
		}
		valid = append(valid, abs)
	}
	if len(valid) == 0 {
		return nil, capabilityruntime.ErrNotFound
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
		return nil, capabilityruntime.ErrNotFound
	}

	sort.Slice(versions, func(i, j int) bool {
		return versions[i] > versions[j]
	})
	return versions, nil
}

func (r *Registry) Package(ctx context.Context, typ, version string) (*capabilityruntime.Package, error) {
	for _, root := range r.roots {
		pkg, err := packageFromVersionDir(root, typ, version)
		if err == nil {
			return pkg, nil
		}
		if !isNotFound(err) {
			return nil, err
		}
	}
	return nil, capabilityruntime.ErrNotFound
}

// PackageFromSpec builds the wire Package a spec describes. The artifact URL
// is file-backed against the resolved payload; when the payload is absent the
// Package carries no artifact and materialization fails later.
func PackageFromSpec(spec *VersionSpec) *capabilityruntime.Package {
	if spec == nil {
		return nil
	}
	pkg := &capabilityruntime.Package{
		Type:     spec.Type,
		Version:  spec.Version,
		Registry: "local",
		Artifact: artifactOf(spec),
	}
	if spec.Manifest == nil {
		return pkg
	}
	pkg.Manifest = spec.ManifestData
	pkg.Description = spec.Manifest.Metadata.Description
	pkg.Capabilities = spec.Manifest.Capabilities
	pkg.Capabilities = spec.Manifest.Capabilities
	pkg.Platforms = spec.Manifest.Platforms
	pkg.Runtime = capabilityruntime.RuntimeSpec{
		Type:       spec.Manifest.Runtime.Type,
		Entrypoint: spec.Manifest.Runtime.Entrypoint,
		Protocol:   spec.Manifest.Runtime.Protocol,
		MaxWorkers: spec.Manifest.Runtime.MaxWorkers,
	}
	return pkg
}

// packageFromVersionDir builds the Package for typ@version stored under root.
func packageFromVersionDir(root, typ, version string) (*capabilityruntime.Package, error) {
	spec, err := DiscoverVersion(root, typ, version)
	if err != nil {
		return nil, err
	}
	if spec.Manifest == nil && spec.Artifact == "" {
		return nil, capabilityruntime.ErrNotFound
	}
	return PackageFromSpec(spec), nil
}

// artifactOf builds the wire Artifact for a spec's resolved payload.
func artifactOf(spec *VersionSpec) capabilityruntime.Artifact {
	if spec.Artifact == "" {
		return capabilityruntime.Artifact{}
	}
	a := capabilityruntime.Artifact{
		URL:  "file://" + filepath.ToSlash(spec.Artifact),
		Name: filepath.Base(spec.Artifact),
	}
	if spec.Manifest != nil {
		if platform, ok := spec.Manifest.Platforms[capabilityruntime.PlatformForRuntime(spec.Manifest.Runtime.Type)]; ok {
			a.SHA256 = platform.SHA256
		}
	}
	return a
}

// archivePayload finds the canonical capability runtime package archive in dir,
// preferring <type>-<version>-capabilityruntime.neuron.tar.gz then any asset carrying
// the canonical package archive suffix.
func archivePayload(dir, typ, version string) (string, bool) {
	exact := filepath.Join(dir, capabilityrt.PackageArchiveName(typ, version))
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
		if strings.HasSuffix(e.Name(), capabilityrt.PackageArchiveSuffix) {
			return filepath.Join(dir, e.Name()), true
		}
	}
	return "", false
}

func typeDirFor(root, typ string) (string, error) {
	path, err := capabilityruntime.TypePath(typ)
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

// findManifests walks root and returns every runtime.json path with at least
// two parent segments (owner/.../version/runtime.json).
func (r *Registry) findManifests(root string) []string {
	var out []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if info.Name() != capabilityrt.ManifestFile {
			return nil
		}
		out = append(out, path)
		return nil
	})
	return out
}

// isNotFound reports whether err represents a missing capability runtime path.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	if err == capabilityruntime.ErrNotFound {
		return true
	}
	if os.IsNotExist(err) {
		return true
	}
	return strings.Contains(err.Error(), capabilityruntime.ErrNotFound.Error())
}
