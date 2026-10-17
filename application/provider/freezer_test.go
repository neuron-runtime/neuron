package provider_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neuron-runtime/neuron/application/provider"
	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
	providertypes "github.com/neuron-runtime/neuron/shared/types/provider"
)

// installWithManifest installs a provider whose manifest declares the given
// runtime kinds, under the given registry provenance.
func installWithManifest(t *testing.T, installer *provider.Installer, name, version, registry string, kinds ...string) {
	t.Helper()
	m := &providertypes.Manifest{
		APIVersion:   providertypes.APIVersion,
		Kind:         providertypes.Kind,
		Metadata:     providertypes.ManifestMetadata{Name: name, Version: version},
		Provider:     providertypes.ManifestProvider{ProtocolVersion: providertypes.ProtocolVersion},
		RuntimeKinds: kinds,
		Platforms:    map[string]providertypes.Platform{"any": {}},
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("validate manifest %s@%s: %v", name, version, err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, providertypes.ManifestFile), []byte(mustJSON(t, m)), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "payload.bin"), []byte("payload-"+name+"-"+version), 0o644); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	if _, err := installer.Install(context.Background(), &provider.Package{
		Name: name, Version: version, Registry: registry, Source: dir,
	}); err != nil {
		t.Fatalf("install %s@%s: %v", name, version, err)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(data)
}

func TestFreezeForKindsExactNameWins(t *testing.T) {
	installer, store := newInstaller(t)
	installWithManifest(t, installer, "aaa", "1.0.0", "local", capabilityrt.RuntimeKindProcess)
	installWithManifest(t, installer, "process", "0.9.0", "local", capabilityrt.RuntimeKindProcess)

	frozen, err := provider.FreezeForKinds(context.Background(), store, []string{capabilityrt.RuntimeKindProcess})
	if err != nil {
		t.Fatalf("FreezeForKinds: %v", err)
	}
	if len(frozen) != 1 || frozen[0].Name != "process" {
		t.Fatalf("frozen = %+v, want the exact-name provider", frozen)
	}
	if frozen[0].ResolvedVersion != "0.9.0" {
		t.Fatalf("version = %s, want 0.9.0", frozen[0].ResolvedVersion)
	}
	if frozen[0].Registry != "local" {
		t.Fatalf("registry = %q, want local", frozen[0].Registry)
	}
	if frozen[0].ProtocolVersion != providertypes.ProtocolVersion {
		t.Fatalf("protocol = %q, want %q", frozen[0].ProtocolVersion, providertypes.ProtocolVersion)
	}
	if len(frozen[0].RuntimeKinds) != 1 || frozen[0].RuntimeKinds[0] != capabilityrt.RuntimeKindProcess {
		t.Fatalf("runtimeKinds = %v", frozen[0].RuntimeKinds)
	}
}

func TestFreezeForKindsPicksHighestVersionAndLexicographicName(t *testing.T) {
	installer, store := newInstaller(t)
	installWithManifest(t, installer, "zeta", "2.0.0", "local", capabilityrt.RuntimeKindWasm)
	installWithManifest(t, installer, "zeta", "1.0.0", "local", capabilityrt.RuntimeKindWasm)
	installWithManifest(t, installer, "alpha", "1.0.0", "local", capabilityrt.RuntimeKindWasm)

	frozen, err := provider.FreezeForKinds(context.Background(), store, []string{capabilityrt.RuntimeKindWasm})
	if err != nil {
		t.Fatalf("FreezeForKinds: %v", err)
	}
	if len(frozen) != 1 || frozen[0].Name != "alpha" || frozen[0].ResolvedVersion != "1.0.0" {
		t.Fatalf("frozen = %+v, want alpha@1.0.0 (lexicographic name)", frozen)
	}
}

func TestFreezeForKindsMissingKindIsActionable(t *testing.T) {
	_, store := newInstaller(t)

	_, err := provider.FreezeForKinds(context.Background(), store, []string{capabilityrt.RuntimeKindProcess})
	if !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("FreezeForKinds = %v, want ErrNotFound", err)
	}
	for _, want := range []string{"neuron provider build", "neuron install --provider"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q must name %q", err, want)
		}
	}
}

func TestFreezeForKindsDeduplicatesProviderAcrossKinds(t *testing.T) {
	installer, store := newInstaller(t)
	installWithManifest(t, installer, "multi", "1.0.0", "bootstrap",
		capabilityrt.RuntimeKindProcess, capabilityrt.RuntimeKindWasm)

	frozen, err := provider.FreezeForKinds(context.Background(), store, []string{
		capabilityrt.RuntimeKindProcess,
		capabilityrt.RuntimeKindWasm,
	})
	if err != nil {
		t.Fatalf("FreezeForKinds: %v", err)
	}
	if len(frozen) != 1 || frozen[0].Name != "multi" {
		t.Fatalf("frozen = %+v, want one entry for the multi-kind provider", frozen)
	}
	if len(frozen[0].RuntimeKinds) != 2 {
		t.Fatalf("runtimeKinds = %v, want both kinds", frozen[0].RuntimeKinds)
	}
}

func TestFreezeForKindsEmptyInput(t *testing.T) {
	_, store := newInstaller(t)
	frozen, err := provider.FreezeForKinds(context.Background(), store, nil)
	if err != nil {
		t.Fatalf("FreezeForKinds(nil): %v", err)
	}
	if frozen != nil {
		t.Fatalf("frozen = %+v, want nil", frozen)
	}
}

func TestFreezeForKindsIsDeterministic(t *testing.T) {
	installer, store := newInstaller(t)
	installWithManifest(t, installer, "b", "1.0.0", "local", capabilityrt.RuntimeKindProcess)
	installWithManifest(t, installer, "a", "1.0.0", "local", capabilityrt.RuntimeKindProcess)
	installWithManifest(t, installer, "wasm", "1.0.0", "local", capabilityrt.RuntimeKindWasm)

	kinds := []string{capabilityrt.RuntimeKindWasm, capabilityrt.RuntimeKindProcess}
	first, err := provider.FreezeForKinds(context.Background(), store, kinds)
	if err != nil {
		t.Fatalf("FreezeForKinds: %v", err)
	}
	second, err := provider.FreezeForKinds(context.Background(), store, kinds)
	if err != nil {
		t.Fatalf("FreezeForKinds: %v", err)
	}
	if len(first) != len(second) {
		t.Fatalf("freeze lengths differ: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if !equalResolved(first[i], second[i]) {
			t.Fatalf("freeze not deterministic: %+v vs %+v", first[i], second[i])
		}
	}
	// Output is ordered by provider name.
	if first[0].Name != "a" || first[1].Name != "wasm" {
		t.Fatalf("frozen order = [%s %s], want [a wasm]", first[0].Name, first[1].Name)
	}
}

func equalResolved(a, b providertypes.ResolvedProvider) bool {
	if a.Name != b.Name || a.ResolvedVersion != b.ResolvedVersion || a.Digest != b.Digest ||
		a.ProtocolVersion != b.ProtocolVersion || a.Registry != b.Registry {
		return false
	}
	if len(a.RuntimeKinds) != len(b.RuntimeKinds) {
		return false
	}
	for i := range a.RuntimeKinds {
		if a.RuntimeKinds[i] != b.RuntimeKinds[i] {
			return false
		}
	}
	return true
}

func TestFreezeForKindsSurvivesVersionCoexistence(t *testing.T) {
	installer, store := newInstaller(t)
	installWithManifest(t, installer, "process", "1.0.0", "local", capabilityrt.RuntimeKindProcess)
	installWithManifest(t, installer, "process", "0.9.0", "local", capabilityrt.RuntimeKindProcess)

	// A build frozen against 0.9.0 keeps resolving that exact identity even
	// after a newer version is installed beside it.
	old, err := store.Get(context.Background(), "process", "0.9.0")
	if err != nil {
		t.Fatalf("store.Get(old): %v", err)
	}

	frozen, err := provider.FreezeForKinds(context.Background(), store, []string{capabilityrt.RuntimeKindProcess})
	if err != nil {
		t.Fatalf("FreezeForKinds: %v", err)
	}
	if len(frozen) != 1 || frozen[0].ResolvedVersion != "1.0.0" {
		t.Fatalf("frozen = %+v, want newest 1.0.0", frozen)
	}
	if frozen[0].Digest == old.Digest {
		t.Fatal("distinct versions must have distinct digests")
	}
	newest, err := store.Get(context.Background(), "process", "1.0.0")
	if err != nil {
		t.Fatalf("store.Get(new): %v", err)
	}
	if newest.RootDir == old.RootDir {
		t.Fatal("coexisting versions must not share an artifact directory")
	}
}

func TestFreezeForKindsResolvesBootstrappedProviders(t *testing.T) {
	installer, store := newInstaller(t)
	boot, err := provider.Bootstrap(context.Background(), installer, "")
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	frozen, err := provider.FreezeForKinds(context.Background(), store, []string{
		capabilityrt.RuntimeKindProcess,
		capabilityrt.RuntimeKindWasm,
	})
	if err != nil {
		t.Fatalf("FreezeForKinds: %v", err)
	}
	if len(frozen) != 2 {
		t.Fatalf("frozen = %+v, want process + wasm", frozen)
	}
	byName := make(map[string]provider.BootstrapResult, len(boot))
	for _, b := range boot {
		byName[b.Name] = b
	}
	for _, f := range frozen {
		b, ok := byName[f.Name]
		if !ok {
			t.Fatalf("frozen provider %q was not bootstrapped", f.Name)
		}
		if f.Digest != b.Digest || f.ResolvedVersion != b.Version {
			t.Fatalf("frozen %s = %s@%s (%s), want bootstrap identity %s@%s (%s)",
				f.Name, f.ResolvedVersion, f.Digest[:16], f.Registry, b.Version, b.Digest[:16], provider.BootstrapRegistry)
		}
		if f.Registry != provider.BootstrapRegistry {
			t.Fatalf("frozen %s registry = %q, want %q", f.Name, f.Registry, provider.BootstrapRegistry)
		}
	}

	// The frozen digest must address the committed artifact directory, so a
	// build record can locate it later without persisting host paths.
	for _, f := range frozen {
		installed, err := store.Get(context.Background(), f.Name, f.ResolvedVersion)
		if err != nil {
			t.Fatalf("store.Get(%s): %v", f.Name, err)
		}
		if _, err := os.Stat(installed.ManifestPath); err != nil {
			t.Fatalf("frozen identity does not address an installed artifact: %v", err)
		}
	}
}
