# Architecture

Neuron is a runtime for building and operating complex software systems from composable, executable capabilities. This document describes how the platform is put together: the canonical model everything converges on, the boundaries that must never blur, and the data flow end to end.

Readers in a hurry can start with [The core idea](#the-core-idea), [The canonical pipeline](#the-canonical-pipeline), and [What lives where](#what-lives-where).

---

## Table of Contents

- [The core idea](#the-core-idea)
- [Design goals](#design-goals)
- [The canonical pipeline](#the-canonical-pipeline)
- [What lives where](#what-lives-where)
- [System definition](#system-definition)
- [Authoring surfaces](#authoring-surfaces)
- [Building & loading](#building--loading)
- [The compiler boundary](#the-compiler-boundary)
- [The module & executor model](#the-module--executor-model)
- [N.O.R.E. — the runtime engine](#nore--the-runtime-engine)
- [The executor protocol](#the-executor-protocol)
- [Persistence](#persistence)
- [Security & isolation](#security--isolation)
- [Performance principles](#performance-principles)
- [Governance](#governance)

---



## The core idea

Modern software is usually assembled from applications, services, workers, libraries, queues, databases, APIs, and infrastructure. As systems grow, the hard part stops being any individual component and becomes making all of them work together as one coherent system.

Neuron's answer is to stop making the runtime understand what a component *is* and instead make it understand what a component *does* — and how it connects. Everything executable is a **capability**. A capability is described by a contract, reached through a relationship, and operated by a runtime that does not need to know how it is implemented.

> Software should be composed from things that can do something, connected by explicit relationships, and operated by a runtime that does not need to understand what those things are.

This leads to a deliberately small set of primitives:


| Primitive     | Meaning                                                                                 |
| ------------- | --------------------------------------------------------------------------------------- |
| **System**    | A definition — what capabilities exist and how they are connected                       |
| **Module**    | An executable capability packaged for Neuron (a Service, or the Executor that runs one) |
| **Service**   | The logical capability — what can be done                                               |
| **Executor**  | The machinery that provides the capability — how it is run                              |
| **Connector** | How two capabilities communicate                                                        |
| **Instance**  | A living realization of a System, with its own state and activity                       |
| **N.O.R.E.**  | The runtime engine — where Systems are registered, instantiated, and executed           |


The separation between Service and Executor is the load-bearing wall. It is what lets Neuron host capabilities implemented in any technology without turning the core into a collection of special cases: the Service stays logical, the Executor stays mechanical, and the runtime only ever sees the executor boundary.

---



## Design goals

In priority order:

1. **Correctness** — behavior is defined, bounded, and tested at the right boundary.
2. **Architectural integrity** — one authoritative place for every important domain behavior, no competing models.
3. **Isolation** — source languages never leak into the runtime; the runtime never parses author input.
4. **Security** — external executors are untrusted code and are hosted accordingly.
5. **Performance** — concurrency and worker reuse are first-class, measured, not assumed.
6. **Scalability** — the primitive model composes from one service to distributed systems.
7. **Maintainability** — packages own a single responsibility and are named after it.
8. **Clear public APIs** — the SDK, the executor protocol, and the CLI are contracts.
9. **Testability** — behavior is exercised through stable boundaries.
10. **Documentation** — intent and constraints are explained where they matter.

---



## The canonical pipeline

All authoring surfaces converge on one canonical representation before anything language-, runtime-, or technology-specific happens:

```text
Source Language (YAML, TypeScript, ...)
    ↓
Language-Specific Loader
    ↓
Canonical System Manifest
    ↓
Validation
    ↓
Compiler
    ↓
Core System
    ↓
Execution Plan
    ↓
Runtime
```

Nothing downstream of the **canonical manifest** knows how a system was authored. YAML systems and TypeScript systems produce the same manifest, compile through the same compiler, and run on the same runtime.

---



## What lives where

The repository is a monorepo organized into strictly separated Go modules:

```text
nuron/
├── application/       the neuron CLI: authoring, building, resolution, client, daemon bootstrap
├── nore/              N.O.R.E.: the runtime engine
├── shared/            canonical types, executor contract, version — agreed through by both
├── packages/
│   ├── sdk/           TypeScript SDK (@neuron/sdk) — a system-definition language
│   └── executor-go/   Go SDK for authoring Neuron modules (executors)
├── examples/          runnable example systems and reference modules
├── docs/              architecture, getting started, installation, module, and runtime docs
└── scripts/           workspace development and release helpers
```

The important fact: `application` and `nore` are **separate Go modules** that only agree through the canonical types and protocol contracts in `shared`. The CLI never reaches into the runtime's internals; the runtime never reads YAML or TypeScript.

Go workspace:

```text
go.work
./nore
./application
./shared
./packages/executor-go
examples/simple_response
```

The version reported by `neuron version` and `nore --version` comes from a single source, `shared/version`, stamped at build time via `-ldflags`.

---



## System definition

A **System** is a composition of capabilities and the explicit relationships between them.

```yaml
# systems/order-processing/system.yaml
apiVersion: neuron/v1
kind: System

metadata:
  name: order-processing
  version: 1.0.0

services:
  - ref: validate-order
    entry: ../services/validate-order.yaml
  - ref: parse-order
    entry: ../services/parse-order.yaml

connectors:
  - from: validate-order
    to: parse-order
    mappings:
      - target: validation_data
        expression: "source.output"
    validations:
      - expression: "source.output.valid == true"
        message: "Order validation failed"
```

A **Service** names the logical capability and the module that provides it:

```yaml
apiVersion: neuron/v1
kind: Service

metadata:
  name: parse-order
spec:
  executor:
    type: example:parse@^1.0.0
```

Execution flows along the connectors. Each connector defines what data flows between the two modules (`mappings`, expressed in CEL) and optionally which conditions must hold (`validations`). Execution input is available to expressions as `execution.input`; the source module's output as `source.output`.

---



## Authoring surfaces



### YAML

The YAML surface is the canonical, zero-tooling authoring experience. A project is a directory with `neuron.yaml`, `systems/`, `services/`, and `connectors/`. `neuron init` scaffolds it, and `neuron register` consumes it.

### TypeScript — the SDK

The TypeScript SDK (`@neuron/sdk`) is the same capability: a typed, always-autocompleted way to describe systems in TypeScript.

```typescript
import { Service, System } from "@neuron/sdk";

const validate = new Service("validate")
  .input({ order: "object" })
  .output({ valid: "boolean" });

export default new System("order-processing")
  .add(validate)
  .connect(validate, "output", "next");
```

The SDK is a **definition tool**. It describes systems; it does not execute them, and it must never become a runtime. The Go side remains responsible for parsing, validating, compiling, and running the canonical representation.

---



## Building & loading

Each authoring surface has a dedicated loader in `application` (`application/build/yaml`, `application/build/typescript`). A loader's only job is to translate its source language into the **canonical manifest** — plus project-level concerns (paths, variables, entry points).

The pipeline in `neuron register`:

```text
project (neuron.yaml)
    ↓
loader (per authoring language)
    ↓
canonical manifest (.neuron/manifest.json)
    ↓
validator
    ↓
compiler → core.System
    ↓
executor resolution + freezing
    ↓
N.O.R.E. registration (POST /v1/register)
```

The validator and compiler are language-agnostic: they consume and emit canonical structures. YAML-specific quirks stay inside the YAML loader; TypeScript-specific quirks stay inside the TS loader.

---



## The compiler boundary

The compiler transforms the canonical manifest into the runtime/core structures N.O.R.E. consumes.

The compiler MUST remain source-language agnostic and MUST NOT:

- parse YAML, parse TypeScript, or own any source-language logic;
- resolve GitHub repositories, download, install, or launch executors;
- own HTTP clients, registries, or registry-specific behavior.

Those responsibilities belong to their own layers. The compiler is pure transformation: canonical manifest in, core system out.

---



## The module & executor model

A module is the public word for a capability packaged for Neuron. Within the model, an executor is distilled through a chain of distinct responsibilities:

```text
Executor Requirement    what the system needs (logical name + version constraint)
        ↓
Executor Registry       where packages can be obtained
        ↓
Executor Resolver       which version satisfies the requirement
        ↓
Executor Package        the immutable, self-describing artifact set
        ↓
Verification            cryptographic checks before anything untrusted runs
        ↓
Executor Installation   install the selected artifact securely
        ↓
Executor Store          where the immutable installed artifact lives
        ↓
Executor Runtime        how the artifact is executed (process, wasm, ...)
        ↓
Executor Instance       a live, running realization
```

No two steps merge:

- The **registry** answers *where* — it is a provider (`github`, `local`, future registries).
- The **resolver** answers *which* — it selects the best version with semantic versioning, above the providers, and prefers already-installed artifacts so running instances stay independent of the network.
- The **installer** answers *how* — it verifies and installs the selected package archive.
- The **store** answers *where the installed artifact lives* — immutably, keyed by name and exact version, under `~/.neuron/executors`.
- The **runtime** answers *how the installed artifact executes* — spawning, supervising, and terminating executor workers.

The CLI runs this resolution pipeline during `neuron register` and then **freezes** the exact resolved versions into the registration. N.O.R.E. therefore never resolves modules itself — it receives a closed set of `ResolvedExecutor`s and launches instances from them.

Built-in modules are the exception that proves the rule: they run in-process inside N.O.R.E., so resolution skips them and the runtime dispatches them directly. See [docs/MODULES.md](./MODULES.md) for the full model.

---



## N.O.R.E. — the runtime engine

**N.O.R.E. (Neuron Operational Runtime Engine)** is the daemon that registers systems, creates instances, executes them, and persists their records.

```text
N.O.R.E.
├── API (HTTP/JSON over Unix socket — TCP opt-in; WebSocket for live event streaming)
├── Instance Manager       live instances, their restoration and lifecycle
├── Execution Engine       planner/compiler, scheduler, executor engine
├── Event Bus              the single source of truth for state transitions
├── Executor Runtimes      process backend, wasm backend, in-process modules
├── Resolver (CEL)         connector mappings and validations
└── Storage                provider interface; SQLite implementation
```

The runtime boundary inside N.O.R.E. is the **executor runtime** abstraction. One interface, multiple backends:

```text
Executor Runtime
    ├── Process Runtime   long-lived worker processes, gRPC over Unix socket
    ├── WASM Runtime      WASI modules hosted in an isolated context
    └── (future) Container, Remote
```

A backend owns starting the executor, connecting to it, health checking, executing requests, cancellation, deadlines, termination, and restart. The registry, installer, and compiler own none of that.

### Instance lifecycle

A System definition is static. An **Instance** is a living realization with its own state. Instances are created from a registered system, and each creation runs the system across its services:

```text
register system → create instance → plan execution → schedule over event bus
   → execute services through the executor runtimes → terminal execution state
   → events streamed to the client over WebSocket (SSE fallback) and retained per storage policy
```

Instances survive runtime restarts: on startup, N.O.R.E. restores persisted instances and their in-flight executions from storage.

### Safe shutdown

On `SIGINT`/`SIGTERM`, N.O.R.E. closes listeners and **gracefully stops live instances** before exiting, so executor-backed resources (worker processes, WASM modules) receive a clean shutdown.

---



## The executor protocol

The runtime never assumes an executor is written in Go, compiled to WASM, or launched as a process. Executors speak a **stable, language-neutral protocol**:

```text
neuron/executor-v1          gRPC (protobuf) — long-lived workers
neuron/executor-v1-json     line-delimited JSON over stdio — one-shot workers
```

The gRPC surface is defined in `shared/protocol/executor/v1`. It covers identity, capabilities, initialization, execution, structured input/output/errors, cancellation, deadlines, and health. For a simple one-shot module, the JSON variant keeps the barrier to entry at "read a line, write a line."

The transport detail lives behind the executor runtime abstraction, which is why the same logical module can be hosted as a process or as WASM without the rest of the system caring. Authoring an executor is covered by the Go SDK in `packages/executor-go`; a reference module is shipped in `examples/executors/echo` (compiled for both runtimes from the same source).

---



## Persistence

N.O.R.E. persists registered systems, instances, executions, and the event log through a small storage provider interface (`storage.Store`), implemented today by SQLite under the configured data directory (`~/.neuron/nore`).

Executions are kept in a memory-backed store that mirrors records into persistent storage, so live executions are fully in-memory for speed and durable for recovery. On restart, N.O.R.E. restores persisted instances and their in-flight executions.

Execution history and retention are intended to become a **configurable storage policy** (`none | memory | local`) so retention is an operational choice rather than a fixed behavior — and so execution semantics never depend on persistent storage being enabled. That policy is tracked in [TODO.md](../TODO.md); the mechanism (a pluggable store) is in place.

---



## Security & isolation

- **Default transport is local.** N.O.R.E. listens on a Unix socket (mode `0600`) owned by the local user. TCP is opt-in and the API is unauthenticated — exposing it over an untrusted network is unsupported.
- **External executors are untrusted.** They are verified by digest before install, hosted out-of-process, and never loaded into the N.O.R.E. address space (with the deliberate exception of modules shipped as part of N.O.R.E. itself).
- **Capabilities are metadata, not permissions.** A module manifest may declare capabilities; the runtime enforces actual permissions at the execution boundary.
- **GitHub is a distribution source, not a security boundary.** Release artifacts are verified by digest before installation.

---



## Performance principles

- Concurrency is bounded by an executor worker pool; long-lived workers are reused across requests rather than respawned per call.
- Resolution prefers already-installed artifacts, so instance execution stays local and offline once modules are in the store.
- Optimization follows measurement, not assumption, see [docs/RUNTIME.md](./RUNTIME.md).

Do not prematurely introduce distributed infrastructure; the local model is the base case, and distribution is an explicit, incremental option.

---



## Governance

Every change to this repository must preserve the boundaries described here. The engineering contract in [AGENTS.md](../AGENTS.md) enumerates them: no source-language logic in the runtime, no registry logic in the compiler, no installer logic in the runtime, no CLI concerns in domain code, one authoritative implementation per behavior, and no dead code.

Additions must answer three questions before they exist:

1. Which boundary requires this responsibility?
2. Which existing abstraction already solves the problem?
3. Which package will owners expect to find it in?

If no answer is sound, the change does not happen yet.

---



## Related

- [docs/MODULES.md](./MODULES.md) — the unified module model in detail
- [docs/RUNTIME.md](./RUNTIME.md) — the runtime deep dive (maintainer-focused)
- [application/README.md](../application/README.md) — the CLI
- [nore/README.md](../nore/README.md) — the runtime engine reference (maintainer-focused)
- [packages/sdk/README.md](../packages/sdk/README.md) — the TypeScript SDK
- [AGENTS.md](../AGENTS.md) — the engineering contract

