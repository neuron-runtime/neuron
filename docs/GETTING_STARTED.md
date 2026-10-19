# Getting Started with Neuron

This guide takes you from a fresh checkout to a running assembly in about fifteen minutes — authored in **TypeScript with `@neuron/sdk`**, built with the CLI, hosted by the project runtime, and executed with `neuron run`.

Assemblies can be authored in **any programming language that produces the Neuron Assembly manifest** (the canonical representation). Today, **TypeScript** and **YAML** are first-class authoring surfaces; this guide uses TypeScript.

No prior Neuron knowledge is assumed: building, validation, compilation, and module resolution are all automatic — the one manual step is starting the project runtime that hosts executions.

The workflow has four steps: author the assembly, `neuron build` to compile and freeze it, start the project runtime with `neuron`, and `neuron run` to execute it.

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

  No daemon setup is required. `neuron build` starts and stops the local runtime it needs for registration itself; running an assembly uses the project runtime that bare `neuron` hosts for you.

---

## Part 1 — Run a shipped example

The repository ships a minimal assembly in `examples/hello`. It defines one capability and uses only built-in capability runtimes — runtimes that run in-process inside the Neuron runtime — so it needs no configuration beyond the SDK.

```bash
cd examples/hello
neuron build
```

`neuron build` runs the whole authoring pipeline in one step:

1. **Build** — the TypeScript assembly is compiled into the canonical manifest (`.neuron/manifest.json`).
2. **Compile** — the manifest is compiled into a runtime assembly representation.
3. **Resolve** — capability runtime references are resolved. Built-in runtimes (`neuron:core:set`) are skipped; they run inside the runtime engine.
4. **Register** — the compiled assembly is handed to the Neuron runtime, which persists it and returns an assembly key.

The output ends with a registration key:

```text
hello@1.0.0#<key>:development
```

Registration is self-contained: `neuron build` starts the local runtime it needs, registers the assembly, and stops it again.

Now run it. `neuron run` is a client of the **project runtime** — the process bare `neuron` hosts for the current project. Start it in one terminal, from the example directory:

```bash
neuron
```

```text
Neuron project runtime started on http://localhost:45387
Project root: .../examples/hello
Press Ctrl-C to stop
```

Leave it running. In a second terminal, from the same directory, execute the assembly. It expects `{ "name": "..." }`, so pass one explicitly:

```bash
neuron run --params '{"name":"World"}'
```

The command sends one synchronous execution request and prints the assembly result when execution reaches a terminal state. This assembly declares no result mapping, so the result is `null` — success is the zero exit code:

```text
null
```

Stop the project runtime with Ctrl-C in the terminal that hosts it.

The example is plain TypeScript — types are checked, mappings are verified, and the canonical manifest is derived from the composition:

```text
examples/hello/
├── assembly.ts        the assembly: params schema + composition (no identity)
└── neuron.config.json the project identity and runtime configuration
```

---

## Part 2 — Author your own assembly

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

A **Capability** is a named unit of work with an identity, a capability runtime, and typed contracts. The smallest useful assembly defines one. Replace the scaffolded `assembly.ts`:

```ts
// assembly.ts
import { Capability, Assembly } from "@neuron/sdk";

const sayHello = Capability("say-hello")
  .runtime({ name: "neuron:core:set" })
  .paramsSchema<{ message: string }>()
  .resultSchema<{ message: string }>();
```

### Compose the assembly

`Assembly` defines the composition: bind the assembly's execution input to the capability with `.withParams(data => ...)`. The Assembly authors no identity — `neuron.config.json` owns the name and version:

```ts
const manifest = Assembly()
  .paramsSchema<{ message: string }>()
  .withParams((data) =>
    sayHello.withParams({ message: data.message })
  )
  .toManifest();

export default manifest;
```

Capabilities can be chained with `.bind()`; execution edges are derived from the data each capability references — a capability that reads the previous step's result implies a chain, and capabilities that read the same source run in parallel. Part 3 chains a second capability; one is enough for now.

### Build and run

```bash
cd examples/my-first-system
neuron build
```

Start this project's runtime the same way as in Part 1 — bare `neuron`, from `examples/my-first-system`, left running in its own terminal:

```bash
neuron
```

Then execute the assembly:

```bash
neuron run --params '{"message":"hello"}'
```

As in Part 1, one synchronous request runs the whole assembly; the result is `null` (no Assembly result mapping is declared), and success is the zero exit code.

### Inspecting executions

Each run is recorded as an execution under the project's runtime, so it stays queryable after it finishes. The CLI exposes this through `neuron instance`, an advanced surface that is not required for authoring, building, or running assemblies. See the [CLI reference](../application/README.md) for the full command set.

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
  "name": "my-first-system",
  "version": "1.0.0",
  "lang": "typescript",
  "assemblies": [
    { "name": "my-first-system", "entry": "assembly.ts", "default": true }
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

Add an echo capability to your assembly:

```ts
// assembly.ts
const echo = Capability("echo")
  .runtime({ name: "example:echo", version: "^1.0.0", registry: "local" })
  .paramsSchema<{ message: string }>();
```

Chain it after `say-hello` — a chain is implied when a capability references the previous step's result:

```ts
withParams((data) =>
  sayHello
    .withParams({ message: data.message })
    .bind(echo.withParams({ message: sayHello.result.message }))
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

1. the registry is queried for available versions (here: `1.0.0`);
2. the best satisfying version is selected with semantic versioning;
3. the canonical package archive is downloaded, verified, and installed immutably into the capability runtime store (`~/.neuron/capabilityRuntimes`);
4. the exact version is frozen into the build record — running the assembly needs no further resolution, and works offline.

You can manage the module directly:

```bash
neuron add example:echo@^1.0.0      # resolve + install into the store
neuron capability runtime list      # installed modules
neuron capability runtime inspect example:echo@1.0.0
neuron remove example:echo@1.0.0
```

Now run:

```bash
neuron run --params '{"message":"hi"}'
```

The execution launches the installed module **out-of-process**, passes your input through the capability runtime protocol, and returns its result. For the echo module, the result mirrors the params: `neuron run -c example:echo --params '{"message":"hi"}'` prints `{"params":{"message":"hi"},"protocol":"neuron/capability-runtime-v1-json","type":"example:echo","version":"1.0.0"}`. Start the project runtime (`neuron` in one terminal) and run from another as before; you can also run the capability **directly** without a project runtime using the `-c` form shown above.

---

## Next steps

- **TypeScript SDK** — every SDK feature in depth: [packages/assembly-sdks/typescript/README.md](../packages/assembly-sdks/typescript/README.md).
- **Modules and capability runtimes** — the module model, packaging, and authoring: [docs/MODULES.md](./MODULES.md).
- **Architecture** — how the platform is assembled and how the Neuron runtime executes: [docs/ARCHITECTURE.md](./ARCHITECTURE.md).
- **CLI reference** — every `neuron` command and flag: [application/README.md](../application/README.md).
- **Project status** — what is available, experimental, and planned: [README.md](../README.md#project-status).
