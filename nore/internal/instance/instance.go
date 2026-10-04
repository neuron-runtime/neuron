package instance

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/analytics"
	"github.com/neuron-runtime/neuron/nore/internal/contracts"
	"github.com/neuron-runtime/neuron/nore/internal/data"
	"github.com/neuron-runtime/neuron/nore/internal/event"
	"github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/execution/engine"
	"github.com/neuron-runtime/neuron/nore/internal/execution/scheduler"
	"github.com/neuron-runtime/neuron/nore/internal/planner"
	"github.com/neuron-runtime/neuron/nore/internal/plugin"
	"github.com/neuron-runtime/neuron/nore/internal/registry"
	"github.com/neuron-runtime/neuron/nore/internal/resolver"
	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/nore/internal/stream"
	"github.com/neuron-runtime/neuron/nore/internal/types"
	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

type Status string

const (
	StatusStarting Status = "starting"
	StatusRunning  Status = "running"
	StatusStopping Status = "stopping"
	StatusStopped  Status = "stopped"
	StatusFailed   Status = "failed"
)

// Option configures an Instance at construction time.
type Option func(*options)

type options struct {
	// resolvedCapabilityRuntimes is the frozen dependency set from the registered
	// assembly. Non-core capability runtimes are launched as subprocesses.
	resolvedCapabilityRuntimes []capabilityrt.ResolvedCapabilityRuntime

	// detachedDrainTimeout is how long detached work may keep running after the
	// instance stops. A non-positive value selects the engine default.
	detachedDrainTimeout time.Duration
}

// WithResolvedCapabilityRuntimes supplies the frozen capability runtime set persisted with the
// assembly's registration.
func WithResolvedCapabilityRuntimes(resolved []capabilityrt.ResolvedCapabilityRuntime) Option {
	return func(o *options) {
		o.resolvedCapabilityRuntimes = resolved
	}
}

// WithDetachedDrainTimeout sets how long detached capability work may continue
// after the instance stops. Detached work outlives its caller by design, so
// draining it is bounded rather than unlimited; a non-positive duration selects
// the engine default.
func WithDetachedDrainTimeout(timeout time.Duration) Option {
	return func(o *options) {
		o.detachedDrainTimeout = timeout
	}
}

type Instance struct {
	ID        string
	Key       protocol.InstanceKey
	Blueprint *types.ExecutionBlueprint

	mu     sync.RWMutex
	wg     sync.WaitGroup
	status Status

	ctx       context.Context
	cancel    context.CancelFunc
	bus       *event.Bus
	createdAt time.Time

	store      contracts.ExecutionRepository
	eventStore *event.Store

	scheduler *scheduler.Scheduler
	engine    *engine.CapabilityRuntimeEngine

	registry *registry.Registry

	analytics *analytics.Analytics

	execPersister *executionPersister
}

func New(
	parent context.Context,
	id string,
	key protocol.InstanceKey,
	assembly *shared.Assembly,
	workers int,
	persistentStore storage.Store,
	opts ...Option,
) (*Instance, error) {
	if assembly == nil {
		return nil, fmt.Errorf("blueprint is required")
	}
	if workers <= 0 {
		workers = 8
	}

	ctx, cancel := context.WithCancel(parent)
	bus := event.NewBus()

	var optsApplied options
	for _, opt := range opts {
		if opt != nil {
			opt(&optsApplied)
		}
	}

	store := execution.NewExecutionStore(persistentStore)
	evtStore := event.NewStore(persistentStore)

	reg := registry.New()
	reg.RegisterCoreRuntimes()

	if err := plugin.RegisterResolvedCapabilityRuntimes(reg, optsApplied.resolvedCapabilityRuntimes); err != nil {
		cancel()
		bus.Close()
		return nil, fmt.Errorf("register resolved capability runtimes: %w", err)
	}

	sched, err := scheduler.New(bus, store)
	if err != nil {
		cancel()
		bus.Close()
		return nil, fmt.Errorf("create scheduler: %w", err)
	}

	celCompiler, err := resolver.NewCELCompiler(resolver.DefaultCELConfig())
	if err != nil {
		cancel()
		bus.Close()
		return nil, fmt.Errorf("create cel compiler: %w", err)
	}

	assemblyCompiler, err := planner.NewCompiler(celCompiler)
	if err != nil {
		cancel()
		bus.Close()
		return nil, fmt.Errorf("create compiler: %w", err)
	}

	blueprint, err := assemblyCompiler.Compile(*assembly)
	if err != nil {
		cancel()
		bus.Close()
		return nil, fmt.Errorf("compile assemblies: %w", err)
	}

	execEngine, err := engine.NewCapabilityRuntimeEngine(bus, reg, store, workers, optsApplied.detachedDrainTimeout)
	if err != nil {
		cancel()
		bus.Close()
		return nil, fmt.Errorf("create capability runtime engine: %w", err)
	}

	anly, err := analytics.New(bus, slog.Default())
	if err != nil {
		cancel()
		bus.Close()
		return nil, fmt.Errorf("create Analytics engine: %w", err)
	}

	persister, err := newExecutionPersister(bus, store)
	if err != nil {
		cancel()
		bus.Close()
		return nil, fmt.Errorf("create execution persister: %w", err)
	}

	i := &Instance{
		ID:            id,
		Key:           key,
		Blueprint:     blueprint,
		status:        StatusStarting,
		ctx:           ctx,
		cancel:        cancel,
		bus:           bus,
		createdAt:     time.Now().UTC(),
		store:         store,
		eventStore:    evtStore,
		scheduler:     sched,
		engine:        execEngine,
		registry:      reg,
		analytics:     anly,
		execPersister: persister,
	}

	return i, nil
}

func (i *Instance) Start() error {
	i.mu.Lock()
	if i.status == StatusRunning {
		i.mu.Unlock()
		return nil
	}
	if i.status != StatusStarting && i.status != StatusStopped {
		status := i.status
		i.mu.Unlock()
		return fmt.Errorf("instance %s cannot start from %s", i.ID, status)
	}

	if i.scheduler == nil || i.engine == nil {
		i.mu.Unlock()
		return fmt.Errorf("instance %s has no runtime; it was restored from metadata", i.ID)
	}

	i.status = StatusRunning
	i.mu.Unlock()

	i.wg.Add(5)

	go func() {
		defer i.wg.Done()
		if err := i.analytics.Serve(i.ctx); err != nil {
			i.fail(err)
		}
	}()
	go func() {
		defer i.wg.Done()
		if err := i.scheduler.Run(i.ctx); err != nil {
			i.fail(err)
		}
	}()
	go func() {
		defer i.wg.Done()
		if err := i.engine.Run(i.ctx); err != nil {
			i.fail(err)
		}
	}()
	go func() {
		defer i.wg.Done()
		i.persistEvents(i.ctx)
	}()
	go func() {
		defer i.wg.Done()
		_ = i.execPersister.Run(i.ctx)
	}()

	return nil
}

func (i *Instance) fail(err error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.status == StatusRunning {
		i.status = StatusFailed
	}
}

func (i *Instance) Stop() error {
	i.mu.Lock()
	if i.status == StatusStopped {
		i.mu.Unlock()
		return nil
	}
	i.status = StatusStopping
	i.mu.Unlock()

	i.cancel()
	// Drain before awaiting the runtime goroutines: once the context is
	// cancelled the scheduler has already exited, so it can no longer report
	// the outcome of an execution it was advancing.
	failUnfinishedExecutions(i.store, shared.ID(i.ID), stoppedBeforeFinished(i.ID))
	if i.bus != nil {
		i.wg.Wait()
	}

	// Release capability runtime-backed resources (wasm runtimes) now that no execution
	// can be in flight.
	if i.registry != nil {
		if err := i.registry.Close(); err != nil {
			slog.Warn("close capability runtimes", slog.String("instance", i.ID), slog.String("error", err.Error()))
		}
	}

	if i.bus != nil {
		i.bus.Close()
	}

	i.mu.Lock()
	i.status = StatusStopped
	i.mu.Unlock()
	return nil
}

func (i *Instance) Status() Status {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.status
}

func (i *Instance) Store() contracts.ExecutionRepository {
	return i.store
}

func (i *Instance) Bus() *event.Bus {
	return i.bus
}

func (i *Instance) EventStore() *event.Store {
	return i.eventStore
}

func (i *Instance) Execute(ctx context.Context, input map[string]any) (*execution.Execution, error) {
	if i.Status() != StatusRunning {
		return nil, fmt.Errorf("instance %s is not running", i.ID)
	}
	// Canonical casing is snake_case; normalize camelCase --input (e.g. from
	// a TypeScript-authored assembly) once so every expression resolves.
	input = data.SnakeMap(input)

	exec, err := execution.NewExecution(i.Blueprint, shared.NewID("request_"), shared.ID(i.ID))
	if err != nil {
		return nil, err
	}
	if err := i.store.Add(exec); err != nil {
		return nil, err
	}

	if err := i.bus.Publish(
		ctx,
		event.New(
			event.ExecutionStarted,
			exec.ID,
			exec.CorrelationID,
			"",
			event.ExecutionStartedPayload{Params: input},
		),
	); err != nil {
		return nil, fmt.Errorf("publish execution start: %w", err)
	}

	return exec, nil
}

func (i *Instance) ListExecutions() []*execution.Execution {
	return i.store.ListByInstance(shared.ID(i.ID))
}

func (i *Instance) GetExecution(id shared.ID) (*execution.Execution, bool) {
	return i.store.Get(id)
}

func (i *Instance) ListExecutionEvents(ctx context.Context, executionID shared.ID) ([]event.Event, error) {
	return i.eventStore.List(ctx, executionID)
}

// Events composes persisted history with live bus events for the execution.
func (i *Instance) Events(ctx context.Context, eventID shared.ID, after shared.ID) (<-chan stream.Message, error) {
	return stream.Subscribe(ctx, i.bus, i.eventStore, eventID, after)
}

func (i *Instance) persistEvents(ctx context.Context) {
	sub, err := i.bus.Subscribe(event.All, 256)
	if err != nil {
		return
	}
	defer sub.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-sub.Events():
			if !ok {
				return
			}
			if err := i.eventStore.Save(ctx, evt); err != nil {
				slog.Error("persist event",
					slog.String("event_id", string(evt.Metadata.EventID)),
					slog.String("execution_id", string(evt.Metadata.ExecutionID)),
					slog.String("error", err.Error()),
				)
			}
		}
	}
}
