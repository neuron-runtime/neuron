package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// Options controls project resolution.
//
// These options are intentionally project-level options.
// Runtime/N.O.R.E. options belong elsewhere.
type Options struct {
	// ProjectRoot is the project directory.
	//
	// If empty, the current working directory is used.
	ProjectRoot string

	// Entry is the system source file for YAML projects. It may be an
	// absolute path or one relative to the project root. An empty value
	// selects the default <root>/system.yaml.
	Entry string

	// Validate controls basic source validation.
	Validate bool

	// Verbose enables diagnostic information for callers that
	// want to expose resolution details.
	//
	// The project package does not print anything itself.
	Verbose bool
}

// DefaultOptions returns sensible MVP defaults.
func DefaultOptions() Options {
	return Options{
		Validate: true,
	}
}

// Result contains the result of source resolution.
type Result struct {
	Project *ResolvedProject
}

// Resolve reads and resolves the project source.
//
// It does not load .neuron/resolved.
// It does not parse into core.System.
// It does not start N.O.R.E.
// It does not install executors.
func Resolve(
	ctx context.Context,
	opts Options,
) (*Result, error) {

	if ctx == nil {
		ctx = context.Background()
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	root := opts.ProjectRoot

	if root == "" {
		var err error

		root, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf(
				"get current directory: %w",
				err,
			)
		}
	}

	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf(
			"resolve project root: %w",
			err,
		)
	}

	root = filepath.Clean(root)

	resolver, err := NewResolver(root)
	if err != nil {
		return nil, err
	}

	resolved, err := resolver.ResolveSystem(opts.Entry)
	if err != nil {
		return nil, err
	}

	return &Result{
		Project: resolved,
	}, nil
}
