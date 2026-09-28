package runtimectl

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Muhammad-Jay/neuron/application/capabilityruntime"
	"github.com/Muhammad-Jay/neuron/application/capabilityruntime/source/local"
	"github.com/Muhammad-Jay/neuron/application/capabilityruntime/store"
	capabilityrt "github.com/Muhammad-Jay/neuron/shared/types/capabilityruntime"
)

// writePoweredLocalCapabilityRuntime lays out a version directory whose payload does
// not exist yet and whose runtime.json declares how to produce it:
//
//	<root>/example/echo/1.0.0/
//	    runtime.json   (build.command + artifact.path)
//
// buildScript, when non-empty, is written as build.sh in the version dir (so a
// simple build.command can `sh build.sh` to emit the artifact).
func writePoweredLocalCapabilityRuntime(t *testing.T, root, typ, version, buildCmd, buildScript, artifactPath string) {
	t.Helper()
	typePath, err := capabilityruntime.TypePath(typ)
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(root, filepath.FromSlash(typePath), version)
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if buildScript != "" {
		if err := os.WriteFile(filepath.Join(base, "build.sh"), []byte(buildScript), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	m := &capabilityrt.Manifest{
		APIVersion: capabilityrt.APIVersion,
		Kind:       capabilityrt.Kind,
		Metadata:   capabilityrt.ManifestMetadata{Name: typ, Version: version, Description: "powered capability runtime"},
		Runtime: capabilityrt.ManifestRuntime{
			Type:       capabilityrt.RuntimeKindProcess,
			Entrypoint: "run.sh",
			Protocol:   capabilityrt.ProtocolJSONV1,
		},
		Capabilities: []string{"echo"},
		Platforms: map[string]capabilityrt.Platform{
			capabilityruntime.HostPlatform(): {Artifact: "run.sh"},
		},
		Build:    &capabilityrt.ManifestBuild{Command: buildCmd},
		Artifact: &capabilityrt.ManifestArtifact{Path: artifactPath},
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, capabilityrt.ManifestFile), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func testCatalog(t *testing.T, root string) *Catalog {
	t.Helper()
	fsStore, err := store.NewFileassemblyStore(filepath.Join(t.TempDir(), "store"))
	if err != nil {
		t.Fatal(err)
	}
	loc, err := local.NewMulti(root)
	if err != nil {
		t.Fatal(err)
	}
	reg := capabilityruntime.NewRegistry()
	if err := reg.Add(loc); err != nil {
		t.Fatal(err)
	}
	return &Catalog{
		roots:      []string{root},
		Registry:   reg,
		Store:      fsStore,
		Installer:  &capabilityruntime.Installer{Store: fsStore, Downloader: capabilityruntime.NewHTTPDownloader()},
		Downloader: capabilityruntime.NewHTTPDownloader(),
		Resolver:   capabilityruntime.NewResolver(reg, fsStore, &capabilityruntime.Installer{Store: fsStore, Downloader: capabilityruntime.NewHTTPDownloader()}),
	}
}

func TestBuildLocalRunsBuildAndStages(t *testing.T) {
	root := t.TempDir()
	// The artifact (a directory payload producing run.sh+run.sh) is emitted by
	// the build command; runtime.json declares artifact.path = ./dist.
	writePoweredLocalCapabilityRuntime(t, root, "example:echo", "1.0.0",
		"sh build.sh", "mkdir -p dist && printf '#!/bin/sh\\n' > dist/run.sh && chmod +x dist/run.sh", "./dist")

	catalog := testCatalog(t, root)
	var statuses []string
	opts := BuildOptions{MaxWorkers: 1, ProjectRoot: root, Status: func(typ, version, msg string) {
		statuses = append(statuses, typ+"@"+version+":"+msg)
	}}

	if err := catalog.BuildLocal(context.Background(), opts); err != nil {
		t.Fatalf("BuildLocal: %v", err)
	}

	installed, err := catalog.Store.Get(context.Background(), "example:echo", "1.0.0")
	if err != nil {
		t.Fatalf("capability runtime not installed: %v", err)
	}
	if installed.Runtime.Entrypoint != "run.sh" {
		t.Errorf("installed entrypoint = %q, want run.sh", installed.Runtime.Entrypoint)
	}
	// Installed manifest must not carry authoring-time hints.
	mm, err := capabilityruntime.ReadManifest(installed.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if mm.Build != nil || mm.Artifact != nil {
		t.Errorf("installed manifest must strip build/artifact hints: %+v", mm)
	}

	// Second run is a cache hit: no rebuild, no reinstantiation, a note.
	statuses = nil
	if err := catalog.BuildLocal(context.Background(), opts); err != nil {
		t.Fatalf("second BuildLocal: %v", err)
	}
	if len(statuses) != 1 || !strings.Contains(statuses[0], "using cached version") {
		t.Errorf("second run statuses = %v, want a single cached note", statuses)
	}
}

func TestBuildLocalMissingPayloadNoBuildIsFatal(t *testing.T) {
	root := t.TempDir()
	writePoweredLocalCapabilityRuntime(t, root, "example:broken", "1.0.0", "", "", "./dist")

	catalog := testCatalog(t, root)
	err := catalog.BuildLocal(context.Background(), BuildOptions{MaxWorkers: 1})
	if err == nil {
		t.Fatal("expected BuildLocal to fail for an capability runtime with no artifact and no build.command")
	}
	if !strings.Contains(err.Error(), "example:broken") {
		t.Errorf("error must name the failing capability runtime, got: %v", err)
	}
}

func TestBuildLocalForceRebuilds(t *testing.T) {
	root := t.TempDir()
	writePoweredLocalCapabilityRuntime(t, root, "example:echo", "1.0.0",
		"sh build.sh", "mkdir -p dist && printf '#!/bin/sh\\n' > dist/run.sh && chmod +x dist/run.sh", "./dist")

	catalog := testCatalog(t, root)
	if err := catalog.BuildLocal(context.Background(), BuildOptions{MaxWorkers: 1}); err != nil {
		t.Fatal(err)
	}

	// --force removes and reinstalls, running the build command again.
	if err := catalog.BuildLocal(context.Background(), BuildOptions{MaxWorkers: 1, Force: true}); err != nil {
		t.Fatalf("force BuildLocal: %v", err)
	}
	if _, err := catalog.Store.Get(context.Background(), "example:echo", "1.0.0"); err != nil {
		t.Fatalf("capability runtime gone after force rebuild: %v", err)
	}
}

func TestBuildLocalBuildFailingCommandIsFatal(t *testing.T) {
	root := t.TempDir()
	writePoweredLocalCapabilityRuntime(t, root, "example:echo", "1.0.0",
		"sh build.sh", "exit 1", "./dist")

	catalog := testCatalog(t, root)
	err := catalog.BuildLocal(context.Background(), BuildOptions{MaxWorkers: 1})
	if err == nil {
		t.Fatal("expected BuildLocal to fail when the build command exits non-zero")
	}
	if !strings.Contains(err.Error(), "example:echo") {
		t.Errorf("error must name the capability runtime, got: %v", err)
	}
}

func TestBuildLocalBareArtifactFile(t *testing.T) {
	root := t.TempDir()
	// A build that emits a single file payload (artifact.path = ./run.sh).
	writePoweredLocalCapabilityRuntime(t, root, "example:echo", "1.0.0",
		"sh build.sh", "printf '#!/bin/sh\\n' > run.sh && chmod +x run.sh", "./run.sh")

	catalog := testCatalog(t, root)
	if err := catalog.BuildLocal(context.Background(), BuildOptions{MaxWorkers: 1}); err != nil {
		t.Fatalf("BuildLocal: %v", err)
	}
	if _, err := catalog.Store.Get(context.Background(), "example:echo", "1.0.0"); err != nil {
		t.Fatalf("capability runtime not installed: %v", err)
	}
}
