## Execution & Scheduler

- [ ]  **Add parallel-execution stress tests**
    - **Files:** `nore/internal/execution/scheduler/`, `nore/internal/execution/`, `nore/internal/execution/engine/`
    - Test multiple executions running simultaneously, fan-out branches, and mixed completion order.
    - Specifically verify that `inFlight` reaches zero and every execution reaches a terminal state.
    - Current inspection found no obvious scheduler deadlock, but this behavior is not sufficiently tested.
- [ ]  **Test event-bus backpressure under concurrent executions**
    - **File:** `nore/internal/event/bus.go`
    - `Publish()` blocks when a subscriber's buffer is full. This is intentional backpressure, but scheduler and engine interaction should be stress-tested with many executions/events so a slow subscriber cannot effectively stall execution indefinitely.
- [ ]  **Handle execution cancellation explicitly**
    - **Files:** `nore/internal/execution/execution.go`, `nore/internal/execution/wait.go`, `nore/internal/instance/instance.go`
    - `StatusCancelled` exists, but there is no `MarkCancelled`/execution cancellation path.
    - Define how cancellation propagates to running capability runtimes and how the execution becomes terminal.

## Runtime Configuration

- [x]  **Move execution configuration from capabilities to capability runtimes**
    - **Files:** `packages/assembly-sdks/typescript/src/capability.ts`, `packages/assembly-sdks/typescript/src/manifest.ts`, `application/compiler/manifest/manifest.go`, `application/compiler/compiler.go`, `shared/types/core/capability.go`, `shared/types/protocol/hash.go`, `nore/internal/execution/engine/`, affected tests/docs/examples
    - **Implemented (modeling and plumbing).** The grouped `runtimeConfig` schema lives in `shared/types/core/runtimeconfig.go` and is wired end to end: TypeScript `.runtime({ runtimeConfig })`, manifest `capabilityRuntime.runtimeConfig`, YAML `capability runtime: runtimeConfig:` with legacy `execution:` folded in, compiler validation and cloning, hashing of declared configuration, and N.O.R.E. default resolution applied once per execution plan (`nore/internal/runtimeconfig`) and delivered on `contracts.ExecutionContext.RuntimeConfig`. Runtime settings now reach the N.O.R.E. boundary; enforcing them is tracked separately below.
    - Remove the current capability-level `.runtimeConfig(...)` / `ExecutionConfig` API.
    - Replace it with runtime configuration supplied through `.runtime(...)`:
        
        ```
        .runtime({
          name: "...",
          version: "...",
          registry: "...",
          runtimeConfig: {
            retry: {...},
            execution: {
              mode: "detach"
            },
            resources: {...}
          }
        })
        ```
        
    - `runtimeConfig` belongs to the **runtime declaration attached to that capability**, not to the capability's input data.
    - A capability using `acme:http` may have a different `runtimeConfig` from another capability using the same `acme:http` runtime.
    - Do **not** deduplicate runtime configurations merely because the runtime identity is the same.
    - Do **not** reject multiple capabilities using the same runtime with different runtime configurations.
    - The runtime artifact and resolved runtime identity may be shared/cached, while the effective runtime configuration remains specific to the capability invocation.
    - Capability input and runtime execution configuration must remain completely separate:
        - `params` = input data supplied to the capability.
        - `runtimeConfig` = instructions controlling how N.O.R.E. handles execution of that capability through its Capability Runtime.
    - `runtimeConfig` must never be inserted into the capability invocation `params` or otherwise treated as user input to the runtime capability.
    - Define one common runtime configuration schema applicable to all Capability Runtimes. The initial schema should support execution controls such as:
        - `execution.mode`
        - `execution.timeout`
        - `retry`
        - `resources`
        - additional runtime lifecycle/resource controls as the runtime system evolves.
    - The schema describes **how Neuron handles the runtime invocation**, not arbitrary configuration data for the implementation of a particular runtime.
    - Preserve the distinction between runtime artifact metadata and invocation configuration:
        - runtime artifact/`runtime.json` describes what the Capability Runtime is and how it is hosted;
        - `runtimeConfig` describes how that particular capability invocation should be executed.
    - Carry the runtime configuration through compilation into the execution representation associated with that capability's runtime invocation.
    - Ensure the configuration survives manifest serialization/deserialization and assembly compilation without being lost.
    - Update `HashAssembly` so changes to a capability's `runtimeConfig` affect the assembly/deployment identity.
    - Update the N.O.R.E. execution engine so it retrieves `runtimeConfig` from the capability's runtime declaration rather than from capability input/configuration.
    - Apply runtime configuration at the runtime invocation boundary:
        - `execution.mode` controls wait/detach behavior.
        - `execution.timeout` controls the invocation deadline.
        - `retry` controls retry/backoff behavior for that runtime invocation.
        - `resources` controls runtime resource constraints where the selected backend supports them.
    - Ensure two capabilities using the same runtime can execute with different runtime configurations simultaneously.
    - Ensure runtime artifact resolution/installation remains independent from runtime configuration. Resolving `acme:http@1.2.0` should identify/install the artifact once as needed, while each capability invocation retains its own `runtimeConfig`.
    - Add compiler, manifest, hashing, resolution, and execution tests covering:
        - a capability with runtime configuration;
        - two capabilities using the same runtime with different runtime configurations;
        - two simultaneous invocations of the same runtime with different `execution.mode` values;
        - runtime configuration changes affecting assembly/deployment identity;
        - runtime configuration never appearing in capability input;
        - runtime artifact resolution remaining independent from invocation-specific runtime configuration;
        - runtime configuration being available to the N.O.R.E. runtime/backend lifecycle.
- [ ]  **Actually apply capability-runtime execution settings**
    - **Files:** `nore/internal/execution/engine/executor_engine.go`, runtime backend implementations, `shared/types/capabilityruntime/`, affected tests
    - **Status:** the effective configuration is now available on `contracts.ExecutionContext.RuntimeConfig` (defaults resolved by `nore/internal/runtimeconfig` at plan time), but the engine and backends do not yet act on it. This item covers enforcement only.
    - Implement the runtime-level settings once the new ownership model is established.
    - `execution.mode` must control runtime/execution handling rather than being treated as capability data.
    - `timeout` must establish the appropriate execution context deadline.
    - `retry` must be enforced around capability-runtime invocation with explicit retry/backoff semantics.
    - `resources` must be represented as runtime execution constraints and only enforced by backends that support the requested resource controls.
    - Unsupported configuration must fail validation or be explicitly ignored according to a documented compatibility policy; do not silently imply that an option is enforced when it is not.
- [x]  **Remove obsolete capability execution configuration**
    - **Files:** `packages/assembly-sdks/typescript/src/capability.ts`, `packages/assembly-sdks/typescript/src/manifest.ts`, `application/compiler/manifest/manifest.go`, `shared/types/core/capability.go`, affected YAML/JSON examples and tests
    - Remove `CapabilityComposition.execution`, `CapabilityManifest.execution`, `manifest.Capability.Execution`, `core.Capability.RuntimeConfigurations`, and related capability-level execution plumbing once the runtime-level configuration model is implemented.
    - Do not retain duplicate capability-level and runtime-level configuration paths.
    - **Done.** The dead TypeScript `.runtimeConfig()` / `ExecutionConfig` and `_composition.execution` plumbing, the manifest `ExecutionConfig`/`Capability.Execution`, and `core.Capability.RuntimeConfigurations` are removed. There is a single path: `capabilityRuntime.runtimeConfig`. The legacy YAML `execution:` block is retained only as deprecated authoring sugar and is folded into that one canonical location by the compiler; it never survives as its own field.

## Capability Runtime / Packaging

- [ ]  **Finish executor → capability-runtime terminology migration**
    - **Files:** `README.md`, `docs/GETTING_STARTED.md`, `docs/RELEASING.md`, `packages/executor-sdks/`, `TODO.md`
    - Current implementation uses capability runtimes, while parts of the documentation and SDK naming still use executor terminology.
    - Keep compatibility/public package names where necessary, but remove stale internal terminology and references such as `executor.json`, `executorctl`, and old runtime paths where they no longer describe the implementation.
- [ ]  **Fix stale runtime-store documentation**
    - **File:** `shared/types/capabilityruntime/resolved.go`
    - Documentation still references the old executor/runtime store location while the implementation uses the capability-runtime store.
- [ ]  **Migrate the capability-runtime installation directory**
    - **Files:** capability-runtime store/configuration and any CLI, installer, resolver, documentation, tests, or examples referencing `~/.neuron/executors/`
    - Change the canonical local runtime store from `~/.neuron/executors/` to `~/.neuron/capability-runtime/`.
    - Update all code paths that construct, read, write, install, resolve, or document the old path.
    - Preserve compatibility/migration behavior only where it is intentionally required; do not leave the old directory as the canonical location.
    - Add or update tests covering runtime discovery and installation using the new location.
- [ ]  **Remove duplicate capability assignment**
    - **File:** `application/capabilityruntime/source/local/registry.go`
    - `pkg.Capabilities` is assigned the same manifest value twice. Remove the redundant assignment.

## Build / Compiler

- [x]  **Verify compiler/runtime configuration preservation**
    - **Files:** `application/compiler/compiler.go`, `shared/types/protocol/hash.go`
    - The compiler generates runtime configuration fields and `HashAssembly` includes them, but the runtime does not currently consume all of them.
    - Add tests ensuring a configuration change changes the deployment/build identity where it is supposed to, without implying that unsupported behavior is implemented.
    - **Done for identity.** `shared/types/protocol/hash_test.go` pins that declared `runtimeConfig` changes the hash while absent and empty configurations hash identically, and `application/compiler/runtimeconfig_test.go` pins that the compiler validates and clones without inventing values. Consumption/enforcement is tracked under "Actually apply capability-runtime execution settings".

## First-Party Runtime Architecture

- [ ]  **Replace `neuron:core:*` built-ins with normal first-party capability runtimes**
    - **Files:** `nore/internal/builtins/`, `nore/internal/registry/`, `application/internal/cli/`, `application/capabilityruntime/`, runtime-resolution/build paths, and all affected tests/docs/examples
    - Remove `nore/internal/builtins/` and the current in-process built-in runtime implementations entirely.
    - Remove the special `neuron:core:*` runtime classification, detection, bypass, registry, aliases, and execution logic from N.O.R.E. and the CLI.
    - Replace first-party runtime identities with the `neuron-runtime:<runtime>` namespace, for example:
        - `neuron-runtime:http`
        - `neuron-runtime:filesystem`
        - `neuron-runtime:process`
        - `neuron-runtime:execution`
    - First-party runtimes must use the same capability-runtime artifact, manifest, resolution, verification, installation, storage, and backend mechanisms as external runtimes rather than being privileged in-process implementations.
    - Build the first-party capability runtimes independently of N.O.R.E. so they can be implemented in any supported language and communicate through the language-independent capability-runtime protocol.
    - Package each first-party runtime as a distributable runtime artifact and publish the artifacts through the Neuron runtime distribution source, initially GitHub under the `neuron-runtime` organization.
    - Ensure the Neuron installation can ship the first-party runtime artifacts alongside Neuron and make them available through the same local capability-runtime installation/resolution model.
    - Ensure the CLI resolver/installer can resolve and install a missing `neuron-runtime:*` artifact through the normal runtime flow rather than requiring special-case built-in handling.
    - Remove or update every `.md`, documentation page, example, test, comment, configuration sample, SDK example, and source reference that uses or describes `neuron:core:*`, built-in capability runtimes, or the old in-process built-in model.
    - Remove obsolete assumptions such as “built-in runtimes are never resolved or installed.”
    - Establish `neuron-runtime:*` as the canonical first-party namespace and treat first-party and third-party capability runtimes as the same runtime artifact type.
    - Add/update tests covering first-party runtime resolution, installation, artifact verification, backend startup, and execution through the normal capability-runtime path.
