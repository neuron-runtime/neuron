package engine

import (
	"context"
	"fmt"
	"time"

	"sync"

	exec "github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/shared/types/core"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
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
	ready      event.Subscription
	semaphore  chan struct{}

	// detachedDrainTimeout is how much of it a detached task may spend running
	// after the instance stops accepting new work.
	detachedDrainTimeout time.Duration
}

func NewCapabilityRuntimeEngine(bus contracts.EventBus, registry contracts.CapabilityRuntimeRegistry, executions contracts.ExecutionRepository, maxConcurrency int, detachedDrainTimeout time.Duration) (*CapabilityRuntimeEngine, error) {
	if maxConcurrency <= 0 {
		maxConcurrency = 8
	}
	if detachedDrainTimeout <= 0 {
		detachedDrainTimeout = DefaultDetachedDrainTimeout
	}
	ready, err := bus.Subscribe(event.CapabilityReady, maxConcurrency*4)
	if err != nil {
		return nil, err
	}
	return &CapabilityRuntimeEngine{bus: bus, registry: registry, executions: executions, ready: ready, semaphore: make(chan struct{}, maxConcurrency), detachedDrainTimeout: detachedDrainTimeout}, nil
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
			"input": execution.InitialParams(),
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

	output, err := e.invoke(ctx, execution, capabilityID, policy,
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

// invoke runs a capability invocation under its resolved policy, giving detached
// work its own shutdown budget.
//
// A detached task deliberately outlives its caller, so it is allowed to keep
// running while the instance drains — but only for a bounded time, because an
// unbounded drain would mean shutdown never finishes. Every other invocation
// inherits the instance context, so a shutdown cancels it promptly rather than
// waiting on capabilities that were never meant to outlive it.
func (e *CapabilityRuntimeEngine) invoke(ctx context.Context, execution *exec.Execution, capabilityID core.ID, policy invocation, execute func(context.Context) (map[string]any, error), onRetry func(retryNotice)) (map[string]any, error) {
	if execution.ParentExecutionID == "" {
		return policy.run(ctx, capabilityID, execute, onRetry)
	}
	drained, cancel := context.WithTimeout(context.WithoutCancel(ctx), e.detachedDrainTimeout)
	defer cancel()
	return policy.run(drained, capabilityID, execute, onRetry)
}

func (e *CapabilityRuntimeEngine) publishFailure(ctx context.Context, execution *exec.Execution, capabilityID core.ID, err error) {
	execution.MarkCapabilityFailed(capabilityID, err)
	_ = e.bus.Publish(ctx, event.New(event.CapabilityFailed, execution.ID, execution.CorrelationID, capabilityID, event.CapabilityFailedPayload{Message: err.Error()}))
}
