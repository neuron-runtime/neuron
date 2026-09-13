package executor

import (
	"context"
	"errors"
	"fmt"
)

// Resolver turns a Requirement into an Installed executor. It prefers
// already-installed artifacts, resolves the best version above the registry
// providers (never trusting a provider to pick compatibility), and installs
// missing artifacts through the Installer.
//
// Resolver never decides what a Deployment needs. It answers the question
// "give me the exact executor satisfying this requirement".
type Resolver struct {
	registries *Registry
	store      Store
	installer  *Installer

	// Observer receives resolution progress events. A nil observer keeps
	// resolution silent.
	Observer Observer
}

// NewResolver wires resolution against a registry catalog, an installed
// executor store, and an installer for missing artifacts.
func NewResolver(
	registries *Registry,
	executorStore Store,
	installer *Installer,
) *Resolver {
	return &Resolver{
		registries: registries,
		store:      executorStore,
		installer:  installer,
	}
}

// Resolve returns the Installed executor satisfying req, installing it when
// the best matching version is not already in the store.
//
// Resolution order:
//
//  1. an installed exact version, when the requirement pins one
//  2. the best installed version satisfying the constraint (no network)
//  3. per-registry: available versions → SelectVersion → Package → Install
func (r *Resolver) Resolve(ctx context.Context, req Requirement) (*Installed, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}

	notify(r.Observer, func(o Observer) { o.Resolving(req) })

	// 1 & 2: prefer the local store whenever an installed version satisfies
	// the requirement. This keeps Instances independent of the network.
	if installed, ok, err := r.installedSatisfying(ctx, req); err != nil {
		return nil, err
	} else if ok {
		notify(r.Observer, func(o Observer) { o.AlreadyInstalled(req, *installed) })
		return installed, nil
	}

	if r.installer == nil {
		return nil, fmt.Errorf("no installer configured; executor %s not installed", req.Type)
	}

	// 3: resolve against the configured registries.
	registries := req.Registries
	if len(registries) == 0 {
		return nil, fmt.Errorf(
			"%w: no executor registries specified for %q",
			ErrRegistryNotConfigured,
			req.Type,
		)
	}

	var lastErr error

	for _, name := range registries {
		registry, ok := r.registries.Get(name)
		if !ok {
			lastErr = fmt.Errorf("%w: %q", ErrRegistryNotConfigured, name)
			continue
		}

		installed, err := r.resolveFrom(ctx, registry, req)
		if err != nil {
			lastErr = err
			continue
		}

		return installed, nil
	}

	if lastErr != nil {
		return nil, lastErr
	}

	return nil, fmt.Errorf("executor %q could not be resolved", req.Type)
}

// ResolveMany resolves a set of requirements in one pass. It returns the
// resolved environment and keeps duplicates down to a single entry per type.
func (r *Resolver) ResolveMany(ctx context.Context, requirements []Requirement) (*Environment, error) {
	env := &Environment{}
	seen := make(map[string]bool)

	for _, req := range requirements {
		if seen[req.Type] {
			continue
		}
		installed, err := r.Resolve(ctx, req)
		if err != nil {
			return nil, err
		}
		seen[req.Type] = true
		env.Executors = append(env.Executors, installed)
	}

	return env, nil
}

func (r *Resolver) resolveFrom(ctx context.Context, registry Provider, req Requirement) (*Installed, error) {
	versions, err := registry.Versions(ctx, req.Type)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("registry %s versions for %s: %w", registry.Name(), req.Type, err)
	}

	version, err := SelectVersion(req.Version, versions)
	if err != nil {
		return nil, err
	}

	pkg, err := registry.Package(ctx, req.Type, version)
	if err != nil {
		return nil, fmt.Errorf("registry %s package %s@%s: %w", registry.Name(), req.Type, version, err)
	}

	result, err := r.installer.Install(ctx, pkg)
	if err != nil {
		return nil, err
	}

	return result.Installed, nil
}

// installedSatisfying returns an already-installed executor that satisfies
// req, preferring an exact pin and otherwise the newest installed version
// matching the constraint. Floating (empty) requirements always resolve
// against the registries so "latest" is whatever the registry says is newest.
func (r *Resolver) installedSatisfying(ctx context.Context, req Requirement) (*Installed, bool, error) {
	if r.store == nil {
		return nil, false, nil
	}

	// Floating requirement: always consult the registries so "latest" is
	// whatever the registry says is newest, not a stale installed version.
	if req.Version == "" {
		return nil, false, nil
	}

	// Exact pin: direct lookup.
	if IsExactVersion(req.Version) {
		installed, err := r.store.Get(ctx, req.Type, req.Version)
		if err == nil && installed != nil {
			return installed, true, nil
		}
		if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, false, err
		}
		return nil, false, nil
	}

	// Constraint or floating: pick the best installed version.
	installed, err := r.store.List(ctx, req.Type)
	if err != nil || len(installed) == 0 {
		return nil, false, nil
	}

	versions := make([]string, 0, len(installed))
	for _, in := range installed {
		versions = append(versions, in.Version)
	}

	best, err := SelectVersion(req.Version, versions)
	if err != nil {
		return nil, false, nil
	}

	for _, in := range installed {
		if in.Version == best {
			return &in, true, nil
		}
	}
	return nil, false, nil
}
