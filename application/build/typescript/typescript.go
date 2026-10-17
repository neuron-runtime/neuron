// Package typescript implements the Builder for TypeScript-authored Neuron
// projects. Building delegates to the neuron-sdk CLI, which evaluates the
// project entry and writes a manifest; the build package then applies the
// project-configured identity, canonicalizes, and persists it.
package typescript

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/neuron-runtime/neuron/application/build/builder"
	"github.com/neuron-runtime/neuron/application/compiler/manifest"
	"github.com/neuron-runtime/neuron/application/language"
	"github.com/neuron-runtime/neuron/application/loader"
	"github.com/neuron-runtime/neuron/application/loader/typescript"
	"github.com/neuron-runtime/neuron/application/process"
)

// SDKBin is the neuron-sdk executable name exposed by @neuron/sdk.
const SDKBin = "neuron-sdk"

// ErrSDKNotFound reports that the neuron-sdk binary could not be located.
var ErrSDKNotFound = errors.New("neuron-sdk not found: install the @neuron/sdk workspace package (pnpm install) and run `pnpm build:sdk` first")

// Builder delegates the TypeScript build to the neuron-sdk CLI.
type Builder struct {
	// Out receives progress output; nil discards it.
	Out io.Writer
}

// Language returns the canonical TypeScript identifier.
func (b Builder) Language() language.Language { return language.TypeScript }

// New returns a Builder with output discarded unless configured otherwise.
func New() Builder { return Builder{} }

// Build invokes the neuron-sdk build command for the project at opts.Root
// and returns the manifest the SDK wrote. Identity injection,
// canonicalization, and persistence are owned by the build package so
// every authoring language shares one injection boundary.
func (b Builder) Build(ctx context.Context, opts builder.Options) (*manifest.Assembly, error) {
	if b.Out == nil {
		b.Out = io.Discard
	}

	root := opts.Root
	if root == "" {
		return nil, fmt.Errorf("project root is required")
	}

	sdkPath, err := ResolveSDK(root)
	if err != nil {
		return nil, err
	}

	args := []string{"build", "--path", root}
	if opts.Entry != "" {
		args = append(args, "--entry", opts.Entry)
	}

	cmd := process.Command{
		Path: sdkPath,
		Args: args,
		Dir:  root,
	}

	tsLoader := typescript.NewTSLoader(cmd)
	if err := loader.New(tsLoader).Handler.BuildContext(ctx); err != nil {
		return nil, fmt.Errorf("typescript build failed: %w", err)
	}

	// The SDK writes camelCase binding keys inherited from JS objects; the
	// build package canonicalizes them after this return so every language
	// produces the same snake_case shape on disk. The SDK writes to its fixed
	// transient path (SDKManifestPath); the build persists the canonical
	// result under the per-Assembly artifact path.
	sys, err := manifest.LoadSDKManifest(root)
	if err != nil {
		return nil, fmt.Errorf("load SDK manifest: %w", err)
	}

	if opts.Verbose {
		fmt.Fprintf(b.Out, "Built TypeScript project at %s\n", root)
	}
	return sys, nil
}

// ResolveSDK locates the neuron-sdk binary, preferring a project-local install
// and falling back to PATH.
func ResolveSDK(root string) (string, error) {
	if root != "" {
		local := filepath.Join(root, "node_modules", ".bin", SDKBin)
		if info, err := os.Stat(local); err == nil && !info.IsDir() {
			return local, nil
		}
	}

	if path, err := exec.LookPath(SDKBin); err == nil {
		return path, nil
	}

	return "", fmt.Errorf("%w (searched for %s on PATH)", ErrSDKNotFound, SDKBin)
}
