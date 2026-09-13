package registry

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/Muhammad-Jay/neuron/nore/internal/contracts"
	"github.com/Muhammad-Jay/neuron/nore/internal/executors"
	"github.com/Muhammad-Jay/neuron/shared/types/core"
)

type Registry struct {
	mu        sync.RWMutex
	executors map[core.ExecutorType]contracts.Executor
}

func New() *Registry {
	return &Registry{executors: make(map[core.ExecutorType]contracts.Executor)}
}

func (r *Registry) Register(executorType core.ExecutorType, executor contracts.Executor) error {
	if executorType == "" || executor == nil {
		return fmt.Errorf("executor type and executor are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.executors[executorType]; exists {
		return fmt.Errorf("executor for executor type %q is already registered", executorType)
	}
	r.executors[executorType] = executor
	return nil
}

func (r *Registry) Resolve(executorType core.ExecutorType) (contracts.Executor, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	executor, exists := r.executors[executorType]
	if !exists {
		return nil, fmt.Errorf("executor for executor type %q was not found", executorType)
	}
	return executor, nil
}

func (r *Registry) RegisterCoreServiceExecutors() {
	builtins := map[string]contracts.Executor{
		"set":     executors.SetExecutor{},
		"ai":      executors.AIMockExecutor{},
		"log":     executors.LogExecutor{},
		"http":    executors.HttpExecutor{},
		"delay":   executors.DelayExecutor{},
		"command": executors.CommandExecutor{},
	}

	// Register each in-process executor under its canonical namespaced name
	// (neuron:core:set, ...) plus the legacy bare name so blueprints authored
	// before the namespace existed still resolve.
	for name, executor := range builtins {
		must(r.Register(core.CoreName(name), executor))
		must(r.Register(core.ExecutorType(name), executor))
	}
}

// Close releases resources held by registered executors that implement
// contracts.ExecutorCloser (e.g. wasm runtimes). It is idempotent and safe to
// call from an instance shutdown path once no execution is in flight.
func (r *Registry) Close() error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var failures []string
	types := make([]string, 0, len(r.executors))
	for executorType := range r.executors {
		types = append(types, string(executorType))
	}
	sort.Strings(types)

	for _, serviceType := range types {
		closer, ok := r.executors[core.ExecutorType(serviceType)].(contracts.ExecutorCloser)
		if !ok {
			continue
		}
		if err := closer.Close(); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", serviceType, err))
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("close executors: %s", strings.Join(failures, "; "))
	}
	return nil
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
