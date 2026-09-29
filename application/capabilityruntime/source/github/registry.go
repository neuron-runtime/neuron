package github

import (
	"context"
	"errors"
	"fmt"

	"github.com/neuron-runtime/neuron/application/capabilityruntime"
)

// RepoRef identifies a GitHub repository as owner/name.
type RepoRef struct {
	Owner string
	Repo  string
}

// Option configures a GitHub registry.
type Option func(*Registry)

// WithClient overrides the HTTP/API client.
func WithClient(c *Client) Option {
	return func(r *Registry) { r.client = c }
}

// WithToken sets the API token (also honored via NEURON_GITHUB_TOKEN by
// callers; this is the explicit path).
func WithToken(token string) Option {
	return func(r *Registry) {
		if r.client != nil {
			r.client.Token = token
		}
	}
}

// WithCatalog overrides the capability runtime-type → repository mapping. Types absent
// from the catalog fall back to the "owner:segments -> owner/segments-joined"
// convention derived from ParseType.
func WithCatalog(catalog map[string]RepoRef) Option {
	return func(r *Registry) {
		if catalog != nil {
			r.catalog = catalog
		}
	}
}

// Registry is a source.Registry backed by GitHub Releases. Version discovery
// uses the Releases API; the runtime.json manifest is read from a release
// asset named runtime.json (or the raw repository file at the release tag);
// the binary is the release asset matching the manifest's host-platform entry.
type Registry struct {
	client  *Client
	catalog map[string]RepoRef
}

// New returns a GitHub registry using default conventions.
func New(opts ...Option) *Registry {
	r := &Registry{
		client:  NewClient(),
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

// repoFor maps a logical capability runtime type to its owner/repo.
func (r *Registry) repoFor(typ string) (RepoRef, error) {
	if ref, ok := r.catalog[typ]; ok {
		if ref.Owner == "" || ref.Repo == "" {
			return RepoRef{}, fmt.Errorf("github: catalog entry for %q is incomplete", typ)
		}
		return ref, nil
	}

	split, err := capabilityruntime.ParseType(typ)
	if err != nil {
		return RepoRef{}, err
	}

	ownerRepo := split.ToGitHubRepo()
	owner, repo, ok := splitOwnerRepo(ownerRepo)
	if !ok {
		return RepoRef{}, fmt.Errorf("github: cannot derive repository for %q", typ)
	}
	return RepoRef{Owner: owner, Repo: repo}, nil
}

// Types lists every type this registry can serve. The GitHub registry is
// catalog-driven, not index-driven: it reports the configured catalog keys
// plus no convention-derived guesses, because guessing at repositories that
// may not exist is surprising.
func (r *Registry) Types(ctx context.Context) ([]string, error) {
	types := make([]string, 0, len(r.catalog))
	for typ := range r.catalog {
		types = append(types, typ)
	}
	return types, nil
}

// Versions lists the semver release tags for typ, newest first.
func (r *Registry) Versions(ctx context.Context, typ string) ([]string, error) {
	ref, err := r.repoFor(typ)
	if err != nil {
		return nil, err
	}

	releases, err := r.client.ListReleases(ctx, ref.Owner, ref.Repo)
	if err != nil {
		if errors.Is(err, notFoundSentinel) {
			return nil, capabilityruntime.ErrNotFound
		}
		return nil, err
	}

	return versionsFromReleases(releases), nil
}

// Package returns the immutable package for an exact version.
func (r *Registry) Package(ctx context.Context, typ, version string) (*capabilityruntime.Package, error) {
	ref, err := r.repoFor(typ)
	if err != nil {
		return nil, err
	}

	release, err := r.client.ReleaseByTag(ctx, ref.Owner, ref.Repo, "v"+version)
	if err != nil {
		if errors.Is(err, notFoundSentinel) {
			return nil, capabilityruntime.ErrNotFound
		}
		return nil, err
	}

	return r.buildPackage(ctx, typ, version, ref, release)
}

func splitOwnerRepo(ownerRepo string) (owner, repo string, ok bool) {
	for i := 0; i < len(ownerRepo); i++ {
		if ownerRepo[i] == '/' {
			return ownerRepo[:i], ownerRepo[i+1:], true
		}
	}
	return "", "", false
}
