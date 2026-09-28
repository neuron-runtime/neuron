package capabilityruntime

import (
	"context"
)

// Provider is the registry-provider contract the capability runtime Resolver consumes.
// Concrete providers (source/github, source/local) implement it. Keeping the
// interface here (consumer-side) avoids an import cycle between the capability runtime
// package and its source implementations.
type Provider interface {
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
