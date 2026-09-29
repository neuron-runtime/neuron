// Package version exposes the build-time Neuron release version.
//
// The value is injected by the release pipeline at build time through
// -ldflags -X so that every artifact reports the version of the source it was
// built from without maintaining a separate in-tree version file:
//
//	go build -ldflags "-X github.com/neuron-runtime/neuron/shared/version.Version=v0.1.0" ...
//
// Local development builds keep the default "dev" value.
package version

// Version is the Neuron release version. It is the single authoritative
// version source shared by every Neuron binary (the neuron CLI and the N.O.R.E.
// daemon), so the CLI and the daemon always report the same product version.
var Version = "dev"

// String returns a stable human-readable version identifier.
func String() string {
	if Version == "" {
		return "dev"
	}
	return Version
}
