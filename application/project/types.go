package project

// This file contains the representation of user-authored YAML files.
//
// These types intentionally do not depend on N.O.R.E. core types.
// The project package is responsible only for understanding the
// developer's project definition.

//
// ------------------------------------------------------------
// Assembly
// ------------------------------------------------------------
//

// AssemblyFile represents a Assembly definition.
//
// A Assembly is deliberately declarative. It does not contain a
// runtime execution graph. N.O.R.E.'s planner/parser layer can
// later interpret the resolved capabilities and their relationships.
type AssemblyFile struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`

	Metadata AssemblyMetadata `yaml:"metadata"`

	// Optional indirection.
	//
	// Example:
	//
	// entry: ./assemblies/customer.yaml
	//
	// If Entry is present, this file acts as a reference.
	Entry string `yaml:"entry,omitempty"`

	Capabilities []CapabilityReference `yaml:"capabilities,omitempty"`

	// Bindings define the data flow between capabilities.
	// Can be defined inline or referenced via entry.
	Bindings []BindingReference `yaml:"bindings,omitempty"`

	// Arbitrary assemblies-level configuration.
	//
	// This intentionally remains generic because the project
	// resolver should not dictate N.O.R.E.'s internal model.
	Config map[string]any `yaml:"config,omitempty"`
}

// BindingValidationRule describes a validation rule for a binding transition.
type BindingValidationRule struct {
	Expression string `yaml:"expression"`
	Message    string `yaml:"message,omitempty"`
}

// BindingReference references a binding definition (inline or via entry).
type BindingReference struct {
	// Inline definition
	From        string                  `yaml:"from"`
	To          string                  `yaml:"to"`
	Mappings    []MappingDefinition     `yaml:"mappings,omitempty"`
	Validations []BindingValidationRule `yaml:"validations,omitempty"`

	// External reference
	Entry string `yaml:"entry,omitempty"`
}

// BindingFile represents a standalone binding definition.
type BindingFile struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"` // "Binding"

	Metadata BindingMetadata `yaml:"metadata"`

	From        string                  `yaml:"from"`
	To          string                  `yaml:"to"`
	Mappings    []MappingDefinition     `yaml:"mappings,omitempty"`
	Validations []BindingValidationRule `yaml:"validations,omitempty"`
}

type BindingMetadata struct {
	Name        string `yaml:"name"`
	Version     string `yaml:"version"`
	Description string `yaml:"description,omitempty"`
}

// AssemblyMetadata identifies a Assembly.
type AssemblyMetadata struct {
	Name        string `yaml:"name"`
	Version     string `yaml:"version"`
	Description string `yaml:"description,omitempty"`
}

// CapabilityReference references a capability definition.
//
// Example:
//
// capabilities:
//   - ref: github.read
//     entry: ./capabilities/github/read.yaml
type CapabilityReference struct {
	Ref   string `yaml:"ref"`
	Entry string `yaml:"entry,omitempty"`
}

//
// ------------------------------------------------------------
// Capability
// ------------------------------------------------------------
//

// CapabilityFile represents one capability definition.
type CapabilityFile struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`

	Metadata CapabilityMetadata `yaml:"metadata"`

	// Optional indirection.
	Entry string `yaml:"entry,omitempty"`

	Spec CapabilitySpec `yaml:"spec"`
}

type CapabilityMetadata struct {
	Name        string `yaml:"name"`
	Version     string `yaml:"version"`
	Description string `yaml:"description,omitempty"`
}

// CapabilitySpec contains everything required to describe a Capability.
//
// This is the important distinction:
//
// Capability
// ├── capability runtime
// ├── config
// ├── mappings
// ├── validation
// └── execution
type CapabilitySpec struct {
	CapabilityRuntime CapabilityRuntimeSpec `yaml:"capability runtime"`

	Config map[string]any `yaml:"config,omitempty"`

	Mappings []MappingDefinition `yaml:"mappings,omitempty"`

	Validation *ValidationConfig `yaml:"validation,omitempty"`

	Execution *ExecutionConfig `yaml:"execution,omitempty"`
}

// CapabilityRuntimeSpec describes the capability runtime required by a capability.
type CapabilityRuntimeSpec struct {
	// CapabilityRuntime type.
	//
	// Examples:
	//   http
	//   wasm
	//   process
	//   github
	//   custom
	Type string `yaml:"type"`

	// Optional capability runtime version.
	Version string `yaml:"version,omitempty"`

	// Optional registry/source identifier.
	Source string `yaml:"source,omitempty"`

	// Optional capability runtime-specific configuration.
	Config map[string]any `yaml:"config,omitempty"`
}

// MappingDefinition describes how data is mapped into or out of
// a Capability.
//
// The actual mapping semantics belong to the N.O.R.E. parser/planner.
// The project package simply resolves and preserves the declaration.
type MappingDefinition struct {
	Name string `yaml:"name"`

	Direction string `yaml:"direction,omitempty"`

	// Source can be a capability output, execution input, configuration
	// value, expression, etc.
	Source string `yaml:"source"`

	// Target identifies the capability input/output field.
	Target string `yaml:"target"`

	// Optional mapping expression.
	Expression string `yaml:"expression,omitempty"`
}

// ValidationConfig describes capability validation declarations.
type ValidationConfig struct {
	Input  map[string]any `yaml:"input,omitempty"`
	Output map[string]any `yaml:"output,omitempty"`
}

// ExecutionConfig contains capability-level execution behavior.
type ExecutionConfig struct {
	Mode           string `yaml:"mode,omitempty"`
	Timeout        string `yaml:"timeout,omitempty"`
	Retries        int    `yaml:"retries,omitempty"`
	Concurrency    int    `yaml:"concurrency,omitempty"`
	ContinueOnFail bool   `yaml:"continueOnFail,omitempty"`
}
