package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/neuron-runtime/neuron/application/internal/sourceregistry"
)

// Source is the provider registry source the Resolver consumes. Concrete
// sources (source/github, source/local) implement it. The name is deliberate:
// "source" keeps artifact registries distinct from provider runtimes, the
// same way the capability runtime package names its registry providers.
//
// Keeping the interface here (consumer-side) avoids an import cycle between
// the provider package and its source implementations.
type Source interface {
	// Name is the unique registry identifier referenced by requirements
	// (e.g. "github", "local").
	Name() string

	// Versions lists the available versions for a provider name (no
	// leading 'v'). Not-found is signaled with ErrNotFound.
	Versions(ctx context.Context, name string) ([]string, error)

	// Package returns the immutable package for an exact version.
	Package(ctx context.Context, name, version string) (*Package, error)
}

// Requirement is what a caller asks for: a provider name, an optional exact
// version or constraint, and the registries allowed to satisfy it.
//
//	name:       "process"
//	version:    "^1.0.0"
//	registries: ["local"]
//
// A Requirement is a request, not an installation. Resolution turns it into
// an immutable Package, and installation turns that Package into an
// Installed provider.
type Requirement struct {
	Name       string   `json:"name"`
	Version    string   `json:"version,omitempty"`
	Registries []string `json:"registries,omitempty"`
}

// Validate verifies a Requirement is addressable.
func (r Requirement) Validate() error {
	if strings.TrimSpace(r.Name) == "" {
		return fmt.Errorf("provider requirement has no name")
	}
	return nil
}

// Registry is the catalog of provider registry sources configured for
// resolution.
type Registry struct {
	sources *sourceregistry.Registry[Source]
}

// NewRegistry returns an empty provider source catalog.
func NewRegistry() *Registry {
	return &Registry{sources: sourceregistry.New[Source]()}
}

// Add registers a source under its own name.
func (r *Registry) Add(source Source) error {
	if source == nil {
		return fmt.Errorf("registry source is nil")
	}
	return r.sources.Add(source.Name(), source)
}

// Get returns the source registered under name.
func (r *Registry) Get(name string) (Source, bool) {
	return r.sources.Get(name)
}

// Names returns the configured source names in sorted order.
func (r *Registry) Names() []string {
	return r.sources.Names()
}
