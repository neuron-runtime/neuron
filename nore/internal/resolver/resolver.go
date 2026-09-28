package resolver

import "context"

// Environment is available to Binding mapping and validation expressions.
type Environment struct {
	Source    map[string]any
	Execution map[string]any
}

// CapabilityEnvironment is available to Capability configuration templates.
type CapabilityEnvironment struct {
	Params     map[string]any
	Execution  map[string]any
	Capability map[string]any
}

// Program is a compiled Binding mapping/validation expression.
type Program interface {
	Evaluate(ctx context.Context, environment Environment) (any, error)
	Expression() string
}

// ConfigurationProgram is a recursively compiled Capability configuration map.
type ConfigurationProgram interface {
	Resolve(ctx context.Context, environment CapabilityEnvironment) (map[string]any, error)
}

// Compiler compiles both Binding expressions and Capability configuration templates.
type Compiler interface {
	CompileTransitionExpression(expression string) (Program, error)
	CompileCapabilityConfigurations(config map[string]any) (ConfigurationProgram, error)
}
