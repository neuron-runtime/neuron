package artifact

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrChecksumMismatch identifies a digest that is missing, malformed, or does
// not match the artifact at hand. It lets callers distinguish a verification
// failure (retrying is pointless) from an I/O failure (retrying may help).
var ErrChecksumMismatch = errors.New("artifact checksum mismatch")

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

// DigestBytes returns the sha256:... digest of data.
func DigestBytes(data []byte) string {
	h := sha256.New()
	_, _ = h.Write(data)
	return fmt.Sprintf("sha256:%s", hex.EncodeToString(h.Sum(nil)))
}

// DigestTree returns a deterministic sha256:... digest of a directory tree.
//
// Every regular file contributes its slash-separated path relative to root and
// its contents, in sorted path order, each length-prefixed so that no two
// distinct trees can hash to the same value. This makes the digest sensitive
// to the layout as well as the bytes, so two trees that merely concatenate to
// the same byte stream are still distinguished, and a directory and an archive
// that expand to the same tree produce the same digest.
func DigestTree(root string) (string, error) {
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)

	h := sha256.New()
	var length [8]byte
	for _, rel := range files {
		binary.BigEndian.PutUint64(length[:], uint64(len(rel)))
		h.Write(length[:])
		h.Write([]byte(rel))

		f, err := os.Open(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		counter := &countingWriter{w: h}
		_, copyErr := io.Copy(counter, f)
		closeErr := f.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}

		binary.BigEndian.PutUint64(length[:], uint64(counter.n))
		h.Write(length[:])
	}
	return fmt.Sprintf("sha256:%s", hex.EncodeToString(h.Sum(nil))), nil
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

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
