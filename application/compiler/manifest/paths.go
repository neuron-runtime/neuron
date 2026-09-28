package manifest

import (
	"path/filepath"
)

const (
	// ManifestDir is the name of the .neuron output directory.
	ManifestDir = ".neuron"
	// ManifestFile is the canonical AssemblyManifest filename.
	ManifestFile = "manifest.json"
)

// ManifestPath returns the absolute path to the canonical manifest
// file for a given project root.
func ManifestPath(projectRoot string) string {
	return filepath.Join(projectRoot, ManifestDir, ManifestFile)
}
