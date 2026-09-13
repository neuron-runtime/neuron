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
	serviceCompleted event.Subscription
	serviceFailed    event.Subscription
}

func New(bus contracts.EventBus, executions contracts.ExecutionRepository) (*Scheduler, error) {
	if bus == nil || executions == nil {
		return nil, fmt.Errorf("event bus and execution repository are required")
	}
	started, err := bus.Subscribe(event.ExecutionStarted, 64)
	if err != nil {
		return nil, err
	}
	completed, err := bus.Subscribe(event.ServiceCompleted, 64)
	if err != nil {
		_ = started.Close()
		return nil, err
	}
	failed, err := bus.Subscribe(event.ServiceFailed, 64)
	if err != nil {
		_ = started.Close()
		_ = completed.Close()
		return nil, err
	}
	return &Scheduler{bus: bus, executions: executions, executionStarted: started, serviceCompleted: completed, serviceFailed: failed}, nil
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
		case received, open := <-s.serviceCompleted.Events():
			if !open {
				return nil
			}
			if err := s.onServiceCompleted(ctx, received); err != nil {
				s.failExecution(ctx, received.Metadata.ExecutionID, err)
			}
		case received, open := <-s.serviceFailed.Events():
			if !open {
				return nil
			}
			payload, ok := received.Payload.(event.ServiceFailedPayload)
			if !ok {
				payload.Message = "service execution failed"
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
	entryIDs := execution.Blueprint.EntryServiceIDs
	if err := execution.Start(payload.Input, len(entryIDs)); err != nil {
		return err
	}
	for _, serviceID := range entryIDs {
		node := execution.Blueprint.Nodes[serviceID]
		input := cloneMap(payload.Input)
		if err := validateInput(node.Service, input); err != nil {
			return fmt.Errorf("invalid entry input for service %s: %w", serviceID, err)
		}
		if err := execution.MarkServiceReady(serviceID, input); err != nil {
			return err
		}
		if err := s.bus.Publish(ctx, event.New(event.ServiceReady, execution.ID, execution.CorrelationID, serviceID, event.ServiceReadyPayload{Input: input})); err != nil {
			return err
		}
	}
	return nil
}

func (s *Scheduler) onServiceCompleted(ctx context.Context, received event.Event) error {
	execution, exists := s.executions.Get(received.Metadata.ExecutionID)
	if !exists {
		return fmt.Errorf("execution %s was not found", received.Metadata.ExecutionID)
	}
	if execution.IsTerminal() {
		return nil
	}
	node, exists := execution.Blueprint.Nodes[received.Metadata.ServiceID]
	if !exists {
		return fmt.Errorf("service %s is not present in the blueprint", received.Metadata.ServiceID)
	}
	output := execution.Output(received.Metadata.ServiceID)

	type scheduledService struct {
		id    core.ID
		input map[string]any
	}
	scheduled := make([]scheduledService, 0, len(node.Next))
	for _, transition := range node.Next {
		target, exists := execution.Blueprint.Nodes[transition.TargetServiceID]
		if !exists {
			return fmt.Errorf("target service %s is missing", transition.TargetServiceID)
		}
		environment := buildTransitionEnvironment(execution, node, output)
		if err := validateTransition(ctx, environment, transition); err != nil {
			return err
		}
		input, err := applyTransition(ctx, environment, transition)
		if err != nil {
			return err
		}
		// Required/type validation always runs. Connector validations are additional and optional.
		if err := validateInput(target.Service, input); err != nil {
			return fmt.Errorf("connector %s produced invalid input for service %s: %w", transition.ConnectorID, target.Service.Metadata.ID, err)
		}
		scheduled = append(scheduled, scheduledService{id: target.Service.Metadata.ID, input: input})
	}

	remaining, err := execution.CompleteCurrentAndSchedule(len(scheduled))
	if err != nil {
		return err
	}
	for _, target := range scheduled {
		if err := execution.MarkServiceReady(target.id, target.input); err != nil {
			return err
		}
		if err := s.bus.Publish(ctx, event.New(event.ServiceReady, execution.ID, execution.CorrelationID, target.id, event.ServiceReadyPayload{Input: target.input})); err != nil {
			return err
		}
	}
	if remaining != 0 || !execution.MarkCompleted() {
		return nil
	}
	return s.bus.Publish(ctx, event.New(event.ExecutionCompleted, execution.ID, execution.CorrelationID, "", event.ExecutionCompletedPayload{
		Outputs: execution.StringKeyedOutputs(),
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
	_ = s.serviceCompleted.Close()
	_ = s.serviceFailed.Close()
}
