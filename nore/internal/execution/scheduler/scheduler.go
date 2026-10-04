package scheduler

import (
	"context"
	"fmt"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
	"github.com/neuron-runtime/neuron/nore/internal/event"
	executionmodel "github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/shared/types/core"
)

type Scheduler struct {
	bus                 contracts.EventBus
	executions          contracts.ExecutionRepository
	scopes              *executionmodel.ScopeRegistry
	executionStarted    event.Subscription
	capabilityCompleted event.Subscription
	capabilityFailed    event.Subscription
	capabilityDetached  event.Subscription
}

// New builds a scheduler that advances executions and owns their cancellable
// scopes. Ownership lives here because advancing an execution and ending it are
// the same responsibility: the scheduler binds a scope when an execution starts,
// releases it when the execution reaches a terminal state, and is what a
// cancellation request goes through.
func New(bus contracts.EventBus, executions contracts.ExecutionRepository, scopes *executionmodel.ScopeRegistry) (*Scheduler, error) {
	if bus == nil || executions == nil {
		return nil, fmt.Errorf("event bus and execution repository are required")
	}
	if scopes == nil {
		return nil, fmt.Errorf("execution scope registry is required")
	}
	started, err := bus.Subscribe(event.ExecutionStarted, 64)
	if err != nil {
		return nil, err
	}
	completed, err := bus.Subscribe(event.CapabilityCompleted, 64)
	if err != nil {
		_ = started.Close()
		return nil, err
	}
	failed, err := bus.Subscribe(event.CapabilityFailed, 64)
	if err != nil {
		_ = started.Close()
		_ = completed.Close()
		return nil, err
	}
	detached, err := bus.Subscribe(event.CapabilityDetached, 64)
	if err != nil {
		_ = started.Close()
		_ = completed.Close()
		_ = failed.Close()
		return nil, err
	}
	return &Scheduler{bus: bus, executions: executions, scopes: scopes, executionStarted: started, capabilityCompleted: completed, capabilityFailed: failed, capabilityDetached: detached}, nil
}

func (s *Scheduler) Run(ctx context.Context) error {
	defer s.closeSubscriptions()
	defer s.scopes.ReleaseAll()
	for {
		select {
		case <-ctx.Done():
			return nil
		case received, open := <-s.executionStarted.Events():
			if !open {
				return nil
			}
			if err := s.onExecutionStarted(ctx, received); err != nil {
				s.failExecution(ctx, received.Metadata.ExecutionID, err)
			}
		case received, open := <-s.capabilityCompleted.Events():
			if !open {
				return nil
			}
			if err := s.onCapabilityCompleted(ctx, received); err != nil {
				s.failExecution(ctx, received.Metadata.ExecutionID, err)
			}
		case received, open := <-s.capabilityFailed.Events():
			if !open {
				return nil
			}
			payload, ok := received.Payload.(event.CapabilityFailedPayload)
			if !ok {
				payload.Message = "capability execution failed"
			}
			s.failExecution(ctx, received.Metadata.ExecutionID, fmt.Errorf("%s", payload.Message))
		case received, open := <-s.capabilityDetached.Events():
			if !open {
				return nil
			}
			if err := s.onCapabilityDetached(ctx, received); err != nil {
				s.failExecution(ctx, received.Metadata.ExecutionID, err)
			}
		}
	}
}

// onCapabilityDetached hands a detached capability's work to a separate
// execution.
//
// The handoff is ordered so that the task exists durably before the caller can
// observe the handoff as accepted: the task is added to the execution repository
// first, then the parent releases the capability and its in-flight slot, and only
// then is the task started. A reader that finds the parent's capability marked
// detached can therefore always find the task that took the work over, even if
// the process stopped immediately afterwards.
//
// The task's input is the detached capability's params, not the root execution's,
// because the task's entry is that one capability rather than the assembly.
func (s *Scheduler) onCapabilityDetached(ctx context.Context, received event.Event) error {
	execution, exists := s.executions.Get(received.Metadata.ExecutionID)
	if !exists {
		return fmt.Errorf("execution %s was not found", received.Metadata.ExecutionID)
	}
	if execution.IsTerminal() {
		return nil
	}
	capabilityID := received.Metadata.CapabilityID

	scope, exists := execution.Blueprint.Detached[capabilityID]
	if !exists {
		return fmt.Errorf("capability %s is detached but no execution scope was compiled for it", capabilityID)
	}

	task, err := executionmodel.NewDetachedTask(scope, execution.CorrelationID, execution.InstanceID, execution.ID)
	if err != nil {
		return err
	}
	if err := s.executions.Add(task); err != nil {
		return fmt.Errorf("persist detached task for capability %s: %w", capabilityID, err)
	}

	input := execution.Params(capabilityID)
	if err := execution.MarkCapabilityDetached(capabilityID); err != nil {
		// The task was never started, so leaving it behind would strand work no
		// one will run.
		s.executions.Delete(task.ID)
		return err
	}
	remaining, err := execution.CompleteCurrentAndSchedule(0)
	if err != nil {
		// The parent is corrupt, but the task has not started. Remove it rather
		// than leave an execution no one will ever run.
		s.executions.Delete(task.ID)
		return err
	}

	if err := s.bus.Publish(ctx, event.New(event.ExecutionStarted, task.ID, task.CorrelationID, "", event.ExecutionStartedPayload{Params: input})); err != nil {
		return err
	}
	return s.completeIfDrained(ctx, execution, remaining)
}

// CancelExecution stops an execution at the operator's request.
//
// The scope is cancelled before the state is recorded, so a capability already
// running is aborted as early as possible rather than after the bookkeeping. The
// execution is then made terminal, which is what actually stops the scheduler
// advancing it: onCapabilityCompleted and onCapabilityDetached both return
// early once an execution is terminal, so no further capability is scheduled and
// no new binding is evaluated.
//
// It reports whether this call cancelled the execution. A false return means the
// execution was already terminal or unknown, and the caller must not claim it
// cancelled something that had already finished.
func (s *Scheduler) CancelExecution(ctx context.Context, executionID core.ID, reason error) bool {
	execution, exists := s.executions.Get(executionID)
	if !exists {
		return false
	}
	// Stop the work even when the state transition loses the race below: a
	// capability must not keep running just because the execution completed in
	// the same instant the cancellation arrived.
	s.scopes.Cancel(executionID)
	if !execution.MarkCancelled(reason) {
		return false
	}
	s.scopes.Release(executionID)
	_ = s.bus.Publish(ctx, event.New(event.ExecutionCancelled, execution.ID, execution.CorrelationID, "", event.ExecutionCancelledPayload{
		Message: reasonMessage(reason),
	}))
	return true
}

// reasonMessage renders a cancellation reason for the event payload, which is
// read by clients that have no access to the Go error.
func reasonMessage(reason error) string {
	if reason == nil {
		return ""
	}
	return reason.Error()
}

func (s *Scheduler) onExecutionStarted(ctx context.Context, received event.Event) error {
	execution, exists := s.executions.Get(received.Metadata.ExecutionID)
	if !exists {
		return fmt.Errorf("execution %s was not found", received.Metadata.ExecutionID)
	}
	payload, ok := received.Payload.(event.ExecutionStartedPayload)
	if !ok {
		return fmt.Errorf("invalid ExecutionStarted payload")
	}
	// Bind before starting, so a capability is never invoked against a context
	// that could not yet be cancelled.
	s.scopes.Bind(execution.ID)
	entryIDs := execution.Blueprint.EntryCapabilityIDs
	if err := execution.Start(payload.Params, len(entryIDs)); err != nil {
		return err
	}
	for _, capabilityID := range entryIDs {
		node := execution.Blueprint.Nodes[capabilityID]
		input := cloneMap(payload.Params)
		if err := validateInput(node.Capability, input); err != nil {
			return fmt.Errorf("invalid entry input for capability %s: %w", capabilityID, err)
		}
		if err := execution.MarkCapabilityReady(capabilityID, input); err != nil {
			return err
		}
		if err := s.bus.Publish(ctx, event.New(event.CapabilityReady, execution.ID, execution.CorrelationID, capabilityID, event.CapabilityReadyPayload{Params: input})); err != nil {
			return err
		}
	}
	return nil
}

func (s *Scheduler) onCapabilityCompleted(ctx context.Context, received event.Event) error {
	execution, exists := s.executions.Get(received.Metadata.ExecutionID)
	if !exists {
		return fmt.Errorf("execution %s was not found", received.Metadata.ExecutionID)
	}
	if execution.IsTerminal() {
		return nil
	}
	node, exists := execution.Blueprint.Nodes[received.Metadata.CapabilityID]
	if !exists {
		return fmt.Errorf("capability %s is not present in the blueprint", received.Metadata.CapabilityID)
	}
	output := execution.Result(received.Metadata.CapabilityID)

	type scheduledCapability struct {
		id    core.ID
		input map[string]any
	}
	scheduled := make([]scheduledCapability, 0, len(node.Next))
	for _, transition := range node.Next {
		target, exists := execution.Blueprint.Nodes[transition.TargetCapabilityID]
		if !exists {
			return fmt.Errorf("target capability %s is missing", transition.TargetCapabilityID)
		}
		environment := buildTransitionEnvironment(execution, node, output)
		if err := validateTransition(ctx, environment, transition); err != nil {
			return err
		}
		input, err := applyTransition(ctx, environment, transition)
		if err != nil {
			return err
		}
		// Required/type validation always runs. Binding validations are additional and optional.
		if err := validateInput(target.Capability, input); err != nil {
			return fmt.Errorf("binding %s produced invalid input for capability %s: %w", transition.BindingID, target.Capability.Metadata.ID, err)
		}
		scheduled = append(scheduled, scheduledCapability{id: target.Capability.Metadata.ID, input: input})
	}

	remaining, err := execution.CompleteCurrentAndSchedule(len(scheduled))
	if err != nil {
		return err
	}
	for _, target := range scheduled {
		if err := execution.MarkCapabilityReady(target.id, target.input); err != nil {
			return err
		}
		if err := s.bus.Publish(ctx, event.New(event.CapabilityReady, execution.ID, execution.CorrelationID, target.id, event.CapabilityReadyPayload{Params: target.input})); err != nil {
			return err
		}
	}
	return s.completeIfDrained(ctx, execution, remaining)
}

// completeIfDrained finishes an execution whose own work is done. A remaining
// count above zero means capabilities are still scheduled here, so there is
// nothing to do. Detached capabilities are deliberately absent from that count:
// once their work is handed off, the scope that owns it is the task, not this
// execution.
func (s *Scheduler) completeIfDrained(ctx context.Context, execution *executionmodel.Execution, remaining int) error {
	if remaining != 0 || !execution.MarkCompleted() {
		return nil
	}
	s.scopes.Release(execution.ID)
	return s.bus.Publish(ctx, event.New(event.ExecutionCompleted, execution.ID, execution.CorrelationID, "", event.ExecutionCompletedPayload{
		Results: execution.StringKeyedResults(),
	}))
}

func (s *Scheduler) failExecution(ctx context.Context, executionID core.ID, err error) {
	execution, exists := s.executions.Get(executionID)
	if !exists || !execution.MarkFailed(err) {
		return
	}
	s.scopes.Release(execution.ID)
	_ = s.bus.Publish(ctx, event.New(event.ExecutionFailed, execution.ID, execution.CorrelationID, "", event.ExecutionFailedPayload{Message: err.Error()}))
}

func (s *Scheduler) closeSubscriptions() {
	_ = s.executionStarted.Close()
	_ = s.capabilityCompleted.Close()
	_ = s.capabilityFailed.Close()
	_ = s.capabilityDetached.Close()
}
