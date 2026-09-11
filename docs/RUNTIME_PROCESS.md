# Process Runtime

**The process executor backend** hosts external Neuron executors as OS child processes. It is registered with the runtime registry for the runtime kind `process` and is implemented in `nore/internal/runtime/process`.

It supports two transports, selected by the executor artifact's declared protocol:

| Protocol | Model | Used by |
| --- | --- | --- |
| `neuron/executor-v1` | Long-lived gRPC server over a Unix domain socket, with a pool of worker processes per executor | Executors built on the Go SDK (canonical) |
| `neuron/executor-v1-json` | One-shot command: one JSON request on stdin, one JSON response on stdout | WASI modules and process executors that predate gRPC (legacy) |

---

## 1. What the runtime owns

The process runtime owns the entire process lifecycle for the executors it hosts:

- starting worker processes,
- connecting to them and negotiating the protocol,
- health checking,
- leasing executions to workers,
- cancellation and deadlines,
- graceful shutdown and reaping.

It never cares what the executor does. It only launches a declared entrypoint, talks the declared protocol, and keeps the process healthy.

---

## 2. The legacy one-shot JSON transport

When the frozen record declares `neuron/executor-v1-json`, each execution spawns a fresh process:

```mermaid
flowchart LR
    A[Request → JSON] --> B[spawn process]
    B --> C[stdin: one JSON request]
    C --> D[process runs to completion]
    D --> E[stdout: one JSON Response]
    E --> F[parse and return]
```

1. The runtime marshals the `Request` to JSON and feeds it to the process on stdin.
2. The process runs to completion, writing a single JSON `Response` to stdout and exiting.
3. The runtime parses the response and returns it. Anything on stderr is diagnostics.

Key properties:

- **No worker reuse** — every execution pays the process startup cost. The transport exists for compatibility with WASI executors and pre-gRPC artifacts; executors that want connection reuse must be migrated to the gRPC protocol.
- A non-zero exit code is a transport failure. A zero exit code with a non-empty `error` in the response is a controlled failure.
- A green context deadline kills the process through `exec.CommandContext`. Each execution also carries a defensive timeout (10 minutes by default); the stricter of the caller deadline and the default bound applies.
- `Health` only verifies the entrypoint exists and is not a directory. There is no long-lived process to probe.
- `Close` is a no-op: one-shot executions own their process lifecycle.

> [!WARNING]
> For process executors this is the **legacy** path. New process executors should prefer the gRPC transport.

---

## 3. The gRPC worker pool

When the frozen record declares `neuron/executor-v1`, the runtime maintains a pool of long-lived worker processes per executor type. Executions are leased to workers, and workers are reused across executions. This is the default for process executors because process startup is the dominant execution cost.

### 3.1 Worker startup and the handshake

Starting a worker is a three-step handshake bounded by a start timeout (30 seconds by default):

```mermaid
sequenceDiagram
    participant RT as Runtime
    participant EX as Executor process

    RT->>RT: create temp socket directory
    RT->>EX: spawn entrypoint with env
    Note over EX: NEURON_EXECUTOR_SOCKET, READY,<br/>PROTOCOL, TYPE, VERSION
    EX->>EX: start gRPC server
    EX->>EX: create readiness file
    RT->>RT: poll readiness file every 50 ms
    RT->>EX: dial Unix socket
    RT->>EX: Initialize(protocol, type, version)
    EX-->>RT: supported protocol + capabilities
    RT->>RT: worker marked healthy, joins pool
```

1. **Spawning.** The runtime creates a temporary socket directory and launches the entrypoint as a child process. The process environment carries:
   - `NEURON_EXECUTOR_PROTOCOL`, `NEURON_EXECUTOR_TYPE`, `NEURON_EXECUTOR_VERSION` describing the execution,
   - `NEURON_EXECUTOR_SOCKET` pointing at the executor's Unix domain socket,
   - `NEURON_EXECUTOR_READY` pointing at a readiness file the executor must create.
   
   The worker's working directory is set to the executor's install root when one is declared.

2. **Readiness.** The executor starts its gRPC server and creates the readiness file. The runtime polls for the file (every 50 ms) until it appears or the start timeout expires.

3. **Negotiation.** The runtime dials the Unix socket, sends an `Initialize` request carrying the declared protocol version plus the executor type and version, and the executor replies with its supported protocol version and capabilities. An empty reply version is rejected. On success the worker is marked healthy and joins the pool.

> [!NOTE]
> If any step fails, the process is shut down and the start fails with the specific failing stage. The runtime also removes a stale socket file before listening so a crash from a previous run never blocks startup.

### 3.2 Leasing executions

Each `Execute` call leases a worker rather than spawning a process:

1. The pool first tries to take an idle, healthy worker from its available channel.
2. If none is idle and the pool is under capacity, a new worker is started.
3. If the pool is at capacity, the request waits for a worker to become available, subject to the caller's context (cancellation propagates).

The worker executes the request over gRPC and is returned to the pool. A scope provided by the caller context becomes the gRPC deadline, so a canceled or timed-out execution aborts the RPC.

### 3.3 Concurrency and capacity

`maxWorkers` (from the manifest's `runtime.maxWorkers`, carried through the frozen record) bounds the number of concurrent worker processes per executor type. A value of 0 or unset uses the runtime default of **1**. Concurrency beyond one worker therefore requires an explicit manifest declaration.

The pool is created lazily: the first `Execute` starts the first worker. `Health` reports unhealthy while no healthy worker exists (an empty pool with no in-flight requests is healthy).

### 3.4 Health, restart, and shutdown

- **Health.** A worker reconnects to its socket on every request and is marked healthy after a successful initialization. `Health` reports the pool healthy when at least one worker is healthy.
- **Restart.** A worker that is found unhealthy is closed and replaced; requests are never sent to a known-unhealthy worker.
- **Graceful shutdown.** Closing the pool first calls `Shutdown` on every worker (with a 10-second bound), then closes the gRPC connection. The runtime waits for each process to exit, kills it only if it overruns the same bound, and clears the socket directory. Closing an already-closed pool is a no-op.

A pool is owned by the process runtime keyed by `type@version`. Recreating an instance for the same executor closes any previous pool for that key before installing the new one, so restarting an instance never leaks producers.

---

## 4. Deciding which transport to use

The transport is decided entirely by the frozen record's `protocol`:

| Declared protocol | Behavior |
| --- | --- |
| `neuron/executor-v1` | gRPC worker pool |
| `neuron/executor-v1-json` | One-shot JSON process |
| anything else | Start fails with an explicit unsupported-protocol error |
| empty | Normalized by the adapter layer to `neuron/executor-v1-json` for legacy compatibility |

> [!TIP]
> If the entrypoint does not exist, `Start` fails fast with a missing-entrypoint error instead of a confusing spawn failure.

---

## 5. Creating a process executor

### 5.1 A gRPC executor (recommended)

Use the Go SDK (`packages/executor-go`). Implement a `Handler` and call `executor.Serve`; the SDK starts the gRPC server, writes the readiness file, and answers the handshake. The same binary falls back to JSON mode when it is launched without a socket, which is exactly what WASI targets need.

Other languages can implement the `ExecutorService` gRPC contract directly; the schema is in `shared/protocol/executor/v1/executor.proto` and the protocol is language-independent.

The manifest declares the truth:

```json
{
  "apiVersion": "neuron/v1",
  "kind": "Executor",
  "metadata": { "name": "my:capability", "version": "1.0.0" },
  "runtime": { "type": "process", "entrypoint": "capability", "protocol": "neuron/executor-v1", "maxWorkers": 4 },
  "services": ["capability"],
  "platforms": { "linux-amd64": { "artifact": "capability-linux-amd64", "sha256": "..." } }
}
```

> [!NOTE]
> The `wasm`-targeted build of the same executor declares `neuron/executor-v1-json` instead; see [RUNTIME_WASM.md](./RUNTIME_WASM.md).

### 5.2 A one-shot JSON executor (legacy)

Any program that reads one JSON request from stdin and writes one JSON response to stdout can be a process executor. Set `protocol` to `neuron/executor-v1-json` in the manifest. The reference implementation in `examples/executors/echo` shows the exact wire shape and stays dependency-free.

---

## 6. Reference

- Implementation: `nore/internal/runtime/process`
- Contract: `shared/types/executor` (`Runtime`, `Instance`, `StartSpec`, `Request`, `Response`, protocol constants)
- gRPC schema: `shared/protocol/executor/v1/executor.proto`
- SDK: `packages/executor-go`
- Tests: `nore/internal/runtime/process/runtime_test.go` (round-trips on both transports, worker reuse, bounded concurrency, pool close)

---

## Related

| | |
| --- | --- |
| **Runtime deep dive** | How N.O.R.E. executes services end to end — [RUNTIME.md](./RUNTIME.md) |
| **WASM runtime** | The WASM executor backend — [RUNTIME_WASM.md](./RUNTIME_WASM.md) |
| **Modules & executors** | The unified module model — [MODULES.md](./MODULES.md) |