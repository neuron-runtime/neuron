# N.O.R.E. — Neuron Operational Runtime Engine

N.O.R.E. is the runtime engine of Neuron. It is where Systems are registered, instantiated, operated, and connected to the capabilities they require.

This reference is **maintainer-focused** and describes what N.O.R.E. is, how it is structured, how to run it, and how it behaves. Users interact with N.O.R.E. exclusively through the `neuron` CLI; see [application/README.md](../application/README.md).

---

## Table of Contents

- [Responsibilities](#responsibilities)
- [Running N.O.R.E.](#running-nore)
- [Flags](#flags)
- [Configuration & Environment](#configuration--environment)
- [Runtime Lifecycle](#runtime-lifecycle)
- [Internal Architecture](#internal-architecture)
- [Safe Shutdown](#safe-shutdown)
- [Security Model](#security-model)
- [Performance Characteristics](#performance-characteristics)
- [Further Reading](#further-reading)

---

## Responsibilities

N.O.R.E. owns the operational side of Neuron:

- **Registration** — receiving compiled System definitions from the CLI and persisting them.
- **Execution planning** — compiling the registered system and its frozen executor set into a plan over an event bus.
- **Instances** — creating, running, pausing, restarting, and removing living realizations of Systems.
- **Executors** — hosting and supervising modules, in-process for built-ins and out-of-process for external modules (process and WASM backends).
- **Scheduling** — advancing executions, evaluating connector mappings and validations, and handling cancellations.
- **Persistence** — durable storage of registered systems, instances, executions, and events through an interchangeable storage provider (SQLite by default).
- **API** — the local HTTP/JSON transport over which the CLI talks to the runtime.

N.O.R.E. deliberately does **not**:

- parse YAML or TypeScript,
- resolve or install external modules (the CLI does that and freezes the result),
- understand the business meaning of the modules it operates.

The canonical manifest is compiled by the CLI; N.O.R.E. receives the compiled core system representation.

---

## Running N.O.R.E.

In normal use you never run N.O.R.E. directly — the `neuron` CLI starts it automatically and talks to it over a Unix domain socket. The binary lives alongside the CLI in the same distribution archive.

To run it by hand (for development or debugging):

```bash
go build -o nore ./nore/cmd/nore
./nore
```

With no flags this binds a Unix socket (`~/.neuron/nore.sock`) and uses `~/.neuron/nore` as its data directory. The process prefers to live long and be supervised by the CLI, but it is a plain foreground process and shuts down cleanly on `SIGINT`/`SIGTERM`.

---

## Flags

```
-port string       TCP address for the N.O.R.E. API; empty disables TCP (default: Unix socket only)
-socket string     Unix socket for local CLI clients; empty disables Unix socket
                   (default: $NEURON_SOCKET or ~/.neuron/nore.sock)
-workers int       executor worker count (default 8)
-data-dir string   persistent data directory (default: $NEURON_DATA_DIR or ~/.neuron/nore)
-version           print the N.O.R.E. version and exit
```

At least one of `-port` or `-socket` must be configured.

### Defaults are local-only and safe

The default configuration is **Unix socket only** — no TCP listener is opened unless `-port` is supplied explicitly. The socket is created with mode `0600`, so only the owning user can connect to the runtime API.

Exposing N.O.R.E. over TCP (`-port :0` style) is opt-in and intended for development and remote operation where the deployment enforces its own network-level protection.

---

## Configuration & Environment

| Variable | Meaning |
| -------- | ------- |
| `NEURON_SOCKET` | Override the daemon Unix socket path |
| `NEURON_DATA_DIR` | Override the daemon persistent data directory |

Command-line flags take precedence over environment variables.

---

## Runtime Lifecycle

```text
start
  ├── initialize storage (SQLite)
  ├── recover persisted instances and their live executions
  ├── start executor runtimes (process / WASM backends)
  └── serve API on the configured listeners
      │
      ├── GET  /health               health check (used by the CLI bootstrap)
      ├── POST /v1/register          register a compiled System
      ├── GET  /v1/instances         list Instances
      ├── POST /v1/instances         create an Instance and begin execution
      ├── DELETE /v1/instances       remove all Instances
      ├── GET  /v1/instances/{id}    Instance detail
      ├── DELETE /v1/instances/{id}  remove an Instance
      ├── POST /v1/instances/{id}/executions           create an execution
      ├── GET  /v1/instances/{id}/executions           list executions
      ├── GET  /v1/instances/{id}/executions/{execID}  execution state
      ├── GET  /v1/instances/{id}/executions/{execID}/events        list events
      ├── GET  /v1/instances/{id}/executions/{execID}/events/stream stream events (Server-Sent Events)
      ├── WS   /v1/ws                 WebSocket endpoint for live event streaming
      └── ...                        (curl the API for the full shape)
stop
  ├── close listeners
  ├── gracefully stop live Instances (clean executor shutdown)
  └── close storage
```

### Execution

When an Instance is created, the planner compiles the registered System into an execution plan over the event bus. The scheduler advances the execution across the System's services and connectors, evaluates mappings and validations through the CEL resolver, and drives service executions through the executor layer. Every transition emits an event (started, completed, failed, cancelled); events are streamed to clients and persisted according to storage policy.

Live events are streamed over the WebSocket endpoint (`WS /v1/ws`); the SSE stream (`GET .../events/stream`) remains available for transports without WebSocket support.

Executions honor deadlines, support cancellation, and finish in a terminal state (`execution.completed`, `execution.failed`, `execution.cancelled`).

---

## Internal Architecture

```text
nore/
├── cmd/nore/                  daemon entry point (flags, listeners, shutdown)
└── internal/
    ├── api/                   HTTP/JSON API, middleware, route wiring
    ├── contracts/             internal interface boundaries (compiler, event bus, repositories)
    ├── data/                  internal data helpers
    ├── event/                 the event bus, event types, durable event log
    ├── execution/             execution model, state machine, scheduler, engine, snapshots
    ├── executors/             built-in in-process executor implementations
    ├── instance/              instance lifecycle manager, registry, restoration
    ├── planner/               compiler from the registered core System to an execution plan
    ├── plugin/                module plugin loading (WASM/process integration)
    ├── registry/              internal executor registry
    ├── resolver/              CEL expression resolver (connector mappings and validations)
    ├── runtime/               executor runtime abstraction (process + WASM backends)
    ├── storage/               storage provider interface + SQLite implementation
    ├── stream/                live stream plumbing for events and execution results
    ├── system/                registered system repository and indexing
    └── types/                 runtime-facing blueprints
```

### Key boundaries

- **`api`** owns transport only. It validates requests, calls into the instance manager and system repository, and serializes responses and streams. It never interprets system semantics.
- **`instance`** owns lifecycle: create/remove/clear, restoration on startup, and the registry of live instances.
- **`execution`** owns the state machine: scheduler transitions, snapshotting, wait-for-completion, and the executor engine that drives module calls.
- **`event`** owns the single source of truth for state transitions and provides the durable event log used for persistence and streaming.
- **`runtime`** owns the executor backend abstraction — one interface, multiple backends (process workers, WASM), with health checks, worker pooling, restart, and cancellation. The registry and installer never own process lifecycle; see [docs/RUNTIME_PROCESS.md](../docs/RUNTIME_PROCESS.md) and [docs/RUNTIME_WASM.md](../docs/RUNTIME_WASM.md).
- **`planner`** compiles the registered System + frozen executors into an executable plan. It is source-language agnostic and owns no HTTP clients, registries, or file downloads.
- **`storage`** is a provider interface; the SQLite implementation persists systems, instances, executions, and events. Verification tests exercise an in-memory provider.

### Built-in modules

N.O.R.E. ships a small set of in-process modules for common operations. They run inside the runtime engine and require no installation or resolution. Referencing one in a `neuron.yaml`/TS system is a plain module reference; N.O.R.E. dispatches it directly to the in-process implementation.

### External modules (executors)

External modules are hosted out-of-process. After the CLI resolves, verifies, and installs a module and freezes its exact version into the registered system, N.O.R.E. launches it through the matching runtime backend:

- **Process backend** — a long-lived native worker, spawned per instance, communicating over the Neuron executor protocol (gRPC over a Unix socket), with health checks, request deadlines, cancellation, and clean termination. See [docs/RUNTIME_PROCESS.md](../docs/RUNTIME_PROCESS.md).
- **WASM backend** — a WebAssembly module loaded into the runtime in an isolated context. See [docs/RUNTIME_WASM.md](../docs/RUNTIME_WASM.md).

The full module model, protocol, and archive contract live in [docs/MODULES.md](../docs/MODULES.md).

---

## Safe Shutdown

On `SIGINT`/`SIGTERM`, N.O.R.E. closes its listeners and then **gracefully stops all live instances** before exiting (`srv.StopInstances()`). This gives executor-backed resources — worker processes and WASM modules — a clean shutdown instead of being torn down mid-operation by process exit. Executions in-flight are flushed to their storage records before the engine exits, so they can be restored on the next startup.

---

## Security Model

- The default transport is a Unix socket with mode `0600` — local to the owning user, no network exposure.
- TCP is opt-in and the API currently has no authentication. **Do not expose a TCP listener on an untrusted network.**
- External executors are treated as untrusted code. They are verified and installed by the CLI before registration and are hosted out-of-process, isolating the runtime from third-party crashes and malicious behavior.
- Capability declarations in module manifests are metadata, not permissions. Permissions are enforced by the runtime backends.

See [AGENTS.md](../AGENTS.md) for the security principles that govern the whole repository.

---

## Performance Characteristics

Concurrency is bounded by the executor worker pool (`-workers`, default 8). Long-lived workers are reused across requests rather than respawning per invocation, which keeps warm execution latency dominated by the executor protocol call itself rather than process startup. For deeper discussion of pool sizing, cold-start, and throughput validation, see [docs/RUNTIME.md](../docs/RUNTIME.md).

---

## Further Reading

- [docs/RUNTIME.md](../docs/RUNTIME.md) — how the runtime works in depth (maintainer-focused)
- [docs/RUNTIME_PROCESS.md](../docs/RUNTIME_PROCESS.md) — the process executor backend
- [docs/RUNTIME_WASM.md](../docs/RUNTIME_WASM.md) — the WASM executor backend
- [docs/MODULES.md](../docs/MODULES.md) — the unified module model
- [application/README.md](../application/README.md) — the `neuron` CLI, the user-facing surface

---

## License

This application is part of Neuron, which is released under the MIT License. See [LICENSE](../LICENSE).