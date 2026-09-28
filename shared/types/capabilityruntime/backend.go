package capabilityruntime

import "context"

// Runtime kinds identify how a frozen capability runtime artifact is
// launched. The value stored in Manifest.Runtime.Type and RuntimeInfo.Type is
// one of these constants. The runtime backend registry dispatches on it to
// select the backend.
const (
	// RuntimeKindProcess runs the entrypoint as an OS child process
	// communicating over gRPC via Unix domain sockets.
	RuntimeKindProcess = "process"

	// RuntimeKindWasm runs the entrypoint inside an embedded WASI runtime,
	// speaking the stdin/stdout JSON protocol.
	RuntimeKindWasm = "wasm"

	// RuntimeKindContainer runs the entrypoint inside an OCI container for
	// strong isolation. Reserved for future implementation.
	RuntimeKindContainer = "container"

	// RuntimeKindRemote runs the entrypoint on a remote capability runtime
	// host. Reserved for future implementation.
	RuntimeKindRemote = "remote"
)

// SupportedRuntimeKinds returns the runtime kinds the runtime backend layer
// can launch.
func SupportedRuntimeKinds() []string {
	return []string{RuntimeKindProcess, RuntimeKindWasm}
}

// Backend starts capability runtime instances and manages their lifecycle.
// Each runtime backend (process, wasm, container, remote) implements this
// interface. The backend registry dispatches to the correct backend based on
// the runtime.json manifest's runtime.type field.
//
// Implementations must be safe for concurrent use. A single Backend instance
// may be shared across multiple capability runtime types of the same kind.
type Backend interface {
	// Start launches a capability runtime instance for the given
	// specification. The returned BackendInstance is ready to accept Execute
	// calls. The backend owns the instance lifecycle; the caller must call
	// Close when done.
	Start(ctx context.Context, spec BackendSpec) (BackendInstance, error)

	// BackendName returns the kind identifier (e.g. "process", "wasm").
	// This matches the runtime.type declared in capability runtime manifests.
	BackendName() string
}

// BackendSpec describes what to launch. It is derived from the frozen
// ResolvedCapabilityRuntime record persisted with a registered assembly.
type BackendSpec struct {
	// Type is the logical capability runtime name (e.g. "github:read").
	Type string

	// Version is the exact resolved version.
	Version string

	// Protocol is the wire protocol the capability runtime speaks
	// (e.g. "neuron/capability-runtime-v1").
	Protocol string

	// Entrypoint is the absolute path to the binary or wasm module.
	Entrypoint string

	// RootDir is the absolute path of the installed capability runtime
	// directory.
	RootDir string

	// MaxWorkers is the maximum number of concurrent workers for this
	// capability runtime type. The backend may start fewer workers based on
	// demand. A value of 0 means the backend uses its own default.
	MaxWorkers int

	// Config carries capability-runtime-specific configuration that the
	// backend may forward to the runtime process or module.
	Config map[string]any
}

// BackendInstance represents a running capability runtime. It provides the
// uniform execution contract regardless of whether the underlying runtime is
// a native process, WASM module, container, or remote host.
//
// Instances are not safe for concurrent Execute calls unless the backend
// explicitly documents concurrent safety. The backend is responsible for
// multiplexing requests across workers.
type BackendInstance interface {
	// Execute runs one request through the capability runtime and returns
	// the result. The context controls cancellation and deadline propagation.
	Execute(ctx context.Context, req *Request) (*Response, error)

	// Health reports whether the instance is ready to accept requests.
	// A healthy instance has at least one worker available and connected.
	Health(ctx context.Context) error

	// Close gracefully shuts down the instance, draining any in-flight
	// requests. After Close returns, the instance must not be reused.
	Close(ctx context.Context) error
}
