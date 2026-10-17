package manifest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Load reads and decodes a manifest from the given path.
func Load(path string) (*Assembly, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("manifest not found at %s", path)
		}
		return nil, fmt.Errorf("read manifest: %w", err)
	}

	m := &Assembly{}
	if err := json.Unmarshal(data, m); err != nil {
		return nil, fmt.Errorf("decode manifest %s: %w", path, err)
	}
	return m, nil
}

// LoadAssembly resolves and loads the canonical manifest artifact for one
// named Assembly of a project.
func LoadAssembly(projectRoot, name string) (*Assembly, error) {
	return Load(AssemblyManifestPath(projectRoot, name))
}

// SaveAssembly writes the canonical manifest artifact for one named Assembly
// of a project.
func SaveAssembly(projectRoot, name string, m *Assembly) error {
	return Save(AssemblyManifestPath(projectRoot, name), m)
}

// LoadSDKManifest loads the transient manifest the TypeScript neuron-sdk CLI
// writes for a project (see SDKManifestPath).
func LoadSDKManifest(projectRoot string) (*Assembly, error) {
	return Load(SDKManifestPath(projectRoot))
}

// Save serializes the manifest to the given path atomically.
// The write uses a temp-file then rename to ensure consumers
// never observe a half-written manifest.
func Save(path string, m *Assembly) error {
	if m == nil {
		return fmt.Errorf("manifest is nil")
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create manifest directory: %w", err)
	}

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("commit manifest: %w", err)
	}
	return nil
}
