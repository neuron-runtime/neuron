package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/neuron-runtime/neuron/application/internal/semverselect"
)

// ErrRegistryNotConfigured marks a requirement that names a registry the
// catalog does not serve, or names no registries at all while no defaults
// apply.
var ErrRegistryNotConfigured = errors.New("provider registry not configured")

// Resolver turns a Requirement into an Installed provider. It prefers
// already-installed artifacts, resolves the best version above the registry
// sources (never trusting a source to pick compatibility), and installs
// missing artifacts through the Installer.
//
// Resolver never decides what an Assembly needs. It answers the question
// "give me the exact provider satisfying this requirement".
type Resolver struct {
	registries *Registry
	store      Store
	installer  *Installer
}

// NewResolver wires resolution against a source catalog, an installed
// provider store, and an installer for missing artifacts.
func NewResolver(registries *Registry, store Store, installer *Installer) *Resolver {
	return &Resolver{
		registries: registries,
		store:      store,
		installer:  installer,
	}
}

// Resolve returns the Installed provider satisfying req, installing it when
// the best matching version is not already in the store.
//
// Resolution order:
//
//  1. an installed exact version, when the requirement pins one
//  2. the best installed version satisfying the constraint (no network)
//  3. per-source: available versions → SelectVersion → Package → Install
func (r *Resolver) Resolve(ctx context.Context, req Requirement) (*Installed, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}

	// 1 & 2: prefer the local store whenever an installed version satisfies
	// the requirement, so resolution stays independent of the network.
	if installed, ok, err := r.installedSatisfying(ctx, req); err != nil {
		return nil, err
	} else if ok {
		return installed, nil
	}

	return r.resolveFromRegistries(ctx, req)
}

// resolveFromRegistries resolves req against every configured source in
// order, returning the first success.
func (r *Resolver) resolveFromRegistries(ctx context.Context, req Requirement) (*Installed, error) {
	if r.installer == nil {
		return nil, fmt.Errorf("no installer configured; provider %s not installed", req.Name)
	}

	registries := req.Registries
	if len(registries) == 0 {
		return nil, fmt.Errorf(
			"%w: no provider registries specified for %q",
			ErrRegistryNotConfigured,
			req.Name,
		)
	}

	var lastErr error

	for _, name := range registries {
		source, ok := r.registries.Get(name)
		if !ok {
			lastErr = fmt.Errorf("%w: %q", ErrRegistryNotConfigured, name)
			continue
		}

		installed, err := r.resolveFrom(ctx, source, req)
		if err != nil {
			lastErr = err
			continue
		}

		return installed, nil
	}

	if lastErr != nil {
		// A pure not-found across every source is the common miss; turn it
		// into an actionable instruction instead of a bare error.
		if errors.Is(lastErr, ErrNotFound) {
			return nil, fmt.Errorf(
				"%w: install it with `neuron install --provider %s@<version>` or build it with `neuron provider build`",
				lastErr, req.Name,
			)
		}
		return nil, lastErr
	}

	return nil, fmt.Errorf("provider %q could not be resolved", req.Name)
}

// resolveFrom selects the best available version from one source and
// installs it.
func (r *Resolver) resolveFrom(ctx context.Context, source Source, req Requirement) (*Installed, error) {
	versions, err := source.Versions(ctx, req.Name)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("registry %s versions for %s: %w", source.Name(), req.Name, err)
	}

	version, err := semverselect.SelectVersion(req.Version, versions)
	if err != nil {
		return nil, fmt.Errorf("provider %s: %w", req.Name, err)
	}

	pkg, err := source.Package(ctx, req.Name, version)
	if err != nil {
		return nil, fmt.Errorf("registry %s package %s@%s: %w", source.Name(), req.Name, version, err)
	}

	result, err := r.installer.Install(ctx, pkg)
	if err != nil {
		return nil, err
	}

	return result.Installed, nil
}

// installedSatisfying returns an already-installed provider that satisfies
// req, preferring an exact pin and otherwise the newest installed version
// matching the constraint. Floating (empty) requirements always resolve
// against the registries so "latest" is whatever the source says is newest.
func (r *Resolver) installedSatisfying(ctx context.Context, req Requirement) (*Installed, bool, error) {
	if r.store == nil {
		return nil, false, nil
	}

	// Floating requirement: always consult the registries so "latest" is
	// whatever the source says is newest, not a stale installed version.
	if req.Version == "" {
		return nil, false, nil
	}

	// Exact pin: direct lookup.
	if semverselect.IsExactVersion(req.Version) {
		installed, err := r.store.Get(ctx, req.Name, req.Version)
		if err == nil && installed != nil {
			return installed, true, nil
		}
		if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, false, err
		}
		return nil, false, nil
	}

	// Constraint: pick the best installed version.
	installed, err := r.store.List(ctx, req.Name)
	if err != nil || len(installed) == 0 {
		return nil, false, nil
	}

	versions := make([]string, 0, len(installed))
	for _, in := range installed {
		versions = append(versions, in.Version)
	}

	best, err := semverselect.SelectVersion(req.Version, versions)
	if err != nil {
		return nil, false, nil
	}

	for i := range installed {
		if installed[i].Version == best {
			return &installed[i], true, nil
		}
	}
	return nil, false, nil
}
