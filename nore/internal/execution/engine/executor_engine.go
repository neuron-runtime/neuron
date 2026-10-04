package engine

import (
	"context"
	"fmt"
	"time"

	"sync"

	exec "github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/shared/types/core"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
	"github.com/neuron-runtime/neuron/nore/internal/data"
	"github.com/neuron-runtime/neuron/nore/internal/event"
	"github.com/neuron-runtime/neuron/nore/internal/resolver"
)

// DefaultDetachedDrainTimeout bounds how long N.O.R.E. keeps running detached
// work after its caller has gone away. Detached work is outlived by design, but
// an unbounded drain would let shutdown never finish, so the budget is a policy
// with a default rather than an author's per-capability setting.
const DefaultDetachedDrainTimeout = 30 * time.Second

type CapabilityRuntimeEngine struct {
	bus        contracts.EventBus
	registry   contracts.CapabilityRuntimeRegistry
	executions contracts.ExecutionRepository
	scopes     *exec.ScopeRegistry
	ready      event.Subscription
	semaphore  chan struct{}

	// detachedDrainTimeout is how much of it a detached task may spend running
	// after the instance stops accepting new work.
	detachedDrainTimeout time.Duration
}

func NewCapabilityRuntimeEngine(bus contracts.EventBus, registry contracts.CapabilityRuntimeRegistry, executions contracts.ExecutionRepository, scopes *exec.ScopeRegistry, maxConcurrency int, detachedDrainTimeout time.Duration) (*CapabilityRuntimeEngine, error) {
	if maxConcurrency <= 0 {
		maxConcurrency = 8
	}
	if detachedDrainTimeout <= 0 {
		detachedDrainTimeout = DefaultDetachedDrainTimeout
	}
	if scopes == nil {
		return nil, fmt.Errorf("execution scope registry is required")
	}
	ready, err := bus.Subscribe(event.CapabilityReady, maxConcurrency*4)
	if err != nil {
		return nil, err
	}
	return &CapabilityRuntimeEngine{bus: bus, registry: registry, executions: executions, scopes: scopes, ready: ready, semaphore: make(chan struct{}, maxConcurrency), detachedDrainTimeout: detachedDrainTimeout}, nil
}

func (e *CapabilityRuntimeEngine) Run(ctx context.Context) error {
	var workers sync.WaitGroup
	defer func() {
		workers.Wait()
		_ = e.ready.Close()
	}()
	for {
		select {
		case <-ctx.Done():
			return nil
		case received, open := <-e.ready.Events():
			if !open {
				return nil
			}
			select {
			case e.semaphore <- struct{}{}:
			case <-ctx.Done():
				return nil
			}
			workers.Add(1)
			go func(received event.Event) {
				defer workers.Done()
				defer func() { <-e.semaphore }()
				e.executeCapability(ctx, received)
			}(received)
		}
	}
}

func (e *CapabilityRuntimeEngine) executeCapability(ctx context.Context, received event.Event) {
	execution, exists := e.executions.Get(received.Metadata.ExecutionID)
	if !exists || execution.IsTerminal() {
		return
	}
	capabilityID := received.Metadata.CapabilityID
	node, exists := execution.Blueprint.Nodes[capabilityID]
	if !exists {
		e.publishFailure(ctx, execution, capabilityID, fmt.Errorf("capability %s does not exist in the blueprint", capabilityID))
		return
	}

	// The invocation runs under its execution's own scope rather than the
	// instance context, so cancelling one execution aborts exactly its
	// capabilities and leaves every other execution running.
	invocationCtx, release := e.invocationContext(ctx, execution)
	defer release()

	input := execution.Params(capabilityID)
	policy, err := newInvocation(node.Capability.RuntimeConfig)
	if err != nil {
		e.publishFailure(ctx, execution, capabilityID, fmt.Errorf("capability %s: %w", capabilityID, err))
		return
	}
	if policy.Detached() {
		e.detachCapability(ctx, execution, capabilityID)
		return
	}

	resolvedConfig, err := node.Configurations.Resolve(ctx, resolver.CapabilityEnvironment{
		Params: input,
		Execution: map[string]any{
			"id": string(execution.ID), "correlation_id": string(execution.CorrelationID),
			// `params` is the one name the Assembly's initial parameters have in
			// every expression dialect. A Capability configuration template and a
			// Binding expression that both want them must agree on how to spell
			// it, or an author has to learn two vocabularies for one value.
			"params": data.SnakeMap(execution.InitialParams()),
			"blueprint": map[string]any{
				"id": string(execution.Blueprint.Metadata.ID), "name": execution.Blueprint.Metadata.Name,
				"version": execution.Blueprint.Metadata.Version,
			},
		},
		Capability: map[string]any{
			"id": string(node.Capability.Metadata.ID), "name": node.Capability.Metadata.Name,
			"type": string(node.Capability.Type), "version": node.Capability.Metadata.Version,
		},
	})
	if err != nil {
		e.publishFailure(ctx, execution, capabilityID, fmt.Errorf("resolve configurations for capability %s: %w", capabilityID, err))
		return
	}

	if err := execution.MarkCapabilityRunning(capabilityID); err != nil {
		e.publishFailure(ctx, execution, capabilityID, err)
		return
	}
	if err := e.bus.Publish(ctx, event.New(event.CapabilityStarted, execution.ID, execution.CorrelationID, capabilityID, event.CapabilityStartedPayload{})); err != nil {
		e.publishFailure(ctx, execution, capabilityID, err)
		return
	}
	cr, err := e.registry.Resolve(node.Capability.Type)
	if err != nil {
		e.publishFailure(ctx, execution, capabilityID, err)
		return
	}

	output, err := policy.run(invocationCtx, capabilityID,
		func(attemptCtx context.Context) (map[string]any, error) {
			return cr.Execute(attemptCtx, contracts.ExecutionContext{
				ExecutionID: execution.ID, CorrelationID: execution.CorrelationID,
				Capability: node.Capability, Params: input, CapabilityConfigurations: resolvedConfig,
				// The runtime configuration comes from the capability's runtime
				// declaration, never from its input. The planner resolved defaults onto
				// the plan, so this is always complete.
				RuntimeConfig: node.Capability.RuntimeConfig,
				Logger:        newExecLogger(e.bus, execution.ID, execution.CorrelationID, capabilityID),
			})
		},
		func(retry retryNotice) {
			_ = e.bus.Publish(ctx, event.New(event.CapabilityRetry, execution.ID, execution.CorrelationID, capabilityID, event.CapabilityRetryPayload{
				Attempt: retry.attempt, NextAttempt: retry.nextAttempt,
				Delay: retry.delay.String(), Message: retry.err.Error(),
			}))
		})
	if err != nil {
		e.publishFailure(ctx, execution, capabilityID, err)
		return
	}
	if output == nil {
		output = map[string]any{}
	}
	if err := execution.MarkCapabilityCompleted(capabilityID, output); err != nil {
		e.publishFailure(ctx, execution, capabilityID, err)
		return
	}
	_ = e.bus.Publish(ctx, event.New(event.CapabilityCompleted, execution.ID, execution.CorrelationID, capabilityID, event.CapabilityCompletedPayload{Result: output}))
}

// detachCapability announces that a capability's work is handed off to a separate
// execution instead of being run here.
//
// The engine decides only this: that the capability leaves this execution's scope
// and that a boundary was crossed. Creating the task that takes the work over
// belongs to the scheduler, because only it can make that task durable before the
// caller is allowed to treat the handoff as acknowledged.
//
// The capability is deliberately left ready rather than marked detached here.
// Marking it is the scheduler's final step, taken after the task is persisted, so
// the parent never reports a boundary it has not yet recorded anywhere. That
// ordering also means a capability cannot be detached twice.
func (e *CapabilityRuntimeEngine) detachCapability(ctx context.Context, execution *exec.Execution, capabilityID core.ID) {
	_ = e.bus.Publish(ctx, event.New(event.CapabilityDetached, execution.ID, execution.CorrelationID, capabilityID, event.CapabilityDetachedPayload{}))
}

// invocationContext returns the context a capability must run under, together
// with the function that releases whatever that context had to allocate.
//
// An ordinary execution runs under its own scope, which the scheduler derived
// from the instance context. Cancelling that execution aborts exactly its own
// capabilities and leaves every other execution alone; stopping the instance
// still aborts everything.
//
// A detached task is the exception, because detach exists so work can outlive
// the execution that started it -- including outliving the instance shutting
// down. Its context is therefore detached from the instance and bounded by the
// drain timeout instead. What it must still honour is its own execution's
// cancellation, or a detached task would be work nobody is able to stop; so the
// drain context is wired to the task's scope rather than to the instance.
func (e *CapabilityRuntimeEngine) invocationContext(ctx context.Context, execution *exec.Execution) (context.Context, func()) {
	scope, scoped := e.scopes.Context(execution.ID)

	if execution.ParentExecutionID == "" {
		if scoped {
			return scope, func() {}
		}
		// The execution has no live scope, so there is nothing to cancel it
		// against. Running under the instance context keeps the invocation
		// bounded and stops it on shutdown rather than leaking.
		return ctx, func() {}
	}

	drained, cancel := context.WithTimeout(context.WithoutCancel(ctx), e.detachedDrainTimeout)
	if !scoped {
		return drained, cancel
	}
	stopOnScopeCancel := context.AfterFunc(scope, cancel)
	return drained, func() {
		stopOnScopeCancel()
		cancel()
	}
}

// publishFailure records a capability's failure and announces it.
//
// An aborted invocation reaches this path too: cancelling an execution stops the
// work, the runtime returns the context error, and that error is not a capability
// fault. When the execution has already recorded an outcome for the capability
// -- cancelled, or completed before the stop arrived -- the failure is neither
// recorded nor announced, because announcing it would overwrite a state the
// execution genuinely holds.
//
// The event is published on the engine's context rather than the execution's
// scope, because an event about a cancelled execution still has to reach the
// subscribers that are watching for the cancellation to end. Publishing it on
// the cancelled scope would race the cancellation against delivery and could
// drop the terminal event.
func (e *CapabilityRuntimeEngine) publishFailure(ctx context.Context, execution *exec.Execution, capabilityID core.ID, err error) {
	if !execution.MarkCapabilityFailed(capabilityID, err) {
		return
	}
	_ = e.bus.Publish(ctx, event.New(event.CapabilityFailed, execution.ID, execution.CorrelationID, capabilityID, event.CapabilityFailedPayload{Message: err.Error()}))
}
