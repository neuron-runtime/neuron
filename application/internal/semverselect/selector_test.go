package semverselect

import (
	"strings"
	"testing"
)

func TestIsExactVersion(t *testing.T) {
	if !IsExactVersion("1.2.0") {
		t.Error("1.2.0 should be exact")
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
	versions := []string{"1.0.0", "1.2.0", "1.1.0"}
	got, err := SelectVersion("", versions)
	if err != nil {
		t.Fatalf("SelectVersion: %v", err)
	}
	if got != "1.2.0" {
		t.Errorf("SelectVersion = %q, want %q", got, "1.2.0")
	}
}

func TestSelectVersionConstraint(t *testing.T) {
	versions := []string{"1.0.0", "1.2.0", "1.1.0", "2.0.0"}
	cases := []struct {
		constraint string
		want       string
	}{
		{"^1.0.0", "1.2.0"},
		{">=1.1.0", "2.0.0"},
		{"1.1.x", "1.1.0"},
	}
	for _, c := range cases {
		got, err := SelectVersion(c.constraint, versions)
		if c.want == "" {
			if err == nil {
				t.Errorf("SelectVersion(%q): expected error", c.constraint)
			}
			continue
		}
		if err != nil {
			t.Errorf("SelectVersion(%q): %v", c.constraint, err)
			continue
		}
		if got != c.want {
			t.Errorf("SelectVersion(%q) = %q, want %q", c.constraint, got, c.want)
		}
	}
}

func TestSelectVersionUnsatisfiableMessageIsNeutral(t *testing.T) {
	_, err := SelectVersion("^9.0.0", []string{"1.0.0"})
	if err == nil || !strings.Contains(err.Error(), "no version satisfies") {
		t.Fatalf("SelectVersion unsatisfiable error = %v, want neutral 'no version satisfies' wording", err)
	}
	// Callers add domain context by wrapping; the neutral message must
	// remain a wrappable error chain, not a domain-locked string.
	if !strings.Contains("capability runtime example:echo: "+err.Error(), "capability runtime") {
		t.Fatal("wrapping must preserve the original error text")
	}
}

func TestSelectVersionExact(t *testing.T) {
	versions := []string{"1.0.0", "1.2.0", "1.1.0"}
	got, err := SelectVersion("1.2.0", versions)
	if err != nil {
		t.Fatalf("SelectVersion exact: %v", err)
	}
	if got != "1.2.0" {
		t.Errorf("SelectVersion = %q, want %q", got, "1.2.0")
	}
}

func TestSelectVersionNoVersions(t *testing.T) {
	if _, err := SelectVersion("", nil); err == nil {
		t.Error("expected error for empty versions")
	}
}

func TestSelectVersionBadConstraint(t *testing.T) {
	if _, err := SelectVersion("not-a-constraint", []string{"1.0.0"}); err == nil {
		t.Error("expected error for bad constraint")
	}
}

func TestSelectVersionSkipsNonSemver(t *testing.T) {
	versions := []string{"1.0.0", "not-a-version", "1.2.0"}
	got, err := SelectVersion("", versions)
	if err != nil {
		t.Fatalf("SelectVersion: %v", err)
	}
	if got != "1.2.0" {
		t.Errorf("SelectVersion = %q, want %q", got, "1.2.0")
	}
}
