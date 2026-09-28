package local_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Muhammad-Jay/neuron/application/capabilityruntime"
	"github.com/Muhammad-Jay/neuron/application/capabilityruntime/source/local"
	capabilityrt "github.com/Muhammad-Jay/neuron/shared/types/capabilityruntime"
)

// writeVersionDir lays out <type path>/<version>/runtime.json under root.
func writeVersionDir(t *testing.T, root, typ, version, renamed string) string {
	t.Helper()
	base := filepath.Join(root, filepath.FromSlash(mustTypePath(t, typ)), version)
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	// "renamed" lets a test ship a manifest claiming a different identity.
	name := typ
	ver := version
	if renamed != "" {
		name = renamed
		ver = renamed
	}
	m := &capabilityrt.Manifest{
		APIVersion: capabilityrt.APIVersion,
		Kind:       capabilityrt.Kind,
		Metadata:   capabilityrt.ManifestMetadata{Name: name, Version: ver},
		Runtime: capabilityrt.ManifestRuntime{
			Type:       capabilityrt.RuntimeKindProcess,
			Entrypoint: "run.sh",
			Protocol:   capabilityrt.ProtocolJSONV1,
		},
		Capabilities: []string{"echo"},
	}
	if err := os.WriteFile(filepath.Join(base, capabilityrt.ManifestFile), mustMarshal(t, m), 0o644); err != nil {
		t.Fatal(err)
	}
	return base
}

func mustTypePath(t *testing.T, typ string) string {
	t.Helper()
	p, err := capabilityruntime.TypePath(typ)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func mustMarshal(t *testing.T, m *capabilityrt.Manifest) []byte {
	t.Helper()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestNewMultiUnionAcrossRoots(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()

	// Type "acme:echo" exists in only rootA; type "acme:ping" only in rootB;
	// "acme:both" appears in both with different versions.
	writeVersionDir(t, rootA, "acme:echo", "1.0.0", "")
	writeVersionDir(t, rootA, "acme:both", "1.0.0", "")
	writeVersionDir(t, rootB, "acme:ping", "2.0.0", "")
	writeVersionDir(t, rootB, "acme:both", "2.1.0", "")

	reg, err := local.NewMulti(rootA, rootB)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	types, err := reg.Types(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantTypes := map[string]bool{"acme:echo": true, "acme:ping": true, "acme:both": true}
	if len(types) != len(wantTypes) {
		t.Fatalf("Types = %v, want %d types", types, len(wantTypes))
	}
	for _, typ := range types {
		if !wantTypes[typ] {
			t.Errorf("unexpected type %q", typ)
		}
	}

	// Versions are the union across roots.
	versions, err := reg.Versions(ctx, "acme:both")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Fatalf("Versions(acme:both) = %v, want both 1.0.0 and 2.1.0", versions)
	}

	// Package resolves from whichever root hosts the version.
	if pkg, err := reg.Package(ctx, "acme:both", "1.0.0"); err != nil || pkg == nil {
		t.Fatalf("Package 1.0.0 from first root: %v %v", pkg, err)
	}
	if pkg, err := reg.Package(ctx, "acme:both", "2.1.0"); err != nil || pkg == nil {
		t.Fatalf("Package 2.1.0 from second root: %v %v", pkg, err)
	}
	if _, err := reg.Package(ctx, "acme:both", "99.0.0"); err != capabilityruntime.ErrNotFound {
		t.Errorf("missing version: err = %v, want ErrNotFound", err)
	}
}

func TestNewMultiRejectsEmptyOrMissingRoots(t *testing.T) {
	if _, err := local.NewMulti(); err == nil {
		t.Fatal("NewMulti() with no roots must error")
	}
	if _, err := local.NewMulti("/does/not/exist"); err == nil {
		t.Fatal("NewMulti with a missing root must error")
	}
}
