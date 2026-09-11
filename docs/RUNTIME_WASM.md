# WASM Runtime

**The WASM executor backend** hosts external Neuron executors as `wasm32-wasi` WebAssembly modules. It is registered with the runtime registry for the runtime kind `wasm` and is implemented in `nore/internal/runtime/wasm` on top of **wazero**, the pure-Go WASI runtime.

A WASM executor is a WASI command that reads one JSON request from stdin and writes one JSON response to stdout. This is exactly the `neuron/executor-v1-json` transport, so a WASM executor and a legacy process executor are **byte-for-byte compatible at the protocol level**.

---

## 1. Why not gRPC

WASI preview1 has no socket interface, so a gRPC server cannot run inside a WASI module. The stdin/stdout JSON transport is therefore the only option for WASM executors — and it is the natural one: a module that reads a request from stdin and writes a response to stdout behaves identically as a native process and as a WASM module. The executor contract hides this difference: `Execute`, `Health`, and `Close` look the same whether a backend talks gRPC (process) or JSON (WASM).

---

## 2. A single shared runtime

> [!IMPORTANT]
> One property dominates everything about the WASM runtime: it is **process-global**.

The wazero runtime is created exactly once per process, lazily, and is shared by every WASM instance. It owns the compilation engine and the module registry. `withCloseOnContextDone` is enabled so wazero inserts periodic checks that interrupt in-flight calls when their context is canceled or reaches its deadline — this is what kills runaway modules cleanly.

### The compiled-module cache

Compiled modules are cached in memory inside the shared runtime, keyed by the frozen entrypoint path. Each distinct module file is compiled exactly once and reused by every subsequent execution and by every other instance that references the same file. The cache holds one compiled module per distinct module file, not one module for the whole process.

Why shared:

- a wazero runtime holds the compiled-code engine and module registry; creating one per instance wastes memory and recompiles native code;
- compiled modules are immutable and safe to instantiate concurrently, so executions never serialize on the cache.

> [!TIP]
> `Health` compiles the module on demand, so a healthy instance implies the module compiles; compilation status is a good proxy for readiness.

---

## 3. Executing a request

Each execution is a full, isolated sandbox cycle:

```mermaid
flowchart LR
    A[Request] --> B[fresh module config<br/>stdin/stdout/stderr buffers + env]
    B --> C[fetch shared compiled module<br/>compile on first use]
    C --> D[instantiate fresh module instance]
    D --> E[invoke _start]
    E --> F[parse stdout as JSON Response]
```

1. The runtime marshals the `Request` to JSON and prepares a fresh module configuration: empty stdin (replaced by the request bytes), stdout and stderr buffers, the executor environment (`NEURON_EXECUTOR_PROTOCOL`, `NEURON_EXECUTOR_TYPE`, `NEURON_EXECUTOR_VERSION`), wall time, nanotime, and sleep support.
2. The shared compiled module is fetched (compiling on first use) and a fresh module instance is instantiated from it. Instantiation from a compiled module is concurrent-safe, so parallel requests run in parallel sandboxes.
3. The `_start` export is invoked against the execution context. A module missing `_start` is rejected.
4. A WASI command exits by calling `proc_exit`: exit code zero closes the module cleanly; a non-zero code is a controlled failure. Any other call error is a transport failure.
5. Whatever the module wrote to stdout is parsed as the JSON `Response` and returned.

The instance owns only its compiled-module reference and timeout; each `Execute` recovers the shared compiled module and instantiates on demand.

---

## 4. Timeouts and runaway modules

Every execution carries a defensive timeout (10 minutes by default); when the caller context carries a deadline, the stricter of the two applies. The deadline is applied to the `_start` call, and because the shared runtime is configured with `withCloseOnContextDone`, wazero interrupts the still-running call and closes the module automatically when the deadline hits.

> [!NOTE]
> A module that loops forever cannot leak a goroutine, block the process, or outlive its window.

---

## 5. Lifecycle and shutdown

The WASM instance's `Close` is a no-op by design. The wazero runtime and the compiled modules are owned by the process and shared by every instance, so closing one adapter never tears down resources other instances still need. The shared runtime is released only when the process shuts down (or a caller explicitly closes the shared runtime after all instances are done).

The entrypoint existence is verified at `Start`, so a missing artifact fails fast when the instance is created rather than on the first execution. Compilation is lazy, so the first `Execute` (or `Health`) pays the compile cost and every later one reuses the cached module.

---

## 6. Creating a WASM executor

A WASM executor is a WASI command. With Go, cross-compile the same source used for the process artifact:

```bash
GOOS=wasip1 GOARCH=wasm go build -o capability.wasm .
```

The source stays stdlib-only so it builds without a C toolchain and is portable. `examples/executors/echo` is built exactly this way: one source, a native binary and a `wasm32-wasi` module.

The manifest declares the WASM runtime and the JSON protocol:

```json
{
  "apiVersion": "neuron/v1",
  "kind": "Executor",
  "metadata": { "name": "my:capability", "version": "1.0.0" },
  "runtime": { "type": "wasm", "entrypoint": "capability.wasm", "protocol": "neuron/executor-v1-json" },
  "services": ["capability"],
  "platforms": { "wasm32-wasi": { "artifact": "capability.wasm" } }
}
```

The contract a WASM executor must satisfy:

- export `_start`;
- read one JSON request document from stdin;
- write exactly one JSON response document to stdout;
- exit zero on success, non-zero on failure (or report a controlled failure through the response `error` field);
- run within the execution deadline.

---

## 7. Reference

- Implementation: `nore/internal/runtime/wasm`
- Runtime engine: wazero (pure-Go WASI)
- Contract: `shared/types/executor` and the legacy `neuron/executor-v1-json` transport
- Example artifact: `examples/executors/echo` (single source, native + WASI)
- Tests: `nore/internal/runtime/wasm/runtime_test.go` (round-trip, timeout, concurrency against the shared runtime, compiled-module cache, instance close safety)

---

## Related

| | |
| --- | --- |
| **Runtime deep dive** | How N.O.R.E. executes services end to end — [RUNTIME.md](./RUNTIME.md) |
| **Process runtime** | The process executor backend — [RUNTIME_PROCESS.md](./RUNTIME_PROCESS.md) |
| **Modules & executors** | The unified module model — [MODULES.md](./MODULES.md) |