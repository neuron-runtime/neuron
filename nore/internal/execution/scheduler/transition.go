package scheduler

import (
	"context"
	"fmt"

	exec "github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/types"

	"github.com/neuron-runtime/neuron/nore/internal/data"
	"github.com/neuron-runtime/neuron/nore/internal/resolver"
	core "github.com/neuron-runtime/neuron/shared/types/core"
)

func buildTransitionEnvironment(execution *exec.Execution, sourceNode types.ExecutionNode, output map[string]any) resolver.Environment {
	capability := sourceNode.Capability
	return resolver.Environment{
		Source: map[string]any{
			"id": string(capability.Metadata.ID), "name": capability.Metadata.Name, "type": string(capability.Type),
			"params": data.SnakeMap(execution.Params(capability.Metadata.ID)), "result": data.SnakeMap(output),
			"metadata": map[string]any{
				"id": string(capability.Metadata.ID), "name": capability.Metadata.Name,
				"description": capability.Metadata.Description, "version": capability.Metadata.Version,
			},
		},
		Execution: map[string]any{
			"id": string(execution.ID), "correlation_id": string(execution.CorrelationID),
			"params": data.SnakeMap(execution.InitialParams()),
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
		value, err := resolveReference(environment, mapping.Source)
		if err != nil {
			return nil, fmt.Errorf("binding %s mapping %q: %w", transition.BindingID, mapping.TargetPath, err)
		}
		if err := setPath(input, mapping.TargetPath, value); err != nil {
			return nil, fmt.Errorf("binding %s could not assign target %q: %w", transition.BindingID, mapping.TargetPath, err)
		}
	}
	return input, nil
}

// resolveReference reads the value a structural mapping reference points at.
// The reference is already frozen into the plan by the planner; here only the
// payload the environment exposes for that source is walked.
func resolveReference(environment resolver.Environment, source core.ValueRef) (any, error) {
	switch source.Kind {
	case core.ValueRefCapabilityResult:
		if err := matchSourceIdentity(environment, source); err != nil {
			return nil, err
		}
		return pathValue(environment.Source["result"], source.Path, source)
	case core.ValueRefCapabilityParams:
		if err := matchSourceIdentity(environment, source); err != nil {
			return nil, err
		}
		return pathValue(environment.Source["params"], source.Path, source)
	case core.ValueRefAssemblyParams:
		return pathValue(environment.Execution["params"], source.Path, source)
	case core.ValueRefLiteral:
		return source.Value, nil
	case core.ValueRefVariable:
		// The structured union reserves this kind for named project variables,
		// but no variable provider is registered with the runtime yet. Fail with
		// a precise boundary error rather than inventing a source for it.
		return nil, fmt.Errorf("%s references are not resolvable: no variable provider is registered", source.Kind)
	default:
		return nil, fmt.Errorf("unknown reference kind %q", source.Kind)
	}
}

// matchSourceIdentity guards against a capability-scoped reference that names a
// Capability other than the one whose environment is being read. The planner
// already enforces this when it freezes a reference, so this is defense in
// depth for plans that were never compiled (tests, hand-built blueprints).
func matchSourceIdentity(environment resolver.Environment, source core.ValueRef) error {
	sourceID, ok := environment.Source["id"].(string)
	if !ok || sourceID != source.Capability {
		return fmt.Errorf("%s reference names capability %q but the environment belongs to %q", source.Kind, source.Capability, sourceID)
	}
	return nil
}

// pathValue walks a ValueRef path into a payload map. A missing key or a
// non-object intermediate reports an error with the reference rendered, so a
// failed binding names the exact field that was absent.
func pathValue(payload any, path []string, source core.ValueRef) (any, error) {
	if len(path) == 0 {
		return payload, nil
	}
	current := payload
	for _, segment := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s could not read %q: %s is not an object", source.Kind, source.String(), currentType(current))
		}
		var exists bool
		current, exists = object[segment]
		if !exists {
			return nil, fmt.Errorf("%s %q has no field %q", source.Kind, source.String(), segment)
		}
	}
	return current, nil
}

func currentType(value any) string {
	if value == nil {
		return "null"
	}
	return fmt.Sprintf("%T", value)
}
