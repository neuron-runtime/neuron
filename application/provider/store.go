package provider

import (
	"errors"
)

// Errors used by the provider store.
var (
	ErrNotFound          = errors.New("provider not found")
	ErrAlreadyInstalled  = errors.New("provider already installed")
	ErrIntegrityConflict = errors.New("provider integrity conflict")
	ErrManifestInvalid   = errors.New("provider manifest invalid")
)
