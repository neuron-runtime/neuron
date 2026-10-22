package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neuron-runtime/neuron/shared/types/builtins"
)

func TestBuiltinPolicyDefaultsToTheCeilings(t *testing.T) {
	cfg, err := Load(Options{GlobalPath: "/nonexistent/global.yaml", ProjectDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

	policy, err := cfg.BuiltinPolicy()
	if err != nil {
		t.Fatal(err)
	}

	ceilings := builtins.DefaultPolicy()
	if policy.MaxCommandOutputBytes != ceilings.MaxCommandOutputBytes {
		t.Errorf("maxCommandOutputBytes = %d, want %d", policy.MaxCommandOutputBytes, ceilings.MaxCommandOutputBytes)
	}
	if policy.MaxHTTPRequestBodyBytes != ceilings.MaxHTTPRequestBodyBytes {
		t.Errorf("maxHTTPRequestBodyBytes = %d, want %d", policy.MaxHTTPRequestBodyBytes, ceilings.MaxHTTPRequestBodyBytes)
	}
	if policy.MaxHTTPResponseBytes != ceilings.MaxHTTPResponseBytes {
		t.Errorf("maxHTTPResponseBytes = %d, want %d", policy.MaxHTTPResponseBytes, ceilings.MaxHTTPResponseBytes)
	}
	if policy.MaxFileReadBytes != ceilings.MaxFileReadBytes {
		t.Errorf("maxFileReadBytes = %d, want %d", policy.MaxFileReadBytes, ceilings.MaxFileReadBytes)
	}
	if policy.MaxDirectoryEntries != ceilings.MaxDirectoryEntries {
		t.Errorf("maxDirectoryEntries = %d, want %d", policy.MaxDirectoryEntries, ceilings.MaxDirectoryEntries)
	}
	if policy.DefaultTimeout != ceilings.DefaultTimeout {
		t.Errorf("defaultTimeout = %q, want %q", policy.DefaultTimeout, ceilings.DefaultTimeout)
	}
	if len(policy.AllowedHTTPHosts) != 0 {
		t.Errorf("allowedHTTPHosts = %v, want none", policy.AllowedHTTPHosts)
	}
	if len(policy.AllowedExecutables) != 0 {
		t.Errorf("allowedExecutables = %v, want none", policy.AllowedExecutables)
	}
}

func TestBuiltinPolicyReadsTheProjectConfiguration(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "neuron.config.yaml", `
builtins:
  filesystem:
    roots:
      - data
      - /srv/shared
  command:
    allowedExecutables:
      - git
      - ls
  http:
    allowedHosts:
      - api.example.com
  limits:
    maxCommandOutputBytes: 4096
    maxHTTPResponseBytes: 65536
    defaultTimeout: 15s
`)

	cfg, err := Load(Options{
		GlobalPath:  "/nonexistent/global.yaml",
		ProjectPath: filepath.Join(dir, "neuron.config.yaml"),
	})
	if err != nil {
		t.Fatal(err)
	}

	policy, err := cfg.BuiltinPolicy()
	if err != nil {
		t.Fatal(err)
	}

	if len(policy.FilesystemRoots) != 2 {
		t.Fatalf("filesystemRoots = %v, want 2 roots", policy.FilesystemRoots)
	}
	// A relative root resolves against the project root.
	if want := filepath.Join(dir, "data"); policy.FilesystemRoots[0] != want {
		t.Errorf("filesystemRoots[0] = %q, want %q", policy.FilesystemRoots[0], want)
	}
	if policy.FilesystemRoots[1] != "/srv/shared" {
		t.Errorf("filesystemRoots[1] = %q, want /srv/shared", policy.FilesystemRoots[1])
	}
	if len(policy.AllowedExecutables) != 2 {
		t.Errorf("allowedExecutables = %v, want 2 entries", policy.AllowedExecutables)
	}
	if len(policy.AllowedHTTPHosts) != 1 || policy.AllowedHTTPHosts[0] != "api.example.com" {
		t.Errorf("allowedHTTPHosts = %v, want [api.example.com]", policy.AllowedHTTPHosts)
	}
	if policy.MaxCommandOutputBytes != 4096 {
		t.Errorf("maxCommandOutputBytes = %d, want 4096", policy.MaxCommandOutputBytes)
	}
	if policy.MaxHTTPResponseBytes != 65536 {
		t.Errorf("maxHTTPResponseBytes = %d, want 65536", policy.MaxHTTPResponseBytes)
	}
	if policy.DefaultTimeout != "15s" {
		t.Errorf("defaultTimeout = %q, want 15s", policy.DefaultTimeout)
	}
	// Limits the configuration did not mention keep the compiled-in ceiling.
	ceilings := builtins.DefaultPolicy()
	if policy.MaxFileReadBytes != ceilings.MaxFileReadBytes {
		t.Errorf("maxFileReadBytes = %d, want %d", policy.MaxFileReadBytes, ceilings.MaxFileReadBytes)
	}
}

func TestBuiltinPolicyRefusesToExceedTheCeilings(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "neuron.config.yaml", `
builtins:
  limits:
    maxCommandOutputBytes: 67108864
`)

	_, err := Load(Options{
		GlobalPath:  "/nonexistent/global.yaml",
		ProjectPath: filepath.Join(dir, "neuron.config.yaml"),
	})
	if err == nil {
		t.Fatal("expected the configuration to be refused")
	}
	if !strings.Contains(err.Error(), "maxCommandOutputBytes") {
		t.Errorf("error = %v, want it to name the limit that exceeded the ceiling", err)
	}
}

func TestBuiltinPolicyRefusesAnUnusableTimeout(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "neuron.config.yaml", `
builtins:
  limits:
    defaultTimeout: never
`)

	if _, err := Load(Options{
		GlobalPath:  "/nonexistent/global.yaml",
		ProjectPath: filepath.Join(dir, "neuron.config.yaml"),
	}); err == nil {
		t.Fatal("expected the configuration to be refused")
	}
}

func TestBuiltinPolicyRejectsAnUnknownKey(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "neuron.config.yaml", `
builtins:
  http:
    allowedHosts:
      - api.example.com
    followRedirects: true
`)

	_, err := Load(Options{
		GlobalPath:  "/nonexistent/global.yaml",
		ProjectPath: filepath.Join(dir, "neuron.config.yaml"),
	})
	if err == nil {
		t.Fatal("expected the configuration to be refused")
	}
	if !strings.Contains(err.Error(), "followRedirects") {
		t.Errorf("error = %v, want it to name the unknown key", err)
	}
}

func TestBuiltinPolicyJSONRoundTrips(t *testing.T) {
	policy, err := builtins.Effective(builtins.Policy{
		AllowedHTTPHosts: []string{"api.example.com"},
		DefaultTimeout:   "30s",
	})
	if err != nil {
		t.Fatal(err)
	}

	encoded, err := BuiltinPolicyJSON(policy)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(encoded, "api.example.com") {
		t.Errorf("encoded policy = %s, want it to carry the allowed host", encoded)
	}
}

func TestBuiltinRootsResolveAgainstTheProjectRoot(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "neuron.config.yaml", `
builtins:
  filesystem:
    roots:
      - ./data
      - ~/shared
`)

	cfg, err := Load(Options{
		GlobalPath:  "/nonexistent/global.yaml",
		ProjectPath: filepath.Join(dir, "neuron.config.yaml"),
	})
	if err != nil {
		t.Fatal(err)
	}

	roots := cfg.Builtins.Filesystem.Roots
	if len(roots) != 2 {
		t.Fatalf("roots = %v, want 2 roots", roots)
	}
	if !filepath.IsAbs(roots[0]) {
		t.Errorf("roots[0] = %q, want an absolute path", roots[0])
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory in this environment")
	}
	if want := filepath.Join(home, "shared"); roots[1] != want {
		t.Errorf("roots[1] = %q, want %q", roots[1], want)
	}
}
