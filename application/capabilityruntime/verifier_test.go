package capabilityruntime

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestVerifySHA256(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "artifact.bin")
	if err := os.WriteFile(path, []byte("neuron-capability-runtime-artifact"), 0o644); err != nil {
		t.Fatal(err)
	}

	good, err := DigestFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := VerifySHA256(path, good); err != nil {
		t.Errorf("expected digest to verify: %v", err)
	}

	// Bare hex works too.
	if err := VerifySHA256(path, good[len("sha256:"):]); err != nil {
		t.Errorf("bare hex should verify: %v", err)
	}

	if err := VerifySHA256(path, "sha256:"+badHex()); err == nil {
		t.Error("wrong digest should mismatch")
	} else if !errors.Is(err, ErrChecksumMismatch) {
		t.Errorf("want ErrChecksumMismatch, got %v", err)
	}

	if err := VerifySHA256(path, ""); err == nil {
		t.Error("missing digest must fail verification")
	}
}

func TestVerifySHA256Malformed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifySHA256(path, "sha256:abc"); err == nil {
		t.Error("malformed digest must fail")
	}
}

func TestVerifySHA256MissingFile(t *testing.T) {
	if err := VerifySHA256(filepath.Join(t.TempDir(), "nope"), "sha256:"+badHex()); err == nil {
		t.Error("missing file must fail")
	}
}

func TestDigestFileFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	digest, err := DigestFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(digest) != len("sha256:")+64 {
		t.Errorf("digest %q has wrong length", digest)
	}
}

func badHex() string {
	return "0000000000000000000000000000000000000000000000000000000000000000"
}
