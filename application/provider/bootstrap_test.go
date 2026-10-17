package provider_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/neuron-runtime/neuron/application/provider"
	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
	providertypes "github.com/neuron-runtime/neuron/shared/types/provider"
)

func TestBootstrapInstallsDescriptorOnlyProviders(t *testing.T) {
	installer, store := newInstaller(t)

	results, err := provider.Bootstrap(context.Background(), installer, "")
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("Bootstrap results = %d, want 2 (process, wasm)", len(results))
	}

	want := map[string]struct {
		kind     string
		platform string
	}{
		"process": {capabilityrt.RuntimeKindProcess, runtime.GOOS + "-" + runtime.GOARCH},
		"wasm":    {capabilityrt.RuntimeKindWasm, capabilityrt.PlatformWasm},
	}

	for _, res := range results {
		if res.AlreadyPresent {
			t.Errorf("%s: fresh bootstrap reported already present", res.Name)
		}
		if res.Version != provider.FirstPartyVersion {
			t.Errorf("%s: version = %q, want %q", res.Name, res.Version, provider.FirstPartyVersion)
		}

		installed, err := store.Get(context.Background(), res.Name, res.Version)
		if err != nil {
			t.Fatalf("store.Get(%s): %v", res.Name, err)
		}
		if installed.Digest != res.Digest {
			t.Errorf("%s: digest mismatch bootstrap=%s store=%s", res.Name, res.Digest, installed.Digest)
		}
		if installed.Registry != provider.BootstrapRegistry {
			t.Errorf("%s: registry = %q, want %q", res.Name, installed.Registry, provider.BootstrapRegistry)
		}

		// Descriptor-only: the committed tree carries exactly the manifest
		// and the install record — no fabricated payload.
		entries, err := os.ReadDir(installed.RootDir)
		if err != nil {
			t.Fatalf("read installed dir: %v", err)
		}
		files := make(map[string]bool, len(entries))
		for _, e := range entries {
			files[e.Name()] = true
		}
		if !files[providertypes.ManifestFile] || !files[providertypes.InstallFile] || len(files) != 2 {
			t.Errorf("%s: committed files = %v, want exactly manifest + install record", res.Name, files)
		}

		m := installed.Manifest
		if m == nil {
			t.Fatalf("%s: installed manifest is nil", res.Name)
		}
		if err := m.Validate(); err != nil {
			t.Errorf("%s: manifest invalid: %v", res.Name, err)
		}

		exp, ok := want[res.Name]
		if !ok {
			t.Errorf("unexpected first-party provider %q", res.Name)
			continue
		}
		if len(m.RuntimeKinds) != 1 || m.RuntimeKinds[0] != exp.kind {
			t.Errorf("%s: runtimeKinds = %v, want [%s]", res.Name, m.RuntimeKinds, exp.kind)
		}
		if _, ok := m.Platforms[exp.platform]; !ok {
			t.Errorf("%s: platforms = %v, want key %q", res.Name, m.Platforms, exp.platform)
		}
	}
}

func TestBootstrapIsIdempotentAndDeterministic(t *testing.T) {
	installer, _ := newInstaller(t)

	first, err := provider.Bootstrap(context.Background(), installer, "")
	if err != nil {
		t.Fatalf("first Bootstrap: %v", err)
	}
	second, err := provider.Bootstrap(context.Background(), installer, "")
	if err != nil {
		t.Fatalf("second Bootstrap: %v", err)
	}
	for i := range first {
		if !second[i].AlreadyPresent {
			t.Errorf("%s: rebuild not reported as already present", second[i].Name)
		}
		if second[i].Digest != first[i].Digest {
			t.Errorf("%s: rebuild digest %s != original %s", second[i].Name, second[i].Digest, first[i].Digest)
		}
	}

	// A rebuild into a fresh store must produce the identical identity, so
	// the build is repeatable wherever it runs.
	installer2, _ := newInstaller(t)
	third, err := provider.Bootstrap(context.Background(), installer2, "")
	if err != nil {
		t.Fatalf("fresh-store Bootstrap: %v", err)
	}
	for i := range first {
		if third[i].Digest != first[i].Digest {
			t.Errorf("%s: fresh-store digest %s != original %s", third[i].Name, third[i].Digest, first[i].Digest)
		}
	}
}

func TestBootstrapProvidersResolveFromStore(t *testing.T) {
	installer, store := newInstaller(t)
	if _, err := provider.Bootstrap(context.Background(), installer, ""); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	// No registry sources at all: an exact pin must resolve straight from
	// the store, proving bootstrapped artifacts behave as installed.
	resolver := provider.NewResolver(provider.NewRegistry(), store, installer)
	for _, name := range []string{"process", "wasm"} {
		installed, err := resolver.Resolve(context.Background(), provider.Requirement{
			Name:    name,
			Version: provider.FirstPartyVersion,
		})
		if err != nil {
			t.Fatalf("Resolve(%s): %v", name, err)
		}
		if installed.Version != provider.FirstPartyVersion {
			t.Fatalf("Resolve(%s) = %s, want %s", name, installed.Version, provider.FirstPartyVersion)
		}
	}
}

func TestBootstrapRejectsNilInstaller(t *testing.T) {
	if _, err := provider.Bootstrap(context.Background(), nil, ""); err == nil {
		t.Fatal("Bootstrap with nil installer succeeded, want error")
	}
}

func TestBootstrapInstalledManifestSurvivesReadBack(t *testing.T) {
	installer, store := newInstaller(t)
	if _, err := provider.Bootstrap(context.Background(), installer, "9.9.9"); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	installed, err := store.Get(context.Background(), "process", "9.9.9")
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(installed.RootDir, providertypes.ManifestFile))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	m, err := provider.ParseManifest(data)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if m.Metadata.Version != "9.9.9" {
		t.Fatalf("manifest version = %q, want 9.9.9", m.Metadata.Version)
	}
}
