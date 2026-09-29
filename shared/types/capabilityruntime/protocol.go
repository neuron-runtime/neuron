package capabilityruntime

// Request is the stdin payload handed to a one-shot capability runtime
// process. Runtimes read a single JSON document from stdin, execute, and
// write a single JSON Response document to stdout.
type Request struct {
	// Params carries the resolved execution params for the capability. Keys
	// map to the capability's declared params.
	Params map[string]any `json:"params"`
}

// Response is the stdout payload produced by a one-shot capability runtime
// process. Runtimes must write exactly one JSON Response document to stdout
// and exit zero on success.
type Response struct {
	// Result carries the results of the execution. Keys map to the
	// capability's declared result fields.
	Result map[string]any `json:"result"`

	// Error, when present, records a controlled failure. The runtime may
	// still exit zero after reporting an error this way; N.O.R.E. treats a
	// non-empty Error as an execution failure and surfaces it downstream.
	Error string `json:"error,omitempty"`
}

// Environment variables injected into a capability runtime process by the
// runtime backend.
const (
	// EnvProtocol declares the expected protocol version.
	EnvProtocol = "NEURON_CAPABILITY_RUNTIME_PROTOCOL"

	// EnvType is the capability runtime type (logical name) being executed.
	EnvType = "NEURON_CAPABILITY_RUNTIME_TYPE"

	// EnvVersion is the exact resolved version of the capability runtime.
	EnvVersion = "NEURON_CAPABILITY_RUNTIME_VERSION"

	// EnvSocket is the Unix domain socket path a gRPC capability runtime must
	// bind. The runtime creates the socket directory, so the capability runtime
	// never chooses this path itself.
	EnvSocket = "NEURON_CAPABILITY_RUNTIME_SOCKET"

	// EnvReady is the file a gRPC capability runtime creates once its server is
	// accepting connections. The runtime polls for this file rather than
	// sleeping, so a slow start does not become a fixed startup delay.
	EnvReady = "NEURON_CAPABILITY_RUNTIME_READY"
)
