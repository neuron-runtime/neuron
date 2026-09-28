package planner

import (
	"fmt"
	"strings"

	"github.com/Muhammad-Jay/neuron/nore/internal/resolver"
	"github.com/Muhammad-Jay/neuron/nore/internal/types"
	shared "github.com/Muhammad-Jay/neuron/shared/types/core"
)

type Compiler struct {
	expressions resolver.Compiler
}

func NewCompiler(expressions resolver.Compiler) (*Compiler, error) {
	if expressions == nil {
		return nil, fmt.Errorf("expression compiler is required")
	}
	return &Compiler{expressions: expressions}, nil
}

func (c *Compiler) Compile(assembly shared.Assembly) (*types.ExecutionBlueprint, error) {
	capabilities := make(map[shared.ID]shared.Capability)
	triggerIDs := make([]shared.ID, 0, len(assembly.Specification.Triggers))

	for _, trigger := range assembly.Specification.Triggers {
		capability := cloneCapability(trigger.Capability)
		if err := addCapability(capabilities, capability); err != nil {
			return nil, err
		}
		triggerIDs = append(triggerIDs, capability.Metadata.ID)
	}
	for _, capability := range assembly.Specification.Capabilities {
		if err := addCapability(capabilities, cloneCapability(capability)); err != nil {
			return nil, err
		}
	}
	if len(capabilities) == 0 {
		return nil, fmt.Errorf("assemblies must contain at least one capability")
	}

	nodes := make(map[shared.ID]types.ExecutionNode, len(capabilities))
	incoming := make(map[shared.ID]int, len(capabilities))
	for id, capability := range capabilities {
		compiledConfig, err := c.expressions.CompileCapabilityConfigurations(capability.CapabilityConfigurations)
		if err != nil {
			return nil, fmt.Errorf("compile configurations for capability %s: %w", id, err)
		}
		nodes[id] = types.ExecutionNode{Capability: capability, Configurations: compiledConfig}
		incoming[id] = 0
	}

	for _, original := range assembly.Specification.Bindings {
		binding := cloneBinding(original)
		fromCapability, fromExists := capabilities[binding.From.CapabilityID]
		if !fromExists {
			return nil, fmt.Errorf("binding %s references missing source capability %s", binding.Metadata.ID, binding.From.CapabilityID)
		}
		toCapability, toExists := capabilities[binding.To.CapabilityID]
		if !toExists {
			return nil, fmt.Errorf("binding %s references missing target capability %s", binding.Metadata.ID, binding.To.CapabilityID)
		}
		if binding.From.Port != "" && !hasPort(fromCapability.Results, binding.From.Port) {
			return nil, fmt.Errorf("source output port %q does not exist on capability %s", binding.From.Port, fromCapability.Metadata.ID)
		}
		if binding.To.Port != "" && !hasPort(toCapability.Params, binding.To.Port) {
			return nil, fmt.Errorf("target input port %q does not exist on capability %s", binding.To.Port, toCapability.Metadata.ID)
		}

		incoming[binding.To.CapabilityID]++
		if incoming[binding.To.CapabilityID] > 1 {
			return nil, fmt.Errorf("capability %s has multiple incoming bindings; use an aggregation capability", binding.To.CapabilityID)
		}

		transition, err := c.compileTransition(binding)
		if err != nil {
			return nil, err
		}
		node := nodes[binding.From.CapabilityID]
		node.Next = append(node.Next, transition)
		nodes[binding.From.CapabilityID] = node
	}

	entryIDs := triggerIDs
	if len(entryIDs) == 0 {
		for capabilityID, count := range incoming {
			if count == 0 {
				entryIDs = append(entryIDs, capabilityID)
			}
		}
	}
	if len(entryIDs) == 0 {
		return nil, fmt.Errorf("assemblies has no entry capability")
	}
	if err := validateAcyclic(nodes, incoming); err != nil {
		return nil, err
	}
	if err := validateReachability(nodes, entryIDs); err != nil {
		return nil, err
	}

	metadata := cloneMetadata(assembly.Metadata)
	if metadata.ID == "" {
		metadata.ID = shared.NewID("blueprint_")
	}
	if metadata.Version == "" {
		metadata.Version = "1"
	}
	return &types.ExecutionBlueprint{Metadata: metadata, Nodes: nodes, EntryCapabilityIDs: entryIDs}, nil
}

func (c *Compiler) compileTransition(binding shared.Binding) (types.ExecutionTransition, error) {
	compiledMappings := make([]types.CompiledMapping, 0, len(binding.Mappings))
	targets := make(map[string]struct{}, len(binding.Mappings))
	for index, mapping := range binding.Mappings {
		targetPath := strings.TrimSpace(mapping.TargetPath)
		expression := strings.TrimSpace(mapping.Expression)
		if targetPath == "" {
			return types.ExecutionTransition{}, fmt.Errorf("binding %s mapping %d has no target path", binding.Metadata.ID, index)
		}
		if expression == "" {
			return types.ExecutionTransition{}, fmt.Errorf("binding %s mapping %q has no expression", binding.Metadata.ID, targetPath)
		}
		if _, exists := targets[targetPath]; exists {
			return types.ExecutionTransition{}, fmt.Errorf("binding %s contains duplicate target path %q", binding.Metadata.ID, targetPath)
		}
		targets[targetPath] = struct{}{}
		program, err := c.expressions.CompileTransitionExpression(expression)
		if err != nil {
			return types.ExecutionTransition{}, fmt.Errorf("binding %s mapping %q: %w", binding.Metadata.ID, targetPath, err)
		}
		compiledMappings = append(compiledMappings, types.CompiledMapping{TargetPath: targetPath, Expression: expression, Program: program})
	}

	compiledValidations := make([]types.CompiledValidation, 0, len(binding.Validations))
	for index, rule := range binding.Validations {
		expression := strings.TrimSpace(rule.Expression)
		if expression == "" {
			return types.ExecutionTransition{}, fmt.Errorf("binding %s validation %d has no expression", binding.Metadata.ID, index)
		}
		program, err := c.expressions.CompileTransitionExpression(expression)
		if err != nil {
			return types.ExecutionTransition{}, fmt.Errorf("binding %s validation %d: %w", binding.Metadata.ID, index, err)
		}
		message := strings.TrimSpace(rule.Message)
		if message == "" {
			message = "binding validation failed"
		}
		compiledValidations = append(compiledValidations, types.CompiledValidation{Expression: expression, Message: message, Program: program})
	}

	return types.ExecutionTransition{
		BindingID: binding.Metadata.ID, TargetCapabilityID: binding.To.CapabilityID,
		Mappings: compiledMappings, Validations: compiledValidations,
	}, nil
}

func addCapability(capabilities map[shared.ID]shared.Capability, capability shared.Capability) error {
	if capability.Metadata.ID == "" {
		return fmt.Errorf("capability ID is required")
	}
	if capability.Type == "" {
		return fmt.Errorf("capability %s has no capability type", capability.Metadata.ID)
	}
	if _, exists := capabilities[capability.Metadata.ID]; exists {
		return fmt.Errorf("duplicate capability ID %s", capability.Metadata.ID)
	}
	capabilities[capability.Metadata.ID] = capability
	return nil
}

func hasPort(ports []shared.Port, name string) bool {
	for _, port := range ports {
		if port.Name == name {
			return true
		}
	}
	return false
}

func validateAcyclic(nodes map[shared.ID]types.ExecutionNode, incoming map[shared.ID]int) error {
	counts := make(map[shared.ID]int, len(incoming))
	queue := make([]shared.ID, 0, len(nodes))
	for id, count := range incoming {
		counts[id] = count
		if count == 0 {
			queue = append(queue, id)
		}
	}
	visited := 0
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		visited++
		for _, transition := range nodes[current].Next {
			target := transition.TargetCapabilityID
			counts[target]--
			if counts[target] == 0 {
				queue = append(queue, target)
			}
		}
	}
	if visited != len(nodes) {
		return fmt.Errorf("assemblies contains a binding cycle")
	}
	return nil
}

func validateReachability(nodes map[shared.ID]types.ExecutionNode, entries []shared.ID) error {
	visited := make(map[shared.ID]bool, len(nodes))
	queue := append([]shared.ID(nil), entries...)
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if visited[current] {
			continue
		}
		visited[current] = true
		for _, transition := range nodes[current].Next {
			queue = append(queue, transition.TargetCapabilityID)
		}
	}
	if len(visited) != len(nodes) {
		return fmt.Errorf("assemblies contains unreachable capabilities")
	}
	return nil
}

func cloneMetadata(metadata shared.Metadata) shared.Metadata {
	labels := make(map[string]string, len(metadata.Labels))
	for key, value := range metadata.Labels {
		labels[key] = value
	}
	metadata.Labels = labels
	return metadata
}

func cloneCapability(capability shared.Capability) shared.Capability {
	capability.Metadata = cloneMetadata(capability.Metadata)
	capability.CapabilityConfigurations = cloneConfiguration(capability.CapabilityConfigurations)
	capability.Params = append([]shared.Port(nil), capability.Params...)
	capability.Results = append([]shared.Port(nil), capability.Results...)
	return capability
}

func cloneBinding(binding shared.Binding) shared.Binding {
	binding.Metadata = cloneMetadata(binding.Metadata)
	if binding.Metadata.ID == "" {
		binding.Metadata.ID = shared.NewID("binding_")
	}
	binding.Mappings = append([]shared.MappingRule(nil), binding.Mappings...)
	binding.Validations = append([]shared.ValidationRule(nil), binding.Validations...)
	return binding
}

func cloneConfiguration(source shared.CapabilityConfigurations) shared.CapabilityConfigurations {
	result := make(shared.CapabilityConfigurations, len(source))
	for key, value := range source {
		result[key] = cloneConfigurationValue(value)
	}
	return result
}

func cloneConfigurationValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			result[key] = cloneConfigurationValue(item)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = cloneConfigurationValue(item)
		}
		return result
	default:
		return typed
	}
}
