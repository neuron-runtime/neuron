package planner

import (
	"fmt"
	"sort"
	"strings"

	"github.com/neuron-runtime/neuron/nore/internal/resolver"
	"github.com/neuron-runtime/neuron/nore/internal/runtimeconfig"
	"github.com/neuron-runtime/neuron/nore/internal/types"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

type Compiler struct {
	expressions resolver.Compiler
	assembly    *shared.Assembly
}

func NewCompiler(expressions resolver.Compiler) (*Compiler, error) {
	if expressions == nil {
		return nil, fmt.Errorf("expression compiler is required")
	}
	return &Compiler{expressions: expressions}, nil
}

// Compile compiles an Assembly into an ExecutionBlueprint. The assembly
// reference is stored for use during result compilation.
func (c *Compiler) Compile(assembly shared.Assembly) (*types.ExecutionBlueprint, error) {
	c.assembly = &assembly
	defer func() { c.assembly = nil }()

	capabilities := make(map[shared.ID]shared.Capability)
	triggerIDs := make([]shared.ID, 0, len(assembly.Specification.Triggers))

	for _, trigger := range assembly.Specification.Triggers {
		capability, err := resolveRuntimeConfig(cloneCapability(trigger.Capability))
		if err != nil {
			return nil, err
		}
		if err := addCapability(capabilities, capability); err != nil {
			return nil, err
		}
		triggerIDs = append(triggerIDs, capability.Metadata.ID)
	}
	for _, capability := range assembly.Specification.Capabilities {
		resolved, err := resolveRuntimeConfig(cloneCapability(capability))
		if err != nil {
			return nil, err
		}
		if err := addCapability(capabilities, resolved); err != nil {
			return nil, err
		}
	}
	if len(capabilities) == 0 {
		return nil, fmt.Errorf("assemblies must contain at least one capability")
	}

	nodes := make(map[shared.ID]types.ExecutionNode, len(capabilities))
	incoming := make(map[shared.ID]int, len(capabilities))
	predecessors := make(map[shared.ID][]types.ExecutionPredecessor, len(capabilities))
	joinPolicies := make(map[shared.ID]shared.JoinPolicy, len(capabilities))
	targetPaths := make(map[shared.ID]map[string]shared.ID, len(capabilities))
	for id, capability := range capabilities {
		compiledConfig, err := c.expressions.CompileCapabilityConfigurations(capability.CapabilityConfigurations)
		if err != nil {
			return nil, fmt.Errorf("compile configurations for capability %s: %w", id, err)
		}
		// A capability with no incoming bindings is an entry point; every
		// capability otherwise joins its predecessors under the same default
		// policy until an explicit declaration overrides it below.
		nodes[id] = types.ExecutionNode{
			Capability:     capability,
			Configurations: compiledConfig,
			JoinPolicy:     shared.JoinPolicyAll,
		}
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

		joinPolicy := binding.JoinPolicy.Effective()
		if err := joinPolicy.Validate(); err != nil {
			return nil, fmt.Errorf("binding %s: %w", binding.Metadata.ID, err)
		}
		if existing, exists := joinPolicies[binding.To.CapabilityID]; exists && existing != joinPolicy {
			return nil, fmt.Errorf(
				"capability %s has conflicting join policies %q and %q",
				binding.To.CapabilityID, existing, joinPolicy)
		}
		joinPolicies[binding.To.CapabilityID] = joinPolicy

		incoming[binding.To.CapabilityID]++
		predecessors[binding.To.CapabilityID] = append(predecessors[binding.To.CapabilityID], types.ExecutionPredecessor{
			BindingID:    binding.Metadata.ID,
			CapabilityID: binding.From.CapabilityID,
		})

		transition, err := c.compileTransition(binding)
		if err != nil {
			return nil, err
		}
		// R3-4: Two bindings feeding the same capability must not write the same
		// target path. Fan-in merges predecessor outputs before dispatch, but a
		// shared path would make the merged value depend on binding order.
		paths := targetPaths[binding.To.CapabilityID]
		if paths == nil {
			paths = make(map[string]shared.ID)
			targetPaths[binding.To.CapabilityID] = paths
		}
		for _, mapping := range transition.Mappings {
			if existingBindingID, exists := paths[mapping.TargetPath]; exists {
				return nil, fmt.Errorf(
					"capability %s has conflicting target path %q between bindings %s and %s",
					binding.To.CapabilityID, mapping.TargetPath, existingBindingID, binding.Metadata.ID)
			}
			paths[mapping.TargetPath] = binding.Metadata.ID
		}
		node := nodes[binding.From.CapabilityID]
		node.Next = append(node.Next, transition)
		nodes[binding.From.CapabilityID] = node
	}

	for capabilityID, capabilityPredecessors := range predecessors {
		// Binding IDs are the stable edge identity. Sorting here means readiness
		// and merged inputs are deterministic regardless of map iteration or the
		// order the bindings were declared in.
		sort.Slice(capabilityPredecessors, func(i, j int) bool {
			if capabilityPredecessors[i].BindingID != capabilityPredecessors[j].BindingID {
				return capabilityPredecessors[i].BindingID < capabilityPredecessors[j].BindingID
			}
			return capabilityPredecessors[i].CapabilityID < capabilityPredecessors[j].CapabilityID
		})
		node := nodes[capabilityID]
		node.Predecessors = capabilityPredecessors
		node.JoinPolicy = joinPolicies[capabilityID]
		nodes[capabilityID] = node
	}

	// R3-16: A detached capability must not participate in a fan-in (multiple
	// incoming bindings). Detached branches run in a separate execution that the
	// parent does not wait for; a join spanning detached and non-detached work
	// would create ambiguous ownership. Reject such configurations at build time.
	for capabilityID, count := range incoming {
		if count > 1 {
			n, ex := nodes[capabilityID]
			if ex && n.Capability.RuntimeConfig != nil && n.Capability.RuntimeConfig.Execution != nil && n.Capability.RuntimeConfig.Execution.Mode == shared.RuntimeExecutionModeDetach {
				return nil, fmt.Errorf(
					"capability %s is detached and has %d incoming bindings (fan-in); "+
						"a detached branch may not participate in a join; "+
						"use an aggregation capability or restructure the graph",
					capabilityID, count)
			}
		}
	}

	// R3-16 extended: reject any join where a predecessor comes from a detached scope.
	// A detached branch runs in a separate execution that the parent never waits for;
	// if such a predecessor feeds into a join, the join can never become satisfied.
	for _, binding := range assembly.Specification.Bindings {
		fromCapability, fromExists := capabilities[binding.From.CapabilityID]
		if !fromExists {
			continue // already errored above
		}
		if fromCapability.RuntimeConfig != nil && fromCapability.RuntimeConfig.Execution != nil && fromCapability.RuntimeConfig.Execution.Mode == shared.RuntimeExecutionModeDetach {
			return nil, fmt.Errorf(
				"capability %s has a predecessor %s from a detached scope; "+
					"a detached branch may not participate in a join; "+
					"use an aggregation capability or restructure the graph",
				binding.To.CapabilityID, binding.From.CapabilityID)
		}
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

	// Compile the Assembly result contract and field mappings.
	result, err := c.compileAssemblyResult(assembly.Result)
	if err != nil {
		return nil, err
	}
	// A result may only read values guaranteed to complete inside the root
	// execution. Detached branches are excluded because the parent never waits
	// for them, so a reference into one could never resolve.
	if err := validateResultDetachment(result, nodes); err != nil {
		return nil, err
	}

	detached, err := buildDetachedScopes(nodes, metadata)
	if err != nil {
		return nil, err
	}
	return &types.ExecutionBlueprint{
		Metadata:           metadata,
		Nodes:              nodes,
		EntryCapabilityIDs: entryIDs,
		Result:             result,
		Detached:           detached,
	}, nil
}

func (c *Compiler) compileTransition(binding shared.Binding) (types.ExecutionTransition, error) {
	compiledMappings := make([]types.CompiledMapping, 0, len(binding.Mappings))
	targets := make(map[string]struct{}, len(binding.Mappings))
	for index, mapping := range binding.Mappings {
		targetPath := strings.TrimSpace(mapping.TargetPath)
		if targetPath == "" {
			return types.ExecutionTransition{}, fmt.Errorf("binding %s mapping %d has no target path", binding.Metadata.ID, index)
		}
		if _, exists := targets[targetPath]; exists {
			return types.ExecutionTransition{}, fmt.Errorf("binding %s contains duplicate target path %q", binding.Metadata.ID, targetPath)
		}
		targets[targetPath] = struct{}{}
		// A mapping loaded from a pre-structure artifact still carries the
		// legacy expression string; resolving it here freezes the reference into
		// the plan so the scheduler never parses.
		source, err := mapping.SourceRef(string(binding.From.CapabilityID))
		if err != nil {
			return types.ExecutionTransition{}, fmt.Errorf("binding %s mapping %q: %w", binding.Metadata.ID, targetPath, err)
		}
		if err := source.Validate(); err != nil {
			return types.ExecutionTransition{}, fmt.Errorf("binding %s mapping %q: %w", binding.Metadata.ID, targetPath, err)
		}
		compiledMappings = append(compiledMappings, types.CompiledMapping{TargetPath: targetPath, Source: source})
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

// resolveRuntimeConfig validates what an author declared and replaces it with the
// effective configuration N.O.R.E. will act on, filling in a default for
// anything the author left unset.
//
// Validation happens here rather than only at authoring time so that a
// malformed declaration fails registration even when it reaches the compiler
// through a path that never called the authoring loader. Without it, N.O.R.E.
// would carry a declaration it cannot honor — an unparsable timeout, an unknown
// retry policy — until the first invocation of that capability.
//
// This runs once per execution plan, so the defaults cost nothing per
// invocation, and it operates on the plan's own copy of the capability. The
// registered assembly stays exactly as it was authored.
func resolveRuntimeConfig(capability shared.Capability) (shared.Capability, error) {
	if err := capability.RuntimeConfig.Validate(); err != nil {
		return shared.Capability{}, fmt.Errorf("capability %s: %w", capability.Metadata.ID, err)
	}
	capability.RuntimeConfig = runtimeconfig.Resolve(capability.RuntimeConfig)
	return capability, nil
}

func cloneCapability(capability shared.Capability) shared.Capability {
	capability.Metadata = cloneMetadata(capability.Metadata)
	capability.CapabilityConfigurations = cloneConfiguration(capability.CapabilityConfigurations)
	capability.RuntimeConfig = capability.RuntimeConfig.Clone()
	capability.Params = append([]shared.Port(nil), capability.Params...)
	capability.Results = append([]shared.Port(nil), capability.Results...)
	return capability
}

func cloneBinding(binding shared.Binding) shared.Binding {
	binding.Metadata = cloneMetadata(binding.Metadata)
	if binding.Metadata.ID == "" {
		binding.Metadata.ID = shared.NewID("binding_")
	}
	mappings := make([]shared.MappingRule, len(binding.Mappings))
	for index, mapping := range binding.Mappings {
		mapping.Source = mapping.Source.Clone()
		mappings[index] = mapping
	}
	binding.Mappings = mappings
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

// compileAssemblyResult compiles the Assembly result contract and field mappings
// into the planner's canonical ExecutionResult representation. It validates that
// every field's source reference is resolvable against the Assembly's capabilities
// and that referenced capability result ports exist.
func (c *Compiler) compileAssemblyResult(assemblyResult *shared.AssemblyResult) (*types.ExecutionResult, error) {
	if assemblyResult == nil {
		return nil, nil
	}

	result := &types.ExecutionResult{
		Schema: make([]shared.Port, len(assemblyResult.Schema)),
		Fields: make([]types.ExecutionResultField, len(assemblyResult.Fields)),
	}
	copy(result.Schema, assemblyResult.Schema)

	// Build a set of all capability IDs in the assembly for validation.
	capabilityIDs := make(map[shared.ID]struct{})
	for _, trigger := range c.assembly.Specification.Triggers {
		capabilityIDs[trigger.Capability.Metadata.ID] = struct{}{}
	}
	for _, capability := range c.assembly.Specification.Capabilities {
		capabilityIDs[capability.Metadata.ID] = struct{}{}
	}

	for i, field := range assemblyResult.Fields {
		if field.Source == nil {
			return nil, fmt.Errorf("assembly result field %q has no source", field.Name)
		}
		if err := field.Source.Validate(); err != nil {
			return nil, fmt.Errorf("assembly result field %q: %w", field.Name, err)
		}
		// Validate that capability-scoped references point to existing capabilities.
		if field.Source.Kind == shared.ValueRefCapabilityResult {
			if _, ok := capabilityIDs[shared.ID(field.Source.Capability)]; !ok {
				return nil, fmt.Errorf("assembly result field %q references unknown capability %q", field.Name, field.Source.Capability)
			}
			// Port existence is validated once, in the build compiler, against
			// the manifest every authoring surface produces. Repeating it here
			// would make two authorities for the same rule.
		}
		result.Fields[i] = types.ExecutionResultField{
			Name:   field.Name,
			Source: field.Source,
		}
	}

	return result, nil
}
