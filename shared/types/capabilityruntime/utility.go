package capabilityruntime

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

// NameSplit holds the structural components of a logical capability runtime name.
//
// A logical name is a ':' separated path whose first segment is the registry
// owner and whose remaining segments are the functional path:
//
//	"github:read"                 -> Owner: "github", PathSegments: ["read"]
//	"neuron-runtime:github:read"    -> Owner: "neuron-runtime", PathSegments: ["github", "read"]
//	"hashicorp:vault:auth"        -> Owner: "hashicorp", PathSegments: ["vault", "auth"]
type NameSplit struct {
	Owner        string   // e.g. "neuron-runtime"
	PathSegments []string // e.g. ["github", "read"]
}

// ParseType decomposes a logical capability runtime name such as "neuron-runtime:github:read"
// or "github:read". The first segment is always the owner; at least one
// functional segment must follow it.
func ParseType(capabilityRuntimeType string) (*NameSplit, error) {
	parts := strings.Split(capabilityRuntimeType, ":")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid capability runtime type format %q; must contain at least one ':' separator", capabilityRuntimeType)
	}

	owner := strings.TrimSpace(parts[0])
	if owner == "" {
		return nil, fmt.Errorf("invalid capability runtime type format %q; owner segment is empty", capabilityRuntimeType)
	}

	rest := make([]string, 0, len(parts)-1)
	for _, seg := range parts[1:] {
		if seg == "" {
			return nil, fmt.Errorf("invalid capability runtime type format %q; empty path segment", capabilityRuntimeType)
		}
		rest = append(rest, seg)
	}

	return &NameSplit{
		Owner:        owner,
		PathSegments: rest,
	}, nil
}

// ToGitHubRepo builds the "owner/repo" string GitHub's API expects. Trailing
// path segments are hyphen-joined because GitHub has no nested repositories.
//
//	"github:read"              -> "github/read"
//	"neuron-runtime:github:read" -> "neuron-runtime/github-read"
func (n *NameSplit) ToGitHubRepo() string {
	repoName := strings.Join(n.PathSegments, "-")
	return fmt.Sprintf("%s/%s", n.Owner, repoName)
}

// ToLocalPath builds the multi-level fileassembly path segments used under the
// capability runtime store. The ':' delimiters become directory separators, which is
// both cross-platform safe and keeps related capability runtimes grouped by owner.
func (n *NameSplit) ToLocalPath() string {
	return joinSegments(append([]string{n.Owner}, n.PathSegments...))
}

// TypePath converts a logical name directly to its store-relative path,
// e.g. "neuron-runtime:github:read" -> "neuron-runtime/github/read".
func TypePath(capabilityRuntimeType string) (string, error) {
	split, err := ParseType(capabilityRuntimeType)
	if err != nil {
		return "", err
	}
	return split.ToLocalPath(), nil
}

// NormalizeType rounds a logical name to its canonical ':' form. This lets
// callers compare requirements across the repository naming and storage path
// variations.
func NormalizeType(capabilityRuntimeType string) (string, error) {
	split, err := ParseType(capabilityRuntimeType)
	if err != nil {
		return "", err
	}
	return split.String(), nil
}

// String renders the canonical logical name.
func (n *NameSplit) String() string {
	return strings.Join(append([]string{n.Owner}, n.PathSegments...), ":")
}

// joinSegments joins path segments with the platform-appropriate separator.
func joinSegments(segments []string) string {
	return filepath.FromSlash(strings.Join(segments, "/"))
}

// HostPlatform returns the GOOS-GOARCH key used to select a platform artifact
// from an capability runtime manifest (e.g. "linux-amd64").
func HostPlatform() string {
	return runtime.GOOS + "-" + runtime.GOARCH
}

// PlatformForRuntime returns the platform key under which an capability runtime of the
// given runtime kind is selected from a manifest's platforms map. WASM
// modules are portable and use the single "wasm32-wasi" key; every other
// kind binds the host's native GOOS-GOARCH key because a frozen artifact is
// always installed and executed on the machine running N.O.R.E.
func PlatformForRuntime(runtimeType string) string {
	if runtimeType == RuntimeKindWasm {
		return PlatformWasm
	}
	return HostPlatform()
}
