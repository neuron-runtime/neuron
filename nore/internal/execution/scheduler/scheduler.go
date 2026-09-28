package scheduler

import (
	"context"
	"fmt"

	"github.com/Muhammad-Jay/neuron/nore/internal/contracts"
	"github.com/Muhammad-Jay/neuron/nore/internal/event"
	"github.com/Muhammad-Jay/neuron/shared/types/core"
)

type Scheduler struct {
	bus              contracts.EventBus
	executions       contracts.ExecutionRepository
	executionStarted event.Subscription
	capabilityCompleted event.Subscription
	capabilityFailed    event.Subscription
}

func New(bus contracts.EventBus, executions contracts.ExecutionRepository) (*Scheduler, error) {
	if bus == nil || executions == nil {
		return nil, fmt.Errorf("event bus and execution repository are required")
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
	return &Scheduler{bus: bus, executions: executions, executionStarted: started, capabilityCompleted: completed, capabilityFailed: failed}, nil
}

func (s *Scheduler) Run(ctx context.Context) error {
	defer s.closeSubscriptions()
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
		}
	}
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
	if remaining != 0 || !execution.MarkCompleted() {
		return nil
	}
	return s.bus.Publish(ctx, event.New(event.ExecutionCompleted, execution.ID, execution.CorrelationID, "", event.ExecutionCompletedPayload{
		Results: execution.StringKeyedResults(),
	}))
}

func (s *Scheduler) failExecution(ctx context.Context, executionID core.ID, err error) {
	execution, exists := s.executions.Get(executionID)
	if !exists || !execution.MarkFailed(err) {
		return
	}
	_ = s.bus.Publish(ctx, event.New(event.ExecutionFailed, execution.ID, execution.CorrelationID, "", event.ExecutionFailedPayload{Message: err.Error()}))
}

func (s *Scheduler) closeSubscriptions() {
	_ = s.executionStarted.Close()
	_ = s.capabilityCompleted.Close()
	_ = s.capabilityFailed.Close()
}
