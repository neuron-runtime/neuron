package capabilityruntime

import (
	"errors"

	"github.com/neuron-runtime/neuron/application/internal/artifact"
)

// IsArchive reports whether path is a gzip-compressed tarball.
func IsArchive(path string) bool {
	return artifact.IsArchive(path)
}

// ExtractTarGz extracts a .tar.gz archive into dst. When every entry shares a
// single top-level directory (the common release layout: <name>-v1.2.0/...)
// that directory is stripped so the payload lands directly in dst. Path
// traversal outside dst is rejected with ErrManifestInvalid.
func ExtractTarGz(archivePath, dst string) error {
	if err := artifact.ExtractTarGz(archivePath, dst); err != nil {
		if errors.Is(err, artifact.ErrManifestInvalid) {
			return ErrManifestInvalid
		}
		return err
	}
	return nil
}
