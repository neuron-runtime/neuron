package artifact

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDigestFileAndBytesAgree(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "artifact.bin")
	data := []byte("neuron provider artifact")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	fromFile, err := DigestFile(path)
	if err != nil {
		t.Fatalf("DigestFile: %v", err)
	}
	if fromFile != DigestBytes(data) {
		t.Fatalf("DigestFile %q != DigestBytes %q", fromFile, DigestBytes(data))
	}
	if len(fromFile) != len("sha256:")+64 {
		t.Fatalf("digest %q is not sha256:<64 hex>", fromFile)
	}
}

func TestVerifySHA256(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "artifact.bin")
	if err := os.WriteFile(path, []byte("payload"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	good, err := DigestFile(path)
	if err != nil {
		t.Fatalf("DigestFile: %v", err)
	}

	if err := VerifySHA256(path, good); err != nil {
		t.Fatalf("prefixed digest should verify: %v", err)
	}
	if err := VerifySHA256(path, good[len("sha256:"):]); err != nil {
		t.Fatalf("bare hex digest should verify: %v", err)
	}
	if err := VerifySHA256(path, "sha256:"+string(bytes.Repeat([]byte("a"), 64))); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("wrong digest: want ErrChecksumMismatch, got %v", err)
	}
	if err := VerifySHA256(path, ""); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("empty digest: want ErrChecksumMismatch, got %v", err)
	}
	if err := VerifySHA256(path, "sha256:abc"); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("malformed digest: want ErrChecksumMismatch, got %v", err)
	}
	if err := VerifySHA256(filepath.Join(dir, "missing"), good); err == nil || errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("missing file: want unwrapped I/O error, got %v", err)
	}
}

func TestIsArchive(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain.txt")
	if err := os.WriteFile(plain, []byte("not an archive"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if IsArchive(plain) {
		t.Fatalf("plain file reported as archive")
	}
	if IsArchive(filepath.Join(dir, "missing")) {
		t.Fatalf("missing file reported as archive")
	}

	archive := filepath.Join(dir, "a.tar.gz")
	writeTarGz(t, archive, map[string]string{"runtime.json": "{}"})
	if !IsArchive(archive) {
		t.Fatalf("tar.gz not detected as archive")
	}
}

func TestExtractTarGzStripsCommonPrefix(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "pkg.tar.gz")
	writeTarGz(t, archive, map[string]string{
		"process-1.0.0/provider.runtime.json": "{}",
		"process-1.0.0/bin/process":           "binary",
	})

	dst := filepath.Join(dir, "out")
	if err := ExtractTarGz(archive, dst); err != nil {
		t.Fatalf("ExtractTarGz: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "provider.runtime.json")); err != nil {
		t.Fatalf("expected stripped payload: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "bin", "process")); err != nil {
		t.Fatalf("expected nested payload: %v", err)
	}
}

func TestExtractTarGzRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "evil.tar.gz")
	// A top-level directory is the discovery prefix, so the escaping entry
	// survives prefix stripping and must be rejected.
	writeTarGz(t, archive, map[string]string{"a/../../escape": "nope"})

	if err := ExtractTarGz(archive, filepath.Join(dir, "out")); !errors.Is(err, ErrManifestInvalid) {
		t.Fatalf("want ErrManifestInvalid, got %v", err)
	}
}

func TestDigestTreeIsDeterministicAndPathSensitive(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "provider.runtime.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(src, "bin"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "bin", "process"), []byte("binary"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}

	first, err := DigestTree(src)
	if err != nil {
		t.Fatalf("DigestTree: %v", err)
	}
	second, err := DigestTree(src)
	if err != nil {
		t.Fatalf("DigestTree: %v", err)
	}
	if first != second {
		t.Fatalf("DigestTree is not deterministic: %q != %q", first, second)
	}

	// The same bytes under a different name must hash differently.
	other := t.TempDir()
	if err := os.MkdirAll(filepath.Join(other, "bin"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(other, "bin", "renamed"), []byte("binary"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(other, "provider.runtime.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	moved, err := DigestTree(other)
	if err != nil {
		t.Fatalf("DigestTree: %v", err)
	}
	if moved == first {
		t.Fatalf("DigestTree ignored a renamed file")
	}
}

func writeTarGz(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	for name, body := range files {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header: %v", err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("write body: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}
}
