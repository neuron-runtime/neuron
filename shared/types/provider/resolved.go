package provider

// ResolvedProvider is the frozen provider identity recorded when an Assembly
// is built. Authoring and registering an Assembly only records capability
// runtime requirements; this record pins the exact provider artifact — name,
// resolved version, protocol version, and content digest — that was selected
// from the installed store at build time, so N.O.R.E. can host the Assembly's
// capability runtimes without resolving, downloading, or installing anything.
//
// The record deliberately carries no host-specific paths: the installed
// artifact directory is derived from the artifact identity (name, version,
// digest) and the provider store root configured on the host.
type ResolvedProvider struct {
	// Name is the provider identity (e.g. "process", "wasm").
	Name string `json:"name"`

	// RequestedVersion is the original constraint when the requirement
	// declared one. Provider freezes select from the installed store, so
	// this is usually empty.
	RequestedVersion string `json:"requestedVersion,omitempty"`

	// ResolvedVersion is the exact installed version frozen into the build
	// (e.g. "0.1.0").
	ResolvedVersion string `json:"resolvedVersion"`

	// ProtocolVersion is the provider protocol version the artifact
	// implements.
	ProtocolVersion string `json:"protocolVersion"`

	// Registry is the provenance recorded at install time (a registry name
	// such as "local" or "github", or the bootstrap marker). It is
	// informational and never part of the artifact identity.
	Registry string `json:"registry,omitempty"`

	// Digest is the content digest of the installed artifact (sha256:...).
	Digest string `json:"digest"`

	// RuntimeKinds lists the Capability Runtime runtime kinds this provider
	// can host (process, wasm, ...).
	RuntimeKinds []string `json:"runtimeKinds,omitempty"`
}
