package capabilityruntime

import (
	"encoding/json"
	"fmt"
	"os"

	capabilityrt "github.com/Muhammad-Jay/neuron/shared/types/capabilityruntime"
)

// ReadManifest loads and validates an runtime.json manifest from path.
func ReadManifest(path string) (*capabilityrt.Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: read %s: %v", ErrManifestInvalid, path, err)
	}
	return ParseManifest(data)
}

// ReadManifestFile is an alias for ReadManifest kept for symmetry with the
// installed record helpers.
func ReadManifestFile(path string) (*capabilityrt.Manifest, error) {
	return ReadManifest(path)
}

// ParseManifest decodes and validates runtime.json bytes.
func ParseManifest(data []byte) (*capabilityrt.Manifest, error) {
	var m capabilityrt.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%w: parse: %v", ErrManifestInvalid, err)
	}
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrManifestInvalid, err)
	}
	return &m, nil
}

// PackageFromManifest converts a validated manifest into the Package returned
// by registry providers. registry and version are supplied by the provider;
// the platform artifact selection happens at install time.
func PackageFromManifest(m *capabilityrt.Manifest, registry string, version string) *Package {
	return &Package{
		Type:        m.Metadata.Name,
		Version:     version,
		Registry:    registry,
		Description: m.Metadata.Description,
		Runtime: RuntimeSpec{
			Type:       m.Runtime.Type,
			Entrypoint: m.Runtime.Entrypoint,
			Protocol:   m.Runtime.Protocol,
			MaxWorkers: m.Runtime.MaxWorkers,
		},
		Capabilities: m.Capabilities,
		Features:     m.Features,
		Platforms:    m.Platforms,
	}
}
