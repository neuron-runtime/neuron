package registry

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/Muhammad-Jay/neuron/nore/internal/builtins"
	"github.com/Muhammad-Jay/neuron/nore/internal/contracts"
	"github.com/Muhammad-Jay/neuron/shared/types/core"
)

type Registry struct {
	mu        sync.RWMutex
	runtimes  map[core.CapabilityRuntimeType]contracts.CapabilityRuntime
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
	return nil
}

func (r *Registry) Resolve(runtimeType core.CapabilityRuntimeType) (contracts.CapabilityRuntime, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	runtime, exists := r.runtimes[runtimeType]
	if !exists {
		return nil, fmt.Errorf("runtime for runtime type %q was not found", runtimeType)
	}
	return runtime, nil
}

func (r *Registry) RegisterCoreRuntimes() {
	builtins := map[string]contracts.CapabilityRuntime{
		"set":     builtins.Set{},
		"ai":      builtins.AIMock{},
		"log":     builtins.Log{},
		"http":    builtins.HTTP{},
		"delay":   builtins.Delay{},
		"command": builtins.Command{},
	}

	// Register each in-process runtime under its canonical namespaced name
	// (neuron:core:set, ...) plus the legacy bare name so assemblies authored
	// before the namespace existed still resolve.
	for name, runtime := range builtins {
		must(r.Register(core.CoreName(name), runtime))
		must(r.Register(core.CapabilityRuntimeType(name), runtime))
	}
}

// Close releases resources held by registered runtimes that implement
// contracts.CapabilityRuntimeCloser (e.g. wasm backends). It is idempotent
// and safe to call from an instance shutdown path once no execution is in
// flight.
func (r *Registry) Close() error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var failures []string
	types := make([]string, 0, len(r.runtimes))
	for runtimeType := range r.runtimes {
		types = append(types, string(runtimeType))
	}
	sort.Strings(types)

	for _, capabilityType := range types {
		closer, ok := r.runtimes[core.CapabilityRuntimeType(capabilityType)].(contracts.CapabilityRuntimeCloser)
		if !ok {
			continue
		}
		if err := closer.Close(); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", capabilityType, err))
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("close runtimes: %s", strings.Join(failures, "; "))
	}
	return nil
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}