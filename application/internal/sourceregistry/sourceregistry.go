// Package sourceregistry is a small, domain-neutral catalog of named registry
// sources. Capability runtime resolution and provider resolution both keep a
// catalog of pluggable sources (github, local, ...); this package owns that
// mechanics once so the two domains do not grow parallel implementations.
package sourceregistry

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Registry is a concurrency-safe catalog of named sources.
type Registry[T any] struct {
	mu    sync.RWMutex
	items map[string]T
}

// New returns an empty catalog.
func New[T any]() *Registry[T] {
	return &Registry[T]{items: make(map[string]T)}
}

// Add registers item under name. Names must be non-empty and unique.
func (r *Registry[T]) Add(name string, item T) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("registry source name is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.items[name]; exists {
		return fmt.Errorf("registry source %q already registered", name)
	}
	r.items[name] = item
	return nil
}

// Get returns the source registered under name.
func (r *Registry[T]) Get(name string) (T, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	item, ok := r.items[name]
	return item, ok
}

// Names returns the registered source names in sorted order.
func (r *Registry[T]) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.items))
	for name := range r.items {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
