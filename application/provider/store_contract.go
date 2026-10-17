package provider

import (
	"context"

	providertypes "github.com/neuron-runtime/neuron/shared/types/provider"
)

// Store is the installed provider catalog contract.
type Store interface {
	// Root returns the absolute store root directory.
	Root() string

	// InstalledDir returns the final installed directory for an exact
	// (name, version, digest).
	InstalledDir(name, version, digest string) (string, error)

	// Stage creates a fresh, empty staging directory inside the store.
	Stage() (string, error)

	// Commit atomically renames a fully-populated staging directory into the
	// final store location and returns the installed record.
	Commit(ctx context.Context, stage, name, version, digest string) (*Installed, error)

	// Get returns the installed provider for an exact version (name@version).
	// Not-found is signaled with ErrNotFound.
	Get(ctx context.Context, name, version string) (*Installed, error)

	// List returns every installed version of name, newest first. An empty
	// name lists all installed providers.
	List(ctx context.Context, name string) ([]Installed, error)

	// Remove deletes an installed version.
	Remove(ctx context.Context, name, version string) error
}

// Installed is a locally installed, immutable provider artifact.
type Installed struct {
	Name    string
	Version string
	Digest  string
	// Registry is the provenance recorded at install time (the source
	// registry name, or the bootstrap marker). It is informational and
	// never part of the artifact identity.
	Registry     string
	RootDir      string
	ManifestPath string
	Manifest     *providertypes.Manifest
}
