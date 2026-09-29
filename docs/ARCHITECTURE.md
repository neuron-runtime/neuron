# Architecture

Neuron is a runtime for building and operating complex software systems from composable, executable capabilities. This document describes how the platform is put together: the canonical model everything converges on, the boundaries that must never blur, and the data flow end to end.

> [!TIP]
> Short on time? Read [The core idea](#the-core-idea), [The canonical pipeline](#the-canonical-pipeline), and [What lives where](#what-lives-where).

---

## The core idea

Modern software is composed of applications, services, workers, libraries, queues, databases, APIs, and infrastructure. As systems grow, the hard part stops being any individual component and becomes making all of them work together as one coherent system.

Neuron's answer is to stop making the runtime understand what a component *is* and instead make it understand what a component *does* — and how it connects. Everything executable is a **capability**: described by a contract, reached through a relationship, and operated by a runtime that does not need to know how it is implemented.

> [!IMPORTANT]
> Software should be composed from things that can do something, connected by explicit relationships, and operated by a runtime that does not need to understand what those things are.

This leads to a deliberately small set of primitives:

| Primitive | Responsibility | What it never knows |
| --- | --- | --- |
| **Assembly** | Defines what exists and how it is connected | How capabilities are implemented |
| **Module** | An executable capability packaged for Neuron | The composition it belongs to |
| **Capability** | Exposes one executable capability | How the capability is executed |
| **Capability Runtime** | Provides the machinery that runs a Capability | The composition of the Assembly |
| **Binding** | Defines how two capabilities communicate | The business meaning of the data |
| **Instance** | A living realization of an Assembly | Implementation details of its Capabilities |
| **N.O.R.E.** | Operates registered Assemblies — instantiate, schedule, execute | What any capability actually means |

The separation between **Capability** and **Capability Runtime** is the load-bearing wall. It is what lets Neuron host capabilities implemented in any technology without turning the core into a collection of special cases: the Capability stays logical, the Capability Runtime stays mechanical, and the runtime only ever sees the capability runtime boundary.

---

## Design goals

In priority order:

1. **Correctness** — behavior is defined, bounded, and tested at the right boundary.
2. **Architectural integrity** — one authoritative place for every important domain behavior, no competing models.
3. **Isolation** — source languages never leak into the runtime; the runtime never parses author input.
4. **Security** — external capability runtimes are untrusted code and are hosted accordingly.
5. **Performance** — concurrency and worker reuse are first-class, measured, not assumed.
6. **Scalability** — the primitive model composes from one service to distributed systems.
7. **Maintainability** — packages own a single responsibility and are named after it.
8. **Clear public APIs** — the SDK, the capability runtime protocol, and the CLI are contracts.
9. **Testability** — behavior is exercised through stable boundaries.
10. **Documentation** — intent and constraints are explained where they matter.

---

## The canonical pipeline

All authoring surfaces converge on one canonical representation before anything language-, runtime-, or technology-specific happens:

```mermaid
flowchart TB
    A[Source language<br/>YAML · TypeScript · future] --> B[Language-specific loader]
    B --> C[Canonical assembly manifest]
    C --> D[Validation]
    D --> E[Compiler]
    E --> F[Core assembly]
    F --> G[Execution plan]
    G --> H[Runtime<br/>N.O.R.E.]
```

Nothing downstream of the **canonical manifest** knows how an assembly was authored. YAML assemblies and TypeScript assemblies produce the same manifest, compile through the same compiler, and run on the same runtime.

---

## What lives where

The repository is a monorepo organized into strictly separated Go modules:

| Path | Responsibility |
| --- | --- |
| `application/` | The `neuron` CLI — authoring, building, module resolution, client, daemon bootstrap |
| `nore/` | N.O.R.E. — the Neuron Operational Runtime Engine |
| `shared/` | Canonical types, capability runtime contract, version — agreed on by both Go modules |
| `packages/assembly-sdks/typescript/` | `@neuron/sdk` — TypeScript assembly-definition language |
| `packages/executor-sdks/golang/` | Go SDK for authoring Neuron modules (capability runtimes) |
| `packages/executor-sdks/dotnet/` | .NET SDK for authoring Neuron modules (`Neuron.Executor`) |
| `examples/` | Runnable example assemblies and reference modules |
| `docs/` | Architecture, getting started, installation, module, and runtime docs |
| `scripts/` | Workspace development and release helpers |

The important fact: `application` and `nore` are **separate Go modules** that only agree through the canonical types and protocol contracts in `shared`. The CLI never reaches into the runtime's internals; the runtime never reads YAML or TypeScript.

```mermaid
flowchart LR
    subgraph app["application module"]
        CLI[neuron CLI]
        BLD[builders<br/>yaml · typescript]
    end
    subgraph sh["shared module"]
        CAN[canonical types]
        PRO[capability runtime protocol]
        VER[version]
    end
    subgraph nore["nore module"]
        NOR[N.O.R.E.]
    end
    CLI -. reads canonical .-> CAN
    BLD -. emits canonical .-> CAN
    NOR -. consumes canonical .-> CAN
    NOR -. implements .-> PRO
```

The version reported by `neuron version` and `nore --version` comes from a single source, `shared/version`, stamped at build time via `-ldflags`.

---

## Assembly definition

An **Assembly** is a composition of capabilities and the explicit relationships between them. The canonical YAML form mirrors the manifest structure in `examples/ecommerce_order`:

```yaml
# assemblies/order-processing/assembly.yaml
apiVersion: neuron/v1
kind: Assembly

metadata:
  name: order-processing
  version: 1.0.0
  description: Order processing pipeline

capabilities:
  - ref: validate-order
    entry: ../../capabilities/validate-order.yaml
  - ref: parse-order
    entry: ../../capabilities/parse-order.yaml

bindings:
  - from: validate-order
    to: parse-order
    mappings:
      - target: validation_data
        expression: "source.result"
    validations:
      - expression: "source.result.valid == true"
        message: "Order validation failed"
```

A **Capability** names the logical unit and the module that provides it:

```yaml
# capabilities/validate-order.yaml
apiVersion: neuron/v1
kind: Capability

metadata:
  name: validate-order
  version: 1.0.0
  description: Validate incoming order request

spec:
  capability runtime:
    type: neuron:core:set

  config:
    status: validated
    valid: true

  mappings:
    - direction: input
      source: execution.params.order
      target: order

  execution:
    mode: wait
    timeout: 5s
```

Execution flows along the bindings. Each binding defines what data flows between the two capabilities (`mappings`, expressed in CEL) and optionally which conditions must hold (`validations`). Execution params are available to expressions as `execution.params`; the upstream capability's result as `source.result`.

---

## Authoring surfaces

### YAML

The YAML surface is the canonical, zero-tooling authoring experience. A project is a directory with `neuron.config.yaml` pointing at a `kind: Assembly` entry file (`assembly.yaml` by default). `neuron init --lang yaml` scaffolds it, and `neuron build` consumes it. See `examples/ecommerce_order` for a complete project.

### TypeScript — the SDK

The TypeScript SDK (`@neuron/sdk`) is the same capability: a typed, always-autocompleted way to describe assemblies in TypeScript, converging on the same canonical manifest.

```ts
// assembly.ts
import { Capability, Assembly } from "@neuron/sdk";

const validate = Capability({
  name: "validate-order",
  version: "1.0.0",
  description: "Validate an incoming order",
})
  .capabilityRuntime({ name: "neuron:core:set" })
  .paramsSchema<{ order: object }>()
  .resultSchema<{ order: object; valid: boolean }>();

export default Assembly({
  name: "order-processing",
  version: "1.0.0",
})
  .paramsSchema<{ order: object }>()
  .withParams((data) =>
    validate.withParams({ order: data.order })
  )
  .toManifest();
```

The SDK is a **definition tool**. It describes assemblies; it does not execute them, and it must never become a runtime. The Go side remains responsible for parsing, validating, compiling, and running the canonical representation. See [packages/assembly-sdks/typescript/README.md](../packages/assembly-sdks/typescript/README.md).

---

## Building & loading

Each authoring surface has a dedicated loader in `application` (`application/build/yaml`, `application/build/typescript`). A loader's only job is to translate its source language into the **canonical manifest** — plus project-level concerns (paths, variables, entry points).

The pipeline in `neuron build`:

```mermaid
flowchart TB
    A[project<br/>neuron.config.*] --> B[loader<br/>per authoring language]
    B --> C[canonical manifest<br/>.neuron/manifest.json]
    C --> D[validator]
    D --> E[compiler<br/>core.Assembly]
    E --> F[capability runtime resolution + freezing]
    F --> G[N.O.R.E. registration<br/>POST /v1/register]
```

The validator and compiler are language-agnostic: they consume and emit canonical structures. YAML-specific quirks stay inside the YAML loader; TypeScript-specific quirks stay inside the TS loader.

The TypeScript loader delegates the actual build to the SDK CLI (`neuron-sdk build --path <root> --entry <file>`), which executes the entry module (default `index.ts`) and writes `.neuron/manifest.json` — then the same canonical path continues. The entry file is selected the same way for both authors: the project's `neuron.config.*` `entry` field.

---

## The compiler boundary

The compiler transforms the canonical manifest into the runtime/core structures N.O.R.E. consumes.

> [!WARNING]
> The compiler MUST remain source-language agnostic. It MUST NOT parse YAML or TypeScript, resolve GitHub repositories, download or install capability runtimes, launch processes, execute capabilities, own HTTP clients, or contain registry-specific behavior.

Those responsibilities belong to their own layers. The compiler is pure transformation: canonical manifest in, core assembly out.

---

## The module & capability runtime model

A module is the public word for a capability packaged for Neuron. Within the model, a capability runtime is distilled through a chain of distinct responsibilities:

```mermaid
flowchart TB
    A[Capability Runtime Requirement<br/>who + what version constraint] --> B[Capability Runtime Registry<br/>where packages are obtained]
    B --> C[Capability Runtime Resolver<br/>which version satisfies the requirement]
    C --> D[Capability Runtime Package<br/>immutable, self-describing artifact set]
    D --> E[Verification<br/>cryptographic checks]
    E --> F[Capability Runtime Installation<br/>install the selected artifact securely]
    F --> G[Capability Runtime Store<br/>where the immutable artifact lives]
    G --> H[Capability Runtime<br/>process · wasm · future]
    H --> I[Capability Runtime Instance<br/>a live running realization]
```

No two steps merge:

| Step | Answers | Owns |
| --- | --- | --- |
| **Registry** | *Where* | Providers (`github`, `local`, future registries) |
| **Resolver** | *Which* | Best version by semantic versioning, above the providers; prefers already-installed artifacts so running instances stay independent of the network |
| **Installer** | *How* | Verifying and installing the selected package archive |
| **Store** | *Where it lives* | The immutable installed artifact, keyed by name and exact version, under `~/.neuron/capabilityRuntimes` |
| **Runtime** | *How it executes* | Spawning, supervising, and terminating capability runtime workers |

The CLI runs this pipeline during `neuron build` and then **freezes** the exact resolved versions into the build record. N.O.R.E. therefore never resolves modules itself — it receives a closed set of resolved capability runtimes and launches instances from them.

Built-in modules are the exception that proves the rule: they run in-process inside N.O.R.E., so resolution skips them and the runtime dispatches them directly. See [docs/MODULES.md](./MODULES.md) for the full model.

---

## N.O.R.E. — the runtime engine

**N.O.R.E. (Neuron Operational Runtime Engine)** is the daemon that registers assemblies, creates instances, executes them, and persists their records.

```mermaid
flowchart TB
    API[API<br/>HTTP/JSON over Unix socket · TCP opt-in<br/>WebSocket for live events] --> IM[Instance Manager]
    API --> SR[Assembly Repository]
    IM --> EE[Execution Engine]
    EE --> EB[Event Bus]
    EE --> ER[Capability Runtimes]
    EB --> ST[Storage<br/>provider interface · SQLite]
    EE --> RE[CEL Resolver]
    ER --> PROC[Process backend]
    ER --> WASM[WASM backend]
```

The runtime boundary inside N.O.R.E. is the **capability runtime** abstraction. One interface, multiple backends:

```mermaid
flowchart TB
    ER[Capability Runtime] --> PROC[Process Runtime<br/>long-lived workers · gRPC over Unix socket]
    ER --> WASM[WASM Runtime<br/>WASI modules in an isolated context]
    ER -. future .-> CONT[Container Runtime]
    ER -. future .-> REM[Remote Runtime]
```

A backend owns starting the capability runtime, connecting to it, health checking, executing requests, cancellation, deadlines, termination, and restart. The registry, installer, and compiler own none of that.

### Instance lifecycle

An Assembly definition is static. An **Instance** is a living realization with its own state. Instances are created from a registered assembly, and each creation plans and runs the assembly across its capabilities:

```mermaid
flowchart LR
    A[register assembly] --> B[create instance]
    B --> C[plan execution]
    C --> D[schedule over event bus]
    D --> E[execute capabilities through capability runtimes]
    E --> F[terminal execution state]
    F --> G[events streamed over WebSocket · SSE fallback]
```

Instances survive runtime restarts: on startup, N.O.R.E. restores persisted instances and their in-flight executions from storage.

### Safe shutdown

On `SIGINT`/`SIGTERM`, N.O.R.E. closes listeners and **gracefully stops live instances** before exiting, so capability-runtime-backed resources (worker processes, WASM modules) receive a clean shutdown.

---

## The capability runtime protocol

The runtime never assumes a capability runtime is written in Go, compiled to WASM, or launched as a process. Capability runtimes speak a **stable, language-neutral protocol**:

| Protocol | Transport | Workers |
| --- | --- | --- |
| `neuron/capability-runtime-v1` | gRPC (protobuf) | Long-lived workers |
| `neuron/capability-runtime-v1-json` | Line-delimited JSON over stdio | One-shot workers |

The gRPC surface is defined in `shared/protocol/capabilityruntime/v1`. It covers handshake, protocol version, identity, capabilities, initialization, execution, structured input/output/errors, cancellation, deadlines, and health. For a simple one-shot module, the JSON variant keeps the barrier to entry at "read a line, write a line."

The transport detail lives behind the capability runtime abstraction, which is why the same logical module can be hosted as a process or as WASM without the rest of the system caring. Authoring a capability runtime is covered by the Go SDK in `packages/executor-sdks/golang` and by the .NET SDK in `packages/executor-sdks/dotnet`; a reference module is shipped in `examples/capability-runtimes/echo`, compiled for both runtimes from the same source.

---

## Persistence

N.O.R.E. persists registered assemblies, instances, executions, and the event log through a small storage provider interface (`storage.Store`), implemented today by SQLite under the configured data directory (`~/.neuron/nore`).

Executions are kept in a memory-backed store that mirrors records into persistent storage, so live executions are fully in-memory for speed and durable for recovery. On restart, N.O.R.E. restores persisted instances and their in-flight executions.

Execution history and retention are intended to become a **configurable storage policy** (`none | memory | local`) so retention is an operational choice rather than a fixed behavior — and so execution semantics never depend on persistent storage being enabled. The pluggable store is in place; the policy is tracked in [TODO.md](../TODO.md).

---

## Security & isolation

- **Default transport is local.** N.O.R.E. listens on a Unix socket (mode `0600`) owned by the local user, in a directory created with mode `0700`.
- **The API is authenticated.** Every route except the health probe requires the daemon's API token as `Authorization: Bearer <token>`, compared in constant time. The daemon publishes the token in `<socket>.token` (mode `0600`) and generates one when none exists, so a local client needs no configuration. TCP is opt-in and refuses to start without a token; the token authenticates the caller but does not encrypt traffic, so a TCP listener belongs behind TLS termination.
- **External capability runtimes are untrusted.** They are verified by digest before install, hosted out-of-process, and never loaded into the N.O.R.E. address space (with the deliberate exception of modules shipped as part of N.O.R.E. itself).
- **Capabilities are metadata, not permissions.** A module manifest may declare capabilities; the runtime enforces actual permissions at the execution boundary.
- **GitHub is a distribution source, not a security boundary.** Release artifacts are verified by digest before installation.

---

## Performance principles

- Concurrency is bounded by a capability runtime worker pool; long-lived workers are reused across requests rather than respawned per call.
- Resolution prefers already-installed artifacts, so instance execution stays local and offline once modules are in the store.
- Optimization follows measurement, not assumption — see [docs/RUNTIME.md](./RUNTIME.md).

> [!NOTE]
> Do not prematurely introduce distributed infrastructure; the local model is the base case, and distribution is an explicit, incremental option.

---

## Related

| | |
| --- | --- |
| **Modules & capability runtimes** | The unified module model in detail — [docs/MODULES.md](./MODULES.md) |
| **Runtime deep dive** | The runtime execution model (maintainer-focused) — [docs/RUNTIME.md](./RUNTIME.md) |
| **CLI** | The `neuron` CLI — [application/README.md](../application/README.md) |
| **Runtime engine** | N.O.R.E. reference (maintainer-focused) — [nore/README.md](../nore/README.md) |
| **TypeScript SDK** | The assembly-definition language — [packages/assembly-sdks/typescript/README.md](../packages/assembly-sdks/typescript/README.md) |
| **Getting started** | Build and run your first assembly — [docs/GETTING_STARTED.md](./GETTING_STARTED.md) |