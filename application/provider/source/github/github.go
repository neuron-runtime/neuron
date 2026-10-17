// Package github implements a GitHub Releases-backed provider registry
// source. Like the capability runtime GitHub registry it is catalog-driven,
// not convention-driven: a provider name must be listed in the configured
// catalog, because guessing at repositories that may not exist is
// surprising.
//
// Distribution contract: releases are tagged v<version> and ship the
// canonical <name>-<version>-provider.neuron.tar.gz package archive as a
// release asset. When the archive asset is absent the source falls back to
// the provider.runtime.json release asset (then the raw repository file) and
// downloads every declared platform payload alongside it.
package github

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/neuron-runtime/neuron/application/internal/githubrepo"
	"github.com/neuron-runtime/neuron/application/provider"
	providertypes "github.com/neuron-runtime/neuron/shared/types/provider"
)

// manifestAssetName is the preferred provider.runtime.json location inside a
// release.
const manifestAssetName = "provider.runtime.json"

// RepoRef identifies a GitHub repository as owner/name.
type RepoRef = githubrepo.RepoRef

// Option configures a GitHub provider source.
type Option func(*Registry)

// WithClient overrides the HTTP/API client.
func WithClient(c *githubrepo.Client) Option {
	return func(r *Registry) { r.client = c }
}

// WithToken sets the API token (NEURON_GITHUB_TOKEN is honored by callers;
// this is the explicit path).
func WithToken(token string) Option {
	return func(r *Registry) {
		if r.client != nil {
			r.client.Token = token
		}
	}
}

// WithCatalog overrides the provider name → repository mapping. Names absent
// from the catalog are rejected; the source never guesses.
func WithCatalog(catalog map[string]RepoRef) Option {
	return func(r *Registry) {
		if catalog != nil {
			r.catalog = catalog
		}
	}
}

// Registry is a provider.Source backed by GitHub Releases.
type Registry struct {
	client  *githubrepo.Client
	catalog map[string]RepoRef
}

// New returns a GitHub provider source with an empty catalog.
func New(opts ...Option) *Registry {
	r := &Registry{
		client:  githubrepo.NewClient(),
		catalog: make(map[string]RepoRef),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

func (r *Registry) Name() string {
	return "github"
}

// repoFor maps a provider name to its owner/repo via the catalog only.
func (r *Registry) repoFor(name string) (RepoRef, error) {
	ref, ok := r.catalog[name]
	if !ok {
		return RepoRef{}, fmt.Errorf("provider %q is not listed in the github provider catalog", name)
	}
	if ref.Owner == "" || ref.Repo == "" {
		return RepoRef{}, fmt.Errorf("github: catalog entry for %q is incomplete", name)
	}
	return ref, nil
}

// Versions lists the semver release tags for a provider name, newest first.
func (r *Registry) Versions(ctx context.Context, name string) ([]string, error) {
	ref, err := r.repoFor(name)
	if err != nil {
		return nil, err
	}

	releases, err := r.client.ListReleases(ctx, ref.Owner, ref.Repo)
	if err != nil {
		if githubrepo.IsNotFound(err) {
			return nil, provider.ErrNotFound
		}
		return nil, err
	}

	return githubrepo.VersionsFromReleases(releases), nil
}

// Package returns the immutable package for an exact version.
func (r *Registry) Package(ctx context.Context, name, version string) (*provider.Package, error) {
	ref, err := r.repoFor(name)
	if err != nil {
		return nil, err
	}

	release, err := r.client.ReleaseByTag(ctx, ref.Owner, ref.Repo, "v"+version)
	if err != nil {
		if githubrepo.IsNotFound(err) {
			return nil, provider.ErrNotFound
		}
		return nil, err
	}

	return r.buildPackage(ctx, name, version, ref, release)
}

func (r *Registry) buildPackage(
	ctx context.Context,
	name, version string,
	ref RepoRef,
	release *githubrepo.Release,
) (*provider.Package, error) {
	// Canonical package archive distribution.
	if asset, ok := release.AssetByName(providertypes.PackageArchiveName(name, version)); ok {
		return &provider.Package{
			Name:     name,
			Version:  version,
			Registry: "github",
			Artifact: provider.Artifact{
				URL:  asset.BrowserDownloadURL,
				Name: asset.Name,
			},
		}, nil
	}

	// Fallback: ship the manifest plus every declared platform payload.
	manifestBytes, source, err := r.fetchManifest(ctx, ref, release)
	if err != nil {
		return nil, err
	}
	if len(manifestBytes) == 0 {
		return nil, fmt.Errorf(
			"provider %s@%s: release %s/%s ships neither the %s package archive nor a %s manifest",
			name, version, ref.Owner, ref.Repo, providertypes.PackageArchiveSuffix, manifestAssetName,
		)
	}

	m, err := provider.ParseManifest(manifestBytes)
	if err != nil {
		return nil, err
	}
	if m.Metadata.Name != name || m.Metadata.Version != version {
		return nil, fmt.Errorf(
			"%w: release %s carries manifest %s@%s",
			provider.ErrManifestInvalid, source, m.Metadata.Name, m.Metadata.Version,
		)
	}

	// Materialize the package as a local directory: the manifest plus the
	// downloaded platform payloads, so the installer treats it like any
	// other local tree.
	dir, err := os.MkdirTemp("", "neuron-provider-src-*")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, manifestAssetName), manifestBytes, 0o644); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	for key, platform := range m.Platforms {
		if platform.Artifact == "" {
			continue
		}
		url := platformURL(ref, release, platform.Artifact)
		data, err := r.client.Get(ctx, url)
		if err != nil {
			os.RemoveAll(dir)
			return nil, fmt.Errorf("provider %s@%s: platform %s payload %s: %w", name, version, key, platform.Artifact, err)
		}
		if err := os.WriteFile(filepath.Join(dir, platform.Artifact), data, 0o644); err != nil {
			os.RemoveAll(dir)
			return nil, err
		}
	}

	return &provider.Package{
		Name:     name,
		Version:  version,
		Registry: "github",
		Manifest: manifestBytes,
		Source:   dir,
	}, nil
}

// fetchManifest returns the validated provider.runtime.json bytes.
// Precedence: release asset named provider.runtime.json, then the raw
// repository file at the release tag.
func (r *Registry) fetchManifest(ctx context.Context, ref RepoRef, release *githubrepo.Release) ([]byte, string, error) {
	if asset, ok := release.AssetByName(manifestAssetName); ok {
		data, err := r.client.Get(ctx, asset.BrowserDownloadURL)
		if err != nil {
			return nil, "", err
		}
		return data, "asset " + asset.Name, nil
	}

	raw := githubrepo.RawFileURL(ref.Owner, ref.Repo, release.TagName, manifestAssetName)
	data, err := r.client.Get(ctx, raw)
	if err != nil {
		if githubrepo.IsNotFound(err) {
			return nil, "", nil
		}
		return nil, "", err
	}
	return data, "raw " + raw, nil
}

// platformURL finds a platform payload asset in the release, falling back to
// the raw repository file at the release tag.
func platformURL(ref RepoRef, release *githubrepo.Release, artifact string) string {
	if asset, ok := release.AssetByName(artifact); ok {
		return asset.BrowserDownloadURL
	}
	return githubrepo.RawFileURL(ref.Owner, ref.Repo, release.TagName, artifact)
}
