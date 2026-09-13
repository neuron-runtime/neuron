package project

// This file contains the representation of user-authored YAML files.
//
// These types intentionally do not depend on N.O.R.E. core types.
// The project package is responsible only for understanding the
// developer's project definition.

//
// ------------------------------------------------------------
// System
// ------------------------------------------------------------
//

// SystemFile represents a System definition.
//
// A System is deliberately declarative. It does not contain a
// runtime execution graph. N.O.R.E.'s planner/parser layer can
// later interpret the resolved services and their relationships.
type SystemFile struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`

	Metadata SystemMetadata `yaml:"metadata"`

	// Optional indirection.
	//
	// Example:
	//
	// entry: ./systems/customer.yaml
	//
	// If Entry is present, this file acts as a reference.
	Entry string `yaml:"entry,omitempty"`

	Services []ServiceReference `yaml:"services,omitempty"`

	// Connectors define the data flow between services.
	// Can be defined inline or referenced via entry.
	Connectors []ConnectorReference `yaml:"connectors,omitempty"`

	// Arbitrary systems-level configuration.
	//
	// This intentionally remains generic because the project
	// resolver should not dictate N.O.R.E.'s internal model.
	Config map[string]any `yaml:"config,omitempty"`
}

// ConnectorValidationRule describes a validation rule for a connector transition.
type ConnectorValidationRule struct {
	Expression string `yaml:"expression"`
	Message    string `yaml:"message,omitempty"`
}

// ConnectorReference references a connector definition (inline or via entry).
type ConnectorReference struct {
	// Inline definition
	From        string                    `yaml:"from"`
	To          string                    `yaml:"to"`
	Mappings    []MappingDefinition       `yaml:"mappings,omitempty"`
	Validations []ConnectorValidationRule `yaml:"validations,omitempty"`

	// External reference
	Entry string `yaml:"entry,omitempty"`
}

// ConnectorFile represents a standalone connector definition.
type ConnectorFile struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"` // "Connector"

	Metadata ConnectorMetadata `yaml:"metadata"`

	From        string                    `yaml:"from"`
	To          string                    `yaml:"to"`
	Mappings    []MappingDefinition       `yaml:"mappings,omitempty"`
	Validations []ConnectorValidationRule `yaml:"validations,omitempty"`
}

type ConnectorMetadata struct {
	Name        string `yaml:"name"`
	Version     string `yaml:"version"`
	Description string `yaml:"description,omitempty"`
}

// SystemMetadata identifies a System.
type SystemMetadata struct {
	Name        string `yaml:"name"`
	Version     string `yaml:"version"`
	Description string `yaml:"description,omitempty"`
}

// ServiceReference references a service definition.
//
// Example:
//
// services:
//   - ref: github.read
//     entry: ./services/github/read.yaml
type ServiceReference struct {
	Ref   string `yaml:"ref"`
	Entry string `yaml:"entry,omitempty"`
}

//
// ------------------------------------------------------------
// Service
// ------------------------------------------------------------
//

// ServiceFile represents one service definition.
type ServiceFile struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`

	Metadata ServiceMetadata `yaml:"metadata"`

	// Optional indirection.
	Entry string `yaml:"entry,omitempty"`

	Spec ServiceSpec `yaml:"spec"`
}

type ServiceMetadata struct {
	Name        string `yaml:"name"`
	Version     string `yaml:"version"`
	Description string `yaml:"description,omitempty"`
}

// ServiceSpec contains everything required to describe a Service.
//
// This is the important distinction:
//
// Service
// ├── executor
// ├── config
// ├── mappings
// ├── validation
// └── execution
type ServiceSpec struct {
	Executor ExecutorSpec `yaml:"executor"`

	Config map[string]any `yaml:"config,omitempty"`

	Mappings []MappingDefinition `yaml:"mappings,omitempty"`

	Validation *ValidationConfig `yaml:"validation,omitempty"`

	Execution *ExecutionConfig `yaml:"execution,omitempty"`
}

// ExecutorSpec describes the executor required by a service.
type ExecutorSpec struct {
	// Executor type.
	//
	// Examples:
	//   http
	//   wasm
	//   process
	//   github
	//   custom
	Type string `yaml:"type"`

	// Optional executor version.
	Version string `yaml:"version,omitempty"`

	// Optional registry/source identifier.
	Source string `yaml:"source,omitempty"`

	// Optional executor-specific configuration.
	Config map[string]any `yaml:"config,omitempty"`
}

// MappingDefinition describes how data is mapped into or out of
// a Service.
//
// The actual mapping semantics belong to the N.O.R.E. parser/planner.
// The project package simply resolves and preserves the declaration.
type MappingDefinition struct {
	Name string `yaml:"name"`

	Direction string `yaml:"direction,omitempty"`

	// Source can be a service output, execution input, configuration
	// value, expression, etc.
	Source string `yaml:"source"`

	// Target identifies the service input/output field.
	Target string `yaml:"target"`

	// Optional mapping expression.
	Expression string `yaml:"expression,omitempty"`
}

// ValidationConfig describes service validation declarations.
type ValidationConfig struct {
	Input  map[string]any `yaml:"input,omitempty"`
	Output map[string]any `yaml:"output,omitempty"`
}

// ExecutionConfig contains service-level execution behavior.
type ExecutionConfig struct {
	Mode           string `yaml:"mode,omitempty"`
	Timeout        string `yaml:"timeout,omitempty"`
	Retries        int    `yaml:"retries,omitempty"`
	Concurrency    int    `yaml:"concurrency,omitempty"`
	ContinueOnFail bool   `yaml:"continueOnFail,omitempty"`
}
