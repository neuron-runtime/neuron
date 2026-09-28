package scheduler

import (
	"context"
	"fmt"

	exec "github.com/Muhammad-Jay/neuron/nore/internal/execution"
	"github.com/Muhammad-Jay/neuron/nore/internal/types"

	"github.com/Muhammad-Jay/neuron/nore/internal/data"
	"github.com/Muhammad-Jay/neuron/nore/internal/resolver"
)

func buildTransitionEnvironment(execution *exec.Execution, sourceNode types.ExecutionNode, output map[string]any) resolver.Environment {
	capability := sourceNode.Capability
	return resolver.Environment{
		Source: map[string]any{
			"id": string(capability.Metadata.ID), "name": capability.Metadata.Name, "type": string(capability.Type),
			"input": data.SnakeMap(execution.Params(capability.Metadata.ID)), "output": data.SnakeMap(output),
			"metadata": map[string]any{
				"id": string(capability.Metadata.ID), "name": capability.Metadata.Name,
				"description": capability.Metadata.Description, "version": capability.Metadata.Version,
			},
		},
		Execution: map[string]any{
			"id": string(execution.ID), "correlation_id": string(execution.CorrelationID),
			"input": data.SnakeMap(execution.InitialParams()),
			"blueprint": map[string]any{
				"id": string(execution.Blueprint.Metadata.ID), "name": execution.Blueprint.Metadata.Name,
				"version": execution.Blueprint.Metadata.Version,
			},
		},
	}
}

func validateTransition(ctx context.Context, environment resolver.Environment, transition types.ExecutionTransition) error {
	for index, rule := range transition.Validations {
		if rule.Program == nil {
			return fmt.Errorf("binding %s contains an uncompiled validation %q", transition.BindingID, rule.Expression)
		}
		value, err := rule.Program.Evaluate(ctx, environment)
		if err != nil {
			return fmt.Errorf("binding %s validation %d failed to evaluate: %w", transition.BindingID, index, err)
		}
		valid, ok := value.(bool)
		if !ok {
			return fmt.Errorf("binding %s validation %q returned %T; expected bool", transition.BindingID, rule.Expression, value)
		}
		if !valid {
			return fmt.Errorf("binding %s: %s", transition.BindingID, rule.Message)
		}
	}
	return nil
}

func applyTransition(ctx context.Context, environment resolver.Environment, transition types.ExecutionTransition) (map[string]any, error) {
	// Empty mappings mean control flow only. No data is transferred.
	if len(transition.Mappings) == 0 {
		return map[string]any{}, nil
	}
	input := make(map[string]any)
	for _, mapping := range transition.Mappings {
		if mapping.Program == nil {
			return nil, fmt.Errorf("binding %s contains an uncompiled expression %q", transition.BindingID, mapping.Expression)
		}
		value, err := mapping.Program.Evaluate(ctx, environment)
		if err != nil {
			return nil, fmt.Errorf("binding %s expression %q failed: %w", transition.BindingID, mapping.Expression, err)
		}
		if err := setPath(input, mapping.TargetPath, value); err != nil {
			return nil, fmt.Errorf("binding %s could not assign target %q: %w", transition.BindingID, mapping.TargetPath, err)
		}
	}
	return input, nil
}
