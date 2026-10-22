package builtins

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDefaultPolicyIsWithinItsOwnCeilings(t *testing.T) {
	effective, err := Effective(Policy{})
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if err := effective.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	ceilings := DefaultPolicy()
	if effective.MaxCommandOutputBytes != ceilings.MaxCommandOutputBytes {
		t.Errorf("MaxCommandOutputBytes = %d, want %d", effective.MaxCommandOutputBytes, ceilings.MaxCommandOutputBytes)
	}
	if effective.MaxHTTPResponseBytes != ceilings.MaxHTTPResponseBytes {
		t.Errorf("MaxHTTPResponseBytes = %d, want %d", effective.MaxHTTPResponseBytes, ceilings.MaxHTTPResponseBytes)
	}
	if effective.MaxFileReadBytes != ceilings.MaxFileReadBytes {
		t.Errorf("MaxFileReadBytes = %d, want %d", effective.MaxFileReadBytes, ceilings.MaxFileReadBytes)
	}
	if effective.MaxDirectoryEntries != ceilings.MaxDirectoryEntries {
		t.Errorf("MaxDirectoryEntries = %d, want %d", effective.MaxDirectoryEntries, ceilings.MaxDirectoryEntries)
	}
}

func TestEffectiveNarrowsTheCeilings(t *testing.T) {
	effective, err := Effective(Policy{
		MaxCommandOutputBytes:   1024,
		MaxHTTPRequestBodyBytes: 2048,
		MaxHTTPResponseBytes:    4096,
		MaxFileReadBytes:        8192,
		MaxDirectoryEntries:     16,
		DefaultTimeout:          "5s",
	})
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}

	if effective.MaxCommandOutputBytes != 1024 {
		t.Errorf("MaxCommandOutputBytes = %d, want 1024", effective.MaxCommandOutputBytes)
	}
	if effective.MaxHTTPRequestBodyBytes != 2048 {
		t.Errorf("MaxHTTPRequestBodyBytes = %d, want 2048", effective.MaxHTTPRequestBodyBytes)
	}
	if effective.MaxHTTPResponseBytes != 4096 {
		t.Errorf("MaxHTTPResponseBytes = %d, want 4096", effective.MaxHTTPResponseBytes)
	}
	if effective.MaxFileReadBytes != 8192 {
		t.Errorf("MaxFileReadBytes = %d, want 8192", effective.MaxFileReadBytes)
	}
	if effective.MaxDirectoryEntries != 16 {
		t.Errorf("MaxDirectoryEntries = %d, want 16", effective.MaxDirectoryEntries)
	}

	timeout, err := effective.Timeout()
	if err != nil {
		t.Fatalf("Timeout: %v", err)
	}
	if timeout != 5*time.Second {
		t.Errorf("Timeout = %s, want 5s", timeout)
	}
}

func TestEffectiveRefusesLimitsAboveTheCeiling(t *testing.T) {
	ceilings := DefaultPolicy()

	cases := []struct {
		name   string
		policy Policy
	}{
		{"maxCommandOutputBytes", Policy{MaxCommandOutputBytes: ceilings.MaxCommandOutputBytes + 1}},
		{"maxHTTPRequestBodyBytes", Policy{MaxHTTPRequestBodyBytes: ceilings.MaxHTTPRequestBodyBytes + 1}},
		{"maxHTTPResponseBytes", Policy{MaxHTTPResponseBytes: ceilings.MaxHTTPResponseBytes + 1}},
		{"maxFileReadBytes", Policy{MaxFileReadBytes: ceilings.MaxFileReadBytes + 1}},
		{"maxDirectoryEntries", Policy{MaxDirectoryEntries: ceilings.MaxDirectoryEntries + 1}},
		{"defaultTimeout", Policy{DefaultTimeout: "10m"}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := Effective(testCase.policy)
			if err == nil {
				t.Fatalf("Effective(%+v) succeeded, want an error", testCase.policy)
			}
			if !strings.Contains(err.Error(), "exceeds the built-in ceiling") {
				t.Errorf("error %q does not explain that the ceiling was exceeded", err)
			}
			if !strings.Contains(err.Error(), testCase.name) {
				t.Errorf("error %q does not name the limit", err)
			}
		})
	}
}

func TestEffectiveRefusesNegativeLimits(t *testing.T) {
	if _, err := Effective(Policy{MaxFileReadBytes: -1}); err == nil {
		t.Fatal("Effective accepted a negative limit")
	}
	if _, err := Effective(Policy{MaxDirectoryEntries: -1}); err == nil {
		t.Fatal("Effective accepted a negative limit")
	}
}

func TestEffectiveRefusesAnUnparseableTimeout(t *testing.T) {
	_, err := Effective(Policy{DefaultTimeout: "soon"})
	if err == nil {
		t.Fatal("Effective accepted an unparseable timeout")
	}
	if !strings.Contains(err.Error(), "defaultTimeout") {
		t.Errorf("error %q does not name the limit", err)
	}
}

func TestEffectiveIsIdempotent(t *testing.T) {
	configured := Policy{
		MaxFileReadBytes: 4096,
		DefaultTimeout:   "15s",
		FilesystemRoots:  []string{"/data"},
	}

	once, err := Effective(configured)
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	twice, err := Effective(once)
	if err != nil {
		t.Fatalf("Effective(Effective(p)): %v", err)
	}

	if !reflect.DeepEqual(once, twice) {
		t.Errorf("Effective is not idempotent: %+v then %+v", once, twice)
	}
}

func TestEffectiveNormalizesTheConfiguredLists(t *testing.T) {
	effective, err := Effective(Policy{
		FilesystemRoots:    []string{"  /data  ", "", "   "},
		AllowedExecutables: []string{" git ", ""},
		AllowedHTTPHosts:   []string{" api.example.com "},
	})
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}

	if len(effective.FilesystemRoots) != 1 || effective.FilesystemRoots[0] != "/data" {
		t.Errorf("FilesystemRoots = %v, want [/data]", effective.FilesystemRoots)
	}
	if len(effective.AllowedExecutables) != 1 || effective.AllowedExecutables[0] != "git" {
		t.Errorf("AllowedExecutables = %v, want [git]", effective.AllowedExecutables)
	}
	if len(effective.AllowedHTTPHosts) != 1 || effective.AllowedHTTPHosts[0] != "api.example.com" {
		t.Errorf("AllowedHTTPHosts = %v, want [api.example.com]", effective.AllowedHTTPHosts)
	}
}

func TestValidateRejectsAnUnresolvedPolicy(t *testing.T) {
	if err := (Policy{}).Validate(); err == nil {
		t.Fatal("Validate accepted a policy with no limits")
	}
}

// The private-network allowance states the trust context of a request rather
// than a bound on it, so it survives the resolution unchanged.
func TestEffectiveCarriesThePrivateNetworkAllowance(t *testing.T) {
	effective, err := Effective(Policy{AllowPrivateNetworks: true})
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if !effective.AllowPrivateNetworks {
		t.Error("Effective dropped the private-network allowance")
	}
}

// The project root is the context of the run rather than a limit on it, so it
// survives the resolution unchanged.
func TestEffectiveCarriesTheProjectRoot(t *testing.T) {
	effective, err := Effective(Policy{ProjectRoot: "  /srv/project  "})
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if effective.ProjectRoot != "/srv/project" {
		t.Errorf("ProjectRoot = %q, want %q", effective.ProjectRoot, "/srv/project")
	}
}
