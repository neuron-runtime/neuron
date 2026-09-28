package capabilityruntime

import (
	"strings"
	"testing"
)

func TestIsExactVersion(t *testing.T) {
	if !IsExactVersion("1.2.0") {
		t.Error("1.2.0 should be an exact version")
	}
	if IsExactVersion("") {
		t.Error("empty should not be exact")
	}
	if IsExactVersion("^1.0.0") {
		t.Error("^1.0.0 should not be exact")
	}
	if IsExactVersion(">=2.0.0") {
		t.Error(">=2.0.0 should not be exact")
	}
}

func TestSelectVersionLatest(t *testing.T) {
	versions := []string{"1.0.0", "2.0.0", "1.5.0", "3.0.0"}
	got, err := SelectVersion("", versions)
	if err != nil {
		t.Fatalf("SelectVersion: %v", err)
	}
	if got != "3.0.0" {
		t.Errorf("latest = %q, want 3.0.0", got)
	}
}

func TestSelectVersionConstraint(t *testing.T) {
	versions := []string{"1.0.0", "1.5.0", "1.9.0", "2.0.0", "2.3.0", "3.0.0"}

	cases := []struct {
		constraint string
		want       string
	}{
		{"^1.0.0", "1.9.0"},
		{"~1.5.0", "1.5.0"},
		{"1.2.0", ""}, // exact pin not present
		{">=2.0.0", "3.0.0"},
	}

	for _, c := range cases {
		got, err := SelectVersion(c.constraint, versions)
		if err != nil {
			if c.want == "" && !strings.Contains(err.Error(), "no capability runtime version satisfies") {
				t.Errorf("SelectVersion(%q): %v", c.constraint, err)
			}
			continue
		}
		if got != c.want {
			t.Errorf("SelectVersion(%q) = %q, want %q", c.constraint, got, c.want)
		}
	}
}

func TestSelectVersionExact(t *testing.T) {
	versions := []string{"1.0.0", "1.2.0", "2.0.0"}
	got, err := SelectVersion("1.2.0", versions)
	if err != nil {
		t.Fatalf("SelectVersion exact: %v", err)
	}
	if got != "1.2.0" {
		t.Errorf("exact = %q, want 1.2.0", got)
	}
}

func TestSelectVersionNoVersions(t *testing.T) {
	if _, err := SelectVersion("", nil); err == nil {
		t.Error("expected error when no versions available")
	}
}

func TestSelectVersionBadConstraint(t *testing.T) {
	if _, err := SelectVersion("not-a-constraint", []string{"1.0.0"}); err == nil {
		t.Error("expected error for malformed constraint")
	}
}

func TestSelectVersionSkipsNonSemver(t *testing.T) {
	versions := []string{"latest", "foo", "1.0.0"}
	got, err := SelectVersion("", versions)
	if err != nil {
		t.Fatalf("SelectVersion: %v", err)
	}
	if got != "1.0.0" {
		t.Errorf("got %q, want 1.0.0 (non-semver entries ignored)", got)
	}
}
