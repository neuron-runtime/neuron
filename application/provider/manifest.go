package provider

import (
	"encoding/json"
	"fmt"
	"os"

	providertypes "github.com/neuron-runtime/neuron/shared/types/provider"
)

// ReadManifest loads and validates a provider.runtime.json manifest from path.
func ReadManifest(path string) (*providertypes.Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read provider manifest: %w", err)
	}
	return ParseManifest(data)
}

// ParseManifest decodes and validates provider.runtime.json bytes.
func ParseManifest(data []byte) (*providertypes.Manifest, error) {
	var m providertypes.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse provider manifest: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("validate provider manifest: %w", err)
	}
	return &m, nil
}
