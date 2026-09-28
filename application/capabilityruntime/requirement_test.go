package capabilityruntime

import (
	"testing"
)

func TestParseType(t *testing.T) {
	cases := []struct {
		in       string
		owner    string
		segments []string
		wantErr  bool
	}{
		{"github:read", "github", []string{"read"}, false},
		{"Muhammad-Jay:github:read", "Muhammad-Jay", []string{"github", "read"}, false},
		{"hashicorp:vault:auth", "hashicorp", []string{"vault", "auth"}, false},
		{"foo", "", nil, true},
		{":read", "", nil, true},
		{"foo::bar", "", nil, true},
		{"", "", nil, true},
	}

	for _, c := range cases {
		split, err := ParseType(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseType(%q): expected error, got %+v", c.in, split)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseType(%q): %v", c.in, err)
			continue
		}
		if split.Owner != c.owner {
			t.Errorf("ParseType(%q) owner = %q, want %q", c.in, split.Owner, c.owner)
		}
		if len(split.PathSegments) != len(c.segments) {
			t.Errorf("ParseType(%q) segments = %v, want %v", c.in, split.PathSegments, c.segments)
			continue
		}
		for i := range c.segments {
			if split.PathSegments[i] != c.segments[i] {
				t.Errorf("ParseType(%q) segment %d = %q, want %q", c.in, i, split.PathSegments[i], c.segments[i])
			}
		}
	}
}

func TestToGitHubRepo(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"github:read", "github/read"},
		{"Muhammad-Jay:github:read", "Muhammad-Jay/github-read"},
		{"hashicorp:vault:auth", "hashicorp/vault-auth"},
	}

	for _, c := range cases {
		split, err := ParseType(c.in)
		if err != nil {
			t.Fatalf("ParseType(%q): %v", c.in, err)
		}
		if got := split.ToGitHubRepo(); got != c.want {
			t.Errorf("ToGitHubRepo(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestToLocalPath(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"github:read", "github/read"},
		{"Muhammad-Jay:github:read", "Muhammad-Jay/github/read"},
		{"hashicorp:vault:auth", "hashicorp/vault/auth"},
	}

	for _, c := range cases {
		split, err := ParseType(c.in)
		if err != nil {
			t.Fatalf("ParseType(%q): %v", c.in, err)
		}
		if got := split.ToLocalPath(); got != c.want {
			t.Errorf("ToLocalPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeTypeRoundTrip(t *testing.T) {
	in := "Muhammad-Jay:github:read"
	got, err := NormalizeType(in)
	if err != nil {
		t.Fatalf("NormalizeType: %v", err)
	}
	if got != in {
		t.Errorf("NormalizeType(%q) = %q, want identity", in, got)
	}
}

func TestTypePath(t *testing.T) {
	got, err := TypePath("Muhammad-Jay:github:read")
	if err != nil {
		t.Fatalf("TypePath: %v", err)
	}
	if got != "Muhammad-Jay/github/read" {
		t.Errorf("TypePath = %q", got)
	}
	if _, err := TypePath("nope"); err == nil {
		t.Error("TypePath with no ':' should error")
	}
}

func TestRequirementValidate(t *testing.T) {
	if err := (Requirement{Type: "github:read", Version: "^1.0.0"}).Validate(); err != nil {
		t.Errorf("valid requirement rejected: %v", err)
	}
	if err := (Requirement{Type: ""}).Validate(); err == nil {
		t.Error("empty type should fail validation")
	}
	if err := (Requirement{Type: "read"}).Validate(); err == nil {
		t.Error("type without ':' should fail validation")
	}
}
