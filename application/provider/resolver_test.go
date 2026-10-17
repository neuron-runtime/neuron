package provider_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/neuron-runtime/neuron/application/provider"
)

// fakeSource is a scriptable provider.Source for resolver tests.
type fakeSource struct {
	name        string
	versions    []string
	pkgs        map[string]*provider.Package
	versionsErr error
}

func (f *fakeSource) Name() string { return f.name }

func (f *fakeSource) Versions(ctx context.Context, name string) ([]string, error) {
	if f.versionsErr != nil {
		return nil, f.versionsErr
	}
	if f.versions == nil {
		return nil, provider.ErrNotFound
	}
	return f.versions, nil
}

func (f *fakeSource) Package(ctx context.Context, name, version string) (*provider.Package, error) {
	pkg, ok := f.pkgs[version]
	if !ok {
		return nil, provider.ErrNotFound
	}
	return pkg, nil
}

func newResolver(t *testing.T, sources ...provider.Source) (*provider.Resolver, provider.Store) {
	t.Helper()
	installer, store := newInstaller(t)
	reg := provider.NewRegistry()
	for _, src := range sources {
		if err := reg.Add(src); err != nil {
			t.Fatalf("add source: %v", err)
		}
	}
	return provider.NewResolver(reg, store, installer), store
}

func TestResolveInstallsFromRegistry(t *testing.T) {
	srcDir := writeSource(t, "process", "1.0.0", map[string]string{"payload.bin": "hello"})
	src := &fakeSource{
		name:     "local",
		versions: []string{"1.0.0"},
		pkgs: map[string]*provider.Package{
			"1.0.0": {Name: "process", Version: "1.0.0", Registry: "local", Source: srcDir},
		},
	}
	resolver, _ := newResolver(t, src)

	installed, err := resolver.Resolve(context.Background(), provider.Requirement{
		Name:       "process",
		Registries: []string{"local"},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if installed.Version != "1.0.0" || installed.Registry != "local" {
		t.Fatalf("installed = %+v, want 1.0.0 from local", installed)
	}
}

func TestResolvePrefersInstalledExactPin(t *testing.T) {
	installer, store := newInstaller(t)
	srcDir := writeSource(t, "process", "1.0.0", map[string]string{"payload.bin": "hello"})
	if _, err := installer.Install(context.Background(), &provider.Package{
		Name: "process", Version: "1.0.0", Registry: "local", Source: srcDir,
	}); err != nil {
		t.Fatalf("seed install: %v", err)
	}

	// The registry would fail loudly if consulted.
	src := &fakeSource{name: "local", versionsErr: fmt.Errorf("network must not be touched")}
	reg := provider.NewRegistry()
	if err := reg.Add(src); err != nil {
		t.Fatalf("add source: %v", err)
	}
	resolver := provider.NewResolver(reg, store, installer)

	installed, err := resolver.Resolve(context.Background(), provider.Requirement{
		Name:       "process",
		Version:    "1.0.0",
		Registries: []string{"local"},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if installed.Version != "1.0.0" {
		t.Fatalf("installed = %+v, want exact pin 1.0.0", installed)
	}
}

func TestResolveConstraintSatisfiedFromStore(t *testing.T) {
	installer, store := newInstaller(t)
	srcDir := writeSource(t, "process", "1.2.0", map[string]string{"payload.bin": "hello"})
	if _, err := installer.Install(context.Background(), &provider.Package{
		Name: "process", Version: "1.2.0", Registry: "local", Source: srcDir,
	}); err != nil {
		t.Fatalf("seed install: %v", err)
	}

	src := &fakeSource{name: "local", versionsErr: fmt.Errorf("network must not be touched")}
	reg := provider.NewRegistry()
	_ = reg.Add(src)
	resolver := provider.NewResolver(reg, store, installer)

	installed, err := resolver.Resolve(context.Background(), provider.Requirement{
		Name:       "process",
		Version:    "^1.0.0",
		Registries: []string{"local"},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if installed.Version != "1.2.0" {
		t.Fatalf("installed = %+v, want best store match 1.2.0", installed)
	}
}

func TestResolveUnconfiguredRegistry(t *testing.T) {
	resolver, _ := newResolver(t)
	_, err := resolver.Resolve(context.Background(), provider.Requirement{Name: "process"})
	if !errors.Is(err, provider.ErrRegistryNotConfigured) {
		t.Fatalf("Resolve with no registries = %v, want ErrRegistryNotConfigured", err)
	}
}

func TestResolveNotFoundIsActionable(t *testing.T) {
	src := &fakeSource{name: "local"} // no versions
	resolver, _ := newResolver(t, src)

	_, err := resolver.Resolve(context.Background(), provider.Requirement{
		Name:       "process",
		Registries: []string{"local"},
	})
	if !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("Resolve = %v, want ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "neuron install --provider") {
		t.Fatalf("Resolve error %q must name the install command", err)
	}
}

func TestResolveSelectsBestRegistryVersion(t *testing.T) {
	srcDirOld := writeSource(t, "process", "1.0.0", map[string]string{"payload.bin": "old"})
	srcDirNew := writeSource(t, "process", "1.2.0", map[string]string{"payload.bin": "new"})
	src := &fakeSource{
		name:     "local",
		versions: []string{"1.0.0", "1.2.0"},
		pkgs: map[string]*provider.Package{
			"1.0.0": {Name: "process", Version: "1.0.0", Registry: "local", Source: srcDirOld},
			"1.2.0": {Name: "process", Version: "1.2.0", Registry: "local", Source: srcDirNew},
		},
	}
	resolver, _ := newResolver(t, src)

	installed, err := resolver.Resolve(context.Background(), provider.Requirement{
		Name:       "process",
		Version:    "^1.0.0",
		Registries: []string{"local"},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if installed.Version != "1.2.0" {
		t.Fatalf("installed = %+v, want newest satisfying 1.2.0", installed)
	}
}
