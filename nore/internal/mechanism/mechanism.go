// Package mechanism provides the provider registry and lifecycle management for
// Capability Runtime hosting. A provider registers itself with the registry, and
// the registry selects the provider that can host a frozen Capability Runtime
// from the runtime kind it declares.
//
// Two providers are registered by this build: process, which hosts a Capability
// Runtime as a worker process, and wasm, which hosts one as a module. The
// container and remote kinds exist in the shared contract but have no provider
// here, so an assembly frozen against them is rejected at resolution rather than
// failing later at execution.
//
// The registry deliberately does not track the instances it launches. It cannot
// own them, because sharing them is the provider's decision and each provider
// shares differently: the process provider keeps one worker pool per Capability
// Runtime and refcounts it by holder, the WASM provider keeps its compiled
// modules and its sandbox engine on the provider itself, and the legacy JSON
// transport holds nothing at all between executions. A registry that tracked
// instances by type@version would only be able to record the last handle it
// handed out, which is exactly the kind of ownership claim it cannot honour. See
// Register and Close.
package mechanism

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/neuron-runtime/neuron/shared/types/provider"
)

// Registry selects the provider that hosts a Capability Runtime from the runtime
// kind it declares. It owns the providers, not the instances they launch.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]provider.Provider
}

// New returns a Registry with no providers registered. Call Register to add
// providers before starting any instances.
func New() *Registry {
	return &Registry{providers: make(map[string]provider.Provider)}
}

// Register adds a provider for every runtime kind it declares. Registering a
// kind already served replaces the provider for that kind, which must not happen
// once anything has been started through it: the replacement would not own the
// instances already running, and they would be released by neither provider.
//
// The provider's declared provider-contract version is validated here, so an
// incompatible provider is rejected at registration rather than at the first
// Start.
func (r *Registry) Register(p provider.Provider) error {
	if p == nil {
		return errors.New("provider is nil")
	}
	desc := p.Descriptor()
	if desc.Name == "" {
		return fmt.Errorf("provider name is required")
	}
	if err := desc.CheckProviderProtocol(); err != nil {
		return err
	}
	if len(desc.RuntimeKinds) == 0 {
		return fmt.Errorf("provider %q declares no runtime kinds", desc.Name)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	for _, kind := range desc.RuntimeKinds {
		if kind == "" {
			return fmt.Errorf("provider %q declares an empty runtime kind", desc.Name)
		}
		r.providers[kind] = p
	}
	return nil
}

// Start selects a provider for the runtime kind in the spec and launches a
// Capability Runtime instance through it.
//
// Selection fails with a *provider.Error that names exactly why no provider
// could host the spec: no provider for the kind, or the selected provider is
// incompatible with the Capability Runtime invocation protocol or the platform.
//
// The returned instance is owned by its provider, not by the caller and not by
// this registry. Callers that share one must each Close their own instance when
// they are done: for the process provider that releases a single holder of a
// shared worker pool, so the runtime survives as long as anything is still using
// it.
func (r *Registry) Start(ctx context.Context, spec provider.Spec) (provider.Instance, error) {
	selected, err := r.Select(spec)
	if err != nil {
		return nil, err
	}

	instance, err := selected.Start(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("start capability runtime %s@%s: %w", spec.Type, spec.Version, err)
	}
	return instance, nil
}

// Select returns the provider that can host the spec without launching it. It is
// the single place provider compatibility is evaluated.
func (r *Registry) Select(spec provider.Spec) (provider.Provider, error) {
	r.mu.RLock()
	selected, ok := r.providers[spec.RuntimeKind]
	kinds := r.registeredKindsLocked()
	r.mu.RUnlock()

	if !ok {
		return nil, &provider.Error{
			Kind:        provider.ErrMissingProvider,
			RuntimeKind: spec.RuntimeKind,
			Err:         fmt.Errorf("no provider registered for runtime kind %q (registered: %v)", spec.RuntimeKind, kinds),
		}
	}

	descriptor := selected.Descriptor()
	if err := descriptor.CheckProviderProtocol(); err != nil {
		return nil, err
	}
	if err := descriptor.Match(spec.RuntimeKind, spec.Protocol, ""); err != nil {
		return nil, err
	}
	return selected, nil
}

// Close releases the process-global resources held by the registered providers
// and collects the failures.
//
// It runs once at shutdown, after the instances have stopped. Instances are
// stopped first because that is where they give up their own references; this is
// then the backstop for anything an instance failed to release, and for the state
// a provider keeps with no per-instance handle at all. Each provider is closed
// once even if it is registered for several kinds.
func (r *Registry) Close(ctx context.Context) error {
	r.mu.RLock()
	unique := make(map[provider.Provider]struct{}, len(r.providers))
	for _, p := range r.providers {
		unique[p] = struct{}{}
	}
	r.mu.RUnlock()

	providers := make([]provider.Provider, 0, len(unique))
	for p := range unique {
		providers = append(providers, p)
	}
	sort.Slice(providers, func(i, j int) bool {
		return providers[i].Descriptor().Name < providers[j].Descriptor().Name
	})

	var failures []string
	for _, p := range providers {
		if err := p.Close(ctx); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", p.Descriptor().Name, err))
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("close capability runtime providers: %s", joinSorted(failures))
	}
	return nil
}

// RegisteredKinds returns the runtime kinds that have a provider, sorted. It is
// used to tell a caller which runtime kinds this build can host.
func (r *Registry) RegisteredKinds() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.registeredKindsLocked()
}

func (r *Registry) registeredKindsLocked() []string {
	kinds := make([]string, 0, len(r.providers))
	for kind := range r.providers {
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
