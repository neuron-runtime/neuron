package capabilityruntime

import "path/filepath"

// ResolvedCapabilityRuntimesKey is the wire key under which the frozen
// capability runtime set travels in a registered assembly's execution
// configurations.
//
// The execution configuration payload is produced by the CLI and consumed by
// N.O.R.E., which are separate Go modules that agree only through this
// package. The frozen set is therefore the one part of that payload that is a
// cross-module contract, and its key must be declared once, here, instead of
// being re-spelled in a hand-maintained mirror struct on either side. Both
// modules pin this constant in tests: the producer asserts it marshals the key,
// the consumer asserts it decodes the key.
const ResolvedCapabilityRuntimesKey = "resolved_capabilityRuntimes"

// RuntimeInfo is the runtime portion of a frozen resolved capability
// runtime. It is duplicated from the package manifest so the deployment
// carries everything required to launch the artifact without re-reading
// remote metadata.
type RuntimeInfo struct {
	Type       string `json:"type"`
	Protocol   string `json:"protocol,omitempty"`
	Entrypoint string `json:"entrypoint"`

	// MaxWorkers bounds the number of concurrent worker processes the runtime
	// backend may spawn for this capability runtime. A value of 0 means the
	// backend default.
	MaxWorkers int `json:"maxWorkers,omitempty"`
}

// ResolvedCapabilityRuntime is the frozen dependency record produced when an
// Assembly is compiled into a Deployment. Authoring an Assembly only declares
// requirements (type + constraint); this record pins the exact resolved
// version, registry, checksum, and installed artifact so a Deployment is
// reproducible.
//
// N.O.R.E. consumes this record to execute capabilities without ever
// performing dependency resolution or installation.
type ResolvedCapabilityRuntime struct {
	// Type is the logical capability runtime name (e.g. "github:read").
	Type string `json:"type"`

	// RequestedVersion is the original constraint (e.g. "^1.0.0"). Empty when
	// the requirement was floating.
	RequestedVersion string `json:"requestedVersion,omitempty"`

	// ResolvedVersion is the exact version frozen into the deployment
	// (e.g. "1.2.0").
	ResolvedVersion string `json:"resolvedVersion"`

	// Registry is the registry that supplied the package.
	Registry string `json:"registry,omitempty"`

	// Digest is the content digest of the installed artifact (sha256:...).
	Digest string `json:"digest,omitempty"`

	// Runtime describes how to launch the artifact.
	Runtime RuntimeInfo `json:"runtime"`

	// Features declared by the capability runtime manifest.
	Features []string `json:"features,omitempty"`

	// Capabilities the capability runtime can execute.
	Capabilities []string `json:"capabilities,omitempty"`

	// RootDir is the absolute path of the installed capability runtime
	// directory. The runtime backend resolves the entrypoint relative to it.
	// It points into the local runtime store (~/.neuron/runtimes/...) and is
	// only meaningful on the host that created the deployment.
	RootDir string `json:"rootDir,omitempty"`
}

// EntrypointPath returns the absolute path to the capability runtime's
// entrypoint.
func (r *ResolvedCapabilityRuntime) EntrypointPath() string {
	if r.RootDir == "" || r.Runtime.Entrypoint == "" {
		return r.Runtime.Entrypoint
	}
	// Entrypoint is manifest-relative and stored with forward slashes.
	return filepath.Join(r.RootDir, filepath.FromSlash(r.Runtime.Entrypoint))
}
