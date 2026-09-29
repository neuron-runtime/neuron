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
}

type ExecutionNode struct {
	Capability shared.Capability

	// Configurations is compiled once during Assembly compilation and resolved
	// for each Capability execution.
	Configurations resolver.ConfigurationProgram

	Next []ExecutionTransition
}

type CompiledMapping struct {
	TargetPath string
	Expression string
	Program    resolver.Program
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
