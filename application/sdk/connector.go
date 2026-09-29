package sdk

import (
	"fmt"

	"github.com/neuron-runtime/neuron/shared/types/core"
)

// Binding is the developer-facing representation of a types Binding.
//
// It deliberately exposes only the operations that make sense while
// constructing a transition:
//
//   - Metadata
//   - AddMapping / AddMappings
//   - AddValidation / AddValidations
//
// Execution, compilation and validation semantics remain in N.O.R.E.
type Binding struct {
	binding core.Binding
}

func NewBinding(
	sourceID core.ID,
	targetID core.ID,
) *Binding {
	return &Binding{
		binding: core.Binding{
			Metadata: core.Metadata{
				ID: core.NewID("binding_"),
			},
			From: core.Endpoint{
				CapabilityID: sourceID,
			},
			To: core.Endpoint{
				CapabilityID: targetID,
			},
			Mappings:    make([]core.MappingRule, 0),
			Validations: make([]core.ValidationRule, 0),
		},
	}
}

// Metadata configures binding metadata.
//
// Example:
//
//	sys.Binding(a, b).
//		Metadata("customer-to-email", "Customer to Email")
func (c *Binding) Metadata(
	id, name string,
) *Binding {
	c.binding.Metadata.ID = core.ID(id)
	c.binding.Metadata.Name = name

	return c
}

func (c *Binding) Description(
	description string,
) *Binding {
	c.binding.Metadata.Description = description

	return c
}

func (c *Binding) Version(
	version string,
) *Binding {
	c.binding.Metadata.Version = version

	return c
}

// AddMapping adds one mapping.
//
// Example:
//
//	binding.AddMapping(
//		Mapping("customer.name", Expr("input.name")),
//	)
func (c *Binding) AddMapping(
	mapping core.MappingRule,
) *Binding {
	c.binding.Mappings =
		append(
			c.binding.Mappings,
			mapping,
		)

	return c
}

// AddMappings adds multiple mappings.
//
// This is the preferred API for configuration packages.
func (c *Binding) AddMappings(
	mappings ...core.MappingRule,
) *Binding {
	c.binding.Mappings =
		append(
			c.binding.Mappings,
			mappings...,
		)

	return c
}

// AddValidation adds one validation rule.
func (c *Binding) AddValidation(
	validation core.ValidationRule,
) *Binding {
	c.binding.Validations =
		append(
			c.binding.Validations,
			validation,
		)

	return c
}

// AddValidations adds multiple validation rules.
func (c *Binding) AddValidations(
	validations ...core.ValidationRule,
) *Binding {
	c.binding.Validations =
		append(
			c.binding.Validations,
			validations...,
		)

	return c
}

// Mapping creates a MappingRule.
//
// The API intentionally uses Target + Expression rather than exposing
// the types struct directly in normal developer code.
func Mapping(
	target string,
	expression string,
) core.MappingRule {
	if target == "" {
		panic("mvp: mapping target cannot be empty")
	}

	if expression == "" {
		panic("mvp: mapping expression cannot be empty")
	}

	return core.MappingRule{
		TargetPath: target,
		Expression: expression,
	}
}

// Validation creates a ValidationRule.
func Validation(
	expression string,
	message string,
) core.ValidationRule {
	if expression == "" {
		panic("mvp: validation expression cannot be empty")
	}

	return core.ValidationRule{
		Expression: expression,
		Message:    message,
	}
}

// Build exposes the underlying types Binding to package-level
// configuration and import/export tooling.
func (c *Binding) Build() core.Binding {
	if c == nil {
		panic("mvp: nil binding")
	}

	return c.binding
}

// Core is an alias for Build for callers that prefer explicit terminology.
func (c *Binding) Core() core.Binding {
	return c.Build()
}

// Source and Target are intentionally read-only developer helpers.
func (c *Binding) Source() core.ID {
	return c.binding.From.CapabilityID
}

func (c *Binding) Target() core.ID {
	return c.binding.To.CapabilityID
}

// Must ensures a binding is structurally complete before it is exported.
func (c *Binding) Must() *Binding {
	if c == nil {
		panic("mvp: binding is nil")
	}

	if c.binding.From.CapabilityID == "" {
		panic("mvp: binding source capability is required")
	}

	if c.binding.To.CapabilityID == "" {
		panic("mvp: binding target capability is required")
	}

	return c
}

func (c *Binding) String() string {
	if c == nil {
		return "<nil>"
	}

	return fmt.Sprintf(
		"%s -> %s",
		c.binding.From.CapabilityID,
		c.binding.To.CapabilityID,
	)
}
