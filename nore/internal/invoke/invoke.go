// Package invoke implements one-shot direct invocation of a Capability
// Runtime (`nore --invoke`).
//
// It is the child process behind `neuron run -c`: the CLI and N.O.R.E. are
// separate Go modules, so the CLI cannot host a provider in-process and instead
// hands the invocation target to a short-lived N.O.R.E. process. The child
// reads a single capabilityruntime.InvokeRequest from stdin: a built-in core
// runtime is resolved from the core registry and executed in-process, while an
// installed artifact is hosted through the same provider registry Assembly
// execution uses. It writes a single capabilityruntime.InvokeResponse to
// stdout.
package invoke

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
	"github.com/neuron-runtime/neuron/nore/internal/plugin"
	"github.com/neuron-runtime/neuron/nore/internal/registry"
	core "github.com/neuron-runtime/neuron/shared/types/core"
	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
)

// shutdownTimeout bounds the release of the provider backends after a single
// invocation, so a backend that hangs on close cannot keep the one-shot process
// alive indefinitely.
const shutdownTimeout = 10 * time.Second

// Run performs exactly one direct invocation. It always writes exactly one
// InvokeResponse to out — even when the request cannot be decoded or the
// invocation fails — so the parent CLI can render a structured error. It
// returns a non-nil error to signal a non-zero exit; diagnostics go to stderr.
//
// storeRoot is the capability runtime store root the artifact was installed
// into; it is a property of the host, not the request.
func Run(ctx context.Context, storeRoot string, in io.Reader, out io.Writer) error {
	req, err := decode(in)
	if err != nil {
		_ = write(out, &capabilityrt.InvokeResponse{Error: err.Error()})
		return err
	}

	// The process provider spawns worker processes, and the WASM provider owns
	// process-global state. A one-shot child must release them instead of
	// leaving workers orphaned behind a process that has already exited.
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := plugin.CloseSharedRuntimes(closeCtx); err != nil {
			slog.Warn("close capability runtime backends", slog.String("error", err.Error()))
		}
	}()

	resp := Invoke(ctx, storeRoot, req)
	if err := write(out, resp); err != nil {
		return err
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}
	return nil
}

// Invoke hosts the request's Capability Runtime and executes it once. A
// request carrying CoreType executes the built-in in-process runtime from the
// core registry; otherwise the frozen artifact is hosted through the provider
// registry. Failures — an unknown runtime, a missing installed artifact, a
// missing provider, or a runtime error — are returned as a structured error
// response, never as a panic, so the parent always receives a parseable
// document.
func Invoke(ctx context.Context, storeRoot string, req capabilityrt.InvokeRequest) *capabilityrt.InvokeResponse {
	if req.CoreType != "" {
		return invokeCore(ctx, req)
	}

	plugin.SetStoreRoot(storeRoot)

	adapter, err := plugin.NewAdapter(req.CapabilityRuntime)
	if err != nil {
		return &capabilityrt.InvokeResponse{Error: err.Error()}
	}
	defer func() {
		if closer, ok := adapter.(contracts.CapabilityRuntimeCloser); ok {
			_ = closer.Close()
		}
	}()

	result, err := adapter.Execute(ctx, contracts.ExecutionContext{Params: req.Params})
	if err != nil {
		return &capabilityrt.InvokeResponse{Error: err.Error()}
	}
	return &capabilityrt.InvokeResponse{Result: result}
}

// invokeCore resolves a built-in core capability runtime from the same core
// registry Assembly execution uses and executes it once in-process. No store,
// provider, or installed artifact is involved; an unknown core name is
// reported as a structured error.
func invokeCore(ctx context.Context, req capabilityrt.InvokeRequest) *capabilityrt.InvokeResponse {
	reg := registry.New()
	reg.RegisterCoreRuntimes()
	defer func() {
		if err := reg.Close(); err != nil {
			slog.Warn("close core capability runtimes", slog.String("error", err.Error()))
		}
	}()

	runtime, err := reg.Resolve(core.CapabilityRuntimeType(req.CoreType))
	if err != nil {
		return &capabilityrt.InvokeResponse{Error: err.Error()}
	}

	result, err := runtime.Execute(ctx, contracts.ExecutionContext{Params: req.Params})
	if err != nil {
		return &capabilityrt.InvokeResponse{Error: err.Error()}
	}
	return &capabilityrt.InvokeResponse{Result: result}
}

func decode(in io.Reader) (capabilityrt.InvokeRequest, error) {
	var req capabilityrt.InvokeRequest
	if err := json.NewDecoder(in).Decode(&req); err != nil {
		return req, fmt.Errorf("decode invoke request: %w", err)
	}
	return req, nil
}

func write(out io.Writer, resp *capabilityrt.InvokeResponse) error {
	buf, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("encode invoke response: %w", err)
	}
	if _, err := out.Write(append(buf, '\n')); err != nil {
		return fmt.Errorf("write invoke response: %w", err)
	}
	return nil
}
