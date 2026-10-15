package capabilityruntime

import (
	"context"
)

// Source is the capability runtime registry source the Resolver consumes.
// Concrete sources (source/github, source/local) implement it. The name is
// deliberate: a "provider" in Neuron is the host that runs a Capability Runtime
// artifact, so the artifact registry a requirement is resolved against is a
// "source" instead, to keep the two concepts from being confused.
//
// Keeping the interface here (consumer-side) avoids an import cycle between the
// capability runtime package and its source implementations.
type Source interface {
	// Name is the unique registry identifier referenced by requirements
	// (e.g. "github", "local").
	Name() string

	// Types lists every capability runtime type this registry can supply.
	Types(ctx context.Context) ([]string, error)

	// Versions lists the available versions for typ (no leading 'v').
	Versions(ctx context.Context, typ string) ([]string, error)

	// Package returns the immutable package for an exact version.
	Package(ctx context.Context, typ, version string) (*Package, error)
}
