package registry

import (
	"fmt"
	"strings"
	"sync"

	"github.com/neuron-runtime/neuron/nore/internal/builtins"
	"github.com/neuron-runtime/neuron/nore/internal/contracts"
	"github.com/neuron-runtime/neuron/shared/types/core"
)

type Registry struct {
	mu       sync.RWMutex
	runtimes map[core.CapabilityRuntimeType]contracts.CapabilityRuntime

	// instances holds every registered runtime exactly once, in registration
	// order. A runtime registered under more than one name — a built-in is
	// reachable under its canonical name and under its legacy bare name —
	// appears here once, so it is closed once.
	instances []contracts.CapabilityRuntime
}

func New() *Registry {
	return &Registry{runtimes: make(map[core.CapabilityRuntimeType]contracts.CapabilityRuntime)}
}

func (r *Registry) Register(runtimeType core.CapabilityRuntimeType, runtime contracts.CapabilityRuntime) error {
	if runtimeType == "" || runtime == nil {
		return fmt.Errorf("runtime type and runtime are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.runtimes[runtimeType]; exists {
		return fmt.Errorf("runtime for runtime type %q is already registered", runtimeType)
	}
	r.runtimes[runtimeType] = runtime
	r.instances = append(r.instances, runtime)
	return nil
}

func (r *Registry) Resolve(runtimeType core.CapabilityRuntimeType) (contracts.CapabilityRuntime, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	runtime, exists := r.runtimes[runtimeType]
	if !exists {
		// A reference to a built-in that does not exist is a mistake in the
		// assembly, and saying so is more useful than reporting a missing
		// runtime.
		if err := core.UnknownBuiltinError(runtimeType); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("runtime for runtime type %q was not found", runtimeType)
	}
	return runtime, nil
}

// RegisterCoreRuntimes registers the built-in capability runtimes that the
// runtime provides in-process.
//
// The set of built-ins is declared in the shared core package, so the runtime,
// the compiler, and the CLI agree on which built-ins exist and what they
// accept. Every declared built-in must have an implementation here: a
// declared built-in with no implementation is a defect in the runtime, and it
// is reported rather than leaving the built-in silently unavailable.
//
// Each built-in is registered under its canonical namespaced name
// (neuron:core:http) and under its legacy bare name (http), so an assembly
// authored before the namespace existed still resolves.
func (r *Registry) RegisterCoreRuntimes() error {
	implementations := map[string]contracts.CapabilityRuntime{
		"set":     builtins.Set{},
		"http":    builtins.HTTP{},
		"command": builtins.Command{},
	}

	for _, name := range core.BuiltinTypeNames() {
		implementation, ok := implementations[name]
		if !ok {
			return fmt.Errorf("built-in capability runtime %q is declared but has no implementation", name)
		}
		if err := r.Register(core.CoreName(name), implementation); err != nil {
			return fmt.Errorf("register built-in capability runtime %q: %w", name, err)
		}
		if err := r.Register(core.CapabilityRuntimeType(name), implementation); err != nil {
			return fmt.Errorf("register built-in capability runtime alias %q: %w", name, err)
		}
	}

	for name := range implementations {
		if !core.IsBuiltinRuntimeType(core.CapabilityRuntimeType(name)) {
			return fmt.Errorf("built-in capability runtime %q is implemented but not declared", name)
		}
	}

	return nil
}

// Close releases resources held by registered runtimes that implement
// contracts.CapabilityRuntimeCloser (e.g. wasm backends). It is idempotent
// and safe to call from an instance shutdown path once no execution is in
// flight.
//
// A runtime registered under several names is closed once.
func (r *Registry) Close() error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var failures []string
	for _, runtime := range r.instances {
		closer, ok := runtime.(contracts.CapabilityRuntimeCloser)
		if !ok {
			continue
		}
		if err := closer.Close(); err != nil {
			failures = append(failures, fmt.Sprintf("%T: %v", runtime, err))
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("close runtimes: %s", strings.Join(failures, "; "))
	}
	return nil
}
