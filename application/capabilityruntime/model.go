package capabilityruntime

import (
	"runtime"

	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
)

// Package is the immutable package obtained from a registry. It describes a
// single resolvable version of one capability runtime, before installation.
type Package struct {
	// Type is the logical capability runtime name (e.g. "github:read").
	Type string

	// Version is the exact package version.
	Version string

	// Digest is the content digest of the artifact (sha256:...).
	Digest string

	// Registry is the registry that supplied this package.
	Registry string

	// Description from the package manifest.
	Description string

	// Runtime describes how the installed artifact is launched.
	Runtime RuntimeSpec

	// Features declared by the package manifest.
	Features []string

	// Capabilities the package can execute.
	Capabilities []string

	// Platforms declared by the package manifest (GOOS-GOARCH -> artifact).
	Platforms map[string]capabilityrt.Platform

	// Artifact is the platform-matching download, when the package is
	// distributed as a remote binary.
	Artifact Artifact

	// Manifest is the validated runtime.json bytes for this package. The
	// installer writes it into the installed directory so an installed
	// capability runtime is fully self-describing.
	Manifest []byte
}

// RuntimeSpec describes how an capability runtime artifact is launched.
type RuntimeSpec struct {
	// Type is the runtime adapter kind: "process" today; "wasm", "container",
	// "remote" in the future.
	Type string

	// Entrypoint is the executable path relative to the installed root.
	Entrypoint string

	// Protocol is the wire protocol the capability runtime speaks
	// (neuron/capability-runtime-v1 for gRPC process capability runtimes,
	// neuron/capability-runtime-v1-json for stdin/stdout JSON capability runtimes).
	Protocol string

	// MaxWorkers bounds the number of concurrent worker processes the runtime
	// may spawn for this capabilityruntime. A value of 0 means the runtime default.
	// Only meaningful for runtime types with long-lived workers (process).
	MaxWorkers int
}

// Artifact is a downloadable binary for one platform.
type Artifact struct {
	// URL is the download location (release asset URL, local path, ...).
	URL string

	// Name is the artifact's base file name (e.g. "github-read-linux-amd64").
	// When empty the installer derives it from URL.
	Name string

	// SHA256 is the expected checksum of the artifact bytes.
	SHA256 string
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
	if runtimeType == capabilityrt.RuntimeKindWasm {
		return capabilityrt.PlatformWasm
	}
	return HostPlatform()
}

// Installed is a locally installed, immutable capability runtime artifact living in the
// capability runtime store (~/.neuron/capabilityRuntimes/...). It is the materialized result of
// installing a Package: a verified, atomically renamed directory.
type Installed struct {
	// Type is the logical capability runtime name.
	Type string

	// Version is the exact installed version.
	Version string

	// Digest is the content digest recorded at install time.
	Digest string

	// Registry is the registry that supplied the package.
	Registry string

	// Platform is the GOOS-GOARCH the artifact was installed for.
	Platform string

	// RootDir is the absolute installed directory.
	RootDir string

	// ManifestPath is the absolute path to runtime.json.
	ManifestPath string

	// ArtifactPath is the absolute path to the extracted artifact root (the
	// directory containing the entrypoint).
	ArtifactPath string

	// Runtime mirrors the manifest launch spec.
	Runtime RuntimeSpec

	// Features declared by the manifest.
	Features []string

	// Capabilities the installed capability runtime can execute.
	Capabilities []string
}

// Manifest loads and validates the installed runtime.json.
func (i *Installed) Manifest() (*capabilityrt.Manifest, error) {
	return ReadManifestFile(i.ManifestPath)
}

// Frozen converts an installed capability runtime into the wire record a Deployment
// persists. It records the requested constraint alongside the exact resolved
// version, so restarting a Deployment reuses the pinned artifact rather than
// whatever is latest tomorrow.
func (i *Installed) Frozen(requestedVersion string) *capabilityrt.ResolvedCapabilityRuntime {
	return &capabilityrt.ResolvedCapabilityRuntime{
		Type:             i.Type,
		RequestedVersion: requestedVersion,
		ResolvedVersion:  i.Version,
		Registry:         i.Registry,
		Digest:           i.Digest,
		Runtime: capabilityrt.RuntimeInfo{
			Type:       i.Runtime.Type,
			Protocol:   i.Runtime.Protocol,
			Entrypoint: i.Runtime.Entrypoint,
			MaxWorkers: i.Runtime.MaxWorkers,
		},
		Capabilities: i.Capabilities,
		Features:     i.Features,
		RootDir:      i.RootDir,
	}
}

// Environment is the resolved execution environment handed to an Instance: the
// compiled assembly plus the exact capability runtime artifacts its capabilities require. An
// Instance never performs dependency resolution or installation.
type Environment struct {
	// CapabilityRuntimes is the frozen, installed capability runtime set.
	CapabilityRuntimes []*Installed
}

// Resolved returns the frozen wire records for the environment.
func (e *Environment) Resolved(requested map[string]string) []*capabilityrt.ResolvedCapabilityRuntime {
	out := make([]*capabilityrt.ResolvedCapabilityRuntime, 0, len(e.CapabilityRuntimes))
	for _, installed := range e.CapabilityRuntimes {
		out = append(out, installed.Frozen(requested[installed.Type]))
	}
	return out
}
