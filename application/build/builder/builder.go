// Package builder defines the contract for project frontends. It lives below
// the build registry package so individual builders can import it without
// creating an import cycle.
package builder

import (
	"context"
	"io"

	"github.com/Muhammad-Jay/neuron/application/language"
)

// Options controls a single build invocation.
type Options struct {
	// Root is the absolute project root the builder operates on.
	Root string

	// Entry is the system source file for the project, absolute or
	// relative to Root. When empty the builder uses its language default
	// (typescript: index.ts, yaml: system.yaml).
	Entry string

	// Variables are project-level values written into the manifest. They
	// come from the project configuration.
	Variables map[string]any

	// Verbose enables builder-level diagnostics.
	Verbose bool

	// Out receives human-readable progress output. When nil output is
	// discarded.
	Out io.Writer
}

// Builder produces the canonical manifest for one project language.
type Builder interface {
	// Language is the canonical language this builder handles.
	Language() language.Language

	// Build produces .neuron/manifest.json for the project at Root.
	Build(ctx context.Context, opts Options) error
}
