package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeExecutable creates a file that behaves like a runnable nore binary.
func writeExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestResolveNoreBinaryCustomOverrideWins(t *testing.T) {
	explicit := "/usr/local/bin/custom-nore"
	got, err := resolveNoreBinary(explicit, "/opt/neuron/neuron")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != explicit {
		t.Fatalf("got %q, want %q", got, explicit)
	}
}

func TestResolveNoreBinaryPrefersBundledSibling(t *testing.T) {
	binDir := t.TempDir()
	sibling := filepath.Join(binDir, "nore")
	writeExecutable(t, sibling)

	got, err := resolveNoreBinary("", filepath.Join(binDir, "neuron"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != sibling {
		t.Fatalf("got %q, want the bundled sibling %q", got, sibling)
	}
}

func TestResolveNoreBinaryCheckoutFallback(t *testing.T) {
	t.Setenv("PATH", "")
	root := t.TempDir()
	// Simulate a checkout three levels deep under the current directory.
	cwd := filepath.Join(root, "one/two/three")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", cwd, err)
	}
	daemon := filepath.Join(root, "nore/cmd/nore/nore-daemon")
	writeExecutable(t, daemon)

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

	got, err := resolveNoreBinary("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != daemon {
		t.Fatalf("got %q, want the checkout daemon %q", got, daemon)
	}
}

func TestResolveNoreBinaryNotFound(t *testing.T) {
	t.Setenv("PATH", "")
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	cwd := t.TempDir()
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

	_, err = resolveNoreBinary("", filepath.Join(t.TempDir(), "neuron"))
	if err == nil {
		t.Fatal("expected an error when no nore binary can be found")
	}
	if !strings.Contains(err.Error(), "--nore-path") {
		t.Fatalf("error should guide the user to set --nore-path, got: %v", err)
	}
}
