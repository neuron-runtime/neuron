package capabilityruntime_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/neuron-runtime/neuron/application/capabilityruntime"
)

// memStore is a minimal in-memory capabilityruntime.Store used to exercise
// store-only resolution without touching the filesystem. Only Get and List are
// meaningfully implemented; the write methods are unused by ResolveInstalled.
type memStore struct {
	entries map[string]map[string]*capabilityruntime.Installed
}

func newMemStore() *memStore {
	return &memStore{entries: map[string]map[string]*capabilityruntime.Installed{}}
}

func (s *memStore) add(typ, version string) *memStore {
	if s.entries[typ] == nil {
		s.entries[typ] = map[string]*capabilityruntime.Installed{}
	}
	s.entries[typ][version] = &capabilityruntime.Installed{Type: typ, Version: version}
	return s
}

func (s *memStore) Root() string { return "/store" }

func (s *memStore) InstalledDir(typ, version, digest string) (string, error) {
	return "/store/" + typ + "/" + version, nil
}

func (s *memStore) Stage() (string, error) { return "/store/.stage", nil }

func (s *memStore) Commit(_ context.Context, _, typ, version, _ string) (*capabilityruntime.Installed, error) {
	return s.entries[typ][version], nil
}

func (s *memStore) Get(_ context.Context, typ, version string) (*capabilityruntime.Installed, error) {
	if in, ok := s.entries[typ][version]; ok {
		return in, nil
	}
	return nil, &capabilityruntime.NotFoundError{Type: typ, Version: version, Err: capabilityruntime.ErrNotFound}
}

func (s *memStore) List(_ context.Context, typ string) ([]capabilityruntime.Installed, error) {
	out := make([]capabilityruntime.Installed, 0, len(s.entries[typ]))
	for _, in := range s.entries[typ] {
		out = append(out, *in)
	}
	return out, nil
}

func (s *memStore) Remove(_ context.Context, typ, version string) error {
	delete(s.entries[typ], version)
	return nil
}

func TestResolveInstalled(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		store   *memStore
		typ     string
		version string
		want    string
	}{
		{
			name:    "exact version",
			store:   newMemStore().add("github:read", "1.2.0").add("github:read", "1.3.0"),
			typ:     "github:read",
			version: "1.2.0",
			want:    "1.2.0",
		},
		{
			name:    "empty version selects highest",
			store:   newMemStore().add("github:read", "1.2.0").add("github:read", "1.10.0"),
			typ:     "github:read",
			version: "",
			want:    "1.10.0",
		},
		{
			name:    "latest selects highest",
			store:   newMemStore().add("github:read", "1.2.0").add("github:read", "2.0.0"),
			typ:     "github:read",
			version: "latest",
			want:    "2.0.0",
		},
		{
			name:    "constraint selects best satisfying",
			store:   newMemStore().add("github:read", "1.2.0").add("github:read", "1.9.0").add("github:read", "2.0.0"),
			typ:     "github:read",
			version: "^1.0.0",
			want:    "1.9.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := capabilityruntime.ResolveInstalled(ctx, tt.store, tt.typ, tt.version)
			if err != nil {
				t.Fatalf("ResolveInstalled() error = %v", err)
			}
			if got.Version != tt.want {
				t.Fatalf("ResolveInstalled() version = %q, want %q", got.Version, tt.want)
			}
		})
	}
}

func TestResolveInstalledErrors(t *testing.T) {
	ctx := context.Background()

	t.Run("exact not installed", func(t *testing.T) {
		_, err := capabilityruntime.ResolveInstalled(ctx, newMemStore(), "github:read", "9.9.9")
		if !errors.Is(err, capabilityruntime.ErrNotFound) {
			t.Fatalf("error = %v, want ErrNotFound", err)
		}
		if !strings.Contains(err.Error(), "neuron add github:read@9.9.9") {
			t.Fatalf("error %q does not suggest an install command", err)
		}
	})

	t.Run("latest not installed", func(t *testing.T) {
		_, err := capabilityruntime.ResolveInstalled(ctx, newMemStore(), "github:read", "latest")
		if !errors.Is(err, capabilityruntime.ErrNotFound) {
			t.Fatalf("error = %v, want ErrNotFound", err)
		}
	})

	t.Run("constraint unsatisfied", func(t *testing.T) {
		_, err := capabilityruntime.ResolveInstalled(ctx, newMemStore().add("github:read", "1.0.0"), "github:read", "^2.0.0")
		if !errors.Is(err, capabilityruntime.ErrNotFound) {
			t.Fatalf("error = %v, want ErrNotFound", err)
		}
		if !strings.Contains(err.Error(), "neuron add github:read@^2.0.0") {
			t.Fatalf("error %q does not name the requested constraint", err)
		}
	})

	t.Run("invalid constraint", func(t *testing.T) {
		_, err := capabilityruntime.ResolveInstalled(ctx, newMemStore().add("github:read", "1.0.0"), "github:read", "not-a-version")
		if err == nil {
			t.Fatal("expected error for invalid version")
		}
		if errors.Is(err, capabilityruntime.ErrNotFound) {
			t.Fatalf("invalid constraint reported as not-found: %v", err)
		}
		if !strings.Contains(err.Error(), "invalid capability runtime version") {
			t.Fatalf("error %q does not describe an invalid version", err)
		}
	})

	t.Run("nil store", func(t *testing.T) {
		_, err := capabilityruntime.ResolveInstalled(ctx, nil, "github:read", "1.0.0")
		if !errors.Is(err, capabilityruntime.ErrNotFound) {
			t.Fatalf("error = %v, want ErrNotFound", err)
		}
	})

	t.Run("empty type", func(t *testing.T) {
		if _, err := capabilityruntime.ResolveInstalled(ctx, newMemStore(), "", "1.0.0"); err == nil {
			t.Fatal("expected error for empty type")
		}
	})

	t.Run("invalid type", func(t *testing.T) {
		if _, err := capabilityruntime.ResolveInstalled(ctx, newMemStore(), "not a valid type", "1.0.0"); err == nil {
			t.Fatal("expected error for invalid type")
		}
	})
}
