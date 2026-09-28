package project

import "errors"

// ImplicitCapabilityRuntimeRoot is the canonical project-scoped directory for
// locally-authored capability runtimes. It is always a local capability runtime search root, so
// capability runtimes placed there resolve without any registry configuration.
const ImplicitCapabilityRuntimeRoot = "neuron/capabilityRuntimes"

var (
	ErrInvalidAssembly   = errors.New("invalid assemblies definition")
	ErrInvalidCapability = errors.New("invalid capability definition")
	ErrInvalidBinding    = errors.New("invalid binding definition")
	ErrCircularReference = errors.New("circular project reference")
	ErrNotRegistered     = errors.New("project is not registered")
	ErrNotBuilt          = errors.New("project has not been built")
)
