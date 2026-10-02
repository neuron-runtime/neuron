# Neuron

![Brand Logo](https://raw.githubusercontent.com/neuron-runtime/.github/main/profile/brand.png)

**A runtime for building and operating complex software systems from composable, executable capabilities.**

Neuron treats software as a composition of **capabilities** connected by **explicit relationships** and operated by a runtime that does not need to understand what those capabilities are.

> Software should be composed from things that can do something, connected by explicit relationships, and operated by a runtime that does not need to understand what those things are.

[Version](https://github.com/neuron-runtime/neuron/releases)
[Go](https://go.dev)
[TypeScript](https://www.typescriptlang.org)
[License](./LICENSE)

---



## The Runtime Model

Neuron defines a deliberately small set of primitives. Each one owns a single responsibility and deliberately ignores everything else:


| Primitive            | Responsibility                                                   | What it deliberately never knows           |
| -------------------- | ---------------------------------------------------------------- | ---------------------------------------- |
| **Assembly**         | Defines what exists and how it is connected                      | How capabilities are implemented           |
| **Capability**       | Exposes one executable capability                                | How the capability is executed             |
| **Binding**          | Defines how two capabilities communicate                         | The business meaning of the data           |
| **Capability Runtime** | Provides the machinery that runs a Capability                  | The composition of the Assembly            |
| **Instance**         | A living realization of an Assembly                              | Implementation details of its Capabilities |
| **N.O.R.E.**         | Operates registered Assemblies — instantiate, schedule, execute  | What any capability actually means         |


N.O.R.E. (**Neuron Operational Runtime Engine**) is the runtime at the center. An Assembly describes what should exist; N.O.R.E. makes it operational — and hosts each capability through a **Capability Runtime boundary** that keeps the runtime independent of any single technology.

```mermaid
flowchart TB
    Sys[Assembly] --> NOR[N.O.R.E. — Neuron Operational Runtime Engine]
    NOR --> MP[In-process built-in modules]
    NOR --> PR[Process Runtime]
    NOR --> WR[WASM Runtime]
    NOR --> CR[Container Runtime]
    NOR --> RR[Remote Runtime]

    MP --> SET[built-in: set / log / delay / command]
    PR --> PROCS[Native capability runtime processes]
    WR --> WASMI[wasm32-wasi modules]

    CR -. planned .-> OCI[OCI images]
    RR -. planned .-> REMOTE[Remote capability runtime hosts]
```



> [!IMPORTANT]
> Neuron is **not** a workflow engine. A workflow is one thing an Assembly can represent — it is not the boundary of the platform. The fundamental abstraction is an Assembly of capabilities and explicit relationships.

A Capability does not have to be a "microservice". A database query, an HTTP call, a model prediction, a browser automation task, a WebAssembly module, a native program, or another Assembly can all be capabilities. What matters is the **contract** describing what a capability provides and how it can be reached.

> [!NOTE]
> A Capability is not necessarily a process, function, API, or worker. A Binding is a relationship, not necessarily an HTTP request.

---



## Build an Assembly

Assemblies are defined in TypeScript with the (`@neuron/sdk`)`[@neuron/sdk](https://github.com/neuron-runtime/neuron/blob/main/packages/assembly-sdks/typescript)` — a typed, autocompleted assembly-definition language — or in YAML. Both authoring surfaces converge on the **same canonical manifest** before anything runtime-specific happens.

Here is a real order-fulfillment definition. Three independent capabilities, wired by explicit relationships, executed by the Neuron runtime:

```ts
import { Capability, Assembly } from "@neuron/sdk";

type Address = { street: string; city: string; zip: string };
type OrderItem = { sku: string; name: string; qty: number; priceCents: number };
type Order = {
  id: string;
  customerId: string;
  customerEmail: string;
  currency: string;
  totalCents: number;
  items: OrderItem[];
  shippingAddress: Address;
};

const validateOrder = Capability({
  name: "order.validate",
  version: "1.0.0",
  description: "Validate an incoming order",
})
  .runtime({ name: "neuron:core:set" })
  .paramsSchema<{ order: Order }>()
  .resultSchema<{ order: Order; valid: boolean }>();

const authorizePayment = Capability({
  name: "payment.authorize",
  version: "1.0.0",
  description: "Authorize payment for an order",
})
  .runtime({ name: "neuron:core:set" })
  .paramsSchema<{ order: Order; amountCents: number; currency: string }>()
  .resultSchema<{ order: Order; amountCents: number }>();

const createShipment = Capability({
  name: "fulfillment.create-shipment",
  version: "1.0.0",
  description: "Create a shipment for a paid order",
})
  .runtime({ name: "neuron:core:set" })
  .paramsSchema<{ order: Order }>()
  .resultSchema<{ order: Order; trackingId: string }>();

const manifest = Assembly({
  name: "order-fulfillment",
  version: "1.0.0",
  description: "Validate, authorize, and fulfill customer orders",
})
  .paramsSchema<{ order: Order }>()
  .withParams((data) =>
    validateOrder
      .withParams({ order: data.order })
      .bind(
        authorizePayment.withParams({
          order: validateOrder.result.order,
          amountCents: validateOrder.result.order.totalCents,
          currency: validateOrder.result.order.currency,
        })
      )
      .bind(
        createShipment.withParams({
          order: authorizePayment.result.order,
        }),
        {
          when: authorizePayment.result.amountCents.greaterThanOrEqualTo(1000),
          message: "Payment authorization threshold not met",
        }
      )
  )
  .toManifest();

export default manifest;
```



### What this definition establishes

```mermaid
flowchart LR
    A[Order received] --> B[order.validate]
    B -->|order, amount| C[payment.authorize]
    C -->|amount >= 1000| D[fulfillment.create-shipment]
    C -.->|amount < 1000| X[Execution rejected]
    D --> E[Confirmed]
```



1. **Three independently executable capabilities** — `order.validate`, `payment.authorize`, `fulfillment.create-shipment`. Neuron does not require them to share an implementation language, process, deployment model, or infrastructure.
2. **Explicit relationships** — `.bind()` chains data from one capability to the next; `.withParams()` maps output fields into the next capability's params contract; `{ when: ... }` guards whether a step runs at all. Execution edges are derived from these references: capabilities reading the same source run in parallel, and independent entries need no explicit operator.
3. **A stationary boundary** — the Assembly defines *what* and *how things connect*. The Capability Runtimes determine *how each capability actually runs*: in-process, as a native worker process, or as a WebAssembly module.

The complete, runnable version of this pipeline ships in `examples/ecommerce_order_ts`. The same logic expressed in YAML lives in `examples/ecommerce_order`.

---



## Why Neuron

The boundary Neuron introduces is between the **definition of an assembly** and the **mechanisms that execute it**.


| Without a runtime model                 | With Neuron                                     |
| --------------------------------------- | ----------------------------------------------- |
| Application-specific integration code   | Explicit Capability contracts                   |
| Implementation leaks into composition   | A stable Capability Runtime boundary            |
| Runtime coupled to one technology stack | Runtime operates contracts, not implementations |
| A single deployment model               | Multiple capability runtimes (process, WASM, …) |
| Implicit, ad-hoc relationships          | Explicit Bindings                               |
| Configuration-heavy composition         | Typed Assembly definitions                      |


Because capabilities are reached through contracts, the same Assembly can run entirely locally today and distribute individual capabilities later — the physical location never has to redefine the logical architecture.

```mermaid
flowchart LR
    subgraph Local
        A[Assembly] --> B[In-process capability]
        A --> C[Worker process capability]
        A --> D[WASM capability]
    end
    subgraph Distributed
        E[Assembly] --> F[Remote capability A]
        E --> G[Remote capability B]
    end
```



> [!WARNING]
> WebAssembly and out-of-process execution are **execution boundaries**, not a requirement for every Capability. They exist so a capability can be portable, isolated, and independently implemented, not so every capability must become one.



## N.O.R.E. as a Small Operating Environment

An operating system provides processes, memory, resources, communication, isolation, and scheduling. N.O.R.E. applies the same discipline one level higher: Assemblies, Instances, Capabilities, Capability Runtimes, Bindings, resource management, and execution. The kernel stays small; capabilities live outside it.

```mermaid
flowchart TB
    CLI[neuron CLI] -->|config / register / run / events| API[N.O.R.E. Local API]
    API --> IM[Instance Manager]
    API --> SR[Assembly Repository]
    IM --> EE[Execution Engine]
    EE --> EB[Event Bus]
    EE --> ER[Capability Runtime Registry]
    EB --> ST[Storage Provider]
    EE --> Sched[Scheduler]
    Sched --> Res[Expr. Resolver]
    ER --> PRO[Process Backend]
    ER --> WASM[WASM Backend]
```



You never interact with N.O.R.E. directly. The `neuron` CLI starts it, checks its health, talks to it over a local Unix socket, and stops it — from your perspective there is a single product.

---



## How an Assembly Runs

```mermaid
flowchart LR
    A[Define an Assembly<br/>TypeScript or YAML] --> B[Register with neuron<br/>build → compile → resolve → freeze]
    B --> C[N.O.R.E. persists the compiled assembly]
    C --> D[Run: create an Instance]
    D --> E[N.O.R.E. plans & executes]
    E --> F[Live execution events streamed back]
```




| Stage            | Responsibility                                                                        |
| ---------------- | ------------------------------------------------------------------------------------- |
| **Definition**   | Author an Assembly in TypeScript or YAML using the SDK or the YAML surface            |
| **Manifest**     | Produce the canonical Assembly representation both surfaces agree on                   |
| **Validation**   | Verify structural and semantic correctness                                            |
| **Compilation**  | Build the runtime representation from the canonical manifest                          |
| **Resolution**   | Resolve required external capability runtime packages and freeze exact versions        |
| **Registration** | Hand the compiled Assembly + frozen capability runtime set to N.O.R.E.                 |
| **Instance**     | Create a living realization of the registered Assembly                                 |
| **Execution**    | N.O.R.E. schedules Capabilities across bindings and runs them through capability runtimes |


```bash
# The fastest way to feel Neuron — run the shipped order pipeline
git clone https://github.com/neuron-runtime/neuron.git
cd neuron/examples/ecommerce_order_ts
pnpm install
neuron build            # build the TS project, compile, register with the runtime
neuron run              # create an instance and stream live execution events
```

Walk through it in detail in [docs/GETTING_STARTED.md](./docs/GETTING_STARTED.md).

---



## Capability Runtimes & External Modules

A Capability is the logical unit. A **Capability Runtime** is the machinery that makes it operate. Neuron distinguishes two kinds:

- **Built-in modules** — shipped inside N.O.R.E., run in-process, require no resolution or installation (for example `neuron:core:set`).
- **External modules (capability runtimes)** — authored, packaged, distributed, and hosted independently. N.O.R.E. resolves them, verifies them, installs them immutably, and executes them out-of-process as native processes or WebAssembly modules.

External modules share one unified contract: a `runtime.json` manifest (identity, runtime type, protocol, platforms), a canonical package archive, and a declared wire protocol (`neuron/capability-runtime-v1` over gRPC, or `neuron/capability-runtime-v1-json` over stdio).

```mermaid
flowchart LR
    REQ[Capability Runtime Requirement<br/>example:echo ^1.0.0] --> RES[Resolver<br/>semver selection]
    RES --> REG[Registry<br/>github / local]
    REG --> PKG[Capability Runtime Package<br/>name-version-capability-runtime.neuron.tar.gz]
    PKG --> VRFY[Verify<br/>manifest + digest]
    VRFY --> INST[Install<br/>immutable store]
    INST --> FRZ[Freeze exact version<br/>into registered Assembly]
    FRZ --> RT[N.O.R.E. Capability Runtime<br/>process / wasm]
    RT --> W[Live worker pool]
```



The exact resolved version is **frozen into every registered assembly**, so instances run the precise modules that were verified at registration time — reproducibly, and offline.

> [!WARNING]
> Neuron treats external capability runtimes as untrusted code. Artifacts are verified by digest before installation and hosted out-of-process, never inside the runtime's address space. GitHub is a distribution source, not a security boundary.

See [docs/MODULES.md](./docs/MODULES.md) for the full module model and how to author one. A reference capability runtime (`examples/capability-runtimes/echo`) ships in this repository, compiled to both a native binary and a WebAssembly module from a single Go source.

---



## Installation

Neuron is one product: the `neuron` CLI plus the N.O.R.E. runtime engine, distributed as a single archive for your platform. There is no daemon to install or service to manage — the CLI runs the engine for you.


| Platform | Architectures    |
| -------- | ---------------- |
| Linux    | `amd64`, `arm64` |
| macOS    | `amd64`, `arm64` |
| Windows  | `amd64`          |


1. Download the latest release archive for your platform from the [releases page](https://github.com/neuron-runtime/neuron/releases).
2. Extract it and place `neuron` on your `PATH`.
3. Verify:

```bash
neuron version
```

See [docs/INSTALLATION.md](./docs/INSTALLATION.md) for the complete guide, including installing from source and verifying release checksums.

---



## Documentation


|                         |                                                                                                            |
| ----------------------- | ---------------------------------------------------------------------------------------------------------- |
| **Getting started**     | Build and run your first Assembly in TypeScript — [docs/GETTING_STARTED.md](./docs/GETTING_STARTED.md)       |
| **Architecture**        | The canonical pipeline, the boundaries that never blur, and how N.O.R.E. executes — [docs/ARCHITECTURE.md](./docs/ARCHITECTURE.md) |
| **Modules & capability runtimes** | The unified module model, packaging, resolution, and protocol — [docs/MODULES.md](./docs/MODULES.md)       |
| **Installation**        | Official release and from-source installs, plus how each artifact is released — [docs/INSTALLATION.md](./docs/INSTALLATION.md)                 |
| **TypeScript SDK**      | Define assemblies as typed, composable capabilities — [packages/assembly-sdks/typescript/README.md](./packages/assembly-sdks/typescript/README.md)      |
| **Go executor SDK**     | Build production capability runtimes — [packages/executor-sdks/golang/README.md](./packages/executor-sdks/golang/README.md)            |
| **.NET executor SDK**  | Build production capability runtimes with C#/.NET — [packages/executor-sdks/dotnet/README.md](./packages/executor-sdks/dotnet/README.md) |
| **CLI reference**       | Every `neuron` command and flag — [application/README.md](./application/README.md)                         |




## Repository

`neuron` is a monorepo with strict architectural boundaries:


| Path                    | Responsibility                                                                      |
| ----------------------- | ----------------------------------------------------------------------------------- |
| `application/`          | The `neuron` CLI — authoring, building, module resolution, client, daemon bootstrap |
| `nore/`                 | N.O.R.E. — the Neuron Operational Runtime Engine                                    |
| `shared/`               | Canonical types and protocol contracts agreed on by both Go modules                 |
| `packages/assembly-sdks/typescript/`         | `@neuron/sdk` — TypeScript assembly-definition language                              |
| `packages/executor-sdks/golang/`        | Go SDK for authoring Neuron modules (capability runtimes)                                                    |
| `packages/executor-sdks/dotnet/`    | .NET SDK for authoring Neuron modules (`Neuron.Executor`)                                               |
| `examples/`             | Runnable assemblies and reference capability runtimes                                        |
| `docs/`                 | Architecture, getting started, installation, module, and runtime docs               |


`application` and `nore` are **separate Go modules**. They agree only through the canonical types and protocol contracts in `shared`. The CLI never reaches into runtime internals; the runtime never parses YAML or TypeScript.

---



## Project Status

**Version** `0.1.0` — first public development release. The core is implemented and deliberately structured for long-term growth; everything is still subject to change until 1.0.

```mermaid
flowchart LR
    A[Available<br/>implemented + tested] --> B[Experimental<br/>works, not hardened]
    B --> C[Planned<br/>designed, not shipped]
```

### Available

Implemented, tested, and intended to work in 0.1.0.

| Area | Surface |
| --- | --- |
| **CLI** | `init`, `build`, `run` (live event streaming over WebSocket with an SSE fallback), `add` / `remove`, `capability runtime list` / `inspect`, `instance list` / `remove` / `clear`, `daemon stop`, `version` — plus global configuration and `NEURON_*` environment overrides |
| **Authoring** | Full YAML authoring (the canonical, zero-tooling surface) and the `@neuron/sdk` typed TypeScript surface, converging on the same canonical manifest |
| **Runtime** | Registration of compiled assemblies with a frozen, resolved capability runtime set; per-capability runtime configuration (execution mode and timeout, retry policy and backoff, reserved resource constraints) declared on the capability runtime and defaulted by N.O.R.E.; instances (create, list, remove, clear) with restoration on restart; execution planning, scheduling, CEL mappings and validations, cancellation, deadlines, and terminal execution states; persisted and live-streamed events; built-in capability runtimes in-process; external capability runtimes out-of-process as process workers or WASM modules; graceful shutdown |
| **Capability runtimes** | The unified module model with semantic-version resolution; `github` and `local` registries; canonical `<name>-<version>-capability-runtime.neuron.tar.gz` archives with digest verification; immutable installation into `~/.neuron/capabilityRuntimes`; both protocols (`neuron/capability-runtime-v1` over gRPC, `neuron/capability-runtime-v1-json` over stdio); the reference `example:echo` module compiled for both runtimes from one Go source |
| **Security** | Unix-socket-only default transport, and API-token authentication on every route except the health probe |

### Experimental

Present and working, but not yet hardened or committed to:

- **External capability runtime resolution over GitHub Releases** — the network path is functional, but the ecosystem and catalog conventions are early. The registry is catalog-driven: unlisted module names are not guessed at.
- **WASM capability runtime backend** — functional, but performance and capability breadth are not yet fully characterized.
- **Execution introspection** — executions are visible through `neuron instance list`; a dedicated inspection command and execution-history retention policies are not configurable yet.
- **Runtime refinements** — concurrency behavior, worker-pool tuning, and restart semantics are still evolving.

### Planned

- **Runtime hardening** — TLS for the opt-in TCP endpoint (API token authentication is in place), per-capability-runtime resource limits, and stronger process isolation.
- **Configurable execution-history retention** (`none | memory | local`).
- **Concurrency backpressure** and request deadlines propagated end to end.
- **Broader registry ecosystem** and published module distribution.
- **Additional capability runtime backends** (container, remote).
- **Stabilization of the public contracts** — SDK API, capability runtime protocol, canonical manifest, and CLI surface — for 1.0 compatibility guarantees.

> [!NOTE]
> These docs describe current behavior and architectural direction. Where something is designed for but not yet shipped — container/remote runtimes, TLS, execution-retention policies — it is labeled as planned, not promised.

### Versioning & compatibility

The product version is `0.1.0`; `neuron version` reports it. The `@neuron/sdk` is versioned independently as `0.1.0` and is not yet published to a package registry; it is consumed from this repository. The public contracts — the SDK API, the capability runtime protocol, the canonical manifest, and the CLI surface — are under active development and may change without notice before 1.0.

## Roadmap

- **0.1.x** — stabilization of the 0.1.0 foundation: bug fixes, documentation corrections, installation refinements, runtime correctness.
- **0.2.x** — new runtime capabilities, a broader external module ecosystem, and new SDK capabilities as they mature.
- **1.0** — a stable public model and compatibility guarantees.



## Development

The repository is a Go workspace plus a pnpm monorepo.

```bash
# Prerequisites: Go 1.26.5+, pnpm 10.33.0 (10.x), Node.js, .NET SDK 10

go test   ./nore/... ./application/... ./shared/... ./packages/executor-sdks/golang/...
go vet    ./nore/... ./application/... ./shared/... ./packages/executor-sdks/golang/...
go build  ./nore/... ./application/... ./shared/... ./packages/executor-sdks/golang/...

dotnet build packages/executor-sdks/dotnet/Neuron.Executor.slnx -c Release   # .NET executor SDK
dotnet test  packages/executor-sdks/dotnet/Neuron.Executor.slnx -c Release

pnpm install
pnpm build:sdk
pnpm test:sdk
pnpm typecheck:sdk
```

Or run the convenience helper — `npm run script` — which validates every Go module and npm package in the workspace.

A local smoke test of the full product flow:

```bash
go build -o /tmp/neuron ./application/cmd/neuron
/tmp/neuron version
cd examples/ecommerce_order_ts && /tmp/neuron build && /tmp/neuron run
```

Contributions follow the repository's engineering contract: preserve architectural boundaries, avoid duplication, remove dead code, and test at the correct boundary.

---



## License

Neuron is released under the MIT License. See [LICENSE](./LICENSE).
