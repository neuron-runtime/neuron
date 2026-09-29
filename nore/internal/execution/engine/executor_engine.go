package engine

import (
	"context"
	"fmt"

	"sync"

	exec "github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/shared/types/core"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
	"github.com/neuron-runtime/neuron/nore/internal/event"
	"github.com/neuron-runtime/neuron/nore/internal/resolver"
)

type CapabilityRuntimeEngine struct {
	bus        contracts.EventBus
	registry   contracts.CapabilityRuntimeRegistry
	executions contracts.ExecutionRepository
	ready      event.Subscription
	semaphore  chan struct{}
}

func NewCapabilityRuntimeEngine(bus contracts.EventBus, registry contracts.CapabilityRuntimeRegistry, executions contracts.ExecutionRepository, maxConcurrency int) (*CapabilityRuntimeEngine, error) {
	if maxConcurrency <= 0 {
		maxConcurrency = 8
	}
	ready, err := bus.Subscribe(event.CapabilityReady, maxConcurrency*4)
	if err != nil {
		return nil, err
	}
	return &CapabilityRuntimeEngine{bus: bus, registry: registry, executions: executions, ready: ready, semaphore: make(chan struct{}, maxConcurrency)}, nil
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
	output, err := cr.Execute(ctx, contracts.ExecutionContext{
		ExecutionID: execution.ID, CorrelationID: execution.CorrelationID,
		Capability: node.Capability, Params: input, CapabilityConfigurations: resolvedConfig,
		Logger: newExecLogger(e.bus, execution.ID, execution.CorrelationID, capabilityID),
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

func (e *CapabilityRuntimeEngine) publishFailure(ctx context.Context, execution *exec.Execution, capabilityID core.ID, err error) {
	execution.MarkCapabilityFailed(capabilityID, err)
	_ = e.bus.Publish(ctx, event.New(event.CapabilityFailed, execution.ID, execution.CorrelationID, capabilityID, event.CapabilityFailedPayload{Message: err.Error()}))
}
