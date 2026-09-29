package sdk

import "github.com/neuron-runtime/neuron/shared/types/core"

// Config is a developer-owned configuration namespace.
//
// The important design decision is that configuration is data, not another
// layer in N.O.R.E. The configuration package can therefore be organized
// however the developer wants.
type Config struct{}

// Mappings is a convenience namespace for configuration packages.
var Mappings mappingConfig

type mappingConfig struct{}

// One creates one mapping.
//
// Example:
//
//	var Mappings = mvp.Mappings.One(
//		mvp.Mapping("customer.name", "source.output.name"),
//	)
func (mappingConfig) One(
	rule core.MappingRule,
) core.MappingRule {
	return rule
}

// Many creates a reusable mapping group.
func (mappingConfig) Many(
	rules ...core.MappingRule,
) []core.MappingRule {
	return rules
}

// Validations is the equivalent validation namespace.
var Validations validationConfig

type validationConfig struct{}

func (validationConfig) One(
	rule core.ValidationRule,
) core.ValidationRule {
	return rule
}

func (validationConfig) Many(
	rules ...core.ValidationRule,
) []core.ValidationRule {
	return rules
}
