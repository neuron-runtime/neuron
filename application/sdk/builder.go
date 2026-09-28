package sdk

import (
	"fmt"

	"github.com/Muhammad-Jay/neuron/shared/types/core"
)

type Assembly struct {
	assembly    *core.Assembly
	dslBindings []*Binding // Tracks pointers so mappings are preserved
}

func NewAssembly(metadata core.Metadata) *Assembly {
	return &Assembly{
		assembly: &core.Assembly{
			Metadata: metadata,
			Specification: core.AssemblySpec{
				Capabilities: make([]core.Capability, 0),
				Triggers:     make([]core.Trigger, 0),
				Bindings:     make([]core.Binding, 0),
			},
		},
		dslBindings: make([]*Binding, 0),
	}
}

// New creates a new assemblies.
//
// Example:
//
//	var sys = mvp.New("Customer Platform", "1.0")
func New(name, version string) *Assembly {
	return NewAssembly(core.Metadata{
		ID:      core.NewID("assembly_"),
		Name:    name,
		Version: version,
	})
}

// AddCapabilities adds a group of capabilities.
func (s *Assembly) AddCapabilities(capabilities ...core.Capability) *Assembly {
	s.assembly.Specification.Capabilities = append(
		s.assembly.Specification.Capabilities,
		capabilities...,
	)
	return s
}

// AddCapability adds one capability.
func (s *Assembly) AddCapability(capability core.Capability) *Assembly {
	return s.AddCapabilities(capability)
}

// Capabilities creates a grouped capability declaration.
func (s *Assembly) Capabilities(capabilities ...core.Capability) *Assembly {
	return s.AddCapabilities(capabilities...)
}

// Binding creates a transition between two capabilities.
func (s *Assembly) Binding(
	source core.Capability,
	target core.Capability,
) *Binding {
	binding := NewBinding(
		source.Metadata.ID,
		target.Metadata.ID,
	)

	// FIX: Track the pointer so subsequent AddMappings() calls are saved.
	s.dslBindings = append(s.dslBindings, binding)

	return binding
}

// AddBindings adds already-created types.Bindings.
func (s *Assembly) AddBindings(
	bindings ...core.Binding,
) *Assembly {
	s.assembly.Specification.Bindings = append(
		s.assembly.Specification.Bindings,
		bindings...,
	)
	return s
}

// AddBinding adds already-created single core.Binding.
func (s *Assembly) AddBinding(
	binding core.Binding,
) *Assembly {
	s.assembly.Specification.Bindings = append(
		s.assembly.Specification.Bindings,
		binding,
	)
	return s
}

// Trigger registers a Capability as a Assembly entry point.
func (s *Assembly) Trigger(capability core.Capability) *Assembly {
	s.assembly.Specification.Triggers = append(
		s.assembly.Specification.Triggers,
		core.Trigger{Capability: capability},
	)
	return s
}

// Build returns the immutable specification consumed by N.O.R.E.
func (s *Assembly) Build() *core.Assembly {
	if s == nil || s.assembly == nil {
		panic("mvp: nil assemblies")
	}

	// Compile all the tracked DSL bindings right before building.
	// This ensures all AddMappings() and AddValidations() are captured.
	var finalBindings []core.Binding
	finalBindings = append(finalBindings, s.assembly.Specification.Bindings...)
	for _, dslConn := range s.dslBindings {
		finalBindings = append(finalBindings, dslConn.Core())
	}
	s.assembly.Specification.Bindings = finalBindings

	// Clear dslBindings to make Build() safe to call multiple times
	s.dslBindings = nil

	if s.assembly.Metadata.Name == "" {
		panic("mvp: assemblies name is required")
	}
	if s.assembly.Metadata.Version == "" {
		panic("mvp: assemblies version is required")
	}
	if len(s.assembly.Specification.Capabilities) == 0 {
		panic("mvp: assemblies must contain at least one capability")
	}

	return s.assembly
}

func (s *Assembly) MustBuild() *core.Assembly {
	return s.Build()
}

// Metadata allows changing assemblies metadata without exposing core.Assembly.
func (s *Assembly) Metadata(name, version string) *Assembly {
	s.assembly.Metadata.Name = name
	s.assembly.Metadata.Version = version
	return s
}

// Description sets the Assembly description.
func (s *Assembly) Description(description string) *Assembly {
	s.assembly.Metadata.Description = description
	return s
}

// Label adds a Assembly label.
func (s *Assembly) Label(key, value string) *Assembly {
	if s.assembly.Metadata.Labels == nil {
		s.assembly.Metadata.Labels = make(map[string]string)
	}
	s.assembly.Metadata.Labels[key] = value
	return s
}

// Validate performs only DSL-level structural checks.
func (s *Assembly) Validate() error {
	if s == nil || s.assembly == nil {
		return fmt.Errorf("assemblies is nil")
	}
	if s.assembly.Metadata.Name == "" {
		return fmt.Errorf("assemblies name is required")
	}
	if s.assembly.Metadata.Version == "" {
		return fmt.Errorf("assemblies version is required")
	}
	if len(s.assembly.Specification.Capabilities) == 0 {
		return fmt.Errorf("assemblies must contain at least one capability")
	}
	return nil
}
