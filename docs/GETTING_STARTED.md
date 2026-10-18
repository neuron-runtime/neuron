# Getting Started with Neuron

This guide takes you from a fresh checkout to a running system in about fifteen minutes — authored in **TypeScript with `@neuron/sdk`**, built with the CLI, hosted by the project runtime, and executed with `neuron run`. No prior Neuron knowledge is assumed: building, validation, compilation, and module resolution are all automatic — the one manual step is starting the project runtime that hosts executions.

```mermaid
flowchart LR
    A[Define an Assembly<br/>in TypeScript] --> B[neuron build<br/>build → compile → resolve → freeze]
    B --> C[The runtime stores<br/>the compiled Assembly]
    C --> D[neuron<br/>start the project runtime]
    D --> E[neuron run<br/>execute → print the result]
```

## Prerequisites

- **The `neuron` binary** on your `PATH` (install from a [release](https://github.com/neuron-runtime/neuron/releases) or build from source — see [INSTALLATION.md](./INSTALLATION.md)).
- **The repository checked out**, to access the shipped example and the SDK workspace:

  ```bash
  git clone https://github.com/neuron-runtime/neuron.git
  cd neuron
  ```

- **Go, pnpm, and Node.js** for the walkthrough only if you build the examples and SDK from source:

  ```bash
  # link the workspace and build @neuron/sdk (produces the neuron-sdk CLI)
  pnpm install
  pnpm build:sdk
  ```

  No daemon setup is required. `neuron build` starts and stops the local runtime it needs for registration itself; running an Assembly uses the project runtime that bare `neuron` hosts for you.

---

## Part 1 — Run a shipped example

The repository ships an order-processing pipeline defined entirely in TypeScript (`examples/ecommerce_order_ts`). It uses only built-in modules — capability runtimes that run in-process inside N.O.R.E. — so it needs zero configuration beyond the SDK.

```bash
cd examples/ecommerce_order_ts
neuron build
```

`neuron build` runs the whole authoring pipeline in one step:

| Step | What happens |
| --- | --- |
| **Build** | The TypeScript assembly is compiled into the canonical manifest (`.neuron/manifest.json`) |
| **Compile** | The manifest is compiled into a runtime assembly representation |
| **Resolve** | Module references are resolved. Built-in modules (`neuron:core:set`) are skipped — they run inside the runtime engine |
| **Register** | The compiled assembly is handed to N.O.R.E., which persists it and returns an assembly key |

The output ends with a registration key:

```text
order-processing-ts@2.0.0#<key>:development
```

> Registration is self-contained: `neuron build` starts the local runtime it needs, registers the Assembly, and stops it again.

Now run it. `neuron run` is a client of the **project runtime** — the process bare `neuron` hosts for the current project. Start it in one terminal, from the example directory:

```bash
neuron
```

```text
N.O.R.E. project runtime started on http://localhost:45387
Project root: .../examples/ecommerce_order_ts
Press Ctrl-C to stop
```

Leave it running. In a second terminal, from the same directory, execute the Assembly. It expects a typed input of `{ order: {...} }`, so pass one explicitly:

```bash
neuron run --params '{"order":{"id":"ord_1001","customerId":"cus_42","customerEmail":"ada@acme.io","currency":"USD","total":4250,"items":[{"sku":"SKU-AG-1","name":"Wireless Mouse","qty":1,"priceCents":4250}],"shippingAddress":{"street":"1 Market St","city":"San Francisco","zip":"94105"}}}'
```

The command sends one synchronous execution request and prints the Assembly result when execution reaches a terminal state. This pipeline declares no Assembly result mapping, so the result is `null` — success is the zero exit code:

```text
null
```

Stop the project runtime with Ctrl-C in the terminal that hosts it.

Look at what you just ran:

```text
examples/ecommerce_order_ts/
├── assembly.ts        the Assembly: params schema + composition (no identity)
├── pipeline.ts        the composition: how capabilities chain and guard
├── capabilities/      each Capability: identity, capability runtime, contracts
└── types.ts           the shared domain types (Order, OrderItem, ...)
```

The whole definition is plain TypeScript — types are checked, mappings are verified, and the manifest is derived from the composition.

---

## Part 2 — Author your own assembly in TypeScript

### Create the project

```bash
neuron init my-first-system
```

`neuron init` creates the directory and scaffolds a **TypeScript** project by default: a `neuron.config.json` declaring the project identity (`name`, `version`) and `lang: typescript`, a `package.json` declaring `@neuron/sdk`, a `tsconfig.json`, a runnable `assembly.ts`, and the canonical local capability runtime root `neuron/capabilityRuntimes/`. Move it under `examples/` so the pnpm workspace picks it up for the SDK:

```bash
mv my-first-system examples/my-first-system
cd examples/my-first-system
npm install
```

> `neuron init --lang yaml` scaffolds the YAML authoring surface instead (`assembly.yaml` + `capabilities/`, no Node toolchain required).

The project configuration (`neuron.config.json` | `neuron.config.yaml` | `neuron.config.yml`) is the single source of truth for how the project is authored and run — including the Assembly identity. The TypeScript scaffold writes `neuron.config.json` (abridged):

```json
{
  "name": "my-first-system",
  "version": "1.0.0",
  "lang": "typescript",
  "assemblies": [
    { "name": "my-first-system", "entry": "assembly.ts", "default": true }
  ],
  "runtime": {
    "host": "localhost",
    "port": "auto",
    "execution": { "mode": "wait", "timeout": "30m" }
  },
  "capabilityRuntimes": {
    "localRoots": ["./neuron/capabilityRuntimes"]
  }
}
```

`name` and `version` are the Assembly identity — required for build. They live in `neuron.config.*` and nowhere else. A project declares one or more `assemblies`, each with a `name` and an `entry`; the one marked `"default": true` is what `neuron run` uses with no argument.

> [!IMPORTANT]
> `name` and `version` are the Assembly identity and are **required for `neuron build`**. They live in `neuron.config.*` and nowhere else: the authored Assembly (TypeScript or YAML) never repeats them, and `neuron build` injects the configured identity into the canonical manifest at the shared build seam. Scaffolded values come from the directory name and `1.0.0`; edit the config to rename or re-version the assembly.

### The scaffolded layout

```text
examples/my-first-system/
├── neuron.config.json         ← config: name, version, lang, entry, runtime
├── package.json               ← declares @neuron/sdk
├── tsconfig.json              ← strict TypeScript, noEmit
├── assembly.ts                ← the Assembly definition (the entry)
└── neuron/capabilityRuntimes/ ← home for locally-authored capability runtimes
```

`package.json`: the scaffold declares `@neuron/sdk` (`^0.1.0`). Because this walkthrough lives inside the repository workspace, pin it to `workspace:*` so pnpm links the local SDK build:

```json
{
  "name": "my-first-system",
  "version": "0.1.0",
  "private": true,
  "type": "module",
  "dependencies": {
    "@neuron/sdk": "workspace:*"
  }
}
```

### Define the capability

A **Capability** is a named unit of work with an identity, a capability runtime, and typed contracts:

```ts
// types.ts
export interface Order {
  id: string;
  customerId: string;
  customerEmail: string;
  currency: string;
  total: number;
  items: { sku: string; name: string; qty: number; priceCents: number }[];
  shippingAddress: { street: string; city: string; zip: string };
}

// assembly.ts
import { Capability, Assembly } from "@neuron/sdk";
import type { Order } from "./types";

const validateOrder = Capability("order.validate")
  .runtime({ name: "neuron:core:set" })
  .paramsSchema<{ order: Order }>()
  .resultSchema<{ order: Order }>();

const authorizePayment = Capability("payment.authorize")
  .runtime({ name: "neuron:core:set" })
  .paramsSchema<{ order: Order; amount: number }>()
  .resultSchema<{ order: Order; amount: number }>();
```

### Compose the assembly

`Assembly` defines the pipeline: bind the assembly's execution input to the first capability with `.withParams(data => ...)`, chain capabilities with `.bind()`, and map each capability's results into the next capability's params with `.withParams({ ... })`:

```ts
// The Assembly authors no identity: neuron.config.json owns the name/version.
const manifest = Assembly()
  .paramsSchema<{ order: Order }>()
  .withParams((data) =>
    validateOrder
      .withParams({ order: data.order })
      .bind(
        authorizePayment.withParams({
          order: validateOrder.result.order,
          amount: data.order.total,
        })
      )
  )
  .toManifest();

export default manifest;
```

Point the project configuration at the entry file (it must default-export the manifest); the config **is** the Assembly identity — the authored entry repeats none of it. Edit the scaffolded file; do not add a second `neuron.config.*` file:

```json
{
  "name": "order-processing",
  "version": "1.0.0",
  "lang": "typescript",
  "assemblies": [
    { "name": "order-processing", "entry": "assembly.ts", "default": true }
  ],
  "runtime": { "host": "localhost", "port": "auto" }
}
```

> [!NOTE]
> `withParams(data => ...)` binds the assembly's execution input (typed by `paramsSchema`) into the first capability. `.bind()` wires one capability to the next; a capability's `.withParams({ ... })` maps fields into its params — every binding is type-checked against the target's params contract. Execution edges are **derived from the data each capability references**: two capabilities that read from the same source run in parallel, and a chain is implied by each step referencing the previous step's result — no explicit `Parallel(...)` exists in the SDK.

### Build and run

```bash
cd examples/my-first-system
neuron build
```

Start this project's runtime the same way as in Part 1 — bare `neuron`, from `examples/my-first-system`, left running in its own terminal:

```bash
neuron
```

Then execute the Assembly:

```bash
neuron run --params '{"message":"hello"}'
```

As in Part 1, one synchronous request runs the whole Assembly and the result is printed (`null` — the scaffold's `say-hello` Assembly declares no Assembly result mapping).

### Inspect what is running

Your instance has real state in the runtime:

```bash
neuron instance list            # list instances
neuron instance list --all      # include inactive ones
neuron instance remove <id>     # remove one instance
neuron instance clear           # remove everything
```

---

## Part 3 — Use an external module

Built-in modules cover simple cases. Real assemblies also use **external modules (capability runtimes)** — capability runtimes authored, packaged, and distributed independently. This part runs the full external-module lifecycle against the repository's reference `echo` module.

### Build the reference module

The echo module is compiled from one Go source (`examples/capability-runtimes/echo`) into both a native process binary and a WebAssembly module. Build it into a local registry catalog:

```bash
cd examples/capability-runtimes
./build.sh
```

This produces `examples/capability-runtimes/catalog/`, containing for each module version:

```text
catalog/example/echo/1.0.0/
    runtime.json                                capability runtime manifest
    echo                                       native binary (process runtime)
    example-echo-1.0.0-capability-runtime.neuron.tar.gz  canonical package archive
```

`runtime.json` declares identity, runtime type, protocol, and platform artifacts.

### Register the catalog as a local registry

Add a `local` registry to the same project configuration (the file also carries the Assembly identity — `name` / `version` are required for build):

```json
{
  "name": "order-processing",
  "version": "1.0.0",
  "lang": "typescript",
  "assemblies": [
    { "name": "order-processing", "entry": "assembly.ts", "default": true }
  ],
  "runtime": { "host": "localhost", "port": "auto" },
  "capabilityRuntimes": {
    "registries": [
      { "name": "local", "url": "/absolute/path/to/neuron/examples/capability-runtimes/catalog" }
    ]
  }
}
```

The `local` registry is directory-backed and served offline. Additional registries (such as `github`) are declared the same way and are opted in explicitly — with no `capabilityRuntimes.registries` block, only built-in capability runtimes are available.

### Require the module from a capability

Add an echo capability to your assembly and chain it at the end:

```ts
// assembly.ts
const echo = Capability("echo")
  .runtime({ name: "example:echo", version: "^1.0.0", registry: "local" })
  .paramsSchema<{ message: string }>();
```

Wire it into the pipeline — the assembly input remains `{ order: ... }`:

```ts
withParams((data) =>
  validateOrder
    .withParams({ order: data.order })
    .bind(
      authorizePayment.withParams({
        order: validateOrder.result.order,
        amount: data.order.total,
      })
    )
    .bind(
      echo.withParams({
        message: data.order.customerEmail,
      })
    )
)
```

> [!NOTE]
> `registry: "local"` tells the resolver to source `example:echo@^1.0.0` from the local catalog. Omit `registry` and the default `local` registry is used; a `github` registry requires an explicit catalog configuration.

### Resolve, install, run

```bash
cd examples/my-first-system
neuron build
```

During the build Neuron resolves `example:echo@^1.0.0`:

```mermaid
flowchart LR
    A[Requirement example:echo @ ^1.0.0] --> B[Registry queries available versions]
    B --> C[best semver-compatible version selected: 1.0.0]
    C --> D[package archive verified + installed immutably]
    D --> E[exact version frozen into the build record]
```

1. the registry is queried for available versions (here: `1.0.0`);
2. the best satisfying version is selected with semantic versioning;
3. the canonical package archive is downloaded, verified, and installed immutably into the capability runtime store (`~/.neuron/capabilityRuntimes`);
4. the exact version is frozen into the build record — running an instance needs no further resolution, and works offline.

You can manage the module directly:

```bash
neuron add example:echo@^1.0.0      # resolve + install into the store
neuron capability runtime list      # installed modules
neuron capability runtime inspect example:echo@1.0.0
neuron remove example:echo@1.0.0
```

Now run:

```bash
neuron run --params '{"order":{"id":"ord_3001","customerId":"cus_11","customerEmail":"leo@acme.io","currency":"USD","total":1900,"items":[{"sku":"SKU-WB-3","name":"Webcam","qty":1,"priceCents":1900}],"shippingAddress":{"street":"3 King St","city":"London","zip":"EC2A 4BX"}}}'
```

The execution launches the installed module **out-of-process**, passes your input through the capability runtime protocol, and returns its result. For the echo module, the result mirrors the params: `neuron run --params '{"message":"hi"}'` prints `{"params":{"message":"hi"},"protocol":"neuron/capability-runtime-v1-json","type":"example:echo","version":"1.0.0"}`. Start the project runtime (`neuron` in one terminal) and run from another as before; in this example you can also run it **directly** without a project runtime using `neuron run -c example:echo --params '{"message":"hi"}'`.

---

## Next steps

| | |
| --- | --- |
| **TypeScript SDK** | Every SDK feature in depth — [packages/assembly-sdks/typescript/README.md](../packages/assembly-sdks/typescript/README.md) |
| **Modules & executors** | The unified module model, packaging, and authoring — [docs/MODULES.md](./MODULES.md) |
| **Architecture** | How the platform is put together, and how N.O.R.E. executes — [docs/ARCHITECTURE.md](./ARCHITECTURE.md) |
| **CLI reference** | Every `neuron` command and flag — [application/README.md](../application/README.md) |
| **Status** | What is available, experimental, and planned — [README.md](../README.md#project-status) |
