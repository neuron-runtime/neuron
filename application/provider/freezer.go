package provider

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/neuron-runtime/neuron/application/internal/semverselect"
	providertypes "github.com/neuron-runtime/neuron/shared/types/provider"
)

// FreezeForKinds returns the frozen identity of the installed provider that
// hosts each Capability Runtime runtime kind.
//
// Freezing is strictly store-only: it never consults a registry, never
// downloads, and never installs. A kind with no installed host fails with an
// actionable error naming the development bootstrap and install commands, so
// a normal build can never silently obtain a provider.
//
// Selection per kind is deterministic: the highest installed version of each
// candidate provider is considered; a provider whose name equals the kind
// wins outright, otherwise the lexicographically first candidate name is
// chosen. The result is ordered by provider name and holds each provider at
// most once, even when one provider hosts several requested kinds.
func FreezeForKinds(ctx context.Context, store Store, kinds []string) ([]providertypes.ResolvedProvider, error) {
	if store == nil {
		return nil, fmt.Errorf("provider store is nil")
	}

	requested := normalizeKinds(kinds)
	if len(requested) == 0 {
		return nil, nil
	}

	installed, err := store.List(ctx, "")
	if err != nil {
		return nil, err
	}

	// Per provider name, keep only its highest installed semver version.
	best := highestPerName(installed)

	selected := make(map[string]*Installed)
	for _, kind := range requested {
		candidateNames := make([]string, 0, len(best))
		for name, in := range best {
			if hostsKind(in, kind) {
				candidateNames = append(candidateNames, name)
			}
		}
		if len(candidateNames) == 0 {
			return nil, fmt.Errorf(
				"%w: no installed provider hosts runtime kind %q; bootstrap the first-party providers with `neuron provider build` or install one with `neuron install --provider %s@<version>`",
				ErrNotFound, kind, kind,
			)
		}
		sort.Strings(candidateNames)
		chosen := candidateNames[0]
		if _, ok := best[kind]; ok && hostsKind(best[kind], kind) {
			chosen = kind
		}
		selected[chosen] = best[chosen]
	}

	names := make([]string, 0, len(selected))
	for name := range selected {
		names = append(names, name)
	}
	sort.Strings(names)

	frozen := make([]providertypes.ResolvedProvider, 0, len(names))
	for _, name := range names {
		frozen = append(frozen, freezeOne(selected[name]))
	}
	return frozen, nil
}

// normalizeKinds trims, deduplicates, and sorts the requested runtime kinds
// so freezing is deterministic regardless of the caller's ordering.
func normalizeKinds(kinds []string) []string {
	seen := make(map[string]bool, len(kinds))
	out := make([]string, 0, len(kinds))
	for _, k := range kinds {
		k = strings.TrimSpace(k)
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// highestPerName maps each provider name to its highest installed version.
// Names whose installed versions are not valid semver are skipped: an
// artifact that cannot be frozen to an exact identity cannot participate in
// a reproducible build.
func highestPerName(installed []Installed) map[string]*Installed {
	byName := make(map[string][]*Installed)
	for i := range installed {
		in := &installed[i]
		byName[in.Name] = append(byName[in.Name], in)
	}

	best := make(map[string]*Installed, len(byName))
	for name, versions := range byName {
		exact := make([]string, 0, len(versions))
		for _, in := range versions {
			if semverselect.IsExactVersion(in.Version) {
				exact = append(exact, in.Version)
			}
		}
		highest, err := semverselect.SelectVersion("", exact)
		if err != nil {
			continue
		}
		for _, in := range versions {
			if in.Version == highest {
				best[name] = in
				break
			}
		}
	}
	return best
}

func hostsKind(in *Installed, kind string) bool {
	if in == nil || in.Manifest == nil {
		return false
	}
	for _, k := range in.Manifest.RuntimeKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// freezeOne converts an installed provider into its frozen wire identity.
// The runtime kinds are carried in sorted order so the record is
// deterministic.
func freezeOne(in *Installed) providertypes.ResolvedProvider {
	protocol := ""
	kinds := []string(nil)
	if in.Manifest != nil {
		protocol = in.Manifest.Provider.ProtocolVersion
		if protocol == "" {
			protocol = providertypes.ProtocolVersion
		}
		kinds = append(kinds, in.Manifest.RuntimeKinds...)
		sort.Strings(kinds)
	}
	return providertypes.ResolvedProvider{
		Name:            in.Name,
		ResolvedVersion: in.Version,
		ProtocolVersion: protocol,
		Registry:        in.Registry,
		Digest:          in.Digest,
		RuntimeKinds:    kinds,
	}
}
