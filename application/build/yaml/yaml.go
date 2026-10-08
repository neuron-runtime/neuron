// Package yaml implements the Builder for YAML-authored Neuron projects. It
// resolves the project layout (assemblies/, capabilities/, bindings/) into a
// manifest; the build package then applies the project-configured identity,
// canonicalizes, and persists it as .neuron/manifest.json.
package yaml

import (
	"context"
	"fmt"
	"io"

	"github.com/neuron-runtime/neuron/application/build/builder"
	"github.com/neuron-runtime/neuron/application/compiler/manifest"
	"github.com/neuron-runtime/neuron/application/language"
	"github.com/neuron-runtime/neuron/application/project"
)

// Builder resolves YAML Neuron projects into the canonical manifest.
type Builder struct {
	// Out receives progress output; nil discards it.
	Out io.Writer
}

// Language returns the canonical YAML identifier.
func (b Builder) Language() language.Language { return language.YAML }

// New returns a Builder with output discarded unless configured otherwise.
func New() Builder { return Builder{} }

// Build resolves the project at opts.Root and returns its manifest. Identity
// injection, canonicalization, and persistence are owned by the build
// package so every authoring language shares one injection boundary.
func (b Builder) Build(ctx context.Context, opts builder.Options) (*manifest.Assembly, error) {
	if b.Out == nil {
		b.Out = io.Discard
	}

	root := opts.Root
	if root == "" {
		return nil, fmt.Errorf("project root is required")
	}

	buildOpts := project.DefaultOptions()
	buildOpts.Verbose = opts.Verbose
	buildOpts.ProjectRoot = root
	buildOpts.Entry = opts.Entry

	result, err := project.Resolve(ctx, buildOpts)
	if err != nil {
		return nil, fmt.Errorf("resolve project: %w", err)
	}

	sys := manifest.FromResolvedProject(result.Project, opts.Variables)

	if opts.Verbose {
		fmt.Fprintf(b.Out, "Resolved YAML project at %s\n", root)
	}
	return sys, nil
}
