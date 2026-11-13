package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"sync"

	exec "github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/shared/types/core"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
	"github.com/neuron-runtime/neuron/nore/internal/data"
	"github.com/neuron-runtime/neuron/nore/internal/event"
	"github.com/neuron-runtime/neuron/nore/internal/resolver"
	"github.com/neuron-runtime/neuron/nore/internal/storage"
)

// DefaultDetachedDrainTimeout bounds how long N.O.R.E. keeps running detached
// work after its caller has gone away. Detached work is outlived by design, but
// an unbounded drain would let shutdown never finish, so the budget is a policy
// with a default rather than an author's per-capability setting.
const DefaultDetachedDrainTimeout = 30 * time.Second

// DefaultDispatchLease is how long an engine's claim on a dispatch intent lasts
// before another worker may take the work over. It is added to the capability's
// own timeout so a claim outlives the invocation it guards; a capability with no
// declared timeout relies on the base lease alone. Claims are not renewed, so a
// capability that runs longer than its lease can be dispatched a second time;
// that is the documented cost of not renewing, and at-least-once dispatch
// tolerates it.
const DefaultDispatchLease = 30 * time.Second

// dispatchCompleteTimeout bounds the bookkeeping call that releases an intent
// after a capability finishes. It is short because completion is a single small
// write, and it is deliberately detached from the invocation context so a
// cancellation does not leave a claim behind.
const dispatchCompleteTimeout = 5 * time.Second

type CapabilityRuntimeEngine struct {
	bus        contracts.EventBus
	registry   contracts.CapabilityRuntimeRegistry
	executions contracts.ExecutionRepository
	scopes     *exec.ScopeRegistry
	committer  contracts.TransitionCommitter
	ready      event.Subscription
	semaphore  chan struct{}

	// owner identifies this engine when it claims dispatch intents. It is stable
	// for the engine's lifetime so a worker cannot release or complete a claim
	// another engine owns.
	owner string

	// detachedDrainTimeout is how much of it a detached task may spend running
	// after the instance stops accepting new work.
	detachedDrainTimeout time.Duration
}

// Option configures a CapabilityRuntimeEngine at construction time.
type Option func(*CapabilityRuntimeEngine)

// WithTransitionCommitter makes the engine claim dispatch intents before
// invoking a capability, so the same ready work is not executed twice. It is
// supplied only in durable mode; without it the engine invokes whatever
// CapabilityReady event it observes, exactly as before.
func WithTransitionCommitter(committer contracts.TransitionCommitter) Option {
	return func(e *CapabilityRuntimeEngine) {
		e.committer = committer
	}
}

func NewCapabilityRuntimeEngine(bus contracts.EventBus, registry contracts.CapabilityRuntimeRegistry, executions contracts.ExecutionRepository, scopes *exec.ScopeRegistry, maxConcurrency int, detachedDrainTimeout time.Duration, opts ...Option) (*CapabilityRuntimeEngine, error) {
	if maxConcurrency <= 0 {
		maxConcurrency = 8
	}
	if detachedDrainTimeout <= 0 {
		detachedDrainTimeout = DefaultDetachedDrainTimeout
	}
	if scopes == nil {
		return nil, fmt.Errorf("execution scope registry is required")
	}
	e := &CapabilityRuntimeEngine{owner: string(core.NewID("engine_"))}
	for _, opt := range opts {
		if opt != nil {
			opt(e)
		}
	}
	ready, err := bus.Subscribe(event.CapabilityReady, maxConcurrency*4)
	if err != nil {
		return nil, err
	}
	e.bus = bus
	e.registry = registry
	e.executions = executions
	e.scopes = scopes
	e.ready = ready
	e.semaphore = make(chan struct{}, maxConcurrency)
	e.detachedDrainTimeout = detachedDrainTimeout
	return e, nil
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
	execution, err := e.executions.Get(ctx, received.Metadata.ExecutionID)
	if errors.Is(err, storage.ErrNotFound) {
		return
	}
	if err != nil {
		slog.Error("load execution for capability",
			slog.String("execution_id", string(received.Metadata.ExecutionID)),
			slog.String("capability_id", string(received.Metadata.CapabilityID)),
			slog.String("error", err.Error()),
		)
		return
	}
	if execution.IsTerminal() {
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
		e.publishFailure(ctx, execution, capabilityID, err)
		return
	}

	// In durable mode the ready capability is represented by a persisted
	// dispatch intent. Claiming it is what makes duplicate CapabilityReady
	// deliveries idempotent: only the worker that wins the claim invokes the
	// capability, and a duplicate that loses the claim does nothing. The claim
	// is released once this worker is done with the capability, whether it ran
	// it, detached it, or failed to prepare it.
	releaseClaim, claimed, err := e.claimDispatch(ctx, execution, capabilityID, policy)
	if err != nil {
		e.publishFailure(ctx, execution, capabilityID, err)
		return
	}
	if !claimed {
		return
	}
	defer releaseClaim()

	if policy.Detached() {
		e.detachCapability(ctx, execution, capabilityID)
		return
	}

	resolvedConfig, err := node.Configurations.Resolve(ctx, resolver.CapabilityEnvironment{
		// The `params` variable always exposes the canonical snake_case keys,
		// whether the capability's input reached it through authored camelCase
		// assembly params or through structured mapping targets. A single
		// spelling keeps configuration templates and binding expressions
		// consistent, which is what P0-23 demanded.
		Params: data.SnakeMap(input),
		Execution: map[string]any{
			"id": string(execution.ID), "correlation_id": string(execution.CorrelationID),
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
		// This is the one place a capability can stop without an outcome of its
		// own, because the execution's scope may be cancelled underneath it.
		// publishStopped decides whether that is a failure or a consequence of
		// someone else's decision; a timeout N.O.R.E. imposed is still a failure.
		e.publishStopped(ctx, invocationCtx, execution, capabilityID, err)
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

// claimDispatch takes ownership of the persisted dispatch intent for one ready
// capability and returns the function that releases it once this worker is done.
//
// The intent's identity is derived from the execution, the capability, and the
// execution's attempt ID, so replaying the same readiness maps to the same
// intent rather than a second one. The lease is the base lease plus the
// capability's own timeout, so a claim outlives the invocation it guards.
//
// It reports claimed=false when the intent is missing, unready, or already held
// under a live lease; the caller must then do nothing, because another worker
// owns the work. Without a committer it reports claimed=true and a no-op
// release, which is how the non-durable modes keep their existing behavior.
func (e *CapabilityRuntimeEngine) claimDispatch(ctx context.Context, execution *exec.Execution, capabilityID core.ID, policy invocation) (func(), bool, error) {
	if e.committer == nil {
		return func() {}, true, nil
	}
	intent := exec.NewDispatchIntent(execution.ID, capabilityID, execution.AttemptID)
	lease := DefaultDispatchLease
	if policy.timeout > 0 {
		lease += policy.timeout
	}
	if _, won, err := e.committer.ClaimDispatchIntent(ctx, intent, e.owner, lease); err != nil {
		return nil, false, fmt.Errorf("claim dispatch intent for capability %s: %w", capabilityID, err)
	} else if !won {
		return nil, false, nil
	}
	return func() {
		completeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), dispatchCompleteTimeout)
		defer cancel()
		if err := e.committer.CompleteDispatchIntent(completeCtx, execution.ID, capabilityID, execution.AttemptID, e.owner); err != nil {
			slog.Error("complete dispatch intent",
				slog.String("execution_id", string(execution.ID)),
				slog.String("capability_id", string(capabilityID)),
				slog.String("error", err.Error()),
			)
		}
	}, true, nil
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
// When the execution has already recorded an outcome for the capability -- it
// completed before a stop arrived, or the execution was cancelled and recorded
// the capability as cancelled -- the failure is neither recorded nor announced,
// because announcing it would overwrite a state the execution genuinely holds.
//
// A capability stopped by a cancellation or by a sibling's failure does not come
// through here; it goes through publishStopped, which tells the two apart.
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

// publishStopped records a capability that did not reach an outcome, and decides
// whether that outcome was a failure or a cancellation.
//
// This distinction is the difference between reporting a fault and reporting a
// consequence. When an operator cancels an execution, or a sibling capability
// fails, the scheduler releases the execution's scope and every capability still
// running is stopped. Those capabilities did not break -- they were stopped by a
// decision made elsewhere in the execution. Recording them as failures blamed
// working implementations for someone else's stop, and because the event carried
// no distinguishing type, a client could only recover the truth by matching the
// message text.
//
// A capability was stopped exactly when the context it was invoked under was
// cancelled. A deadline that N.O.R.E. imposed stays a failure: nobody chose to
// stop that work, it ran out of time, and that is the author's to fix. The
// decision is made here, in one place, rather than at each call site, so every
// path that can end an invocation early is classified the same way.
//
// Both outcomes are published on the engine's context rather than the cancelled
// scope, for the reason given on publishFailure: publishing on a context that is
// already cancelled would race the cancellation against delivery.
func (e *CapabilityRuntimeEngine) publishStopped(ctx, scopeCtx context.Context, execution *exec.Execution, capabilityID core.ID, err error) {
	if scopeCtx.Err() == nil {
		e.publishFailure(ctx, execution, capabilityID, err)
		return
	}
	if !execution.MarkCapabilityCancelled(capabilityID, err) {
		return
	}
	_ = e.bus.Publish(ctx, event.New(event.CapabilityCancelled, execution.ID, execution.CorrelationID, capabilityID, event.CapabilityCancelledPayload{Message: err.Error()}))
}
