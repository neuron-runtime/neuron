package project

import "errors"

// ImplicitExecutorRoot is the canonical project-scoped directory for
// locally-authored executors. It is always a local executor search root, so
// executors placed there resolve without any registry configuration.
const ImplicitExecutorRoot = "neuron/executors"

var (
	ErrInvalidSystem     = errors.New("invalid systems definition")
	ErrInvalidService    = errors.New("invalid service definition")
	ErrInvalidConnector  = errors.New("invalid connector definition")
	ErrCircularReference = errors.New("circular project reference")
	ErrNotRegistered     = errors.New("project is not registered")
	ErrNotBuilt          = errors.New("project has not been built")
)
