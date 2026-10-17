package capabilityruntime

// Runtime kinds identify how a frozen capability runtime artifact is launched.
// The value stored in Manifest.Runtime.Type and RuntimeInfo.Type is one of these
// constants. The provider registry dispatches on it to select the provider that
// hosts the artifact.
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

// SupportedRuntimeKinds returns the runtime kinds this build can host. It is the
// set of kinds a first-party provider is registered for; container and remote
// exist in the contract but have no provider yet, so an assembly frozen against
// them is rejected at resolution rather than failing later at execution.
func SupportedRuntimeKinds() []string {
	return []string{RuntimeKindProcess, RuntimeKindWasm}
}
