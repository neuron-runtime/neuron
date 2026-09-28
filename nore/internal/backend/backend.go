// Package backend provides the capability runtime backend registry and
// lifecycle management. Each runtime backend (process, wasm, container,
// remote) registers itself with the registry, and the registry dispatches
// Start calls to the correct backend based on the capability runtime
// manifest's runtime.type field.
//
// The registry owns the lifecycle of all backend instances. When an instance
// is started, the registry tracks it and ensures proper cleanup on shutdown.
package backend

import (
	"context"
	"fmt"
	"sync"

	capabilityrt "github.com/Muhammad-Jay/neuron/shared/types/capabilityruntime"
)

// Registry manages runtime backends and their instances. It is the central
// dispatch point for capability runtime lifecycle management.
type Registry struct {
	mu        sync.RWMutex
	backends  map[string]capabilityrt.Backend
	instances map[string]capabilityrt.BackendInstance // keyed by "type@version"
}

// New returns a Registry with no backends registered. Call Register to add
// runtime backends before starting any instances.
func New() *Registry {
	return &Registry{
		backends:  make(map[string]capabilityrt.Backend),
		instances: make(map[string]capabilityrt.BackendInstance),
	}
}

// Register adds a runtime backend for the given kind. Registering an existing
// kind replaces the backend. This must be called before any Start calls for
// that kind.
func (r *Registry) Register(kind string, backend capabilityrt.Backend) error {
	if kind == "" {
		return fmt.Errorf("runtime kind is required")
	}
	if backend == nil {
		return fmt.Errorf("runtime backend is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.backends[kind] = backend
	return nil
}

// Start launches a capability runtime instance using the backend registered
// for the given kind. The instance is tracked by the registry and must be
// closed via Close or CloseAll.
func (r *Registry) Start(ctx context.Context, kind string, spec capabilityrt.BackendSpec) (capabilityrt.BackendInstance, error) {
	r.mu.RLock()
	backend, ok := r.backends[kind]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf(
			"no runtime backend registered for kind %q (supported: %v)",
			kind,
			r.registeredKinds(),
		)
	}

	instance, err := backend.Start(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("start capability runtime %s@%s: %w", spec.Type, spec.Version, err)
	}

	key := instanceKey(spec.Type, spec.Version)
	r.mu.Lock()
	r.instances[key] = instance
	r.mu.Unlock()

	return instance, nil
}

// Get returns a running instance by capability runtime type and version.
// Returns nil if no instance is running for the given key.
func (r *Registry) Get(typ, version string) capabilityrt.BackendInstance {
	key := instanceKey(typ, version)
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.instances[key]
}

// Close shuts down a specific instance and removes it from the registry.
func (r *Registry) Close(ctx context.Context, typ, version string) error {
	key := instanceKey(typ, version)
	r.mu.Lock()
	instance, ok := r.instances[key]
	if ok {
		delete(r.instances, key)
	}
	r.mu.Unlock()

	if !ok {
		return nil
	}
	return instance.Close(ctx)
}

// CloseAll shuts down all tracked instances. It attempts to close every
// instance and collects all errors.
func (r *Registry) CloseAll(ctx context.Context) error {
	r.mu.Lock()
	instances := make(map[string]capabilityrt.BackendInstance, len(r.instances))
	for k, v := range r.instances {
		instances[k] = v
	}
	r.instances = make(map[string]capabilityrt.BackendInstance)
	r.mu.Unlock()

	var errs []error
	for key, instance := range instances {
		if err := instance.Close(ctx); err != nil {
			errs = append(errs, fmt.Errorf("close %s: %w", key, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("close all instances: %v", errs)
	}
	return nil
}

// registeredKinds returns the list of registered backend kinds for error
// messages.
func (r *Registry) registeredKinds() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	kinds := make([]string, 0, len(r.backends))
	for kind := range r.backends {
		kinds = append(kinds, kind)
	}
	return kinds
}

func instanceKey(typ, version string) string {
	return typ + "@" + version
}