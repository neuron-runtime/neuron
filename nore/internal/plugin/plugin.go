// Package plugin adapts frozen capability runtime artifacts into N.O.R.E.'s
// in-process CapabilityRuntime contract. A ResolvedCapabilityRuntime is hosted
// by a runtime backend (process, wasm) selected by the artifact's declared
// runtime kind.
//
// The plugin package is the boundary between the frozen capability runtime set
// persisted with a registered assembly and the runtime backends in
// nore/internal/backend. It owns no process, socket, or WASM machinery itself;
// it only maps a ResolvedCapabilityRuntime to a runtime backend instance.
package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"github.com/Muhammad-Jay/neuron/nore/internal/backend"
	"github.com/Muhammad-Jay/neuron/nore/internal/backend/process"
	"github.com/Muhammad-Jay/neuron/nore/internal/backend/wasm"
	"github.com/Muhammad-Jay/neuron/nore/internal/contracts"
	core "github.com/Muhammad-Jay/neuron/shared/types/core"
	capabilityrt "github.com/Muhammad-Jay/neuron/shared/types/capabilityruntime"
)

// config stores frozen capability runtime specifications keyed by the logical
// type.
type config struct {
	ResolvedCapabilityRuntimes []capabilityrt.ResolvedCapabilityRuntime `json:"resolved_capability_runtimes"`
}

// DecodeResolvedCapabilityRuntimes extracts the frozen capability runtime set
// from the opaque ExecutionConfigurations payload stored on a
// RegisteredAssembly. The payload is JSON-round-tripped so it works for both
// typed values and values re-read from disk as map[string]any.
func DecodeResolvedCapabilityRuntimes(payload any) ([]capabilityrt.ResolvedCapabilityRuntime, error) {
	if payload == nil {
		return nil, nil
	}

	buf, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal execution configurations: %w", err)
	}

	var cfg config
	if err := json.Unmarshal(buf, &cfg); err != nil {
		return nil, fmt.Errorf("decode resolved capability runtimes: %w", err)
	}
	return cfg.ResolvedCapabilityRuntimes, nil
}

// sharedRuntimes provides the process-wide set of runtime backends. It is
// created once because the WASM backend owns process-global compiled-module
// state that must be shared by every adapter (see backend/wasm), and the
// process backend tracks its worker pools by capability runtime identity.
var sharedRuntimes = sync.OnceValues(func() (*backend.Registry, error) {
	reg := backend.New()

	wm, err := wasm.New()
	if err != nil {
		return nil, fmt.Errorf("initialize wasm backend: %w", err)
	}
	if err := reg.Register(capabilityrt.RuntimeKindWasm, wm); err != nil {
		return nil, err
	}

	proc := process.New(slog.Default())
	if err := reg.Register(capabilityrt.RuntimeKindProcess, proc); err != nil {
		return nil, err
	}

	return reg, nil
})

// RegisterResolvedCapabilityRuntimes registers a runtime adapter for every
// frozen type that does not already have an in-process capability runtime
// (core capability runtimes win). The adapter is chosen by the capability
// runtime's runtime kind.
func RegisterResolvedCapabilityRuntimes(reg contracts.CapabilityRuntimeRegistry, resolved []capabilityrt.ResolvedCapabilityRuntime) error {
	for _, r := range resolved {
		if _, err := reg.Resolve(core.CapabilityRuntimeType(r.Type)); err == nil {
			// Core in-process capability runtime already registered; prefer it.
			continue
		}
		adapter, err := NewAdapter(r)
		if err != nil {
			return fmt.Errorf("create capability runtime for %s: %w", r.Type, err)
		}
		if err := reg.Register(core.CapabilityRuntimeType(r.Type), adapter); err != nil {
			return fmt.Errorf("register capability runtime for %s: %w", r.Type, err)
		}
	}
	return nil
}

// NewAdapter builds the runtime adapter matching a frozen capability runtime's
// runtime kind. It rejects kinds this build cannot host. An empty kind
// defaults to the process backend (the original capability runtime model).
func NewAdapter(resolved capabilityrt.ResolvedCapabilityRuntime) (contracts.CapabilityRuntime, error) {
	reg, err := sharedRuntimes()
	if err != nil {
		return nil, err
	}

	kind := resolved.Runtime.Type
	if kind == "" {
		kind = capabilityrt.RuntimeKindProcess
	}

	protocol := resolved.Runtime.Protocol
	if protocol == "" {
		// Legacy capability runtimes that omit the protocol declaration speak
		// the JSON transport. Explicitly declaring it keeps Start unambiguous.
		protocol = capabilityrt.ProtocolJSONV1
	}

	spec := capabilityrt.BackendSpec{
		Type:       resolved.Type,
		Version:    resolved.ResolvedVersion,
		Protocol:   protocol,
		Entrypoint: resolved.EntrypointPath(),
		RootDir:    resolved.RootDir,
		MaxWorkers: resolved.Runtime.MaxWorkers,
	}

	inst, err := reg.Start(context.Background(), kind, spec)
	if err != nil {
		return nil, fmt.Errorf("capability runtime %s: %w", resolved.Type, err)
	}

	return &instanceAdapter{instance: inst, typ: resolved.Type}, nil
}

// instanceAdapter bridges a capabilityrt.BackendInstance (backend contract)
// onto the contracts.CapabilityRuntime interface consumed by N.O.R.E.'s
// execution engine. It maps the engine's ExecutionContext to a Request and
// back.
type instanceAdapter struct {
	instance capabilityrt.BackendInstance
	typ      string
}

// Execute runs one execution through the runtime-backed instance.
func (a *instanceAdapter) Execute(ctx context.Context, execution contracts.ExecutionContext) (map[string]any, error) {
	req := &capabilityrt.Request{Params: execution.Params}
	resp, err := a.instance.Execute(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("capability runtime %s: %w", a.typ, err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("capability runtime %s: %s", a.typ, resp.Error)
	}
	return resp.Result, nil
}

// Close releases the runtime-backed instance.
func (a *instanceAdapter) Close() error {
	return a.instance.Close(context.Background())
}