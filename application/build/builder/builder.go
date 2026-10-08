// Package builder defines the contract for project frontends. It lives below
// the build registry package so individual builders can import it without
// creating an import cycle.
package builder

import (
	"context"
	"io"

	"github.com/neuron-runtime/neuron/application/compiler/manifest"
	"github.com/neuron-runtime/neuron/application/language"
)

// Options controls a single build invocation.
type Options struct {
	// Root is the absolute project root the builder operates on.
	Root string

	// Entry is the assembly source file for the project, absolute or
	// relative to Root. When empty the builder uses its language default
	// (typescript: index.ts, yaml: assembly.yaml).
	Entry string

	// Name is the Assembly name declared by the project configuration
	// (neuron.config.*). It is injected into the manifest identity by the
	// build package after the builder returns; builders never apply it.
	Name string

	// Version is the Assembly version declared by the project configuration
	// (neuron.config.*). Like Name it is injected by the build package
	// after the builder returns.
	Version string

	// Variables are project-level values written into the manifest. They
	// come from the project configuration and are applied by the build
	// package after the builder returns; builders never apply them.
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

	// Build loads the project at opts.Root and returns its manifest. The
	// returned manifest carries only what the authoring surface produced:
	// it is not identity-injected, canonicalized, or persisted. Those steps
	// happen exactly once in the build package so every language shares one
	// injection boundary.
	Build(ctx context.Context, opts Options) (*manifest.Assembly, error)
}
