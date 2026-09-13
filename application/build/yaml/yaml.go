// Package yaml implements the Builder for YAML-authored Neuron projects. It
// resolves the project layout (systems/, services/, connectors/) and converts
// it into the canonical .neuron/manifest.json.
package yaml

import (
	"context"
	"fmt"
	"io"

	"github.com/Muhammad-Jay/neuron/application/build/builder"
	"github.com/Muhammad-Jay/neuron/application/compiler/manifest"
	"github.com/Muhammad-Jay/neuron/application/language"
	"github.com/Muhammad-Jay/neuron/application/project"
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

// Build resolves the project at opts.Root and writes .neuron/manifest.json.
func (b Builder) Build(ctx context.Context, opts builder.Options) error {
	if b.Out == nil {
		b.Out = io.Discard
	}

	root := opts.Root
	if root == "" {
		return fmt.Errorf("project root is required")
	}

	buildOpts := project.DefaultOptions()
	buildOpts.Verbose = opts.Verbose
	buildOpts.ProjectRoot = root
	buildOpts.Entry = opts.Entry

	result, err := project.Resolve(ctx, buildOpts)
	if err != nil {
		return fmt.Errorf("resolve project: %w", err)
	}

	sys := manifest.FromResolvedProject(result.Project, opts.Variables)
	// Canonicalize is identity for YAML-authored manifests but keeps the
	// canonicalization invariant (snake_case connector keys) uniform.
	if err := manifest.SaveToProjectRoot(root, manifest.Canonicalize(sys)); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}

	if opts.Verbose {
		fmt.Fprintf(b.Out, "Wrote manifest to %s\n", manifest.ManifestPath(root))
	}
	return nil
}
