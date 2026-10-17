package manifest

import (
	"path/filepath"
)

const (
	// ManifestDir is the name of the .neuron output directory.
	ManifestDir = ".neuron"
	// AssembliesDir is the directory under .neuron that holds one manifest
	// artifact per declared Assembly.
	AssembliesDir = "assemblies"
	// ManifestFile is the canonical AssemblyManifest filename.
	ManifestFile = "manifest.json"
)

// SDKManifestPath returns the fixed manifest output path the TypeScript
// neuron-sdk CLI writes for a project. It is the SDK's contract and is
// consumed as a transient: the build reads it, applies the project-configured
// identity, canonicalizes it, and persists the result under the per-Assembly
// artifact path. It is never the canonical location of a built manifest.
func SDKManifestPath(projectRoot string) string {
	return filepath.Join(projectRoot, ManifestDir, ManifestFile)
}

// AssemblyManifestPath returns the canonical manifest artifact path for one
// named Assembly of a project. Each Assembly of a project has its own manifest
// artifact so its identity and dependency set are stable independently of the
// other Assemblies.
func AssemblyManifestPath(projectRoot, name string) string {
	return filepath.Join(projectRoot, ManifestDir, AssembliesDir, name, ManifestFile)
}
