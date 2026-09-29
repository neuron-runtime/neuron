// Package wasm implements a Backend that hosts capability runtimes as WebAssembly
// modules using wazero, the pure-Go WASI runtime. It speaks the same
// stdin/stdout JSON protocol as process capability runtimes: a WASI command that
// reads a JSON Request from stdin and writes a JSON Response to stdout
// works identically when run as a native process or as a WASM module.
//
// Why not gRPC: WASI preview1 has no socket interface, so a gRPC server
// cannot run inside a WASI module. The stdin/stdout JSON protocol is the
// natural transport for WASM capability runtimes. The Backend interface abstracts
// this difference: callers see the same Execute/Health/Close contract
// regardless of whether the backend is gRPC (process) or JSON (wasm).
//
// Each execution instantiates a fresh sandboxed module from a shared,
// precompiled module cached per entrypoint. Concurrent executions are safe
// and never serialize on the compile cache: instantiating from a compiled
// module is guarded by the wazero runtime, and each execution owns its own
// module instance with its own stdin/stdout buffers.
package wasm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
)

const (
	// defaultTimeout bounds every execution when the caller context has no
	// deadline. A runaway pure-compute WASI module is interrupted by wazero's
	// WithCloseOnContextDone.
	defaultTimeout = 10 * time.Minute
	// compileTimeout bounds the compilation of a capability runtime module.
	//
	// Compilation is deliberately bounded by its own, generous budget rather
	// than by the per-execution deadline. It is cold-start work performed once
	// per module and shared by every instance, and on a large module it can take
	// seconds. If it ran under the execution deadline, a caller that configured
	// a short execution timeout could never make a first call succeed, because
	// the budget would be consumed compiling before the module ever ran.
	compileTimeout = 2 * time.Minute
)

// sharedRuntime is created once per process and reused by every instance.
// It owns the wazero. (compilation engine and WASI host function
// bridge) plus an in-memory cache of compiled modules keyed by the frozen
// entrypoint path.
//
// Why shared: a wazero. holds the compiled-code engine and the module
// registry. Creating one per instance wastes memory and, with the compiler
// engine, recompiles native code. Compiled modules are immutable and safe to
// instantiate concurrently, so executions never serialize on this structure:
// each execution instantiates its own sandboxed module from the cached
// CompiledModule against the shared runtime.
//
// WithCloseOnContextDone makes wazero insert periodic checks so that an
// in-flight function call is interrupted (and its module closed) when the
// context passed to Call is canceled or reaches its deadline. This is how a
// runaway pure-compute WASI module gets killed cleanly on timeout.
var sharedProcessRuntime = sync.OnceValues(func() (*wasmRuntime, error) {
	rt := wazero.NewRuntimeWithConfig(
		context.Background(),
		wazero.NewRuntimeConfig().WithCloseOnContextDone(true),
	)
	if _, err := wasi_snapshot_preview1.Instantiate(context.Background(), rt); err != nil {
		_ = rt.Close(context.Background())
		return nil, fmt.Errorf("instantiate wasi: %w", err)
	}
	return &wasmRuntime{rt: rt, compiled: map[string]wazero.CompiledModule{}}, nil
})

// wasmRuntime is the process-wide wazero runtime and compiled-module cache.
type wasmRuntime struct {
	rt wazero.Runtime

	mu       sync.RWMutex // guards compiled
	compiled map[string]wazero.CompiledModule
}

// compiledModule returns the compiled module for a frozen entrypoint path,
// compiling and caching it on first use. Compiling is done once per module;
// every instance for the same frozen module shares the returned
// CompiledModule.
func (w *wasmRuntime) compiledModule(ctx context.Context, entrypoint string) (wazero.CompiledModule, error) {
	w.mu.RLock()
	mod, ok := w.compiled[entrypoint]
	w.mu.RUnlock()
	if ok {
		return mod, nil
	}

	bin, err := os.ReadFile(entrypoint)
	if err != nil {
		return nil, err
	}

	compiled, err := w.rt.CompileModule(ctx, bin)
	if err != nil {
		return nil, err
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	// A concurrent caller may have compiled the same module while we were
	// compiling; prefer the existing cached copy and release the duplicate.
	if existing, ok := w.compiled[entrypoint]; ok {
		_ = compiled.Close(ctx)
		return existing, nil
	}
	w.compiled[entrypoint] = compiled
	return compiled, nil
}

// close releases the compiled modules and the runtime. It is only safe to
// call once all executions have finished (e.g. process shutdown).
func (w *wasmRuntime) close(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	for key, mod := range w.compiled {
		if err := mod.Close(ctx); err != nil {
			return fmt.Errorf("close compiled module %s: %w", key, err)
		}
		delete(w.compiled, key)
	}
	return w.rt.Close(ctx)
}

// Backend hosts WASM capability runtime modules. It implements capabilityrt.Backend and is
// registered with the runtime registry for the "wasm" kind.
type Backend struct {
	shared *wasmRuntime
}

// New returns a WASM Backend using the process-global wazero runtime.
func New() (*Backend, error) {
	rt, err := sharedProcessRuntime()
	if err != nil {
		return nil, err
	}
	return &Backend{shared: rt}, nil
}

// RuntimeName returns "wasm", matching RuntimeKindWasm.
func (r *Backend) BackendName() string {
	return capabilityrt.RuntimeKindWasm
}

// Start validates the frozen capability runtime and returns an Instance ready for
// execution. The module is compiled lazily on first Execute (warm start),
// but the entrypoint existence is checked here so Start fails fast when
// the artifact is missing or invalid.
func (r *Backend) Start(ctx context.Context, spec capabilityrt.BackendSpec) (capabilityrt.BackendInstance, error) {
	if spec.Entrypoint == "" {
		return nil, fmt.Errorf("capability runtime %s: no entrypoint", spec.Type)
	}
	if _, err := os.Stat(spec.Entrypoint); err != nil {
		return nil, fmt.Errorf("capability runtime %s: entrypoint %s: %w", spec.Type, spec.Entrypoint, err)
	}

	protocol := spec.Protocol
	if protocol == "" {
		protocol = capabilityrt.ProtocolV1
	}

	return &instance{
		type_:      spec.Type,
		version:    spec.Version,
		entrypoint: spec.Entrypoint,
		protocol:   protocol,
		rootDir:    spec.RootDir,
		runtime:    r,
		timeout:    defaultTimeout,
	}, nil
}

// Close releases the shared compiled-module cache and the process-global wazero
// runtime.
//
// This is process-wide and irreversible: the runtime is created once via
// sync.OnceValues, so closing it permanently disables the WASM backend for
// every instance in the process, and any later Start or Execute fails with
// "runtime closed". It is therefore only safe at process shutdown, once all
// instances have been closed and no execution will follow. It is not a
// per-instance teardown, and callers must not use it to release one instance.
func (r *Backend) Close(ctx context.Context) error {
	return r.shared.close(ctx)
}

// instance implements capabilityrt.BackendInstance for WASM capability runtimes. Each Execute
// instantiates a fresh sandboxed module from the shared compiled module,
// feeds it the JSON request on stdin, and collects the JSON response from
// stdout.
type instance struct {
	type_      string
	version    string
	entrypoint string
	protocol   string
	rootDir    string
	runtime    *Backend
	timeout    time.Duration

	compiled wazero.CompiledModule // cached on first use, shared across executions
}

// Execute instantiates a fresh module from the shared compiled module, feeds
// it the protocol request, and returns the parsed response output.
//
// The call is synchronous: wazero's WithCloseOnContextDone interrupts a
// still-running _start when execCtx reaches its deadline and closes the module
// automatically, so a runaway capability runtime can never leak a goroutine or block
// forever. Concurrent executions are safe: instantiating from a compiled
// module is guarded by the runtime and each execution isolates its own module.
func (i *instance) Execute(ctx context.Context, req *capabilityrt.Request) (*capabilityrt.Response, error) {
	if req == nil {
		req = &capabilityrt.Request{}
	}

	// timeout bounds how long the module may run. It is the smaller of the
	// instance's configured budget and whatever the caller left on its own
	// deadline, so a caller can always shorten an execution but never extend it.
	timeout := i.timeout
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < timeout {
			timeout = remaining
		}
	}

	stdin, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("capability runtime %s: encode request: %w", i.type_, err)
	}

	var stdout, stderr bytes.Buffer

	moduleConfig := wazero.NewModuleConfig().
		WithName("").
		WithStdin(bytes.NewReader(stdin)).
		WithStdout(&stdout).
		WithStderr(&stderr).
		WithArgs("neuron-capability runtime", i.type_).
		WithEnv(capabilityrt.EnvProtocol, i.protocol).
		WithEnv(capabilityrt.EnvType, i.type_).
		WithEnv(capabilityrt.EnvVersion, i.version).
		WithSysWalltime().
		WithSysNanotime().
		WithSysNanosleep()

	// Cold start: compiling and instantiating the module is setup work, shared
	// per module and performed once. It runs under the cold-start budget rather
	// than the execution deadline, because a caller's execution timeout must
	// bound how long the module may *run*, not how long its first load takes.
	// It remains cancellable by the caller, so abandoning a call does not leave
	// a compile running on the caller's behalf.
	startCtx, cancelStart := context.WithTimeout(context.WithoutCancel(ctx), compileTimeout)
	defer cancelStart()
	stopOnCallerCancel := context.AfterFunc(ctx, cancelStart)
	defer stopOnCallerCancel()

	compiled, err := i.compileForExecution(startCtx)
	if err != nil {
		return nil, fmt.Errorf("capability runtime %s: compile wasm module %s: %w", i.type_, i.entrypoint, err)
	}

	// Instantiate without invoking start functions so we run _start ourselves
	// against execCtx: a deadline then interrupts the call and auto-closes the
	// module. Instantiation from the shared compiled module is concurrent-safe.
	mod, err := i.runtime.shared.rt.InstantiateModule(startCtx, compiled, moduleConfig.WithStartFunctions())
	if err != nil {
		return nil, fmt.Errorf("capability runtime %s: instantiate wasm module: %w", i.type_, err)
	}

	// From here on the module is running, so the execution deadline applies.
	// wazero's WithCloseOnContextDone interrupts an in-flight _start when this
	// context is done, which is what kills a runaway module.
	runCtx, cancelRun := context.WithTimeout(ctx, timeout)
	defer cancelRun()

	_start := mod.ExportedFunction("_start")
	if _start == nil {
		_ = mod.Close(context.Background())
		return nil, fmt.Errorf("capability runtime %s: wasm module has no _start export", i.type_)
	}

	_, callErr := _start.Call(runCtx)

	if ctxErr := runCtx.Err(); ctxErr != nil {
		if errors.Is(ctxErr, context.DeadlineExceeded) {
			return nil, fmt.Errorf("capability runtime %s: timed out after %s", i.type_, timeout)
		}
		return nil, fmt.Errorf("capability runtime %s: %w", i.type_, ctxErr)
	}

	// A WASI command exits by calling proc_exit: exit code zero means success
	// and closes the module; a non-zero code is a controlled failure.
	var exitErr *sys.ExitError
	if callErr != nil && !errors.As(callErr, &exitErr) {
		return nil, fmt.Errorf("capability runtime %s: run wasm: %w: %s", i.type_, callErr, stderr.String())
	}
	if exitErr != nil && exitErr.ExitCode() != 0 {
		return nil, fmt.Errorf("capability runtime %s: run wasm: %w (stderr: %s)", i.type_, callErr, stderr.String())
	}

	if !mod.IsClosed() {
		if err := mod.Close(context.Background()); err != nil {
			return nil, fmt.Errorf("capability runtime %s: close wasm module: %w", i.type_, err)
		}
	}

	var resp capabilityrt.Response
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("capability runtime %s: decode response: %w (stderr: %s)", i.type_, err, stderr.String())
	}

	return &resp, nil
}

// Health reports the WASM runtime and module are ready. Compilation status is
// a good proxy: if the module compiles, it will execute.
func (i *instance) Health(ctx context.Context) error {
	_, err := i.compiledModule(ctx)
	return err
}

// Close is a no-op: the wazero runtime and compiled modules are owned by the
// process and shared by every instance. They are released when the process
// exits (or shutdown closes the shared runtime explicitly). Closing an
// instance never tears down runtimes other instances still need.
func (i *instance) Close(ctx context.Context) error {
	return nil
}

// compiledModule returns the shared compiled module for this instance's
// entrypoint, compiling and caching it within the runtime on first use.
func (i *instance) compiledModule(ctx context.Context) (wazero.CompiledModule, error) {
	if i.compiled != nil {
		return i.compiled, nil
	}
	compiled, err := i.runtime.shared.compiledModule(ctx, i.entrypoint)
	if err != nil {
		return nil, err
	}
	i.compiled = compiled
	return compiled, nil
}

// compileForExecution resolves the compiled module under the cold-start budget
// rather than the caller's execution deadline.
//
// The caller's context still cancels the wait: a caller that gives up does not
// keep the compile running on its behalf. What is deliberately not allowed is
// the execution deadline itself cutting the compile short, because that would
// make the first execution of a module fail for a reason unrelated to the
// module's behaviour.
func (i *instance) compileForExecution(ctx context.Context) (wazero.CompiledModule, error) {
	if i.compiled != nil {
		return i.compiled, nil
	}

	compileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), compileTimeout)
	defer cancel()

	// Preserve caller cancellation: the compile stops if the caller goes away,
	// but it is not bound by the caller's remaining execution budget.
	stop := context.AfterFunc(ctx, cancel)
	defer stop()

	compiled, err := i.runtime.shared.compiledModule(compileCtx, i.entrypoint)
	if err != nil {
		return nil, err
	}
	i.compiled = compiled
	return compiled, nil
}

// SetTimeout overrides the per-execution timeout (for tests).
func (i *instance) SetTimeout(d time.Duration) {
	i.timeout = d
}
