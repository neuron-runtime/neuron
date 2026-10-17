package github

import (
	"testing"

	"github.com/neuron-runtime/neuron/application/internal/githubrepo"
	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
)

func TestPackageArchiveAsset(t *testing.T) {
	exact := githubrepo.Asset{Name: capabilityrt.PackageArchiveName("example:echo", "1.0.0"), BrowserDownloadURL: "https://example/exact"}
	other := githubrepo.Asset{Name: "example-echo-other-capability-runtime.neuron.tar.gz", BrowserDownloadURL: "https://example/other"}
	legacy := githubrepo.Asset{Name: "echo-linux-amd64", BrowserDownloadURL: "https://example/legacy"}

	cases := []struct {
		name    string
		release *githubrepo.Release
		typ     string
		version string
		want    string
		ok      bool
	}{
		{"exact name wins", &githubrepo.Release{Assets: []githubrepo.Asset{legacy, exact}}, "example:echo", "1.0.0", "https://example/exact", true},
		{"suffix fallback", &githubrepo.Release{Assets: []githubrepo.Asset{legacy, other}}, "example:echo", "1.0.0", "https://example/other", true},
		{"none present", &githubrepo.Release{Assets: []githubrepo.Asset{legacy}}, "example:echo", "1.0.0", "", false},
		{"no assets", &githubrepo.Release{}, "example:echo", "1.0.0", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := packageArchiveAsset(tc.release, tc.typ, tc.version)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if ok && got.BrowserDownloadURL != tc.want {
				t.Errorf("asset URL = %q, want %q", got.BrowserDownloadURL, tc.want)
			}
		})
	}
}
