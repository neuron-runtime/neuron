// Package typescript implements the Builder for TypeScript-authored Neuron
// projects. Building delegates to the neuron-sdk CLI, which evaluates the
// project entry and writes the canonical .neuron/manifest.json.
package typescript

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/Muhammad-Jay/neuron/application/build/builder"
	"github.com/Muhammad-Jay/neuron/application/compiler/manifest"
	"github.com/Muhammad-Jay/neuron/application/language"
	"github.com/Muhammad-Jay/neuron/application/loader"
	"github.com/Muhammad-Jay/neuron/application/loader/typescript"
	"github.com/Muhammad-Jay/neuron/application/process"
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

// Build produces .neuron/manifest.json for the project at opts.Root by
// invoking the neuron-sdk build command.
func (b Builder) Build(ctx context.Context, opts builder.Options) error {
	if b.Out == nil {
		b.Out = io.Discard
	}

	root := opts.Root
	if root == "" {
		return fmt.Errorf("project root is required")
	}

	sdkPath, err := ResolveSDK(root)
	if err != nil {
		return err
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
		return fmt.Errorf("typescript build failed: %w", err)
	}

	// The SDK writes camelCase connector keys inherited from JS objects;
	// canonicalize them so every language produces the same snake_case shape.
	sys, err := manifest.LoadFromProjectRoot(root)
	if err != nil {
		return fmt.Errorf("load manifest: %w", err)
	}
	manifest.Canonicalize(sys)
	if err := manifest.SaveToProjectRoot(root, sys); err != nil {
		return fmt.Errorf("write canonical manifest: %w", err)
	}

	if opts.Verbose {
		fmt.Fprintf(b.Out, "Built TypeScript project at %s\n", root)
	}
	return nil
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
