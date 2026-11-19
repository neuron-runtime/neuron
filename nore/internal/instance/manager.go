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
	core "github.com/neuron-runtime/neuron/shared/types/core"
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

	// durability is the execution durability mode ("none", "history", or
	// "durable"). It decides whether interrupted executions are failed on
	// restart or left for recovery to resume.
	durability string
}

// ErrActiveExecutions reports a destructive operation refused because an
// instance still owns executions that have not reached a terminal state.
var ErrActiveExecutions = errors.New("instance has unfinished executions")

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
	durability, err := NormalizeDurability(optsApplied.Durability)
	if err != nil {
		return nil, err
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
		durability:           durability,
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
	// Only durable mode may leave interrupted executions non-terminal for
	// recovery. "none" and "history" keep the previous behavior: an execution
	// with no runtime to advance it is failed during restore.
	resumeDurable := m.durability == DurabilityDurable
	for _, rec := range records {
		i, err := restoreInstance(
			m.parent,
			rec,
			execution.NewExecutionStore(m.store),
			event.NewStore(m.store),
			resumeDurable,
		)
		if err != nil {
			return fmt.Errorf("restore instance %s: %w", rec.ID, err)
		}
		m.instancesByID[rec.ID] = i
		m.instancesByKey[i.Key] = i
	}
	// Phase 4: recover unfinished durable executions.
	if err := m.recoverExecutions(ctx); err != nil {
		return fmt.Errorf("recover executions: %w", err)
	}
	return nil
}

func (m *Manager) recoverExecutions(ctx context.Context) error {
	// Recovery is meaningful only in durable mode. In "none" and "history"
	// modes restoreInstance has already failed every unfinished execution, so
	// there is nothing to resume and recovery is deliberately a no-op.
	if m.durability != DurabilityDurable {
		return nil
	}

	execStore := execution.NewExecutionStore(m.store)
	all, err := execStore.List(ctx)
	if err != nil {
		return fmt.Errorf("list executions for recovery: %w", err)
	}

	byID := make(map[core.ID]*execution.Execution, len(all))
	groups := make(map[core.ID][]*execution.Execution)
	for _, exec := range all {
		byID[exec.ID] = exec
		if exec.IsTerminal() {
			continue
		}
		// An execution without assembly identity and without a parent predates
		// durable execution. Its mode never promised resumption, so it is left
		// to the non-durable sweep rather than guessed at here.
		if exec.AssemblyID == "" && exec.ParentExecutionID == "" {
			continue
		}
		groups[exec.InstanceID] = append(groups[exec.InstanceID], exec)
	}

	for instanceID, executions := range groups {
		rec, err := m.metadata.Get(ctx, string(instanceID))
		if errors.Is(err, storage.ErrNotFound) {
			// The execution carries the immutable artifact reference itself, so
			// missing instance metadata is not a reason to abandon work that can
			// still be resumed. Recovery recreates the metadata after the
			// instance starts.
			rec = metadata{ID: string(instanceID)}
		} else if err != nil {
			return fmt.Errorf("recover instance %s: load metadata: %w", instanceID, err)
		}

		key := recoveryKey(rec, executions)
		if key.AssemblyID == "" || key.Version == "" {
			// Legacy durable records without an immutable identity cannot be
			// resumed safely. The non-durable sweep already owns their fate.
			continue
		}
		reg, err := m.assemblies.Resolve(ctx, key)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				if failErr := failUnrecoverable(ctx, execStore, executions, key, err); failErr != nil {
					return failErr
				}
				continue
			}
			return fmt.Errorf("recover instance %s against %s: %w", instanceID, key.String(), err)
		}
		if err := m.recoverInstance(ctx, rec, reg, executions, byID, execStore); err != nil {
			return err
		}
	}
	return nil
}

// recoveryKey prefers the instance's recorded artifact reference, falling back to
// the identity carried by a durable execution. The hash and environment are
// carried through so recovery resolves the exact artifact the execution started
// with, never the newest artifact for the version.
func recoveryKey(rec metadata, executions []*execution.Execution) protocol.InstanceKey {
	key := protocol.InstanceKey{
		AssemblyID: rec.AssemblyID,
		Version:    rec.Version,
		Hash:       rec.Hash,
		Env:        rec.Env,
	}
	if key.AssemblyID != "" && key.Version != "" {
		return key
	}
	for _, exec := range executions {
		if exec.AssemblyID == "" {
			continue
		}
		return protocol.InstanceKey{
			AssemblyID: exec.AssemblyID,
			Version:    exec.Version,
			Hash:       exec.Hash,
			Env:        exec.Env,
		}
	}
	return key
}

// failUnrecoverable records an explicit failure for executions whose immutable
// artifact is gone. Leaving them running would report work that no process can
// advance; silently dropping them would hide the loss. The failure names the
// artifact so an operator can decide whether retention or the execution is at
// fault.
func failUnrecoverable(ctx context.Context, store *execution.ExecutionStore, executions []*execution.Execution, key protocol.InstanceKey, cause error) error {
	var failures []error
	for _, exec := range executions {
		reason := fmt.Errorf("recover execution %s: immutable assembly artifact %s is unavailable: %w", exec.ID, key.String(), cause)
		if !exec.MarkFailed(reason) {
			continue
		}
		if err := store.Save(ctx, exec); err != nil {
			failures = append(failures, fmt.Errorf("persist unrecoverable execution %s: %w", exec.ID, err))
		}
	}
	return errors.Join(failures...)
}

// recoverInstance rebuilds the runtime for an instance that has unfinished
// durable work, attaches every execution to it, and starts it. The metadata-only
// instance restored earlier remains in the maps if any step fails, so a failed
// recovery does not pretend the runtime is live.
func (m *Manager) recoverInstance(
	ctx context.Context,
	rec metadata,
	reg assembly.RegisteredAssembly,
	executions []*execution.Execution,
	byID map[core.ID]*execution.Execution,
	execStore *execution.ExecutionStore,
) error {
	execOpt, err := withCapabilityRuntimes(reg)
	if err != nil {
		return fmt.Errorf("recover instance %s: %w", rec.ID, err)
	}
	inst, err := New(
		m.parent,
		rec.ID,
		reg.Key,
		&reg.Assembly,
		m.workers,
		m.store,
		execOpt,
		WithDetachedDrainTimeout(m.detachedDrainTimeout),
		WithBuiltinPolicy(m.builtinPolicy),
		WithDurability(m.durability),
	)
	if err != nil {
		return fmt.Errorf("create recovery instance %s: %w", rec.ID, err)
	}
	inst.createdAt = rec.CreatedAt

	// Roots first: a detached task's pending input comes from its parent's
	// recorded params, so the parent must be attached before the task.
	for _, exec := range executions {
		if exec.ParentExecutionID != "" {
			continue
		}
		if err := inst.RecoverExecution(ctx, exec, inst.Blueprint, nil); err != nil {
			_ = inst.Stop()
			return fmt.Errorf("recover execution %s on instance %s: %w", exec.ID, rec.ID, err)
		}
	}
	for _, exec := range executions {
		if exec.ParentExecutionID == "" {
			continue
		}
		if exec.DetachedCapabilityID == "" {
			_ = inst.Stop()
			return fmt.Errorf("recover detached task %s on instance %s: task has no origin capability", exec.ID, rec.ID)
		}
		scope, ok := inst.Blueprint.Detached[exec.DetachedCapabilityID]
		if !ok {
			_ = inst.Stop()
			return fmt.Errorf("recover detached task %s on instance %s: capability %s has no detached scope", exec.ID, rec.ID, exec.DetachedCapabilityID)
		}
		var pendingInput map[string]any
		if exec.Status() == execution.StatusPending {
			parent := byID[exec.ParentExecutionID]
			if parent == nil {
				parent, err = execStore.Get(ctx, exec.ParentExecutionID)
				if err != nil {
					_ = inst.Stop()
					return fmt.Errorf("recover detached task %s: load parent %s: %w", exec.ID, exec.ParentExecutionID, err)
				}
			}
			pendingInput = parent.Params(exec.DetachedCapabilityID)
		}
		if err := inst.RecoverExecution(ctx, exec, scope, pendingInput); err != nil {
			_ = inst.Stop()
			return fmt.Errorf("recover detached task %s on instance %s: %w", exec.ID, rec.ID, err)
		}
	}

	if err := inst.Start(); err != nil {
		_ = inst.Stop()
		return fmt.Errorf("start recovered instance %s: %w", rec.ID, err)
	}

	m.mu.Lock()
	if previous, ok := m.instancesByID[rec.ID]; ok {
		delete(m.instancesByKey, previous.Key)
	}
	m.instancesByID[rec.ID] = inst
	m.instancesByKey[inst.Key] = inst
	m.mu.Unlock()

	if err := m.metadata.Save(ctx, recordFor(inst, inst.Status())); err != nil {
		return fmt.Errorf("persist recovered instance metadata for %s: %w", inst.ID, err)
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

	id := core.NewID("inst_")
	if previous, ok := m.instancesByKey[canonical]; ok {
		id = core.ID(previous.ID)
		delete(m.instancesByKey, canonical)
		delete(m.instancesByID, previous.ID)
	}

	execOpt, err := withCapabilityRuntimes(reg)
	if err != nil {
		return nil, false, err
	}
	i, err := New(m.parent, string(id), canonical, &reg.Assembly, m.workers, m.store,
		execOpt, WithDetachedDrainTimeout(m.detachedDrainTimeout), WithBuiltinPolicy(m.builtinPolicy), WithDurability(m.durability))
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
// deleted along with its durable metadata. Unless force is set, removal is
// refused while the instance owns unfinished executions.
func (m *Manager) Remove(ctx context.Context, target string, force bool) (removed bool, err error) {
	if strings.HasPrefix(strings.TrimSpace(target), "inst_") {
		var inst *Instance
		inst, ok := m.GetByID(target)
		if !ok {
			return false, nil
		}
		return true, m.removeInstance(ctx, inst, force)
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
	return true, m.removeInstance(ctx, inst, force)
}

// removeInstance deletes an instance's runtime, executions, events and
// metadata. It is safe for both live and metadata-only (restored) instances.
// All deletions are performed atomically in a single storage transaction when
// possible; in-memory state is only removed after successful commit.
func (m *Manager) removeInstance(ctx context.Context, inst *Instance, force bool) error {
	if !force {
		if err := refuseActiveExecutions(ctx, inst); err != nil {
			return err
		}
	}
	if err := inst.Stop(); err != nil {
		return err
	}

	executions, err := inst.ListExecutions(ctx)
	if err != nil {
		return fmt.Errorf("list executions for instance %s: %w", inst.ID, err)
	}

	var keys []string
	for _, exec := range executions {
		k, err := storage.Key("executions", string(exec.ID))
		if err != nil {
			return err
		}
		keys = append(keys, k)
		prefix, err := storage.Prefix("events", string(exec.ID))
		if err != nil {
			return err
		}
		ekeys, err := m.store.List(ctx, prefix)
		if err != nil {
			return fmt.Errorf("list events for execution %s on %s: %w", exec.ID, inst.ID, err)
		}
		keys = append(keys, ekeys...)
	}
	mkey, err := storage.Key("instances", inst.ID)
	if err != nil {
		return err
	}
	keys = append(keys, mkey)

	if err := m.store.WithTx(ctx, func(tx storage.Tx) error {
		for _, k := range keys {
			if err := tx.Delete(ctx, k); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}

	// Clear memory caches if present (best effort)
	for _, exec := range executions {
		if es := inst.Store(); es != nil {
			_ = es.Delete(ctx, exec.ID)
		}
		_ = inst.EventStore().DeleteExecution(ctx, exec.ID)
	}
	// metadata already deleted in tx; ensure mem cleared
	_ = m.metadata.Delete(ctx, inst.ID)

	m.mu.Lock()
	delete(m.instancesByID, inst.ID)
	delete(m.instancesByKey, inst.Key)
	m.mu.Unlock()
	return nil
}

// RemoveByAssembly stops and removes every instance whose key addresses the
// given assembly, optionally scoped to the key's version. Returns the number of
// instances removed. Unless force is set, removal is refused when any matching
// instance still owns unfinished executions.
func (m *Manager) RemoveByAssembly(ctx context.Context, key protocol.InstanceKey, force bool) (int, error) {
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

	if !force {
		for _, inst := range matches {
			if err := refuseActiveExecutions(ctx, inst); err != nil {
				return 0, err
			}
		}
	}
	for _, inst := range matches {
		if err := m.removeInstance(ctx, inst, force); err != nil {
			return 0, err
		}
	}
	return len(matches), nil
}

// refuseActiveExecutions protects execution history that a live or recoverable
// execution still depends on. Stopping the instance would otherwise sweep those
// executions to failed before their records are deleted.
func refuseActiveExecutions(ctx context.Context, inst *Instance) error {
	executions, err := inst.ListExecutions(ctx)
	if err != nil {
		return fmt.Errorf("list executions for instance %s: %w", inst.ID, err)
	}
	active := 0
	for _, exec := range executions {
		if !exec.IsTerminal() {
			active++
		}
	}
	if active > 0 {
		return fmt.Errorf("%w: instance %s has %d unfinished execution(s); use --force to remove it and their history", ErrActiveExecutions, inst.ID, active)
	}
	return nil
}

// Clear stops and removes every tracked instance. Unless force is set, clearing
// is refused when any instance still owns unfinished executions.
func (m *Manager) Clear(ctx context.Context, force bool) error {
	m.mu.RLock()
	instances := make([]*Instance, 0, len(m.instancesByID))
	for _, inst := range m.instancesByID {
		instances = append(instances, inst)
	}
	m.mu.RUnlock()

	if !force {
		for _, inst := range instances {
			if err := refuseActiveExecutions(ctx, inst); err != nil {
				return err
			}
		}
	}

	var failures []error
	for _, inst := range instances {
		if err := m.removeInstance(ctx, inst, force); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
