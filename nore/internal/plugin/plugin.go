// Package plugin adapts frozen executor artifacts into N.O.R.E.'s in-process
// Executor contract. A ResolvedExecutor is hosted by an executor runtime
// backend (process, wasm) selected by the artifact's declared runtime kind.
//
// The plugin package is the boundary between the frozen executor set persisted
// with a registered system and the runtime backends in
// nore/internal/runtime. It owns no process, socket, or WASM machinery
// itself; it only maps a ResolvedExecutor to a runtime backend instance.
package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"github.com/Muhammad-Jay/neuron/nore/internal/contracts"
	"github.com/Muhammad-Jay/neuron/nore/internal/runtime"
	"github.com/Muhammad-Jay/neuron/nore/internal/runtime/process"
	"github.com/Muhammad-Jay/neuron/nore/internal/runtime/wasm"
	core "github.com/Muhammad-Jay/neuron/shared/types/core"
	shadexec "github.com/Muhammad-Jay/neuron/shared/types/executor"
)

// config stores frozen executor specifications keyed by the logical type.
type config struct {
	ResolvedExecutors []shadexec.ResolvedExecutor `json:"resolved_executors"`
}

// DecodeResolvedExecutors extracts the frozen executor set from the opaque
// ExecutionConfigurations payload stored on a RegisteredSystem. The payload is
// JSON-round-tripped so it works for both typed values and values re-read from
// disk as map[string]any.
func DecodeResolvedExecutors(payload any) ([]shadexec.ResolvedExecutor, error) {
	if payload == nil {
		return nil, nil
	}

	buf, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal execution configurations: %w", err)
	}

	var cfg config
	if err := json.Unmarshal(buf, &cfg); err != nil {
		return nil, fmt.Errorf("decode resolved executors: %w", err)
	}
	return cfg.ResolvedExecutors, nil
}

// sharedRuntimes provides the process-wide set of runtime backends. It is
// created once because the WASM runtime owns process-global compiled-module
// state that must be shared by every adapter (see runtime/wasm), and the
// process runtime tracks its worker pools by executor identity.
var sharedRuntimes = sync.OnceValues(func() (*runtime.Registry, error) {
	reg := runtime.New()

	wm, err := wasm.New()
	if err != nil {
		return nil, fmt.Errorf("initialize wasm runtime: %w", err)
	}
	if err := reg.Register(shadexec.RuntimeKindWasm, wm); err != nil {
		return nil, err
	}

	proc := process.New(slog.Default())
	if err := reg.Register(shadexec.RuntimeKindProcess, proc); err != nil {
		return nil, err
	}

	return reg, nil
})

// RegisterResolvedExecutors registers a runtime adapter for every frozen type
// that does not already have an in-process executor (core executors win).
// The adapter is chosen by the executor's runtime kind.
func RegisterResolvedExecutors(reg contracts.ExecutorRegistry, resolved []shadexec.ResolvedExecutor) error {
	for _, r := range resolved {
		if _, err := reg.Resolve(core.ExecutorType(r.Type)); err == nil {
			// Core in-process executor already registered; prefer it.
			continue
		}
		adapter, err := NewAdapter(r)
		if err != nil {
			return fmt.Errorf("create executor for %s: %w", r.Type, err)
		}
		if err := reg.Register(core.ExecutorType(r.Type), adapter); err != nil {
			return fmt.Errorf("register executor for %s: %w", r.Type, err)
		}
	}
	return nil
}

// NewAdapter builds the runtime adapter matching a frozen executor's runtime
// kind. It rejects kinds this build cannot host. An empty kind defaults to the
// process runtime (the original executor model).
func NewAdapter(resolved shadexec.ResolvedExecutor) (contracts.Executor, error) {
	reg, err := sharedRuntimes()
	if err != nil {
		return nil, err
	}

	kind := resolved.Runtime.Type
	if kind == "" {
		kind = shadexec.RuntimeKindProcess
	}

	protocol := resolved.Runtime.Protocol
	if protocol == "" {
		// Legacy executors that omit the protocol declaration speak the JSON
		// transport. Explicitly declaring it keeps Start unambiguous.
		protocol = shadexec.ProtocolJSONV1
	}

	spec := shadexec.StartSpec{
		Type:       resolved.Type,
		Version:    resolved.ResolvedVersion,
		Protocol:   protocol,
		Entrypoint: resolved.EntrypointPath(),
		RootDir:    resolved.RootDir,
		MaxWorkers: resolved.Runtime.MaxWorkers,
	}

	inst, err := reg.Start(context.Background(), kind, spec)
	if err != nil {
		return nil, fmt.Errorf("executor %s: %w", resolved.Type, err)
	}

	return &instanceAdapter{instance: inst, typ: resolved.Type}, nil
}

// instanceAdapter bridges a shadexec.Instance (runtime contract) onto the
// contracts.Executor interface consumed by N.O.R.E.'s executor engine. It
// maps the engine's ExecutionContext to a Request and back.
type instanceAdapter struct {
	instance shadexec.Instance
	typ      string
}

// Execute runs one execution through the runtime-backed instance.
func (a *instanceAdapter) Execute(ctx context.Context, execution contracts.ExecutionContext) (map[string]any, error) {
	req := &shadexec.Request{Input: execution.Input}
	resp, err := a.instance.Execute(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("executor %s: %w", a.typ, err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("executor %s: %s", a.typ, resp.Error)
	}
	return resp.Output, nil
}

// Close releases the runtime-backed instance.
func (a *instanceAdapter) Close() error {
	return a.instance.Close(context.Background())
}
