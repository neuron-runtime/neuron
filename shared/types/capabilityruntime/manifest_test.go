package capabilityruntime

import (
	"strings"
	"testing"
)

func TestPackageArchiveName(t *testing.T) {
	cases := []struct {
		name, version string
		want          string
	}{
		{"github:read", "1.2.0", "github-read-1.2.0" + PackageArchiveSuffix},
		{"example:echo", "1.0.0", "example-echo-1.0.0" + PackageArchiveSuffix},
		{"Muhammad-Jay/github:read", "v1.0.0", "Muhammad-Jay-github-read-v1.0.0" + PackageArchiveSuffix},
		{"  github:read  ", " 2.0.0 ", "github-read-2.0.0" + PackageArchiveSuffix},
	}
	for _, tc := range cases {
		if got := PackageArchiveName(tc.name, tc.version); got != tc.want {
			t.Errorf("PackageArchiveName(%q, %q) = %q, want %q", tc.name, tc.version, got, tc.want)
		}
	}
}

func TestPackageArchiveConstants(t *testing.T) {
	if !strings.HasSuffix(PackageArchiveSuffix, ".tar.gz") {
		t.Errorf("PackageArchiveSuffix %q must end in .tar.gz", PackageArchiveSuffix)
	}
	if PackageArchiveName("a", "1") == "" {
		t.Error("PackageArchiveName must never return an empty name")
	}
}
