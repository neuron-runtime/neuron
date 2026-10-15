package provider

import (
	"errors"
	"os"
	"path/filepath"
)

// Errors used by the provider store.
var (
	ErrNotFound          = errors.New("provider not found")
	ErrAlreadyInstalled  = errors.New("provider already installed")
	ErrIntegrityConflict = errors.New("provider integrity conflict")
	ErrManifestInvalid   = errors.New("provider manifest invalid")
)

// DefaultStoreDir returns the default provider store directory (~/.neuron/providers).
func DefaultStoreDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".neuron/providers"
	}
	return filepath.Join(home, ".neuron", "providers")
}
