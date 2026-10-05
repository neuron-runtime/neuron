// Package backend provides the capability runtime backend registry and
// lifecycle management. A runtime backend registers itself with the registry,
// and the registry dispatches Start calls to the backend named by the frozen
// capability runtime's runtime.kind.
//
// Two backends are registered by this build: process, which hosts a capability
// runtime as a worker process, and wasm, which hosts one as a module. The
// container and remote kinds exist in the shared contract but have no backend
// here, so an assembly frozen against them is rejected at resolution rather than
// failing later at execution.
//
// The registry deliberately does not track the instances it launches. It cannot
// own them, because sharing them is the backend's decision and each backend
// shares differently: the process backend keeps one worker pool per capability
// runtime and refcounts it by holder, the WASM backend keeps its compiled modules
// and its sandbox runtime on the backend itself, and the legacy JSON transport
// holds nothing at all between executions. A registry that tracked instances by
// type@version would only be able to record the last handle it handed out, which
// is exactly the kind of ownership claim it cannot honour. See Register and
// CloseBackends.
package backend

import (
	"context"
	"fmt"
	"sort"
	"sync"

	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
)

// closer is implemented by backends that hold resources for the whole process
// and must be released at shutdown rather than per instance.
//
// Both shipped backends implement it. The process backend tears down any worker
// pool left behind by an instance that failed to release its share, and the WASM
// backend releases the process-global wazero runtime and compiled-module cache,
// which is irreversible and therefore only correct once nothing will execute
// again.
type closer interface {
	Close(ctx context.Context) error
}

// Registry routes capability runtime launches to the backend registered for a
// runtime kind. It owns the backends, not the instances they launch.
type Registry struct {
	mu       sync.RWMutex
	backends map[string]capabilityrt.Backend
}

// New returns a Registry with no backends registered. Call Register to add
// runtime backends before starting any instances.
func New() *Registry {
	return &Registry{backends: make(map[string]capabilityrt.Backend)}
}

// Register adds a runtime backend for the given kind. Registering an existing
// kind replaces the backend, which must not happen once anything has been started
// through it: the replacement would not own the instances already running, and
// they would be released by neither backend.
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

// Start launches a capability runtime instance through the backend registered for
// the given kind.
//
// The returned instance is owned by its backend, not by the caller and not by this
// registry. Callers that share one must each close their own instance when they are
// done: for the process backend that releases a single holder of a shared worker
// pool, so the runtime survives as long as anything is still using it.
func (r *Registry) Start(ctx context.Context, kind string, spec capabilityrt.BackendSpec) (capabilityrt.BackendInstance, error) {
	r.mu.RLock()
	selected, ok := r.backends[kind]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf(
			"no runtime backend registered for kind %q (supported: %v)",
			kind,
			r.RegisteredKinds(),
		)
	}

	instance, err := selected.Start(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("start capability runtime %s@%s: %w", spec.Type, spec.Version, err)
	}
	return instance, nil
}

// CloseBackends releases the process-global resources held by the registered
// backends and collects the failures.
//
// It runs once at shutdown, after the instances have stopped. Instances are
// stopped first because that is where they give up their own references; this is
// then the backstop for anything an instance failed to release, and for the state
// a backend keeps with no per-instance handle at all.
func (r *Registry) CloseBackends(ctx context.Context) error {
	r.mu.RLock()
	backends := make([]capabilityrt.Backend, 0, len(r.backends))
	for _, backend := range r.backends {
		backends = append(backends, backend)
	}
	r.mu.RUnlock()

	var failures []string
	for _, backend := range backends {
		shuttable, ok := backend.(closer)
		if !ok {
			continue
		}
		if err := shuttable.Close(ctx); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", backend.BackendName(), err))
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("close capability runtime backends: %s", joinSorted(failures))
	}
	return nil
}

// RegisteredKinds returns the registered backend kinds, sorted. It is used to
// tell a caller which runtime kinds this build can host.
func (r *Registry) RegisteredKinds() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	kinds := make([]string, 0, len(r.backends))
	for kind := range r.backends {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}

func joinSorted(values []string) string {
	out := ""
	for i, value := range values {
		if i > 0 {
			out += "; "
		}
		out += value
	}
	return out
}
