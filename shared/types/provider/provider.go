// Package provider defines the versioned contract between N.O.R.E. and the
// providers that host Capability Runtime artifacts.
//
// A provider is the host or engine that runs a Capability Runtime artifact. The
// process provider runs one as an OS child process; the WASM provider runs one
// as a WebAssembly module. The provider boundary is deliberately separate from
// the Capability Runtime invocation protocol: a provider launches and supervises
// a Capability Runtime, and hands it capabilityruntime.Request values and
// receives capabilityruntime.Response values unchanged. A provider never
// redefines what params, result, lifecycle, or structured errors mean.
//
// The contract is internal to the two Go modules that must agree on it: the CLI,
// which builds and installs provider artifacts, and N.O.R.E., which hosts them.
// It is not a public Neuron control-plane API, and it is not the Capability
// Runtime protocol that runtime authors implement.
//
// Third-party providers do not run inside N.O.R.E.'s address space. The shipped
// process and WASM providers are trusted first-party built-ins; the same
// contract is what an out-of-process provider will satisfy.
package provider

import (
	"context"
	"fmt"
	"strings"

	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
)

// ProtocolVersion is the version of the provider contract itself. It is
// distinct from a Capability Runtime invocation protocol version
// (capabilityrt.ProtocolV1 and capabilityrt.ProtocolJSONV1): a provider speaks
// this contract to N.O.R.E. and speaks a Capability Runtime protocol to the
// artifacts it hosts.
const ProtocolVersion = "neuron/provider-v1"

// Descriptor is a provider's identity and its compatibility requirements. It is
// everything N.O.R.E. needs to decide whether a provider can host a given
// Capability Runtime: the runtime kinds it can launch, the Capability Runtime
// invocation protocols it can carry, and the platforms it runs on.
type Descriptor struct {
	// Name is the provider's unique identity (for example "process", "wasm").
	Name string

	// Version is the provider's own version. It is independent of any
	// Capability Runtime version.
	Version string

	// ProtocolVersion is the provider contract version this provider
	// implements. An empty value is treated as ProtocolVersion.
	ProtocolVersion string

	// RuntimeKinds are the Capability Runtime kinds this provider can host,
	// drawn from the capabilityrt.RuntimeKind* constants (for example
	// "process", "wasm").
	RuntimeKinds []string

	// CapabilityRuntimeProtocols are the Capability Runtime invocation
	// protocols this provider can carry (for example capabilityrt.ProtocolV1).
	// An empty list means the provider does not constrain the protocol.
	CapabilityRuntimeProtocols []string

	// Platforms are the platform keys this provider runs on, in the
	// capabilityrt.HostPlatform() form (for example "linux-amd64"); the WASM
	// provider uses the portable "wasm32-wasi" key. An empty list means every
	// platform.
	Platforms []string
}

// SupportsRuntimeKind reports whether the provider can host the given runtime
// kind.
func (d Descriptor) SupportsRuntimeKind(kind string) bool {
	return contains(d.RuntimeKinds, kind)
}

// SupportsCapabilityRuntimeProtocol reports whether the provider can carry the
// given Capability Runtime invocation protocol. A provider that declares no
// protocols constrains nothing.
func (d Descriptor) SupportsCapabilityRuntimeProtocol(protocol string) bool {
	if len(d.CapabilityRuntimeProtocols) == 0 || protocol == "" {
		return true
	}
	return contains(d.CapabilityRuntimeProtocols, protocol)
}

// SupportsPlatform reports whether the provider runs on the given platform key.
// A provider that declares no platforms runs everywhere.
func (d Descriptor) SupportsPlatform(platform string) bool {
	if len(d.Platforms) == 0 || platform == "" {
		return true
	}
	return contains(d.Platforms, platform)
}

// Match reports whether this provider can host a Capability Runtime with the
// given kind, invocation protocol, and platform. A nil result means compatible;
// a non-nil *Error names exactly which requirement was not met. An empty
// platform is derived from the runtime kind using the Capability Runtime
// platform vocabulary, so the compatibility rule is stated in one place.
func (d Descriptor) Match(kind, protocol, platform string) error {
	if platform == "" {
		platform = capabilityrt.PlatformForRuntime(kind)
	}
	if !d.SupportsRuntimeKind(kind) {
		return &Error{Kind: ErrUnsupportedRuntimeKind, Provider: d.Name, RuntimeKind: kind}
	}
	if !d.SupportsCapabilityRuntimeProtocol(protocol) {
		return &Error{Kind: ErrUnsupportedProtocol, Provider: d.Name, Protocol: protocol}
	}
	if !d.SupportsPlatform(platform) {
		return &Error{Kind: ErrUnsupportedPlatform, Provider: d.Name, Platform: platform}
	}
	return nil
}

// CheckProviderProtocol validates that the provider implements a provider
// contract version this build speaks, so a newer provider is rejected as an
// explicit incompatibility rather than failing later at a call site.
func (d Descriptor) CheckProviderProtocol() error {
	version := d.ProtocolVersion
	if version == "" {
		version = ProtocolVersion
	}
	if version != ProtocolVersion {
		return &Error{Kind: ErrIncompatibleProviderProtocol, Provider: d.Name, Protocol: version}
	}
	return nil
}

// Spec describes the Capability Runtime instance a provider is asked to launch.
// It is derived from the frozen ResolvedCapabilityRuntime record persisted with
// a registered assembly, so a provider never resolves, downloads, or installs
// anything: normal invocation only launches an already-installed artifact.
type Spec struct {
	// Type is the logical Capability Runtime name (for example "github:read").
	Type string

	// Version is the exact resolved Capability Runtime version.
	Version string

	// RuntimeKind is the Capability Runtime kind to launch, matching the
	// provider that was selected (for example "process", "wasm").
	RuntimeKind string

	// Protocol is the Capability Runtime invocation protocol the artifact
	// speaks (for example capabilityrt.ProtocolV1).
	Protocol string

	// Entrypoint is the artifact entrypoint. It is absolute when the provider
	// requires an installed path.
	Entrypoint string

	// RootDir is the installed artifact directory. It may be empty for a
	// provider that does not require one.
	RootDir string

	// MaxWorkers bounds the concurrent workers the provider may run for this
	// Capability Runtime. Zero means the provider's default.
	MaxWorkers int

	// Config is optional provider-specific configuration. It must not change
	// the meaning of the Capability Runtime protocol.
	Config map[string]any
}

// Instance is a launched Capability Runtime hosted by a provider. Every provider
// returns the same Instance shape so N.O.R.E. executes capabilities the same way
// regardless of how the artifact is hosted.
//
// Execute and Health take the caller's context, so cancellation and deadlines
// propagate into the provider without a second control channel. The provider
// owns the instance lifecycle; the caller must Close it.
type Instance interface {
	// Execute runs one capabilityruntime.Request through the hosted Capability
	// Runtime and returns its capabilityruntime.Response. The provider must
	// pass the request through unchanged and must not reinterpret the response.
	Execute(ctx context.Context, req *capabilityrt.Request) (*capabilityrt.Response, error)

	// Health reports whether the instance is ready to accept requests.
	Health(ctx context.Context) error

	// Close releases the instance. After Close returns the instance must not
	// be reused. Closing one instance must not tear down shared resources that
	// other instances still hold.
	Close(ctx context.Context) error
}

// Provider hosts Capability Runtime artifacts of the kinds it declares. A single
// Provider value may be shared by many instances and must be safe for
// concurrent use.
type Provider interface {
	// Descriptor returns the provider's identity and compatibility
	// requirements.
	Descriptor() Descriptor

	// Start prepares and launches a Capability Runtime instance for the given
	// spec. The returned instance is owned by the caller, which must Close it.
	Start(ctx context.Context, spec Spec) (Instance, error)

	// Close releases resources the provider holds for the whole process, such
	// as shared worker pools or a WASM engine. It runs once at shutdown, after
	// the instances have stopped, and is not per-instance teardown.
	Close(ctx context.Context) error
}

// ErrorKind classifies a provider-boundary failure so N.O.R.E. can map it to a
// stable terminal outcome instead of surfacing a provider-specific error.
type ErrorKind string

const (
	// ErrMissingProvider means no provider is registered for the runtime kind.
	ErrMissingProvider ErrorKind = "missing-provider"
	// ErrUnsupportedRuntimeKind means the selected provider cannot host the
	// runtime kind.
	ErrUnsupportedRuntimeKind ErrorKind = "unsupported-runtime-kind"
	// ErrUnsupportedProtocol means the provider cannot carry the Capability
	// Runtime invocation protocol.
	ErrUnsupportedProtocol ErrorKind = "unsupported-protocol"
	// ErrUnsupportedPlatform means the provider does not run on this platform.
	ErrUnsupportedPlatform ErrorKind = "unsupported-platform"
	// ErrIncompatibleProviderProtocol means the provider implements a provider
	// contract version this build does not speak.
	ErrIncompatibleProviderProtocol ErrorKind = "incompatible-provider-protocol"
	// ErrUnavailable means a provider exists and is compatible but cannot
	// currently accept work (for example its shared engine is closed).
	ErrUnavailable ErrorKind = "provider-unavailable"
)

// Error is a normalized provider-boundary failure. Kind is stable; the message
// is for humans.
type Error struct {
	Kind        ErrorKind
	Provider    string
	RuntimeKind string
	Protocol    string
	Platform    string
	Err         error
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString(string(e.Kind))
	if e.Provider != "" {
		fmt.Fprintf(&b, " (provider %q)", e.Provider)
	}
	if e.RuntimeKind != "" {
		fmt.Fprintf(&b, " [runtime kind %q]", e.RuntimeKind)
	}
	if e.Protocol != "" {
		fmt.Fprintf(&b, " [protocol %q]", e.Protocol)
	}
	if e.Platform != "" {
		fmt.Fprintf(&b, " [platform %q]", e.Platform)
	}
	if e.Err != nil {
		fmt.Fprintf(&b, ": %v", e.Err)
	}
	return b.String()
}

func (e *Error) Unwrap() error { return e.Err }

// MissingProvider returns the error for a runtime kind with no registered
// provider.
func MissingProvider(kind string, available []string) error {
	return &Error{
		Kind:        ErrMissingProvider,
		RuntimeKind: kind,
		Err:         fmt.Errorf("no provider registered for runtime kind %q (registered: %v)", kind, available),
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
