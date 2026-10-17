package capabilityruntime

import (
	"fmt"

	"github.com/neuron-runtime/neuron/application/internal/sourceregistry"
)

// Registry is the catalog of registry sources configured for resolution. It
// is not the installed capability runtime catalog (that is the store); it only
// answers "which sources can I ask for packages".
type Registry struct {
	sources *sourceregistry.Registry[Source]
}

func NewRegistry() *Registry {
	return &Registry{sources: sourceregistry.New[Source]()}
}

// NewRegistryFrom builds a catalog from an initial set of sources.
func NewRegistryFrom(registries ...Source) (*Registry, error) {
	r := NewRegistry()
	for _, reg := range registries {
		if reg == nil {
			continue
		}
		if err := r.Add(reg); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func (r *Registry) Add(registry Source) error {
	if registry == nil {
		return fmt.Errorf("registry is nil")
	}
	return r.sources.Add(registry.Name(), registry)
}

func (r *Registry) Get(name string) (Source, bool) {
	return r.sources.Get(name)
}

// Names returns the configured registry names in sorted order.
func (r *Registry) Names() []string {
	return r.sources.Names()
}
