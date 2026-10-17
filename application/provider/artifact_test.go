package provider_test

import (
	"testing"

	"github.com/neuron-runtime/neuron/application/provider"
	providertypes "github.com/neuron-runtime/neuron/shared/types/provider"
)

// The store path segment and the canonical archive name apply the same
// provider-name sanitization in two different modules; this test pins both
// so the rules cannot drift apart silently.
func TestNameSanitizationMatchesAcrossLayouts(t *testing.T) {
	path, err := provider.ProviderTypePath("a:b/c")
	if err != nil {
		t.Fatalf("provider type path: %v", err)
	}
	if path != "a-b-c" {
		t.Fatalf("ProviderTypePath = %q, want %q", path, "a-b-c")
	}
	if got, want := providertypes.PackageArchiveName("a:b/c", "1.0.0"), "a-b-c-1.0.0"+providertypes.PackageArchiveSuffix; got != want {
		t.Fatalf("PackageArchiveName = %q, want %q", got, want)
	}
}

func TestProviderTypePathRejectsEmptyName(t *testing.T) {
	if _, err := provider.ProviderTypePath("  "); err == nil {
		t.Fatal("empty provider name must be rejected")
	}
}
