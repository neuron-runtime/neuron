package sdk

import "github.com/Muhammad-Jay/neuron/shared/types/core"

// Capability creates a developer-facing Capability declaration.
//
// This does not replace core.Capability. It is simply an ergonomic constructor
// that keeps application code readable.
func Capability(
	id string,
	capabilityRuntimeType core.CapabilityRuntimeType,
) core.Capability {
	return core.Capability{
		Metadata: core.Metadata{
			ID:   core.ID(id),
			Name: id,
		},
		Type:    capabilityRuntimeType,
		Params:  make([]core.Port, 0),
		Results: make([]core.Port, 0),
		CapabilityConfigurations: make(
			core.CapabilityConfigurations,
		),
	}
}

// NamedCapability creates a Capability with a human-readable name.
func NamedCapability(
	id string,
	name string,
	capabilityRuntimeType core.CapabilityRuntimeType,
) core.Capability {
	capability := Capability(id, capabilityRuntimeType)
	capability.Metadata.Name = name
	return capability
}

// Input creates a capability input declaration.
func Input(
	name string,
	valueType core.ValueType,
	required bool,
) core.Port {
	return core.Port{
		Name:     name,
		Type:     valueType,
		Required: required,
	}
}

// Output creates a capability output declaration.
func Output(
	name string,
	valueType core.ValueType,
) core.Port {
	return core.Port{
		Name: name,
		Type: valueType,
	}
}
