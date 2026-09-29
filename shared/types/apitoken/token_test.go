package apitoken

import (
	"os"
	"testing"
)

func TestTokenFilePath(t *testing.T) {
	if got := TokenFilePath("/run/neuron/nore.sock"); got != "/run/neuron/nore.sock.token" {
		t.Fatalf("TokenFilePath = %q", got)
	}
	if got := TokenFilePath(""); got != "" {
		t.Fatalf("TokenFilePath(\"\") = %q, want empty", got)
	}
}

func TestGenerateIsUniqueAndRandom(t *testing.T) {
	first, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	second, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if first == second {
		t.Fatal("Generate returned the same token twice")
	}
	if len(first) < 32 {
		t.Fatalf("token is too short to resist guessing: %q", first)
	}
}

func TestWriteAndReadAppliesOwnerOnlyPermissions(t *testing.T) {
	path := t.TempDir() + "/nested/nore.sock.token"
	token := "s3cret"

	if err := Write(path, token); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, ok := Read(path)
	if !ok || got != token {
		t.Fatalf("Read = %q, %v; want %q, true", got, ok, token)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("token file permissions = %v, want -rw-------", perm)
	}
}

func TestWriteReplacesLoosePermissions(t *testing.T) {
	path := t.TempDir() + "/nore.sock.token"
	if err := Write(path, "old"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatalf("Chmod: %v", err)
	}

	if err := Write(path, "new"); err != nil {
		t.Fatalf("Write: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("token file permissions = %v, want -rw-------", perm)
	}
}

func TestReadMissingOrEmptyFile(t *testing.T) {
	if _, ok := Read(t.TempDir() + "/absent"); ok {
		t.Fatal("Read reported a token for a missing file")
	}

	path := t.TempDir() + "/empty.token"
	if err := Write(path, "   \n"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, ok := Read(path); ok {
		t.Fatal("Read reported a token for a blank file")
	}
}

func TestHeader(t *testing.T) {
	if got := Header("abc"); got != "Bearer abc" {
		t.Fatalf("Header = %q", got)
	}
	if got := Header(""); got != "" {
		t.Fatalf("Header(\"\") = %q, want empty", got)
	}
}
