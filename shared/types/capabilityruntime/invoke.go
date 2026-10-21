package capabilityruntime

// This file defines the wire contract for direct, one-shot invocation of an
// installed Capability Runtime (`neuron run -c <ref>`). The neuron CLI resolves
// the artifact and its provider from the local stores and then hands the frozen
// identity to a short-lived `nore --invoke` child process, which hosts the
// artifact through the same provider registry used for Assembly execution. The
// two modules agree only through this package, so the contract lives here.
//
// The capability runtime store root is not part of the request: it is a
// property of the host, passed to the child alongside the invoke mode, exactly
// as the long-running daemon receives it at startup.

// InvokeRequest is the JSON document the CLI writes to a `nore --invoke`
// process's stdin. It carries the frozen identity of the installed artifact to
// run, never a host path: the child derives the installed directory from the
// artifact identity and the store root it was launched with.
type InvokeRequest struct {
	// CapabilityRuntime is the frozen resolved record for the artifact to
	// invoke. It pins the exact version, digest, runtime kind, invocation
	// protocol, and entrypoint so the child needs no registry or manifest.
	// Empty when CoreType is set: core runtimes are compiled into N.O.R.E.
	// and are never resolved or installed as artifacts.
	CapabilityRuntime ResolvedCapabilityRuntime `json:"capabilityRuntime"`

	// CoreType, when set, names a built-in in-process core capability
	// runtime (canonical neuron:core:<name> form) to execute instead of an
	// installed artifact. The child resolves it from its core registry; no
	// store, provider, or frozen identity is involved.
	CoreType string `json:"coreType,omitempty"`

	// Params are the invocation params handed to the capability runtime. Keys
	// map to the capability's declared params.
	Params map[string]any `json:"params"`
}

// InvokeResponse is the JSON document the `nore --invoke` process writes to its
// stdout. Exactly one document is written. Diagnostics and logs go to stderr;
// stdout carries only this document so the CLI can render the result verbatim.
type InvokeResponse struct {
	// Result carries the result fields returned by the capability runtime.
	Result map[string]any `json:"result"`

	// Error, when present, records a controlled failure — the artifact or its
	// provider was unavailable, the runtime reported an error, or invocation
	// failed. The CLI maps a non-empty Error (or a non-zero child exit) to a
	// non-zero process exit.
	Error string `json:"error,omitempty"`
}
