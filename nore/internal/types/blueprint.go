package types

import (
	"github.com/neuron-runtime/neuron/nore/internal/resolver"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

// ExecutionBlueprint is the compiled, reusable representation of a Assembly.
// It is an in-memory runtime object and should not be serialized directly.
type ExecutionBlueprint struct {
	Metadata shared.Metadata
	Nodes    map[shared.ID]ExecutionNode

	EntryCapabilityIDs []shared.ID

	// Result is the compiled Assembly result contract and field mappings.
	// It is derived from assembly.Result during compilation and used by the
	// scheduler to produce the final Assembly result object at completion.
	Result *ExecutionResult

	// Detached holds the execution scope each detached capability owns,
	// compiled once when the assembly was compiled.
	//
	// Detach is a lifecycle boundary, not a severed dependency: the detached
	// capability keeps its bindings, contracts, and failure semantics, but the
	// work it and everything downstream of it represents is handed to a
	// separately tracked execution that may outlive its caller. Compiling those
	// scopes here means splitting an execution costs no graph work at runtime
	// and the registered assembly is never modified.
	//
	// Keys are detached capability IDs; each scope's entry capability is the key
	// itself. Nesting is expressed by a scope's own Detached map, so a detached
	// capability inside another detached scope owns a further scope of its own.
	Detached map[shared.ID]*ExecutionBlueprint
}

// ExecutionResult is the compiled Assembly result contract and field mappings.
// It mirrors core.AssemblyResult but is defined here to avoid circular imports
// and to be the planner's canonical representation.
type ExecutionResult struct {
	Schema []shared.Port
	Fields []ExecutionResultField
}

// ExecutionResultField maps one declared Assembly result field to the value
// that must produce it at execution time. The Source is a structured ValueRef
// resolved at plan time.
type ExecutionResultField struct {
	Name   string
	Source *shared.ValueRef
}

type ExecutionNode struct {
	Capability shared.Capability

	// Configurations is compiled once during Assembly compilation and resolved
	// for each Capability execution.
	Configurations resolver.ConfigurationProgram

	Next []ExecutionTransition
}

// CompiledMapping is a structurally resolved binding mapping: the planner
// resolves a mapping's authored source into a canonical ValueRef, and the
// scheduler reads the referenced value out of the transition environment at
// execution time. Mappings are never evaluated as programs.
type CompiledMapping struct {
	TargetPath string
	Source     shared.ValueRef
}

type CompiledValidation struct {
	Expression string
	Message    string
	Program    resolver.Program
}

type ExecutionTransition struct {
	BindingID          shared.ID
	TargetCapabilityID shared.ID

	Mappings    []CompiledMapping
	Validations []CompiledValidation
}
