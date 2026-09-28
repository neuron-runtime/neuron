package capabilityruntime

import (
	"fmt"
	"sort"
	"sync"
)

// Registry is the catalog of registry providers configured for resolution. It
// is not the installed capability runtime catalog (that is the store); it only answers
// "which providers can I ask for packages".
type Registry struct {
	mu      sync.RWMutex
	sources map[string]Provider
}

func NewRegistry() *Registry {
	return &Registry{
		sources: make(map[string]Provider),
	}
}

// NewRegistryFrom builds a catalog from an initial set of providers.
func NewRegistryFrom(registries ...Provider) (*Registry, error) {
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

func (r *Registry) Add(registry Provider) error {
	if registry == nil {
		return fmt.Errorf("registry is nil")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	name := registry.Name()

	if _, exists := r.sources[name]; exists {
		return fmt.Errorf("capability runtime registry %q already registered", name)
	}

	r.sources[name] = registry

	return nil
}

func (r *Registry) Get(name string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	registry, ok := r.sources[name]
	return registry, ok
}

// Names returns the configured registry names in sorted order.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.sources))
	for name := range r.sources {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
