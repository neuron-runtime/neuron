package capabilityruntime

import (
	"github.com/neuron-runtime/neuron/application/internal/artifact"
)

// VerifySHA256 checks that the file at path has the expected SHA-256 digest.
// The expected digest may be bare hex or carry the "sha256:" prefix used by
// install records. An empty expected digest is an error: verification must
// never be silently skipped.
//
// A verification failure reports ErrChecksumMismatch; an I/O failure is
// reported unwrapped so callers do not mistake it for a bad digest.
func VerifySHA256(path, expected string) error {
	return artifact.VerifySHA256(path, expected)
}

// DigestFile returns the sha256:... digest of a file.
func DigestFile(path string) (string, error) {
	return artifact.DigestFile(path)
}
