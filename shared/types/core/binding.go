package core

// MappingRule maps a value from the source Capability/execution context into
// a target Capability param path.
type MappingRule struct {
	TargetPath string
	Expression string
}

// ValidationRule is an optional transition assertion evaluated against
// the source Capability result and execution context.
// The expression must return bool.
type ValidationRule struct {
	Expression string
	Message    string
}

// Binding has three responsibilities only:
//  1. establish execution order;
//  2. optionally map source result into target params;
//  3. optionally assert that the transition is allowed.
type Binding struct {
	Metadata Metadata

	From Endpoint
	To   Endpoint

	Mappings    []MappingRule
	Validations []ValidationRule
}
