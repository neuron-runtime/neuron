package capabilityruntime

import (
	"context"
)

// Store is the installed capability runtime catalog contract consumed by the Resolver
// and Installer. Concrete implementations (capabilityruntime/store) live below this
// package so the import direction stays capabilityruntime → store.
type Store interface {
	// Root returns the absolute store root directory.
	Root() string

	// InstalledDir returns the final installed directory for an exact
	// (type, version, digest). The directory may not exist yet; the method
	// only derives the path from the store layout.
	InstalledDir(typ, version, digest string) (string, error)

	// Stage creates a fresh, empty staging directory inside the store.
	Stage() (string, error)

	// Commit atomically renames a fully-populated staging directory into the
	// final store location and returns the installed record it contains.
	// The staging directory must already contain a complete install.json with
	// final paths; the committed directory is never rewritten in place.
	Commit(ctx context.Context, stage, typ, version, digest string) (*Installed, error)

	// Get returns the installed capability runtime for an exact version. Not-found is
	// signaled with ErrNotFound.
	Get(ctx context.Context, typ, version string) (*Installed, error)

	// List returns every installed version of typ, newest first. An empty typ
	// lists all installed capability runtimes.
	List(ctx context.Context, typ string) ([]Installed, error)

	// Remove deletes an installed version.
	Remove(ctx context.Context, typ, version string) error
}
