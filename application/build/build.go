// Package build produces the canonical .neuron/manifest.json from a project
// root for a given authoring language. It is the language-agnostic seam between
// the CLI and the project frontends: the CLI resolves a language and asks this
// package to build; it never touches project files itself. This package also
// owns the shared post-build tail — project-configured Assembly identity is
// injected, the manifest is canonicalized, and the result is persisted here,
// exactly once, for every language.
//
// Adding a future language is a new Builder registered via Register (or init)
// — the CLI and runtime are untouched.
package build

import (
	"context"
	"fmt"
	"sync"

	"github.com/neuron-runtime/neuron/application/build/builder"
	ts "github.com/neuron-runtime/neuron/application/build/typescript"
	yamlb "github.com/neuron-runtime/neuron/application/build/yaml"
	"github.com/neuron-runtime/neuron/application/compiler/manifest"
	"github.com/neuron-runtime/neuron/application/language"
)

// Options and Builder are re-exported for callers of this package.
type (
	// Options controls a single build invocation. Name and Version carry the
	// project-configured Assembly identity, which Build injects into the
	// manifest after the language builder returns.
	Options = builder.Options

	// Builder produces the canonical manifest for one project language. It
	// returns the authored manifest only; Build owns identity injection,
	// canonicalization, and persistence.
	Builder = builder.Builder
)

var (
	mu       sync.RWMutex
	builders = make(map[language.Language]Builder)
)

// Register installs a builder. Registering the same language twice is an
// error; a language may only have one builder.
func Register(b Builder) error {
	if b == nil {
		return fmt.Errorf("builder is nil")
	}
	lang := b.Language()
	if lang == "" {
		return fmt.Errorf("builder %T declares no language", b)
	}
	mu.Lock()
	defer mu.Unlock()
	if _, exists := builders[lang]; exists {
		return fmt.Errorf("builder for language %q is already registered", lang)
	}
	builders[lang] = b
	return nil
}

// Lookup returns the builder registered for a canonical language.
func Lookup(lang language.Language) (Builder, error) {
	mu.RLock()
	defer mu.RUnlock()
	builder, ok := builders[lang]
	if !ok {
		return nil, fmt.Errorf("no builder registered for language %q", lang)
	}
	return builder, nil
}

// Build dispatches to the builder registered for lang, then makes the
// project configuration the single source of the Assembly's identity,
// canonicalizes the manifest, and persists it as the project's
// .neuron/manifest.json. It is the one place in the pipeline where
// identity injection, canonicalization, and persistence happen, so every
// authoring language gets identical behavior.
func Build(ctx context.Context, lang language.Language, opts Options) error {
	b, err := Lookup(lang)
	if err != nil {
		return err
	}
	sys, err := b.Build(ctx, opts)
	if err != nil {
		return err
	}
	if err := manifest.ApplyIdentity(sys, opts.Name, opts.Version); err != nil {
		return fmt.Errorf("apply assembly identity: %w", err)
	}
	canonical, err := manifest.Canonicalize(sys)
	if err != nil {
		return fmt.Errorf("canonicalize manifest: %w", err)
	}
	if err := manifest.SaveToProjectRoot(opts.Root, canonical); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

func init() {
	must(Register(yamlb.New()))
	must(Register(ts.New()))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
