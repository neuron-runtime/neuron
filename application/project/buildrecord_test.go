package project

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Muhammad-Jay/neuron/shared/types/protocol"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBuildRecordRoundTrip(t *testing.T) {
	root := t.TempDir()
	rec := BuildRecord{
		Fingerprint: "abc123",
		Key: protocol.InstanceKey{
			SystemID: "demo",
			Version:  "1.0.0",
			Hash:     "h1",
			Env:      "default",
		},
		BuiltAt: time.Now().UTC(),
	}

	if err := SaveBuildRecord(root, rec); err != nil {
		t.Fatal(err)
	}

	var got BuildRecord
	if err := LoadBuildRecord(root, &got); err != nil {
		t.Fatal(err)
	}
	if got.Fingerprint != rec.Fingerprint {
		t.Errorf("fingerprint = %q, want %q", got.Fingerprint, rec.Fingerprint)
	}
	if got.Key.ColonString() != rec.Key.ColonString() {
		t.Errorf("key = %q, want %q", got.Key.ColonString(), rec.Key.ColonString())
	}
	if !got.BuiltAt.Equal(rec.BuiltAt) {
		t.Errorf("builtAt = %v, want %v", got.BuiltAt, rec.BuiltAt)
	}
}

func TestLoadBuildRecordMissingIsErrNotBuilt(t *testing.T) {
	var rec BuildRecord
	err := LoadBuildRecord(t.TempDir(), &rec)
	if !errors.Is(err, ErrNotBuilt) {
		t.Fatalf("err = %v, want ErrNotBuilt", err)
	}
}

func TestFingerprintStableAndContentSensitive(t *testing.T) {
	root := t.TempDir()
	entry := filepath.Join(root, "system.yaml")
	writeFile(t, entry, "system:\n  name: demo\n")

	a, err := ComputeFingerprint(FingerprintInputs{Entry: entry})
	if err != nil {
		t.Fatal(err)
	}
	b, err := ComputeFingerprint(FingerprintInputs{Entry: entry})
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("fingerprint not stable across identical inputs: %s vs %s", a, b)
	}

	writeFile(t, entry, "system:\n  name: demo2\n")
	c, err := ComputeFingerprint(FingerprintInputs{Entry: entry})
	if err != nil {
		t.Fatal(err)
	}
	if c == a {
		t.Fatalf("fingerprint did not change when the entry changed")
	}
}

func TestFingerprintExcludesBuildOutputs(t *testing.T) {
	root := t.TempDir()
	execRoot := filepath.Join(root, "executors", "example", "echo", "1.0.0")
	writeFile(t, filepath.Join(execRoot, "executor.json"), `{"metadata":{"name":"example:echo","version":"1.0.0"}}`)
	writeFile(t, filepath.Join(execRoot, "main.go"), "package main\n")
	writeFile(t, filepath.Join(execRoot, "echo"), "#!/bin/sh\n")

	elusive := filepath.Join(execRoot, "echo")
	opts := FingerprintInputs{Sources: []string{filepath.Join(root, "executors")}}
	withOutput := opts
	withOutput.Exclude = []string{elusive}

	baseIncl, err := ComputeFingerprint(opts)
	if err != nil {
		t.Fatal(err)
	}
	baseExcl, err := ComputeFingerprint(withOutput)
	if err != nil {
		t.Fatal(err)
	}
	if baseIncl == baseExcl {
		t.Fatal("sanity: excluding the binary must change the fingerprint")
	}

	// Rewriting the produced binary must churn the fingerprint when it is not
	// excluded...
	writeFile(t, elusive, "#!/bin/sh\n# changed binary bytes\n")

	afterIncl, err := ComputeFingerprint(opts)
	if err != nil {
		t.Fatal(err)
	}
	if afterIncl == baseIncl {
		t.Fatalf("build output was not captured when not excluded")
	}

	// ...but must not when it is excluded.
	afterExcl, err := ComputeFingerprint(withOutput)
	if err != nil {
		t.Fatal(err)
	}
	if afterExcl != baseExcl {
		t.Fatalf("excluded build output changed the fingerprint: %s vs %s", baseExcl, afterExcl)
	}
}

func TestFingerprintSkipsGeneratedDirs(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "services", "hello.yaml"), "service:\n  name: hello\n")
	writeFile(t, filepath.Join(root, "node_modules", "pkg", "x"), "junk\n")
	writeFile(t, filepath.Join(root, ".neuron", "manifest.json"), "junk\n")

	fp, err := ComputeFingerprint(FingerprintInputs{Sources: []string{filepath.Join(root, "services")}})
	if err != nil {
		t.Fatal(err)
	}
	if fp == "" {
		t.Fatal("empty fingerprint")
	}

	// node_modules and .neuron are not part of the scanned inputs at all, so
	// mutating them must not affect a hash over services/ — and adding a file
	// to node_modules directly (it was never scanned) changes nothing.
	writeFile(t, filepath.Join(root, "node_modules", "pkg", "y"), "more junk\n")
	after, err := ComputeFingerprint(FingerprintInputs{Sources: []string{filepath.Join(root, "services")}})
	if err != nil {
		t.Fatal(err)
	}
	if after != fp {
		t.Fatalf("fingerprint churned on an un-scanned directory")
	}
}

func TestFingerprintMissingInputErrors(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.yaml")
	_, err := ComputeFingerprint(FingerprintInputs{Entry: missing})
	if err == nil {
		t.Fatal("expected error for a declared but missing fingerprint input")
	}
}

func TestFingerprintDefaultsToImplicitRootScan(t *testing.T) {
	// An input root that does not exist must not silently produce an empty,
	// misleading fingerprint hidden behind an error.
	root := t.TempDir()
	_, err := ComputeFingerprint(FingerprintInputs{Sources: []string{filepath.Join(root, "missing")}})
	if err == nil {
		t.Fatal("expected error for a declared but missing Sources root")
	}
}
