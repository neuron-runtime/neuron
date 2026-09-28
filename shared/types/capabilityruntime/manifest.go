// Package capabilityruntime defines the wire contract shared between N.O.R.E.
// and the Neuron CLI for distributing and executing external capability
// runtime packages.
//
// The contract is deliberately minimal. It answers three questions:
//
//  1. What is this artifact? (runtime.json manifest)
//  2. How do I talk to the running runtime?  (process/stdin protocol)
//  3. What was resolved/frozen for a Deployment? (ResolvedCapabilityRuntime)
//
// Registry, resolution, and installation machinery never lives here. Those
// belong to application/capabilityruntime. This package only declares the
// schema both sides agree on so the runtime never needs to import registry
// code.
package capabilityruntime

import (
	"fmt"
	"strings"
)

// Protocol identifiers for the capability runtime handshake.
const (
	// APIVersion is the schema version of every runtime.json manifest.
	APIVersion = "neuron/v1"

	// Kind is the discriminant of every runtime.json manifest.
	Kind = "CapabilityRuntime"

	// ProtocolV1 is the canonical wire protocol spoken by capability runtime
	// processes over gRPC (Unix domain sockets). Runtimes must declare this
	// (or a newer compatible protocol) in their manifest runtime.protocol.
	ProtocolV1 = "neuron/capability-runtime-v1"

	// ProtocolJSONV1 is the legacy stdin/stdout JSON protocol spoken by
	// one-shot capability runtime processes and WASI modules (WASI has no
	// socket interface, so gRPC is unavailable there). Runtimes that cannot
	// host a gRPC server MUST declare this protocol in their manifest.
	ProtocolJSONV1 = "neuron/capability-runtime-v1-json"

	// ManifestFile is the mandatory manifest file inside every capability
	// runtime package and every installed capability runtime directory.
	ManifestFile = "runtime.json"

	// InstallFile is the installation record written into an installed
	// capability runtime directory after a verified, atomic install.
	InstallFile = "install.json"

	// PlatformWasm is the portable platform key for WASM capability runtime
	// artifacts in a manifest's platforms map. It names the platform by its
	// execution boundary rather than a host GOOS-GOARCH pair, because a WASI
	// module runs on any host. All other platform keys are Neuron-owned
	// GOOS-GOARCH ids (e.g. "linux-amd64") naming native process artifacts.
	PlatformWasm = "wasm32-wasi"

	// PackageArchiveSuffix is the canonical suffix of the capability runtime
	// package archive asset:
	// <name>-<version>-capability-runtime.neuron.tar.gz. A package archive is
	// a single immutable artifact containing runtime.json at its root plus
	// every platform artifact referenced by the manifest. Registries prefer
	// it over per-platform assets; the manifest inside the archive is
	// authoritative.
	PackageArchiveSuffix = "-capability-runtime.neuron.tar.gz"
)

// PackageArchiveName returns the canonical name of the capability runtime
// package archive for a type and version:
// <name>-<version>-capability-runtime.neuron.tar.gz with ':' and '/' replaced
// by '-'. The name is informational only; the manifest inside the archive is
// authoritative for identity and content.
func PackageArchiveName(name, version string) string {
	clean := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(name), ":", "-"), "/", "-")
	return clean + "-" + strings.TrimSpace(version) + PackageArchiveSuffix
}

// Manifest is the runtime.json package manifest. It describes one immutable
// capability runtime artifact: what it is, what launches it, which
// capabilities it can execute, and which platform binaries back it.
type Manifest struct {
	APIVersion   string              `json:"apiVersion"`
	Kind         string              `json:"kind"`
	Metadata     ManifestMetadata    `json:"metadata"`
	Runtime      ManifestRuntime     `json:"runtime"`
	Capabilities []string            `json:"capabilities"`
	Features     []string            `json:"features,omitempty"`
	Platforms    map[string]Platform `json:"platforms"`

	// Build and Artifact carry authoring-time hints for how this runtime's
	// payload is produced when it lives in a local source tree. They tell the
	// CLI how to materialize the artifact (build.command) and where it lands
	// (artifact.path) relative to the package directory. They are NEVER part
	// of the installed runtime contract: the installer strips them from the
	// stored runtime.json. Released packages (e.g. GitHub archives) ship
	// prebuilt artifacts and omit both.
	Build    *ManifestBuild    `json:"build,omitempty"`
	Artifact *ManifestArtifact `json:"artifact,omitempty"`
}

// ManifestBuild is an optional authoring-time instruction for producing a
// local capability runtime artifact. Distributed packages omit it.
type ManifestBuild struct {
	// Command is the shell command that produces the artifact declared by
	// Artifact.Path. It runs in the package directory (the version
	// directory), inheriting the parent environment plus NEURON_PROJECT_ROOT
	// and NEURON_CAPABILITY_RUNTIME_DIR.
	Command string `json:"command,omitempty"`
}

// ManifestArtifact locates the built payload relative to the package
// directory. A directory payload is copied recursively; a file payload is
// treated like a single binary (or archive) artifact. Distributed packages
// may list the release asset name here.
type ManifestArtifact struct {
	// Path is the artifact file or directory produced by Build.Command.
	Path string `json:"path,omitempty"`
}

// ManifestMetadata identifies the capability runtime artifact.
type ManifestMetadata struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
}

// ManifestRuntime describes how the capability runtime is launched.
type ManifestRuntime struct {
	// Type is the runtime backend kind: "process" today, "wasm", "container",
	// "remote", ... in the future. The backend dispatches on this value.
	Type string `json:"type"`

	// Entrypoint is the location of the executable relative to the installed
	// package root. For platform-specific artifacts the filesystem store
	// resolves the artifact name before writing it.
	Entrypoint string `json:"entrypoint"`

	// Protocol is the wire protocol spoken by the capability runtime
	// (neuron/capability-runtime-v1 for gRPC process runtimes,
	// neuron/capability-runtime-v1-json for stdin/stdout JSON runtimes).
	Protocol string `json:"protocol,omitempty"`

	// MaxWorkers bounds the number of concurrent worker processes the runtime
	// backend may spawn for this capability runtime. A value of 0 means the
	// backend default. Only meaningful for backends with long-lived workers
	// (process).
	MaxWorkers int `json:"maxWorkers,omitempty"`
}

// Platform maps one host platform (GOOS-GOARCH) to its artifact.
type Platform struct {
	// Artifact is the binary/archive file name for this platform.
	Artifact string `json:"artifact"`

	// SHA256 is the checksum of the artifact. Required when the artifact is
	// distributed as a release asset.
	SHA256 string `json:"sha256,omitempty"`
}

// Validate verifies the manifest conforms to the runtime.json schema.
func (m *Manifest) Validate() error {
	if m == nil {
		return fmt.Errorf("capability runtime manifest is nil")
	}
	if m.APIVersion != APIVersion {
		return fmt.Errorf("capability runtime manifest: unsupported apiVersion %q (want %q)", m.APIVersion, APIVersion)
	}
	if m.Kind != Kind {
		return fmt.Errorf("capability runtime manifest: unsupported kind %q (want %q)", m.Kind, Kind)
	}
	if m.Metadata.Name == "" {
		return fmt.Errorf("capability runtime manifest: metadata.name is required")
	}
	if m.Metadata.Version == "" {
		return fmt.Errorf("capability runtime manifest: metadata.version is required")
	}
	if m.Runtime.Type == "" {
		return fmt.Errorf("capability runtime manifest %s: runtime.type is required", m.Metadata.Name)
	}
	if m.Runtime.Entrypoint == "" {
		return fmt.Errorf("capability runtime manifest %s: runtime.entrypoint is required", m.Metadata.Name)
	}
	if len(m.Capabilities) == 0 {
		return fmt.Errorf("capability runtime manifest %s: at least one capability is required", m.Metadata.Name)
	}
	return nil
}

// HasFeature reports whether the manifest declares the given feature; an
// empty feature list answers false (features are opt-in declarations).
func (m *Manifest) HasFeature(feature string) bool {
	for _, f := range m.Features {
		if f == feature {
			return true
		}
	}
	return false
}

// BuildCommand returns the authoring-time build command, or "" when the
// manifest ships a prebuilt artifact (or none).
func (m *Manifest) BuildCommand() string {
	if m != nil && m.Build != nil {
		return strings.TrimSpace(m.Build.Command)
	}
	return ""
}

// Sanitized returns a copy of the manifest safe to persist inside an
// installed capability runtime: authoring-time build/artifact hints are
// stripped because an installed artifact has no build step. The runtime
// contract only carries identity, runtime, capabilities, features, and
// platforms.
func (m *Manifest) Sanitized() *Manifest {
	if m == nil {
		return nil
	}
	clone := *m
	clone.Build = nil
	clone.Artifact = nil
	if clone.Platforms != nil {
		pc := make(map[string]Platform, len(clone.Platforms))
		for k, v := range clone.Platforms {
			pc[k] = v
		}
		clone.Platforms = pc
	}
	return &clone
}
