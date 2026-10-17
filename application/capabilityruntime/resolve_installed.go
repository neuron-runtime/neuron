package capabilityruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/neuron-runtime/neuron/application/internal/semverselect"
	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
)

// LatestVersion is the floating version token accepted by direct invocation. It
// is equivalent to an empty version: the highest installed version is selected.
const LatestVersion = "latest"

// ResolveInstalled returns the installed capability runtime satisfying a type
// and optional version, using only the local store.
//
// It is the resolution path for direct invocation (`neuron run -c`), which must
// never consult a registry or install anything implicitly:
//
//   - an exact version ("1.2.0") is a direct store lookup;
//   - an empty version or "latest" selects the highest installed version;
//   - any other value is a constraint ("^1.0.0") selecting the highest installed
//     version that satisfies it.
//
// A type with no satisfying installed version fails with ErrNotFound and an
// actionable message naming `neuron add`. Unlike Resolver.Resolve, a missing
// artifact is never fetched.
func ResolveInstalled(ctx context.Context, store Store, typ, version string) (*Installed, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: no capability runtime store configured", ErrNotFound)
	}

	typ = strings.TrimSpace(typ)
	version = strings.TrimSpace(version)
	if typ == "" {
		return nil, fmt.Errorf("capability runtime type is required")
	}
	if _, err := capabilityrt.ParseType(typ); err != nil {
		return nil, err
	}

	// Exact pin: direct lookup.
	if semverselect.IsExactVersion(version) {
		installed, err := store.Get(ctx, typ, version)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil, notInstalledError(typ, version)
			}
			return nil, err
		}
		return installed, nil
	}

	// Empty, "latest", or a constraint: pick the best installed version.
	constraint := version
	if constraint == LatestVersion {
		constraint = ""
	}

	// Reject a malformed constraint up front, so a typo is reported as an
	// invalid reference rather than as a missing installed version.
	if err := semverselect.ValidateConstraint(constraint); err != nil {
		return nil, fmt.Errorf("invalid capability runtime version %q: %w", version, err)
	}

	installed, ok, err := bestInstalled(ctx, store, typ, constraint)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, notInstalledError(typ, version)
	}
	return installed, nil
}

// notInstalledError reports a capability runtime that is not installed at a
// satisfying version, with a command that would install it. The suggestion
// omits the version for "latest" because a floating reference is installed by
// name.
func notInstalledError(typ, version string) error {
	ref := typ
	if version != "" && version != LatestVersion {
		ref = typ + "@" + version
	}
	return fmt.Errorf("%w: %s is not installed; install it with `neuron add %s`", ErrNotFound, ref, ref)
}

// bestInstalled returns the highest installed version of typ satisfying
// constraint, along with its Installed record. An empty constraint selects the
// highest installed version. ok is false when nothing satisfies.
func bestInstalled(ctx context.Context, store Store, typ, constraint string) (*Installed, bool, error) {
	installed, err := store.List(ctx, typ)
	if err != nil {
		return nil, false, err
	}
	if len(installed) == 0 {
		return nil, false, nil
	}

	versions := make([]string, 0, len(installed))
	for i := range installed {
		versions = append(versions, installed[i].Version)
	}

	best, err := semverselect.SelectVersion(constraint, versions)
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
