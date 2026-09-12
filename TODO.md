# TODO

Personal development checklist for Neuron. This file is intentionally **not tracked in git**
(it is listed in `.gitignore`) it is a working todo list, not documentation.

Tick items off as they are completed. Group by type: **Fixes**, **Improvements**,
**Distribution**, **Documentation**.

---

## Fixes

- [ ] **API authentication.** N.O.R.E.'s API is unauthenticated and bound to a local socket
  ```
  by default. Define a token-based model before any loopback exposure.
  ```
- [ ] **Worker crash/restart recovery.** Exercise worker-pool restart behavior after repeated
  ```
  executor failures under load; fix whatever explodes.
  ```
- [ ] **Windows daemon lifecycle.** Verify socket-path handling and `neuron daemon` start/stop
  ```
  on Windows (best-effort so far: Linux/macOS only).
  ```
- [ ] **Storage path normalization.** `storage.directory` accepts relative paths (e.g.
  ```
  `./.neuron/data`) that are resolved against the daemon's working directory; resolve them
  against the project root and pass an absolute path to `--data-dir`.
  ```
- [ ] **Two-config-file problem for TS projects with external executors.** TS authoring is
  ```
  `neuron.config.ts` but executor resolution reads `neuron.yaml`; `config.executorRegistries`
  is compiled into the manifest (`application/compiler/config.go`) and never consulted at
  resolution time (`executorctl.BuildCatalog`). Decide: make one the single source, or make
  the manifest path fully functional. (docs/FIRST_EXECUTOR.md A1)
  ```
- [ ] **`local://` dead default registry.** `defaults.go` ships `{name: local, url: local://}`
  ```
  but `BuildCatalog` silently filters entries with URL `local://`. Fresh projects are
  pre-broken for external executors from `local`. Remove the default or make it functional;
  explode loudly on unknown registry names instead of dropping them. (A2/A4)
  ```
- [ ] **`defaultRegistries` never populated.** Declared in `config.go` and used as the
  ```
  requirement fallback but `Defaults()` never sets it; requirements without an explicit
  `registry` get no fallback and a confusing "no executor registries" error. Ship
  `["local", "github"]` defaults and make the error state the exact fix. (A3)
  ```
- [ ] **SDK default executor name = service name.** Services without `.executor()` get an
  ```
  executor name equal to the service name (e.g. `content.extract`), which fails
  `ParseType` at registration ("must contain at least one ':' separator"). Validate in the
  SDK or relax `ParseType`; document the `owner:capability:sub` convention. (B1)
  ```
- [ ] **Local-registry identity reconciliation skipped.** The installer verifies
  ```
  name/version match only when `pkg.Manifest == nil` (`installer.go:78`); the local
  registry always sets the manifest, so a mismatched `metadata.name` installs under an
  unrelated type path. Always verify identity. (B4)
  ```
- [ ] **Instance status not persisted on clean stop.** `StopInstances` does not write
  ```
  `stopped`; restored instances show `failed`. Persist terminal status on shutdown. (D5)
  ```
- [ ] **Orphaned executor processes on daemon SIGKILL.** Spawned executors have no
  ```
  kill-on-parent-death process group; gRPC executors with no connection-loss watcher block
  in `Serve` forever. Add Pdeathsig/setsid handling or connection-loss detection. (D7)
  ```
- [ ] **Stale socket file left on shutdown.** The daemon closes the listener but never
  ```
  unlinks the socket path. Unlink on graceful shutdown. (D3)
  ```



## Improvements

- [x] **Real-time Websocket Connection** `application/connection` uses an `http.Client`
  ```
  implement websocket connection 
  with room subscribtions and structure json data.
  ```
- [ ] **Single config surface decision.** Pick one source of truth for executor-registry
  ```
  config: either consume the manifest's `executorRegistries` at resolution time, or make
  `neuron.yaml` the documented single source and drop the misleading TS path. "Both, with
  one ignored" is the current trap. (docs/FIRST_EXECUTOR.md A1)
  ```
- [ ] **`neuron init --lang ts` scaffolding.** Generate a runnable TS project (package.json,
  ```
  `neuron.config.ts`, `system.ts`, `neuron.yaml` with a `local` registry block) so fresh
  projects are not pre-broken for external executors. (F4)
  ```
- [ ] **`neuron daemon status` command.** Show running/stopped, PID, data dir, socket path,
  ```
  uptime. Today the daemon can only be observed through full lifecycle commands. (D1)
  ```
- [ ] **Surface daemon stderr by default.** A failing daemon start reports only "context
  ```
  deadline exceeded"; attach daemon stderr unless quiet, or point at a log path. (D2)
  ```
- [ ] **Rename `core.ServiceType` → `core.ExecutorType`.** The runtime type holds the
  ```
  executor identity, not the service identity; the misnomer compounds the service/executor
  confusion at every layer. Align the `type`/`name`/`Type`/`Tag` vocabulary once. (B2)
  ```
- [ ] **Remove dead config.** `neuron.config.ts` `script.build` (`cli/config.ts`), SDK
  ```
  `Project.entryFile` (`cli/project.ts:33`, recomputed in `build.ts:15`), and
  `storage.provider` (file-based today; example ships `postgres`). Delete or wire up. (C2/C3/E2)
  ```
- [ ] **SDK manifest validation.** Validate the built `SystemManifest` shape before writing
  ```
  `.neuron/manifest.json`; today only the default-export presence is checked and malformed
  manifests fail later in the Go compiler. (C5)
  ```
- [ ] **Short-circuit config-file discovery.** Warn (or error) when multiple
  ```
  `neuron.config.*` candidates exist; last-found-wins is silent today. (C4)
  ```
- [ ] **Clarify or enforce `services[]` in `executor.json`.** It is required-by-schema but
  ```
  never matched against System service names; both examples repeat the executor name inside
  it, reinforcing the service/executor conflation. (B3)
  ```
- [ ] **Path-resolution consistency.** Expand `executors.registries[].url` (local) against
  ```
  the project root like `storage.directory`/`storeDir`, not the CLI working directory. (F2)
  ```
- [ ] **Config slice merge semantics.** Viper replaces `executors.registries` instead of
  ```
  merging, silently dropping the default `github` registry when a user declares `local`.
  (F3)
  ```
- [ ] **`neuron instance` with no subcommand prints help.** It exits silently today; teach
  ```
  the user the subcommands instead. (E6)
  ```
- [ ] **Execution-history retention policies.** Implement `storage.executionHistory:
  ```
  none | memory | local`; execution semantics must not depend on persistence.
  ```
- [ ] `neuron execution` **inspection command.** Re-add as a real surface that lists
  ```
  executions across instances (currently only visible via `neuron instance list`).
  ```
- [ ] **Per-executor resource limits.** CPU, memory, file count, and runtime limits in the
  ```
  process runtime.
  ```
- [ ] **Stronger process isolation.** Sandboxing (seccomp, namespaces) or a dedicated runtime
  ```
  backend for external modules.
  ```
- [ ] **Concurrency backpressure.** Bound in-flight executions per instance and per daemon.
- [ ] **Execution record audit.** Decide which persisted record fields matter in the product
  ```
  model vs. internal use.
  ```
- [ ] **TLS for the opt-in TCP endpoint.** Document client-side verification.
- [ ] **Protocol negotiation.** Add explicit `neuron/executor-v1` version/capability negotiation
  ```
  checks in the runtimes' handshake.
  ```
- [ ] **Executor-go examples.** Add gRPC-only, JSON-only, and two-runtime reference executors.
- [ ] **Request deadlines end-to-end.** Propagate CLI-provided execution deadlines through the
  ```
  whole execution path instead of relying on transport timeouts.
  ```



## Distribution

- [x] **Build and publish first executor**
  ```
  Build the .NET SDK 'executor-dotnet', in the packages/ folder and make sure it implement the neuron runtime protocol. the executor should be written in .NET, and it should contain the typescript Service package in it repo,
  Not yet pushed to GitHub / not yet published to a registry:
  - packages/executor-dotnet/Neuron.Executor: builds, 26 tests green, packs as NuGet. Referenced by ProjectReference from the content-extract repo.
  - content-extract repo (Desktop/content-extract): .NET executor (neuron/executor-v1 gRPC), executor.json manifest, @neuron/content-extract TS package, release.sh, CI + release workflows. Locally E2E-verified via `neuron register` + `neuron run` through the local catalog.
  - Remaining for a public release: push content-extract to GitHub, create v1.0.0 tag, publish Neuron.Executor to NuGet, replace the sibling-checkout ProjectReference/file: dependency with versioned package references.
  ```

- [ ] **Publish first official** `v0.1.0` **release.** `scripts/release.sh` and the tag-triggered
  ```
  workflow are ready; create the tag when CI is green.
  ```
- [ ] **Publish** `@neuron/sdk` to a package registry with `engines` metadata and clear versioning.
- [ ] **Publish the shared Go module** (`github.com/Muhammad-Jay/neuron/shared`) so consumers can
  ```
  depend on `executor-go` and the protocol contracts without vendoring.
  ```
- [ ] **Public module ecosystem.** Publish the reference `echo` module and a starter catalog
  ```
  reachable by the default `github` registry; document catalog conventions.
  ```
- [ ] **Additional registry runtimes.** Progress on `oci` and `remote` executor runtimes.



## Documentation

- [ ] **Stale event names in GETTING_STARTED.** The guide shows `service.evaluating`;
  ```
  the real events are `service.started`/`service.completed`/`execution.completed`
  (`nore/internal/event/event_types.go`). Align the shipped runnable transcripts. (E1)
  ```
- [ ] **`neuron` root help says "workflow engine CLI".** Neuron is explicitly not a workflow
  ```
  engine (`application/internal/cli/cli.go:25`); change to "Neuron CLI". (E3)
  ```
- [ ] **`application/README.md` daemon-lifecycle fix.** It claims the daemon stops when the
  ```
  CLI process exits; the daemon persists. Correct the text. (E4)
  ```
- [ ] **Remove stale example config.** `examples/ecommerce_order_ts/neuron.config.ts` ships
  ```
  dead `script.build`, `storage.directory: "./home"`, and `storage.provider: "postgres"`;
  `examples/ecommerce_order/neuron.yaml` ships an `official` registry that `BuildCatalog`
  ignores. (E2)
  ```
- [ ] **Create or remove `docs/executors/2026-09-05-*.md`.** Referenced by the item below
  ```
  but the directory does not exist. (E5)
  ```
- [ ] **Document executor naming + config requirement.** Explain `owner:capability:sub` and
  ```
  that `neuron.yaml` `executors.registries` is required for external executors even in TS
  projects, in the SDK README and MODULES.md. (A1/B1/F1)
  ```
- [ ] **Design-notes sync.** `docs/executors/2026-09-05-*.md` must never contradict shipped
  ```
  registry/installer behavior; update as the ecosystem evolves.
  ```
- [ ] **Runtime deep dives.** Extend `docs/RUNTIME.md` with the execution plan, event breakdown,
  ```
  and recovery paths.
  ```
- [ ] **Changelog / migration notes.** Record breaking changes between 0.1.x releases.
- [ ] **Pre-1.0 API audit.** Audit every exported symbol in the SDK and `executor-go`; unexport
  ```
  anything that is not a deliberate contract.
  ```

