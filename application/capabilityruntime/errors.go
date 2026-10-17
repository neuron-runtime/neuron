package capabilityruntime

import (
	"errors"
	"fmt"

	"github.com/neuron-runtime/neuron/application/internal/artifact"
)

// Sentinel errors for the capability runtime registry pipeline. Callers can use
// errors.Is to distinguish "not found" (retry against a registry) from
// "checksum mismatch" (retry is pointless or dangerous).
var (
	// ErrNotFound is returned when an capability runtime cannot be located.
	ErrNotFound = errors.New("capability runtime not found")

	// ErrRegistryNotConfigured is returned when a requirement references a
	// registry that is not registered with the Registry catalog.
	ErrRegistryNotConfigured = errors.New("capability runtime registry not configured")

	// ErrChecksumMismatch is returned when an artifact fails SHA-256
	// verification. It is the shared artifact-package sentinel so that a
	// checksum failure is identical across every artifact kind.
	ErrChecksumMismatch = artifact.ErrChecksumMismatch

	// ErrNoVersionSatisfies is returned when no available version satisfies a
	// requirement's version constraint.
	ErrNoVersionSatisfies = errors.New("no capability runtime version satisfies constraint")

	// ErrManifestInvalid is returned when an runtime.json fails validation.
	ErrManifestInvalid = errors.New("invalid capability runtime manifest")

	// ErrAlreadyInstalled is returned by Store.Put when the exact version is
	// already installed. Strict installs may treat it as an error; idempotent
	// installs may treat it as success.
	ErrAlreadyInstalled = errors.New("capability runtime already installed")

	// ErrIntegrityConflict is returned when an install is attempted for a
	// (type, version) that is already committed with a different content
	// digest. The store never replaces a committed artifact in place.
	ErrIntegrityConflict = errors.New("capability runtime integrity conflict")
)

// NotFoundError wraps a concrete "not found" with context.
type NotFoundError struct {
	Type    string
	Version string
	Err     error
}

func (e *NotFoundError) Error() string {
	if e.Version != "" {
		return fmt.Sprintf("capability runtime %s@%s not found", e.Type, e.Version)
	}
	return fmt.Sprintf("capability runtime %s not found", e.Type)
}

func (e *NotFoundError) Unwrap() error {
	return e.Err
}
