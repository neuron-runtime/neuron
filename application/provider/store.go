package provider

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	providertypes "github.com/neuron-runtime/neuron/shared/types/provider"
)

const (
	storeDir = "providers"
)

// Errors used by the provider store.
var (
	ErrNotFound = errors.New("provider not found")
)

// Store is an immutable provider artifact store. Artifacts are installed under
// <root>/<provider>/<version>/<digest> with the manifest, payload, and
// install.json written atomically.
type Store struct {
	root string
}

// NewStore constructs a provider artifact store rooted at root.
func NewStore(root string) *Store {
	return &Store{root: root}
}

// Root returns the store root directory.
func Root(root string) string {
	return filepath.Join(root, storeDir)
}

// InstalledDir returns the installed directory for the given provider identity.
func InstalledDir(root, name, version, digest string) (string, error) {
	path, err := providertypes.ProviderTypePath(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(Root(root), path, version, digest), nil
}

// Install installs a provider artifact from srcDir into the store, verifying
// the manifest and digesting the payload. The function is a placeholder for
// Phase 13 implementation - for now it returns a stub.
func (s *Store) Install(ctx context.Context, srcDir string, name, version, digest string) (string, error) {
	// Phase 13: full implementation to follow. For now, return the target dir.
	dir, err := InstalledDir(s.root, name, version, digest)
	if err != nil {
		return "", err
	}
	return dir, nil
}

// Has returns true if the artifact is already installed.
func (s *Store) Has(root, name, version, digest string) (bool, error) {
	dir, err := InstalledDir(root, name, version, digest)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(filepath.Join(dir, providertypes.InstallFile))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}
