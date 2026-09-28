package capabilityruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

// VerifySHA256 checks that the file at path has the expected SHA-256 digest.
// The expected digest may be bare hex or carry the "sha256:" prefix used by
// install records. An empty expected digest is an error: verification must
// never be silently skipped.
func VerifySHA256(path, expected string) error {
	if strings.TrimSpace(expected) == "" {
		return fmt.Errorf("%w: expected sha256 missing for %s", ErrChecksumMismatch, path)
	}

	want := strings.TrimPrefix(strings.TrimSpace(expected), "sha256:")

	if len(want) != sha256.Size*2 {
		return fmt.Errorf("%w: malformed expected sha256 %q", ErrChecksumMismatch, expected)
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open artifact for verification: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hash artifact: %w", err)
	}

	got := hex.EncodeToString(h.Sum(nil))

	if !strings.EqualFold(got, want) {
		return fmt.Errorf("%w: %s: got %s want %s", ErrChecksumMismatch, path, got, want)
	}

	return nil
}

// DigestFile returns the sha256:... digest of a file.
func DigestFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return fmt.Sprintf("sha256:%s", hex.EncodeToString(h.Sum(nil))), nil
}
