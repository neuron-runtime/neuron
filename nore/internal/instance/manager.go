package instance

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/assembly"
	"github.com/neuron-runtime/neuron/nore/internal/event"
	"github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/plugin"
	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/shared/types/builtins"
	core2 "github.com/neuron-runtime/neuron/shared/types/core"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

type Manager struct {
	mu sync.RWMutex

	instancesByKey map[protocol.InstanceKey]*Instance
	instancesByID  map[string]*Instance

	parent     context.Context
	workers    int
	store      storage.Store
	metadata   *metadataStore
	assemblies *assembly.Repository

	// detachedDrainTimeout is the budget each instance gives detached work to
	// finish after the instance stops. A non-positive value selects the engine
	// default.
	detachedDrainTimeout time.Duration

	// builtinPolicy bounds the built-in capability runtimes every instance
	// this manager creates registers.
	builtinPolicy builtins.Policy
}

func NewManager(parent context.Context, workers int, detachedDrainTimeout time.Duration, store storage.Store, assemblies *assembly.Repository, opts ...Option) (*Manager, error) {
	if parent == nil {
		parent = context.Background()
	}
	if workers <= 0 {
		workers = 8
	}
	var optsApplied options
	for _, opt := range opts {
		if opt != nil {
			opt(&optsApplied)
		}
	}
	m := &Manager{
		instancesByKey:       make(map[protocol.InstanceKey]*Instance),
		instancesByID:        make(map[string]*Instance),
		parent:               parent,
		workers:              workers,
		store:                store,
		metadata:             newMetadataStore(store),
		assemblies:           assemblies,
		detachedDrainTimeout: detachedDrainTimeout,
		builtinPolicy:        optsApplied.builtinPolicy,
	}
	if err := m.reconcile(parent); err != nil {
		return nil, err
	}
	return m, nil
}

// reconcile registers instances whose metadata was persisted by a previous
// process so their executions and events remain queryable after a restart.
// Instances are restored as metadata-only records and are not started.
func (m *Manager) reconcile(ctx context.Context) error {
	records, err := m.metadata.List(ctx)
	if err != nil {
		return fmt.Errorf("load instance metadata: %w", err)
	}
	for _, rec := range records {
		i, err := restoreInstance(
			m.parent,
			rec,
			execution.NewExecutionStore(m.store),
			event.NewStore(m.store),
		)
		if err != nil {
			return fmt.Errorf("restore instance %s: %w", rec.ID, err)
		}
		m.instancesByID[rec.ID] = i
		m.instancesByKey[i.Key] = i
	}
	return nil
}

func (m *Manager) Get(key protocol.InstanceKey) (*Instance, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	i, ok := m.instancesByKey[key]
	return i, ok
}

func (m *Manager) GetByID(id string) (*Instance, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	i, ok := m.instancesByID[id]
	return i, ok
}

// Resolve returns the live runtime for key without creating it. An exact key
// matches directly; a partial key (name, name:version, or one carrying a hash
// or env) is resolved through the registered assemblies so version-without-hash
// addresses the most recently registered artifact. Returns false when no
// instance exists for the key.
func (m *Manager) Resolve(ctx context.Context, key protocol.InstanceKey) (*Instance, bool, error) {
	m.mu.RLock()
	if i, ok := m.instancesByKey[key]; ok {
		m.mu.RUnlock()
		return i, true, nil
	}
	m.mu.RUnlock()

	reg, err := m.assemblies.Resolve(ctx, key)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if i, ok := m.instancesByKey[reg.Key]; ok {
		return i, true, nil
	}
	return nil, false, nil
}

// GetOrCreate returns the live runtime for key, lazily constructing it from
// the durable RegisteredAssembly when it is not already running. Registration
// itself never creates an instance; this is the only entry point that does.
// The key may be partial (assemblyID, assemblyID:latest, assemblyID:version); it is
// resolved to its canonical form first so every addressing style shares the
// same runtime.
func (m *Manager) GetOrCreate(ctx context.Context, key protocol.InstanceKey) (*Instance, bool, error) {
	reg, err := m.assemblies.Get(ctx, key)
	if err != nil {
		return nil, false, err
	}
	canonical := reg.Key

	m.mu.RLock()
	previous, ok := m.instancesByKey[canonical]
	m.mu.RUnlock()
	if ok && (previous.Status() == StatusRunning || previous.Status() == StatusStarting) {
		return previous, false, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	id := core2.NewID("inst_")
	if previous, ok := m.instancesByKey[canonical]; ok {
		id = core2.ID(previous.ID)
		delete(m.instancesByKey, canonical)
		delete(m.instancesByID, previous.ID)
	}

	execOpt, err := withCapabilityRuntimes(reg)
	if err != nil {
		return nil, false, err
	}
	i, err := New(m.parent, string(id), canonical, &reg.Assembly, m.workers, m.store,
		execOpt, WithDetachedDrainTimeout(m.detachedDrainTimeout), WithBuiltinPolicy(m.builtinPolicy))
	if err != nil {
		return nil, false, err
	}
	if err := i.Start(); err != nil {
		return nil, false, err
	}

	m.instancesByKey[canonical] = i
	m.instancesByID[string(id)] = i
	if err := m.metadata.Save(ctx, recordFor(i, i.Status())); err != nil {
		delete(m.instancesByKey, canonical)
		delete(m.instancesByID, string(id))
		stopErr := i.Stop()
		return nil, false, errors.Join(
			fmt.Errorf("persist instance metadata for %s: %w", i.ID, err),
			stopErr,
		)
	}
	return i, true, nil
}

func (m *Manager) List(opts protocol.ListOptions) []*Instance {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*Instance, 0, len(m.instancesByID))

	for _, i := range m.instancesByID {
		if opts.All {
			result = append(result, i)
		} else if opts.Status != "" {
			if string(i.Status()) == opts.Status {
				result = append(result, i)
			}
		} else {
			if i.Status() != StatusStopped && i.Status() != StatusFailed {
				result = append(result, i)
			}
		}
	}

	return result
}

// withCapabilityRuntimes decodes the frozen capability runtime set from the registered assembly's
// opaque configuration so non-core capability runtimes can be launched.
func withCapabilityRuntimes(reg assembly.RegisteredAssembly) (Option, error) {
	resolved, err := plugin.DecodeResolvedCapabilityRuntimes(reg.ExecutionConfigurations)
	if err != nil {
		return nil, fmt.Errorf("decode resolved capability runtimes for %s: %w", reg.Key.String(), err)
	}
	return WithResolvedCapabilityRuntimes(resolved), nil
}

func (m *Manager) Stop(ctx context.Context, id string) error {
	m.mu.RLock()
	i, ok := m.instancesByID[id]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("instance %s not found", id)
	}

	if err := i.Stop(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.metadata.Save(ctx, recordFor(i, StatusStopped)); err != nil {
		return fmt.Errorf("persist instance metadata for %s: %w", i.ID, err)
	}
	return nil
}

// Remove stops and removes the instance addressed by target (an instance ID
// or a colon-encoded assembly key). The instance's executions and events are
// deleted along with its durable metadata.
func (m *Manager) Remove(ctx context.Context, target string) (removed bool, err error) {
	if strings.HasPrefix(strings.TrimSpace(target), "inst_") {
		var inst *Instance
		inst, ok := m.GetByID(target)
		if !ok {
			return false, nil
		}
		return true, m.removeInstance(ctx, inst)
	}

	key, err := protocol.ParseKey(target)
	if err != nil {
		return false, fmt.Errorf("invalid instance target %q: %w", target, err)
	}
	inst, ok, err := m.Resolve(ctx, key)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	return true, m.removeInstance(ctx, inst)
}

// removeInstance deletes an instance's runtime, executions, events and
// metadata. It is safe for both live and metadata-only (restored) instances.
func (m *Manager) removeInstance(ctx context.Context, inst *Instance) error {
	if err := inst.Stop(); err != nil {
		return err
	}

	executions, err := inst.ListExecutions(ctx)
	if err != nil {
		return fmt.Errorf("list executions for instance %s: %w", inst.ID, err)
	}

	var failures []error
	for _, exec := range executions {
		if err := inst.Store().Delete(ctx, exec.ID); err != nil {
			failures = append(failures, fmt.Errorf("delete execution %s on %s: %w", exec.ID, inst.ID, err))
		}
		if err := inst.EventStore().DeleteExecution(ctx, exec.ID); err != nil {
			failures = append(failures, fmt.Errorf("delete events for execution %s on %s: %w", exec.ID, inst.ID, err))
		}
	}
	if err := m.metadata.Delete(ctx, inst.ID); err != nil {
		failures = append(failures, fmt.Errorf("delete instance metadata for %s: %w", inst.ID, err))
	}
	if len(failures) > 0 {
		// Leave the in-memory maps intact so the caller can retry the removal.
		return errors.Join(failures...)
	}

	m.mu.Lock()
	delete(m.instancesByID, inst.ID)
	delete(m.instancesByKey, inst.Key)
	m.mu.Unlock()
	return nil
}

// RemoveByAssembly stops and removes every instance whose key addresses the
// given assembly, optionally scoped to the key's version. Returns the number of
// instances removed.
func (m *Manager) RemoveByAssembly(ctx context.Context, key protocol.InstanceKey) (int, error) {
	m.mu.RLock()
	var matches []*Instance
	for _, inst := range m.instancesByID {
		if inst.Key.AssemblyID != key.AssemblyID {
			continue
		}
		if key.Version != "" && key.Version != protocol.VersionLatest && inst.Key.Version != key.Version {
			continue
		}
		matches = append(matches, inst)
	}
	m.mu.RUnlock()

	for _, inst := range matches {
		if err := m.removeInstance(ctx, inst); err != nil {
			return 0, err
		}
	}
	return len(matches), nil
}

// Clear stops and removes every tracked instance.
func (m *Manager) Clear(ctx context.Context) error {
	m.mu.RLock()
	instances := make([]*Instance, 0, len(m.instancesByID))
	for _, inst := range m.instancesByID {
		instances = append(instances, inst)
	}
	m.mu.RUnlock()

	var failures []error
	for _, inst := range instances {
		if err := m.removeInstance(ctx, inst); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
