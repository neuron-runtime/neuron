package executor_test

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Muhammad-Jay/neuron/application/executor"
	"github.com/Muhammad-Jay/neuron/application/executor/source/local"
	execstore "github.com/Muhammad-Jay/neuron/application/executor/store"
)

// recordingObserver captures the sequence of Observer events emitted during
// resolution and installation.
type recordingObserver struct {
	events []string
}

func (o *recordingObserver) Resolving(res executor.Requirement) {
	o.events = append(o.events, "resolve:"+res.Type)
}

func (o *recordingObserver) AlreadyInstalled(_ executor.Requirement, installed executor.Installed) {
	o.events = append(o.events, "already:"+installed.Type+"@"+installed.Version)
}

func (o *recordingObserver) Installing(pkg executor.Package) {
	o.events = append(o.events, "install:"+pkg.Type+"@"+pkg.Version)
}

func (o *recordingObserver) Installed(result executor.InstallResult) {
	if result.Installed == nil {
		o.events = append(o.events, "installed:<nil>")
		return
	}
	if result.AlreadyPresent {
		o.events = append(o.events, "installed:present:"+result.Installed.Type+"@"+result.Installed.Version)
		return
	}
	o.events = append(o.events, "installed:"+result.Installed.Type+"@"+result.Installed.Version)
}

func TestResolveObserverEventSequence(t *testing.T) {
	ctx := context.Background()

	regRoot := t.TempDir()
	storeRoot := filepath.Join(t.TempDir(), "store")
	writeExecutorPackage(t, regRoot, "github:read", "1.2.0")

	reg, err := local.New(regRoot)
	if err != nil {
		t.Fatal(err)
	}
	fsStore, err := execstore.NewFilesystemStore(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	catalog := executor.NewRegistry()
	if err := catalog.Add(reg); err != nil {
		t.Fatal(err)
	}

	obs := &recordingObserver{}
	installer := &executor.Installer{Store: fsStore, Downloader: executor.NewHTTPDownloader(), Observer: obs}
	resolver := executor.NewResolver(catalog, fsStore, installer)
	resolver.Observer = obs

	req := executor.Requirement{Type: "github:read", Version: "^1.0.0", Registries: []string{"local"}}

	// First resolution installs the artifact.
	installed, err := resolver.Resolve(ctx, req)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if installed.Version != "1.2.0" {
		t.Fatalf("resolved version = %q, want 1.2.0", installed.Version)
	}

	wantFirst := []string{
		"resolve:github:read",
		"install:github:read@1.2.0",
		"installed:github:read@1.2.0",
	}
	if !reflect.DeepEqual(obs.events, wantFirst) {
		t.Fatalf("first resolution events = %v, want %v", obs.events, wantFirst)
	}

	// A second resolution shorts out in the store and never installs.
	obs.events = nil
	again, err := resolver.Resolve(ctx, req)
	if err != nil {
		t.Fatalf("Resolve again: %v", err)
	}
	if again.Version != "1.2.0" {
		t.Fatalf("second resolved version = %q", again.Version)
	}

	wantSecond := []string{
		"resolve:github:read",
		"already:github:read@1.2.0",
	}
	if !reflect.DeepEqual(obs.events, wantSecond) {
		t.Fatalf("second resolution events = %v, want %v", obs.events, wantSecond)
	}
}

func TestNilObserverKeepsResolutionSilent(t *testing.T) {
	// The pipeline must not panic (nor emit) when no observer is attached.
	ctx := context.Background()

	regRoot := t.TempDir()
	storeRoot := filepath.Join(t.TempDir(), "store")
	writeExecutorPackage(t, regRoot, "github:read", "1.2.0")

	reg, err := local.New(regRoot)
	if err != nil {
		t.Fatal(err)
	}
	fsStore, err := execstore.NewFilesystemStore(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	catalog := executor.NewRegistry()
	if err := catalog.Add(reg); err != nil {
		t.Fatal(err)
	}

	installer := &executor.Installer{Store: fsStore, Downloader: executor.NewHTTPDownloader()}
	resolver := executor.NewResolver(catalog, fsStore, installer)

	installed, err := resolver.Resolve(ctx, executor.Requirement{Type: "github:read", Version: "^1.0.0", Registries: []string{"local"}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if installed.Version != "1.2.0" {
		t.Fatalf("resolved version = %q, want 1.2.0", installed.Version)
	}
}
