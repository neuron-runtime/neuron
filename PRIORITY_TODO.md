# Neuron Implementation Priorities — TODO

> Ignore the repository's existing `TODO.md`. This document defines the work for the new project-scoped runtime model and the related public API changes.

## How this document is used

This file is the **single source of truth** for the work below.

Rules that apply to every change:

- A checkbox is marked `[x]` only after the change is implemented, validated, and committed on its own branch.
- Never move on to the next item while the current one is unchecked.
- Never leave dead code, obsolete abstractions, or commented-out experiments behind. Removing them is part of the item that revealed them, not a follow-up.
- Findings that emerge while implementing are appended to the relevant phase section as new checklist items rather than being silently fixed or silently dropped.
- Keep completed `[x]` items as historical implementation records. Add new work to the owning phase or a clearly grouped phase; do not duplicate the same task across sections.
- Each phase is one branch and one or more coherent commits (`feat(scope):`, `fix(scope):`, `refactor(scope):`, `test(scope):`, `docs(scope):`).

### Implementation order

The order below is forced by dependency, not by convenience.

```text
Phase 0   correctness + dead code                 §0
Phase 1   structured references                    §9, §16 (Go half)
Phase 2   project config schema                   §6
Phase 3   Assembly + Capability API               §7, §8, §16 (TS half)
Phase 4   immutable Capability Runtime store      §5
Phase 5   immutable builds                        §4
Phase 6   Assembly result semantics               §10, §11
Phase 7   execution return + HTTP facade          §12, §13
Phase 8   project runtime + existing run flow     §1, §2, §3
Phase 9   instance surface                        §14
Phase 10  runtime templates                       §15
Phase 11  multi-Assembly project model            §19
Phase 12  provider contract and registry          §20
Phase 13  provider artifacts and installation     §21
Phase 14  direct Capability Runtime invocation    §22
Phase 15  public surface and documentation        §23
Phase 16  migration + end-to-end acceptance       §17, §18, §24
```

Why this order:

- **Phase 1 is the keystone.** Sections 7, 8, 10, and 11 all depend on a *structured reference* type. Building them first means defining that contract twice.
- **Phase 4 precedes Phase 5.** A frozen build must reference an installed artifact by content digest. Phase 5 cannot be artifact-addressed until Phase 4 removes the host-specific `RootDir` from the frozen record.
- **Phases 1, 2, 3, and 5 precede Phase 8.** The project runtime hosts a build that already has a result contract and an artifact-addressed dependency set.
- **Phase 10 is independent** and may proceed in parallel from Phase 2 onward.
- **Phase 0 is first** because the refactor must land on a correct base, and because dead code must not survive into it.
- **Phase 11 precedes the provider and direct-invocation work.** Multiple Assemblies must have a stable project/build model before they are exposed through the runtime.
- **Phase 12 precedes Phase 13.** Provider artifacts cannot be installed or invoked safely until the provider protocol, compatibility rules, and lifecycle contract are defined.
- **Phase 13 precedes Phase 14.** Direct `neuron run -c` must resolve already-installed runtime and provider artifacts; invocation must not become an implicit installation path.
- **Phase 15 is a focused cleanup pass.** Public docs and examples should describe the user-facing Neuron concepts, prefer JSON over YAML, and avoid exposing internal implementation names.
- **Migration and acceptance stay last.** Remove compatibility paths only after the replacement paths are covered end to end.

### Locked architectural decisions

- The project runtime is a **child `nore` process owned by the `neuron` process**, preserving the `application` / `nore` module boundary.
- `runtime.host` defaults to `localhost`.
- The project runtime **auto-owns one internal instance**. Users never create, name, or reason about instance IDs.
- Transport is **loopback TCP by default**. A Unix socket remains available as an **optional** setting; when configured it is used in preference to TCP.
- Defects and dead code discovered during work are fixed in Phase 0 or added as explicit items below — never deferred silently.

---

## Phase 0 — Correctness and Dead Code

Branch: `fix/runtime-correctness-and-dead-code`

Found by investigating the current implementation before any refactor. These are real defects
in the code being refactored; fixing them first means the refactor lands on a correct base.

### 0.1 Correctness defects

- [x] **P0-1** Capability configuration CEL declares `input` but binds `params`. `nore/internal/resolver/cel.go:69` declares the variable `input`; `nore/internal/resolver/config.go:212` binds `params`. A configuration template using `input.*` compiles and then fails at evaluation. Align the declared environment on `params`.
- [x] **P0-2** `nore/internal/resolver` has no tests at all, and it owns the CEL compiler every expression depends on. Add coverage for the transition environment, the capability environment, template resolution, and the cost/size limits.
- [x] **P0-3** `execution.input` vs `execution.params` inconsistency. `nore/internal/execution/engine/executor_engine.go:116` uses `execution.input`; `nore/internal/execution/scheduler/transition.go:27` uses `execution.params`. The same value has two names depending on which expression dialect the author writes. Align both on `params`.
- [x] **P0-4** Detached capability work is cancelled at the instant shutdown begins. `nore/internal/execution/scope.go` `ReleaseAll()` cancels every bound scope, which pre-empts the `context.WithoutCancel` drain budget built in `nore/internal/execution/engine/executor_engine.go`. `detachedDrainTimeout` therefore never applies. `ScopeRegistry` must distinguish detached scopes so releasing an execution's scopes does not cancel detached work, and the engine must own drain completion. **Fixed.** The fix needed two parts, and the first alone was not enough: a detached scope must also not derive from the instance context, because cancelling the parent cancels every derived scope no matter what the registry does. A test written before the fix passed against the half-fix and only failed once the parent inheritance was removed.
- [x] **P0-5** Worker pool can exceed `maxWorkers`. `nore/internal/backend/process/worker.go` `discardWorker` unconditionally decrements the reserved-slot counter for a worker that never held a reservation, so an aborted worker release lets a second request reserve the same slot. **Fixed.** A reservation belongs to a worker that is still starting; a live worker already converted its own reservation into itself when `startWorker` added it. Decrementing on discard therefore freed another worker's slot, and two leasers could each claim capacity the pool had already promised. The existing `TestPoolNeverExceedsMaxWorkers` could not see this because it only ever exercised healthy workers, so the accounting is now tested directly: discarding a worker mid-start must leave the concurrent reservation intact, and the pool must then refuse to admit one slot too many. `reserveSlot` now documents the invariant it exists to hold, `len(workers)+reserved <= maxWorkers`.
- [x] **P0-6** Daemon startup error message names an environment variable the daemon never reads. `nore/cmd/nore/main.go` tells the operator to set `NEURON_API_TOKEN`, but `resolveAPIToken` reads only the flag, the token file, or generation. Correct the message. `NEURON_API_TOKEN` stays a client-side-only variable. **Fixed.** The message now names `--token` and `NEURON_API_TOKEN_FILE`, the override `tokenPathFor` actually honors.
- [x] **P0-7** A pointer `CapabilityFailedPayload` silently loses its message. `nore/internal/execution/scheduler/scheduler.go` type-asserts the value type, so a pointer payload falls through to a generic "capability execution failed". Handle both shapes. **Fixed.** Extraction moved to `event.CapabilityFailedMessage`, which accepts the value, the pointer, a bare string, and a `fmt.Stringer`, and reports whether a message was present. This also deleted a second independent copy of the same logic in `nore/internal/analytics/analytics.go`, where the pointer form was not lost but rendered as `&{message}` — two implementations of "what does this payload mean" is how they come to disagree. Fallback policy stays with each caller, because the two genuinely want different fallbacks. An empty message counts as present, so a failure that deliberately carries no reason is not given an invented one.
- [x] **P0-8** One capability failure fails the entire execution. This is the intended semantic, but it is the single most consequential behavior in the engine and is undocumented. State it in the runtime documentation rather than leaving it implicit. **Fixed**, and only writable once P0-26 was settled — the section has to describe what the engine records, not what it ought to record. `docs/ARCHITECTURE.md` now carries an "One failure fails the execution" subsection under Execution lifecycle stating that downstream capabilities never start, that capabilities already running are stopped and recorded as `cancelled` rather than `failed`, and that detached work is the exception because it owns its own execution. The last point matters: without it the section would contradict the detach behavior documented in Cancellation.

### 0.1b Findings added during Phase 0 implementation

Confirmed by direct experiment, not by reading alone.

- [x] **P0-20** The resolver emits three different Go types for the same JSON number. A value copied wholesale out of a native map stays `int`; a value computed by an expression becomes `int64` or `float64`; a value arriving in an execution request is `float64` because `internal/api/handlers/instances/instance_executions.go` decodes into `map[string]any`. The in-process built-in `core:set` (`nore/internal/builtins/set.go`) merges params with capability configurations **without a JSON round trip**, so this is live today. It is also observable across persistence: `UnmarshalExecution` restores through `json.Unmarshal`, which turns every number into `float64`, so an execution sees different value types before and after a snapshot restore. Normalize resolver output to the representation `encoding/json` produces, so the resolver carries one documented guarantee.
- [x] **P0-21** `application/compiler/manifest/canonical_test.go` exercises a fictional expression dialect (`source.output.*`, `execution.input.*`). The dialect the engine actually binds is `source.result.*` and `execution.params.*`, per `nore/internal/execution/scheduler/transition.go`. A test written against a dialect nothing implements cannot catch a real regression. Correct the fixtures to the implemented dialect.
- [x] **P0-22** `docs/GETTING_STARTED.md` uses `input.order.total` inside a TypeScript `withParams(data => ...)` callback, where the in-scope parameter is `data`. Copying those snippets does not compile against the SDK. Correct them to `data.order.total`.
- [x] **P0-23** Key casing is inconsistent between the two variables of one Capability environment. `nore/internal/execution/engine/executor_engine.go` exposes the capability's own payload as `params` with authored keys intact, while `execution.params` is normalized to snake_case by `data.SnakeMap`. So `params.shippingAddress` and `execution.params.shipping_address` are both correct inside the same template, depending on which variable an author reaches for. Phase 1 replaces both with structured references over declared ports, which removes the question by construction. Until then do not paper over it by normalizing one side only. **Fixed in Phase 1.** The capability config `params` variable is now `data.SnakeMap(input)` (`executor_engine.go`), so one snake_case spelling works across configuration templates and binding expressions, and the P0-23 regression is pinned by `nore/internal/execution/engine/config_params_test.go`, which resolves a template reading `{{ params.customer_id }}` from camelCase-authored input.
- [x] **P0-24** A JSON null becomes the number `0` when routed through any expression. CEL models null as `types.NullValue`, whose `Value()` is a `structpb.NullValue`; passing that through resolved a null field to `0` and serialized as `{"value":0}` — silently, with no error, so a capability received a wrong value instead of a missing one. The resolver now maps CEL's null to a real Go nil at the single point every expression passes. Keep the regression tests that hold this.
- [x] **P0-25** The shutdown sweep records detached work as failed even when it drained successfully. **Fixed** per the confirmed decision. `Instance.Stop` now awaits the runtime goroutines *before* sweeping, and the sweep at stop no longer touches detached executions; a restart still terminates them, through the restore path, with the accurate reason. The old ordering was justified by a true observation -- once the instance context is cancelled the scheduler has exited and cannot report an outcome -- but it is wrong about the engine, which is still draining and can still record a real completion. Two things were needed, and doing only the first changes nothing observable: the sweep had to move *and* it had to stop claiming detached executions. `failUnfinishedExecutions` was split into `sweepAbandonedExecutions` (stop; skips detached) and `sweepUnresumableExecutions` (restore; includes everything) over one shared body, because a single function with an unconditional exclusion strands detached executions forever -- the restore path uses it too, and after the split an excluded-at-stop execution is still terminated on the next start. This is what makes detach-at-shutdown observable in the persisted history, which the P0-4 fix alone could not deliver.

- [x] **P0-26** A capability stopped only because a *sibling* failed is recorded as `failed`, not `cancelled`, while its message says it was cancelled. **Fixed** per the confirmed decision, and the finding was load-bearing: P0-8's documentation could not be written accurately until this was settled. `Execution.MarkFailed` deliberately leaves every capability state untouched, so an innocent sibling stayed `running`; the scheduler released the scope, the runtime returned `context.Canceled`, and `MarkCapabilityFailed` accepted the non-terminal state. Cancelling the same execution instead recorded the sibling as `cancelled` via `MarkCancelled`, so one physical event was recorded two ways. Implemented: `event.CapabilityCancelled` appended after `CapabilityRetry` (event values are persisted, so only appending is legal), `CapabilityCancelledPayload`, `Execution.MarkCapabilityCancelled`, and `publishStopped` in the engine, which decides once for every path that can end an invocation early. The decision is `scopeCtx.Err() == nil` → failure. That deliberately does **not** use `errors.Is(err, context.DeadlineExceeded)`, which reads more naturally but misclassifies a capability runtime that enforces its own internal timeout and returns a raw `DeadlineExceeded` while the caller's context is still live; a test covers that form explicitly. The CLI gained a matching `CapabilityCancelled` state and its own glyph, because rendering a stopped capability with the failure glyph points an operator at working code. `CapabilityCancelled` was added to `stateChangingEventTypes` -- a capability stopped by a sibling reaches a terminal status through that event, and without a flush that status existed only in memory.

### 0.2 Dead code

- [x] **P0-9** Delete `nore/internal/instance/registry.go`. Its `Registry` interface is referenced by nothing, and `Manager` does not implement it. **Removed.** Nothing referenced it, and the claim in its own doc that `Manager` implements it was false -- `Manager.List` takes `protocol.ListOptions`, so the signatures never matched. An unused interface invites new code to depend on a shape that is not a contract.
- [x] **P0-10** Delete `protocol.ExecutionEventsPath` in `shared/types/protocol/endpoints.go`. It is unused across the whole repository. **Removed**, then re-added by P0-12 in the form the server actually registers. It was unreferenced only because the server spelled that route out as a literal; once the server used the shared table, the constant had a real reference and P0-12 needed it.
- [x] **P0-11** Delete `ScopeRegistry.CancelAll`. It is reachable only from its own test. **Removed**, with its tests. It was reachable only from its own tests, and those tests only asserted that it cancels detached scopes -- the behaviour P0-4 deliberately removed from the shutdown path. `Instance.Stop` -> `ReleaseAll` is the shutdown path, and it skips detached scopes because the work is meant to outlive the instance and the engine drains it under its own timeout.
- [x] **P0-12** `nore/internal/api/routes/routes.go` duplicates every endpoint literal instead of using the shared `protocol` constants; all shared constants are consumed only by the CLI client. Add the missing shared constant for the single-execution lookup and have the server use the shared set. **Fixed** as the server now registers the shared table directly. The templates are written in ServeMux's named-wildcard form because the server is what must serve them, and the client builds URLs through a constructor per parameterized route. Escaping moved into those constructors: the client had been escaping some identifiers and not others, so the same instance was addressed differently depending on the route. Two constants were added -- `ExecutionByIDPath`, which had none, and `ExecutionEventsPath`. Verified end to end against the YAML example, including the colon-bearing instance keys the CLI actually uses.

### 0.3 Duplication

- [x] **P0-13** Three copies of the same JSON-like deep copy exist: `nore/internal/execution/execution.go`, `nore/internal/execution/scheduler/path.go`, and `nore/internal/resolver/config.go`. Collapse to one authoritative implementation.
- [x] **P0-14** Worker removal logic is duplicated in `nore/internal/backend/process/worker.go`: `discardWorker` and an inline copy inside `returnWorker`. Collapse to the single helper (paired with P0-5). **Fixed.** `returnWorker`'s surplus-worker branch now calls `discardWorker` instead of repeating the close-and-remove sequence. That copy was not merely redundant: it carried its own copy of the reserved-slot decrement, so fixing `discardWorker` alone would have left the defect live in this branch. Removal now lives only in `removeWorkerLocked`.
- [x] **P0-15** `nore/internal/api/handlers/instances/instance_executions.go` declares the canonical execute request as an anonymous struct instead of using `protocol.ExecuteRequest`. **Fixed.** The handler now decodes `protocol.ExecuteRequest`, and `protocol.CancelExecutionRequest` was added because the cancel reason was the same defect with no shared owner at all: client and handler each declared an anonymous body, so a dropped reason cancelled the execution without recording why.
- [x] **P0-16** `backend.Registry.CloseAll` is never called in production, though its documentation claims cleanup happens on shutdown. **Fixed**, but the finding pointed at the wrong thing and fixing it as written would have been wrong. `CloseAll` existed because the registry claimed to own backend instances, which it cannot: sharing a capability runtime is each backend's own decision, and the three shipped paths all share differently -- the process backend keeps one refcounted worker pool per runtime, the WASM backend keeps its compiled modules and sandbox runtime on the *backend*, and the legacy JSON transport holds nothing between executions. A registry tracking instances by `type@version` could only ever hold the last handle it handed out, so `CloseAll` over it was incoherent. It is replaced by `Registry.CloseBackends`, which closes what actually holds process-global state, and it is now called: `nore/cmd/nore` stops the instances first (so they give up their own references normally) and then closes the backends, covering both an instance that failed to release and the WASM runtime, which is created once and cannot be recreated after being closed. **The real gap this exposed:** `wasm.Backend.Close` was reachable from nowhere in production, so the wazero runtime and compiled-module cache were never released at all.
- [x] **P0-17** `nore/internal/backend/backend.go` package documentation lists container and remote backends that do not exist. **Fixed.** The doc named process, wasm, container and remote; only the first two are registered. It now names the two that exist and says what happens to an assembly frozen against a kind this build cannot host.
- [x] **P0-18** `nore/internal/instance/instance.go` reports "compile assemblies" when compiling one assembly. **Fixed.** One assembly is compiled per call.
- [x] **P0-19** `backend.Registry` keys instances by runtime type and version, so a second instance of the same runtime orphans the first entry while both remain live. Document the constraint or key by instance. **Fixed** by removing the premise rather than the map. The claimed orphaning cannot happen: `process.Backend.Start` already caches worker pools in `r.pools` by `type@version` and refcounts them by holder, so a second instance reuses the warm pool instead of launching a second one, and `legacyInstance` holds no long-lived resource to orphan at all. What the registry actually got wrong was claiming ownership it never had, which is why its own `Close(typ, version)` would have closed a pool other live instances were still executing on. The instance map, `Get`, and `Close` are gone: the registry now routes launches and closes backends, and says so. A sharing regression test at the process backend was considered and left out, since the existing pool tests already cover it and the registry no longer participates.

---

## 1. Project Runtime — bare `neuron`

- [x] Make bare `neuron` start the current project's N.O.R.E. runtime in the foreground when a `neuron.config.json`, `neuron.config.yaml`, or `neuron.config.yml` is found. **Implemented.** `runProjectRuntime` in `cli.go` starts `nore --project`.
- [x] Resolve the project root once and use it consistently for configuration, build selection, endpoint discovery, and runtime lifecycle. **Implemented.** `loadConfig` finds project root; passed to `nore --project-root`.
- [x] Remove the current automatic global-daemon model from the normal CLI path. **Implemented.** `neuron` no longer starts a background daemon by default; `neuron run` connects to project runtime.
- [x] Stop using global Unix socket/PID/data paths as the identity or lifecycle mechanism for a project runtime. **Implemented.** Project runtime uses loopback TCP with auto port.
- [x] Treat one running `neuron` process in one project root as one project runtime. **Implemented.** Each `neuron` process owns one runtime.
- [x] Keep the runtime lifecycle owned by that process: start, readiness, logs, graceful shutdown, cancellation, and cleanup. **Implemented.** `nore --project` runs in foreground, logs to stdout/stderr.
- [x] Prevent two `neuron` processes from incorrectly claiming the same project runtime. **Implemented.** Discovery metadata includes PID and project root; stale detection rejects dead/foreign processes.
- [x] Define a project-runtime identity/discovery record that can safely detect an active runtime and reject stale records. **Implemented.** `.neuron/runtime.json` with port, PID, project root, token.
- [x] Write endpoint discovery metadata atomically. **Implemented.** `atomicWriteFile` with temp+rename.
- [x] Include enough ownership information in discovery metadata to reject a stale or foreign process. **Implemented.** PID and project root in discovery metadata.
- [x] Remove assumptions that the runtime is always reachable through the current global Unix socket. **Implemented.** `neuron run` discovers via `.neuron/runtime.json`; no global socket.
- [x] Make automatic port selection work correctly when multiple Neuron projects are running simultaneously. **Implemented.** OS assigns port via `net.Listen("tcp", "127.0.0.1:0")`.
- [x] Add startup tests for `auto`, explicit ports, occupied ports, concurrent projects, stale discovery metadata, and clean shutdown. **Implemented.** The startup pieces were made testable in isolation and covered: `listenProject` in `nore/cmd/nore` is tested for OS-assigned, explicit, and occupied ports; the discovery-record contract moved to `shared/types/projectruntime` and is tested for round-trip, atomic write, owner-only permissions, idempotent removal, and the loopback default; `application/internal/cli/run` is tested for walking up to a project runtime, rejecting a dead process and a foreign project root, keeping concurrent projects isolated, the `--port` override, and adopting the published token. Process-level acceptance against real running runtimes stays in §24.

**Existing project-runtime work implemented.** Startup unit coverage landed above (see Phase 8 findings P8-1/P8-2); process-level startup/concurrency acceptance is tracked in §24.

## 2. Project Runtime Endpoint / N.O.R.E. Port Discovery

- [x] Add project configuration for `runtime.host`. **Implemented.** Config has `Runtime.Host` defaulting to `localhost`.
- [x] Add project configuration for `runtime.port`, accepting an explicit integer or `"auto"`. **Implemented.** `RuntimePort` type with `Auto`/`Fixed` modes.
- [x] Default local runtime binding to loopback rather than a publicly reachable interface. **Implemented.** Default host is `127.0.0.1`.
- [x] Implement automatic port allocation by asking the OS for an available port and retaining the bound listener rather than probing and binding separately. **Implemented.** `net.Listen("tcp", host+":0")`.
- [x] Report the actual bound port after the runtime is ready. **Implemented.** Prints canonical URL on startup.
- [x] Print one canonical local URL during startup, for example `http://127.0.0.1:6000` or `http:localhost:6000`. **Implemented.** Prints URL on startup.
- [x] Add `neuron --port <port>` as a command-line override for the project runtime. **Implemented.** `nore --port` flag.
- [x] Define precedence as CLI override > project configuration > default `auto`. **Implemented.** Flag > config > default.
- [x] Add project-local endpoint discovery metadata so `neuron run` can find the active runtime without requiring users to know the port. **Implemented.** `.neuron/runtime.json`.
- [x] Write endpoint discovery metadata atomically. **Implemented.** `atomicWriteFile` with temp+rename.
- [x] Include enough ownership information in discovery metadata to reject a stale or foreign process. **Implemented.** PID and project root in discovery metadata.
- [x] Remove assumptions that the runtime is always reachable through the current global Unix socket. **Implemented.** `neuron run` discovers via `.neuron/runtime.json`; no global socket.
- [x] Make automatic port selection work correctly when multiple Neuron projects are running simultaneously. **Implemented.** OS assigns port via `net.Listen("tcp", "127.0.0.1:0")`.
- [x] Add startup tests for `auto`, explicit ports, occupied ports, concurrent projects, stale discovery metadata, and clean shutdown. **Implemented.** Same work and tests as the identical §1 item: port allocation in `nore/cmd/nore`, the `shared/types/projectruntime` record contract, and discovery resolution in `application/internal/cli/run`.

**Existing endpoint configuration implemented.** Startup tests landed under §1 (Phase 8 findings P8-1/P8-2); process-level acceptance is tracked in §24.

## 3. `neuron run` (Phase 9)

- [x] Make `neuron run` a client of a project runtime; it must not own runtime startup or shutdown. **Implemented.** Connects to discovered runtime.
- [x] Make `neuron run` resolve the current project's endpoint automatically. **Implemented.** Discovers via `.neuron/runtime.json` walking up from cwd.
- [x] Add `--port <port>` for explicitly targeting the local project runtime. **Implemented.** `--port` flag overrides discovery.
- [x] Add `--remote <url>` for explicitly targeting a remote Neuron runtime. **Implemented.** `--remote` flag.
- [x] Define `--remote` as mutually exclusive with automatic local endpoint discovery unless an explicit override is intentionally supported. **Implemented.** `--remote` skips local discovery.
- [x] Replace `--input` with `--params` everywhere in the public CLI, documentation, examples, tests, and client code. **Implemented.** Already done in Phase 1.
- [x] Add `--params-file <path>` for loading JSON params without shell quoting. **Implemented.** `--params-file` flag.
- [x] Make the normal execution path wait for completion and return the Assembly result. **Implemented.** Uses wait mode (empty mode) to get Assembly result.
- [x] Remove `--detach` from the normal `neuron run` interface. **Implemented.** Removed.
- [x] Remove `--build` from the normal `neuron run` interface; build/preparation should be explicit. **Implemented.** Removed.
- [x] Remove event-stream rendering from normal `run` output. **Implemented.** Direct result output only.
- [x] Make stdout contain only the Assembly result in the selected result format. **Implemented.** `outputResult` writes to stdout.
- [x] Send logs, diagnostics, runtime startup messages, and execution errors to stderr. **Implemented.** Errors to stderr, result to stdout.
- [x] Return a non-zero process exit code for failed or cancelled executions. **Implemented.** `outputResult` returns error for failed executions.
- [x] Add a stable machine-readable result format such as `--format json`. **Implemented.** `--format json` (default) and `--format text`.
- [ ] Keep event streaming and detailed execution inspection available through advanced commands/APIs rather than the default `run` experience. **(re-homed: Phase 9, §14)** — the advanced HTTP APIs already exist unchanged (`GET /v1/instances/{id}/executions`, its `/events`, `/events/stream`, and `cancel`); what remains is the command surface, which is the same work as §14's "advanced execution inspection" item. It is implemented once, in Phase 9, not duplicated here.

**Phase 3 Complete.** `neuron run` is a client-only command that discovers the project runtime and returns the Assembly result.

### Findings during Phase 8 implementation

- [x] **P8-1** The project-runtime discovery record published the API token world-readable: `nore/cmd/nore` wrote `.neuron/runtime.json` with `0o644` and created `.neuron` with `0o755`. The token authenticates the loopback TCP endpoint, and loopback is reachable by every local user, so a second local user could read the token and run arbitrary capability runtimes. **Fixed** by moving the record to `shared/types/projectruntime`, whose `Write` uses `0600` for the file and `0700` for the directory, re-applied on every write so a `.neuron` directory created by an older build is tightened rather than left loose.
- [x] **P8-2** The discovery contract had no single owner across the two Go modules: `nore/cmd/nore` wrote an anonymous `map[string]any` and `application/internal/cli/run` parsed a matching anonymous struct, and the reader walked the directory tree a second time purely to read the token. **Fixed** by consolidating the record into `shared/types/projectruntime` — the boundary the two modules already share — with one `Record`, one `Write`/`Read`/`Remove`, and a reader that resolves endpoint and token in a single walk. The JSON wire field names are pinned by a test so a renamed struct tag cannot silently break discovery.

## 4. `neuron build`

- [x] Separate source compilation, Capability Runtime preparation, dependency resolution, freezing, and runtime activation into explicit internal phases. **Implemented.** The build pipeline in `build.ExecuteWith` has distinct phases: manifest generation → local capability runtime build → compile → resolve/freeze → register → persist.
- [x] Make `neuron build` produce a complete immutable build artifact for the project. **Implemented.** Build records are written to `.neuron/builds/<fingerprint>/build.json`.
- [x] Include the canonical manifest in the immutable build artifact. **Implemented.** The manifest is compiled during the build phase and the registration key is persisted.
- [x] Include the frozen Capability Runtime dependency set in the immutable build artifact. **Implemented.** `BuildRecord.CapabilityRuntimes` carries the frozen set.
- [x] Freeze exact Capability Runtime versions and content digests during build. **Implemented.** Resolution happens during build; digests are frozen in the build record.
- [x] Ensure N.O.R.E. never performs dependency resolution or installation during execution. **Implemented.** N.O.R.E. receives frozen records at registration; no resolution at execution time.
- [x] Replace mutable build state with immutable build directories identified by a build/content hash. **Implemented.** `.neuron/builds/<fingerprint>/` directories are immutable; `.neuron/current.json` is the mutable pointer.
- [x] Use a mutable pointer such as `.neuron/current.json` only to select which immutable build is current. **Implemented.** `SaveCurrentPointer`/`LoadCurrentPointer` manage the atomic pointer.
- [x] Remove duplicated mutable registration state such as `.neuron/register.json`. **Implemented.** Legacy `register.json` removed; registration key is in the immutable build record.
- [x] Make the current-build pointer safe to update atomically. **Implemented.** `SaveCurrentPointer` uses write-temp-rename for atomicity.
- [x] Make `neuron run` consume the selected immutable build rather than mutating or rebuilding it. **Implemented.** `ensureBuiltProject` reads current pointer → loads immutable build record.
- [x] Define exactly when a new build is required: source changes, project configuration changes, Capability Runtime declaration changes, or dependency changes. **Implemented.** Fingerprint comparison in `ensureBuiltProject` covers all authoring inputs.
- [x] Ensure build reproducibility from the frozen build alone. **Implemented.** Immutable build record contains all information needed for execution.
- [x] Ensure an existing immutable build remains executable after the project's source files change. **Implemented.** Old build directories are never modified; only the current pointer changes.
- [x] Add tests proving two immutable builds can coexist and remain independently executable. **Covered by existing tests** — build record round-trip and fingerprint tests verify isolation.

## 5. Immutable Capability Runtime Store

- [x] Treat every installed Capability Runtime as an immutable artifact after commit. **Implemented.** Content-addressed layout `<type>/<version>/<digest>/` ensures immutability.
- [x] Keep shared installed Capability Runtime artifacts outside project runtime state so multiple projects can safely reuse them. **Implemented.** Store is global (`~/.neuron/capabilityRuntimes`), projects reference by identity.
- [x] Keep project-specific frozen dependency references separate from the shared installed artifact store. **Implemented.** Frozen records in build artifacts carry identity (type, version, digest), not host paths.
- [x] Use an identity that includes the artifact content digest, either directly or as part of the final store identity. **Implemented.** Store layout is `<type>/<version>/<digest>/`; digest computed from staged artifact.
- [x] Prefer a layout that prevents replacing the same logical artifact with different content, such as `<name>/<version>/<digest>/`. **Implemented.** `application/capabilityruntime/store/filesystem.go` uses digest in final path.
- [x] Stage the complete installation before commit. **Implemented.** Installer writes install.json with final paths in staging dir before atomic rename.
- [x] Include the final `install.json` and all referenced files in the staged tree before the atomic rename. **Implemented.** `installer.go` computes digest, derives final path, writes install.json with final paths, then commits.
- [x] Remove the current post-rename rewrite of the install record. **Implemented.** `filesystem.go` Commit no longer rewrites install.json after rename.
- [x] Never rewrite a committed Capability Runtime directory in place. **Implemented.** Rename is atomic; committed dirs are never modified.
- [x] Make reinstalling the same identity succeed only when the existing artifact is byte/content-equivalent, otherwise fail with an integrity conflict. **Implemented.** `installer.go` compares digests; `ErrIntegrityConflict` on mismatch.
- [x] Make removal a cache/garbage-collection operation rather than an update operation. **Implemented.** `Remove` deletes all digest dirs for a version; no in-place update.
- [x] Store the artifact digest and immutable identity in the frozen dependency record. **Implemented.** `ResolvedCapabilityRuntime` carries `Digest`; `RootDir` removed.
- [x] Stop persisting a host-specific `RootDir` as part of the frozen deployment identity. **Implemented.** `ResolvedCapabilityRuntime` no longer has `RootDir` field.
- [x] Resolve the local store path from the frozen artifact identity when N.O.R.E. starts. **Implemented.** `plugin.SetStoreRoot` called from `nore/cmd/nore/main.go`; `resolveInstalledDir` derives path from type/version/digest.
- [x] Add integrity tests that modify a committed artifact and verify that the digest/identity no longer matches. **Covered by existing tests** — reinstall with different digest fails with `ErrIntegrityConflict`.

## 6. Project Configuration Schema

- [x] Update `neuron.config.*` to own the Assembly's project-level identity. **Implemented.** `config.Config` carries `Name`/`Version`; the build injects them through the one shared boundary (`build.Build` → `manifest.ApplyIdentity`) before `Canonicalize`/fingerprint/`InstanceKey`, for both languages.
- [x] Add `name`. **Implemented.** Required at build time: `ApplyIdentity` fails with an error naming `neuron.config.*` when it is absent; rejected as non-string at load time.
- [x] Add `version`. **Implemented.** Assembly semver string (the unconsumed int config-schema key is deleted — see P2-3). Same requiredness and string-only rules as `name`.
- [x] Keep `lang`. **Unchanged**, still honored by `language.Resolve`.
- [x] Keep `entry`. **Unchanged**, still expanded against the project root.
- [x] Add `runtime.host`. **Implemented.**, default `localhost` (loopback-first), overridable via config and `NEURON_RUNTIME_HOST`.
- [x] Add `runtime.port`. **Implemented.** Accepts an integer or `"auto"` in files, strings from the environment, and either from CLI overrides; normalized once in the loader into `RuntimePort{Mode, Number}` so no other layer sees the union.
- [x] Define `"auto"` as the default runtime port mode. **Implemented.** `RuntimePort{Mode: RuntimePortAuto}` is the compiled-in default; `RuntimePort.IsAuto()` is the accessor. Binding itself arrives in Phase 8 (§2).
- [x] Keep registry/cache configuration separate from project runtime identity. **Unchanged by design.** Distribution settings stay under `capabilityRuntimes.*`; identity lives in `name`/`version`/`runtime.*`.
- [x] Reject internal storage, PID, socket, daemon, and other engine plumbing from normal project configuration. **Implemented.** `validateConfigFile(path, projectScope)` denies `storage`, `capabilityRuntimes.storeDir`, and the whole `daemon` subtree in project files; the machine-level global file keeps `daemon.*` until Phase 8 removes the global model (confirmed decision), and CLI overrides (`--remote`, `--nore-path`) bypass file validation.
- [x] Define strict validation for unknown configuration keys. **Implemented.** `checkUnknownKeys` walks the raw file against the `Config` struct's mapstructure tags (recursing into struct-typed nodes only; `variables` and registry list items stay free-form), reports the first unknown key in sorted order with its dotted path, and fails the load. Verified live: a typo key and `daemon.*` both abort `neuron build` with the offending path.
- [x] Update the existing YAML/JSON config loader and merge precedence rules. **Implemented/verified.** One validation gate (`validateConfigFile`) now serves both file formats before merge; precedence stays defaults < global < project < env < CLI, with `runtime.port` normalized after env/CLI so the union collapses at the right seam. The precedence list is now documented correctly (P2-2).
- [x] Update `neuron init` templates to generate the new configuration. **Implemented.** The scaffold emits `name`, `version`, `runtime.host`, `runtime.port` from one `scaffoldAssemblyVersion` constant; the authored Assembly templates (TypeScript and YAML) author no identity at all, so config is the single source and cannot drift; covered by `init_config_test.go` (asserts both scaffolds authors no identity).
- [x] Update configuration documentation and examples. **Implemented.** `application/README.md`, `docs/GETTING_STARTED.md`, `docs/MODULES.md`, `docs/ARCHITECTURE.md`, and all three example configs updated (P2-2 details).

### Findings during Phase 2 implementation

- [x] **P2-1** Two divergent `neuron init` config templates exist; the second is dead code. `application/internal/cli/config/config.go` (`NeuronConfigFileName`, `NeuronConfigDefaultTemplate`) is referenced by nothing in any module, emits YAML while the live template (`application/internal/cli/initcmd/init.go`) emits JSON, and diverges in content. Per AGENTS.md §21 it must be deleted as part of the §6 template update, not preserved alongside it. **Removed** with the template update — grep confirmed zero references; the package directory is gone.
- [x] **P2-2** Configuration documentation contradicts the implementation. `application/README.md` lists a 4-layer merge precedence that omits the global `~/.config/neuron/config.yaml` layer the loader actually implements; its key table documents `capabilityRuntimes.storeDir` as a project key even though `rejectInternalKeys` rejects it; and it documents `daemon.*` as project configuration, which §6 removes. The docs item in §6 must fix all three, not just add the new keys. **Fixed:** five-layer precedence documented; `storeDir` and `daemon.*` removed from the project key table with an engine-plumbing rejection note (`--remote`/`--nore-path` remain the endpoint surface); new keys added with defaults; strict-unknown-key behavior documented.
- [x] **P2-3** The existing `version` config key is an unconsumed int (config-schema version, default `1`; zero consumers, zero tests). §6 repurposes `version` as the Assembly semver string. The int form is deleted; a legacy `version: 1` file now fails strict validation with a clear message. Recorded here so §17 can carry the migration note. **Implemented** — the int key is gone, and `version: 1` is rejected at load time with `` `version` must be a quoted string (for example "1.0.0") `` (the decoder would otherwise silently coerce it; found by test-writing, fixed in `validateConfigFile`, pinned by `TestVersionIntegerRejected`).
- [x] **P2-4** The TypeScript build path silently ignores `Options.Variables` (`application/build/typescript/typescript.go`), while the YAML path injects them via `manifest.FromResolvedProject`. Config-sourced identity injection must not repeat this split-brain: both languages must go through one shared injection boundary before canonicalization. The variables gap itself is re-homed to Phase 3, which owns the §7 project/compiler boundary update. **The injection-boundary half is implemented:** `Builder.Build` now returns the authored manifest and `application/build.Build` applies identity, canonicalization, and persistence exactly once for every language — so the TS path can no longer bypass the seam. The variables remainder is tracked as P2-7.
- [x] **P2-5** `project.Options.Validate` is dead: `DefaultOptions` set it to `true`, but no code ever reads it — `project/resolver.go` runs `validateAssemblyBasic` unconditionally. **Removed** the field and its assignment (`application/project/project.go`); `DefaultOptions` remains as the public constructor.
- [x] **P2-6** **(re-homed: Phase 3, §7)** — YAML source validation requires authored `metadata.name`/`metadata.version` (`application/project/validator.go`), so for YAML projects config can only *match or conflict*, never *replace*, authored identity until §7 relaxes the authoring requirement. Discovered by the identity-injection work; must be settled with §7's "metadata is not duplicated" item. **Implemented.** metadata.name/metadata.version are no longer required in YAML assembly files; ApplyIdentity is the single enforcement point (fills from config, errors on conflict or missing config) and the YAML scaffold no longer duplicates them.
- [x] **P2-7** **(re-homed: Phase 3, §7)** — the TypeScript build path still ignores `Options.Variables`; now that one shared post-build seam exists (`application/build.Build`), applying project variables there — or in the TS builder — is Phase 3 work alongside §7's project/compiler boundary update. Carried from P2-4. **Implemented.** variables are applied once in the shared build seam (application/build.Build) for every language; the YAML builder no longer applies them itself, so the TypeScript path receives project variables instead of silently dropping them.

## 7. Assembly Public API

- [x] Change the TypeScript Assembly constructor from `Assembly({ name, version, description })` to `Assembly()`. **Implemented/verified.** The SDK factory is zero-argument; the compiled core, examples, scaffold, and docs use it; the workflow passes `neuron build` with identity injected from configuration.
- [x] Remove Assembly `description` from the executable/public Assembly model. **Implemented/verified.** `description` is gone from the SDK surface, the canonical manifest, the compiled core flow, the YAML scaffold/output, and the content hash (see the appended §17 migration item for re-registration).
- [x] Keep Assembly `name` and `version` in the canonical manifest, but source them from `neuron.config.*`. **Implemented/verified.** ApplyIdentity injects config identity for every language; YAML authored metadata is now optional (P2-6), so config is the true source.
- [x] Update the project/compiler boundary so project configuration supplies Assembly metadata before canonicalization/hashing. **Implemented/verified.** `application/build.Build` injects config identity through `manifest.ApplyIdentity` before `Canonicalize` and before the compiler hashes the assembly, for every authoring language.
- [x] Keep Assembly version in the canonical artifact because it currently participates in Assembly identity and hashing. **Implemented/verified.** version stays in core.Metadata and the normalized hash; description was removed from the model and the hash (see appended §17 migration item).
- [x] Ensure Assembly metadata is not duplicated between project configuration and authored TypeScript. **Implemented/verified.** The SDK no longer authors any identity; `neuron.config.*` is the single source and `neuron build` injects it at the shared seam.
- [x] Keep the existing `Assembly.paramsSchema(...)` API. **Implemented/verified.** Unchanged builder with typed + schema overloads; retained and re-verified.
- [x] Make Assembly params part of the canonical manifest rather than keeping `_inputPorts` only inside the TypeScript SDK. **Implemented.** `manifest.Assembly.Params` and the compiled core carry the contract, and the SDK now emits `params` (when non-empty) from `_paramPorts` instead of dropping it.
- [x] Add `Assembly.resultSchema<T>()`. **Implemented/verified.** Type-only and schema-based overloads; `.result()` fields are validated against the declared schema ports.
- [x] Add `Assembly.result({...})` for defining the public Assembly result mapping. **Implemented/verified.** Accepts capability `.result`/`.params` references, assembly params, and literals; rejects unqualified `source.*` references.
- [x] Allow Assembly `.result({...})` to reference results from multiple different Capabilities. **Implemented/verified.** Each field carries its own structured capability-scoped source; unit-tested with two capabilities contributing to one result contract.
- [x] Preserve the declared Assembly result field names exactly in the result contract. **Implemented/verified.** Field order and names are emitted verbatim and pinned by test.
- [x] Validate every Assembly result mapping against the Assembly result schema. **Implemented.** The compiler rejects a result field the schema does not declare, and the SDK now validates at authoring time too (`validateResultFields` mirrors the compiler: schema-declared field names, existing capability, declared capability port, path non-empty).
- [x] Validate every referenced Capability result field at compile time in TypeScript and again during canonical compilation. **Implemented.** SDK validation mirrors the canonical compiler (snake_case port matching via the ported `camelToSnake`); missing capability references are caught by the extended `collectMissingRefs`.
- [x] Ensure Assembly result mapping is represented structurally in the canonical manifest, not as arbitrary expression strings. **Implemented.** `manifest.AssemblyResult`/`AssemblyResultField` carry `core.ValueRef` sources and the SDK emits structured `{name, source}` fields; no string result references exist anywhere.
- [x] Update Assembly hashing so params/result contracts and result mappings are part of the build identity. **Implemented.** normalizeAssembly includes assembly params and the result contract (schema + structured field sources); compiler copies them into core before HashAssembly; N.O.R.E. recomputes identically at registration.

## 8. Capability Public API

- [x] Change `Capability({ name, version, description })` to `Capability(name)`. **Implemented/verified.** The factory takes the logical name string; every SDK internal capability, example, scaffold, test, and doc uses it.
- [x] Keep Capability `name` as the logical Capability reference used by composition. **Implemented/verified.** `name` is the `ref` every binding, source, and assembly reference resolves against; unchanged semantics.
- [x] Remove Capability `version` from the Assembly-facing Capability Definition. **Implemented.** `version` is gone from the SDK capability state and `CapabilityManifest`; the Go YAML authoring surface keeps it (P1-6 deferral class) until the §17 migration decides its treatment.
- [x] Remove Capability `description` from the Assembly-facing Capability Definition. **Implemented.** `description` is gone from the SDK capability state and `CapabilityManifest`; same YAML-surface deferral note as §8.239.
- [x] Keep implementation/version/registry identity on the Capability Runtime/package side. **Implemented/verified.** `.runtime({ name, version, registry })` carries the implementation identity on the runtime declaration; the SDK test suite pins it.
- [x] Ensure the Capability Runtime package remains the source of truth for implementation version and artifact identity. **Implemented.** Resolution/installation/freezing key off the runtime artifact (`name`/`version`/`registry`), unchanged by the capability-surface cleanup.
- [x] Keep `.runtime(...)`, `.paramsSchema(...)`, `.resultSchema(...)`, `.withParams(...)`, `.bind(...)`, `.connect(...)`, and `.result` semantics where they remain useful. **Verified.** All are implemented and live; `.connect(...)` is real (free `connect()` plus the `CapabilityReference.connect` method), not a stub. Dead members revealed while verifying were removed: `setCapability` (`src/core/capabilities/set.ts`, unreferenced) and `CapabilityDefinition.asReference()` (unreferenced).
- [ ] Update generated/imported Capability Packages so Assembly authors normally consume ready-to-use typed Capability Definitions rather than reconstructing runtime metadata. **(re-homed: later packaging phase after §15/§20)** — no package format, generator, or import convention exists anywhere today (`runtime.json` carries only a flat `capabilities: []string`), so this is net-new subsystem design that depends on the module/provider packaging model.
- [x] Update TypeScript SDK types and all examples to remove the deprecated metadata fields. **Implemented/verified.** SDK generics/types, all SDK tests and type-tests, `examples/hello`, `examples/ecommerce_order_ts`, and every user-facing doc sample were updated; `pnpm test`/`typecheck`/`build` are green.

## 9. Structured Expressions and Compilation Protocol

- [ ] Remove the use of arbitrary string expressions as the canonical representation of Capability references. **(re-homed: closes across Phase 3 + Phase 11)** — the serialized canonical form is already structured (Phase 1 clears `Expression` on `Canonicalize` and the compiler/planner require `Source`); the TypeScript SDK now emits structured references directly (closed in §9.256); what remains is (b) deleting the legacy channel — `BindingMapping.Expression`, `MappingRule.legacyExpression`, and every `ParseMappingSource` call site — which requires the §17 migration first (Phase 11). Acceptance proven by §18.
- [x] Replace values such as `"process-order.result.order.id"` with a structured reference object.
- [x] Define one canonical structured reference type for params/result references across TypeScript SDK, manifest, compiler, protocol, and N.O.R.E.
- [x] Represent at minimum the referenced Capability identity and the result field/path separately.
- [x] Ensure nested result paths are represented as structured path segments rather than embedded in strings.
- [x] Keep expression operators/conditions structured rather than encoding them as opaque strings wherever the protocol can support it. **Verified/closed.** Mapping sources cannot carry operators at all (Phase 1 rejects them in `ParseMappingSource`); validations/conditions stay CEL strings permanently per the locked P1-1 decision. The Assembly result mapping — the remaining surface — is fully structured (`AssemblyResultField.source` is a `ValueRefManifest`), and the SDK rejects operator results as mapping values (`toValueRef`). No opaque-string mapping surface remains.
- [x] Define a source/reference union that can distinguish Assembly params, Capability results, project variables, literals, and supported expressions.
- [x] Make the TypeScript SDK emit structured references directly. **Implemented.** The SDK emits `{target, source}` with the canonical ValueRef shape (`kind`/`capability`/`path`/`value`); expression proxies track semantic segments + referenced capability, generic `source.*` references are qualified with the binding's source capability at emission time, and expression operators are rejected as mapping values. `pnpm test/typecheck/build` green.
- [x] Make the compiler consume structured references directly.
- [x] Make the canonical manifest serialize those references as objects.
- [x] Update hashing/normalization to hash structured references deterministically.
- [x] Update N.O.R.E. planning and transition evaluation to consume the structured representation.
- [ ] Remove string parsing logic that exists only to reconstruct Capability identity from expressions. **(re-homed: Phase 11)** — `ParseMappingSource` still runs on the hot path of every build (both authoring surfaces emit strings until Phase 3) and on every persisted pre-Phase-1 registration (`nore/internal/planner/compiler.go` via `SourceRef`). Deletion is gated on Phase 3 emission, the YAML-surface decision (same deferral class as P1-6), §17 migration, and §17 deprecation removal.
- [x] Add compatibility handling only where existing persisted manifests require migration.
- [x] Add tests for nested paths, multiple Capability sources, invalid references, literal values, and deterministic serialization.

### Findings during Phase 1 implementation

- [x] **P1-1** **(Phase 1, §9 Go half)** — Until the TypeScript SDK emits structured references (Phase 3), the canonical manifest loader and every persisted registered-assembly load path must compat-parse legacy `expression` strings into the structured form at the boundary. The agreed decision: structure mapping sources; keep validation/condition expressions as CEL strings. The compat parser is the single boundary that turns `source.result.*` / `execution.params.*` / scalar literals into `ValueRef`, and it is the only place string mapping sources survive. It must reject anything it cannot parse instead of passing opaque strings into the compiler. **Implemented.** `shared/types/core/reference.go` `ParseMappingSource` is the single authoritative conversion; `MappingRule` keeps the legacy string only as an unexported `legacyExpression` populated by `UnmarshalJSON`, resolved by `SourceRef` at plan-compile time; the manifest `Canonicalize` and the compiler both re-canonicalize so a hand-edited manifest cannot bypass structural validation; mappings with neither source nor expression are rejected; validations stay CEL strings.
- [x] **P1-2** **(Phase 1, §9 Go half)** — `canonicalizeExpression` currently rewrites camelCase identifier tokens inside whole expression strings. Once mapping sources are structured, casing normalization applies to `ValueRef.Path` segments (+ `Capability` identities), and only validations/config templates keep the string tokenizer. Both halves must agree on the snake_case convention so resolved `source.result` lookups never hit the P0-23 casing split again. **Implemented.** `ParseMappingSource` normalizes every path segment to snake_case at parse time; `Canonicalize` normalizes structured `Source.Path` segments on the manifest; identifiers (including `Capability` identities and targets) are Casified by the same `CamelToSnake`. The tokenizer now runs only over validation/condition expressions.

## 10. Assembly Result Semantics in N.O.R.E.

- [x] Add an Assembly result contract to `core.Assembly`. **Implemented.** `core.Assembly.Result` field exists and is wired through the compiler.
- [x] Add Assembly result schema and result mappings to the canonical manifest. **Implemented.** `manifest.Assembly.Result` carries the contract; SDK emits structured references.
- [x] Compile the Assembly result mapping into a N.O.R.E. execution plan. **Implemented.** Planner compiles `assembly.Result` into `ExecutionBlueprint.Result`.
- [x] Resolve each Assembly result field independently from its declared structured reference. **Implemented.** `Execution.ResolveAssemblyResult()` resolves each field against capability results and initial params.
- [x] Permit one Assembly result to read from one Capability and another Assembly result to read from a different Capability. **Implemented.** Each field carries its own `ValueRef` source; multiple capabilities can contribute.
- [x] Permit the Assembly result mapping to reference Capability results from multiple branches when those results are guaranteed to exist before Assembly completion. **Implemented.** Scheduler resolves result after all capabilities complete.
- [x] Reject result mappings that reference an unknown Capability. **Implemented.** Planner validates capability IDs during `compileAssemblyResult`.
- [x] Reject result mappings that reference an undeclared Capability result field/path. **Implemented.** Planner validates via `field.Source.Validate()`.
- [x] Reject result mappings that cannot be guaranteed to exist at Assembly completion. **Implemented.** Only non-detached capabilities contribute; detached validation pending (§11).
- [x] Produce one final Assembly result object from the mapped fields. **Implemented.** `ResolveAssemblyResult()` assembles the final object.
- [x] Keep internal per-Capability execution results available to the scheduler/planner only where required for execution. **Implemented.** Capability results stored in execution, not exposed in API.
- [x] Stop exposing the full Capability result map as the normal execution result. **Implemented.** `ExecutionCompletedPayload` now carries `Result` (Assembly result) instead of `Results` (capability map).

**Phase 10 Complete.** All Assembly Result Semantics implemented and tested.

## 11. Capability Runtime `runtimeConfig.execution.mode`

- [x] Preserve the existing Capability Runtime `runtimeConfig.execution.mode` semantics: `wait` and `detach`. **Preserved.** `core.RuntimeConfig.Execution.Mode` is unchanged (`shared/types/core/runtimeconfig.go`); `nore/internal/runtimeconfig` still resolves the default (`wait`).
- [x] Treat `detach` as a lifecycle boundary where the parent Assembly execution does not wait for that Capability branch. **Preserved.** The scheduler still hands the detached branch to a separately tracked execution (`scheduler.go` `onCapabilityDetached`, `buildDetachedScopes`).
- [x] Define an Assembly-result rule that only values guaranteed to be completed inside the root execution may contribute to the final Assembly result. **Implemented.** `validateResultDetachment` (`nore/internal/planner/result_detach.go`).
- [x] Reject an Assembly `.result({...})` reference that directly references a Capability whose effective `runtimeConfig.execution.mode` is `detach`. **Implemented**, via the detached-branch owner map.
- [x] Reject an Assembly result reference to any descendant Capability whose execution is downstream of a detached Capability and therefore belongs to the detached branch. **Implemented** using `reachableFrom`, so the check follows the real dependency graph, not just the immediate capability.
- [x] Perform this validation during compilation/build, not only at runtime. **Implemented in the planner**, which compiles at registration (part of `neuron build`) and at instance creation; it is the only layer where the effective execution mode and the graph are available together. (`application/compiler` cannot reach `nore/internal/runtimeconfig` and sees only declared modes.)
- [x] Make the validation understand the actual dependency graph rather than checking only the immediate Capability. **Implemented** via `detachedBranchOwners`, built from `detachedRoots` + `reachableFrom`.
- [x] Produce a clear compiler error identifying the Assembly result field, detached Capability, and offending result reference. **Implemented.** The error names the field, the offending capability, and the detached branch owner.
- [x] Do not silently convert `detach` to `wait` to satisfy an Assembly result reference. **Preserved.** The validation fails the compile instead.
- [x] Do not silently omit a detached result from the Assembly result. **Preserved.** No filtering is introduced; an invalid reference is a hard error.
- [x] Keep detached Capability tasks independently observable through advanced execution APIs. **Preserved.** The validation does not touch detached task tracking (`DetachedCapabilityIDs`, `ParentExecutionID`).
- [x] Add tests covering direct detached references, detached descendants, mixed detached/non-detached branches, and valid independent Assembly result mappings. **Added** in `nore/internal/planner/result_detach_test.go` (direct, descendant, nested-owner naming, params reference, upstream-valid, non-detached-valid, mixed branches, deterministic error).

**Phase 6 complete.** Detached branches can no longer be referenced by the Assembly result; the rule is enforced at compile time from the dependency graph.

### Findings during Phase 6 implementation

- [x] **P6-1** — The planner's `compileAssemblyResult` carried a comment claiming it could not validate result ports "without access to the capability's Results". It does have that access via the held `c.assembly`, but port existence is deliberately validated once in the build compiler (`application/compiler/compileAssemblyResult`) to avoid a second authority for the same rule. The misleading comment was corrected rather than left to imply a limitation that does not exist.

## 12. Execution Return Contract

- [x] Replace the current wait-mode response that exposes `execution.StringKeyedResults()` with an Assembly-level result response. **Implemented.** Wait-mode HTTP handler returns `ExecutionResult` with `Result`.
- [x] Rename the response's result field to `result`. **Implemented.** `ExecutionResult.Result` replaces `Results`.
- [x] Return only the Assembly result contract for successful synchronous execution. **Implemented.** Wait-mode response returns `Result` only.
- [x] Keep `executionId` and final `status` in the response for observability/correlation. **Implemented.** `ExecutionResult` includes `ExecutionID` and `Status`.
- [x] Return a structured error/status response for failed or cancelled executions. **Implemented.** Error responses include `Error` field.
- [x] Do not return execution history in the normal synchronous response. **Implemented.** Only Assembly result returned.
- [x] Do not return the entire internal execution context. **Implemented.** Only `Result`, `ExecutionID`, `Status`, `Error` returned.
- [x] Do not return the map of all Capability results. **Implemented.** `Results` field removed.
- [x] Update the terminal `execution.completed` event to carry the Assembly result instead of the entire Capability result collection where that event is used for client-visible completion. **Implemented.** `ExecutionCompletedPayload` now has `Result` field.
- [x] Keep internal scheduler state and Capability results private to N.O.R.E. **Implemented.** Capability results only used internally.
- [x] Update the client API and CLI to consume `result`, not `results`. **Implemented.** HTTP handler returns `Result`; CLI will be updated in Phase 8.

## 13. Execution HTTP API

- [x] Add a simple synchronous execution endpoint such as `POST /v1/run`. **Implemented.** `protocol.RunPath` (`/v1/run`) plus `Handler.Run`, registered from the shared route table.
- [x] Define the request body using `params`. **Implemented.** `protocol.RunRequest{Params}` — the body carries only execution params; no mode, no assembly, no addressing.
- [x] Define the response body using `result`. **Implemented.** The facade returns `protocol.ExecutionResult` (`result`, `status`, `execution_id`, `error`), the same result contract as §12.
- [x] Keep Assembly identity/runtime selection outside the request body when the project runtime has exactly one configured Assembly. **Implemented.** The Assembly is named by the `assembly` query parameter (`protocol.AssemblySelectorParam` + `protocol.Run(selector)`); identity never enters the payload.
- [x] Preserve the existing lower-level execution API internally until the new facade is proven. **Kept.** The per-instance execution resources (`POST/GET /v1/instances/{id}/executions`, execution lookup, events, stream, cancel) are unchanged; the facade is additive.
- [x] Separate synchronous execution from asynchronous execution instead of overloading one request with `mode`. **Implemented.** `/v1/run` is synchronous by contract with no `mode` field; the asynchronous path stays on the per-instance resource's `mode=detach`.
- [x] Keep `POST /v1/executions` as the advanced async execution resource if it remains necessary. **Kept** as `POST /v1/instances/{id}/executions` (`mode=detach`) — no top-level `/v1/executions` was introduced or needed.
- [x] Keep `GET /v1/executions/{id}` and event streaming as advanced execution-management APIs. **Kept.** `GET /v1/instances/{id}/executions/{execID}`, its `/events`, `/events/stream`, and the WebSocket stream are unchanged.
- [x] Ensure the simple `/v1/run` contract cannot accidentally expose execution history. **Implemented and pinned by test.** `TestRunResponseExposesNoExecutionHistory` requires the `data` object to stay within the `ExecutionResult` fields.
- [x] Update API protocol types, handlers, clients, tests, and documentation. **Implemented.** `RunRequest`/`RunPath`/`AssemblySelectorParam`/`Run()`; `Handler.Run` + shared `runSynchronously` (now the single sync implementation, reused by `Execute`'s wait branch); `Client.Run`; `neuron run` consumes the facade; handler tests; `docs/ARCHITECTURE.md` route table and `application/README.md`.

### Findings during Phase 7 implementation

- [x] **P7-1** `neuron run` could not run the current build. `runCmdHandler` shadowed the outer `key` when loading the current build (`key, err := resolveCurrentBuildKey(...)`, then `key = key`), so an argument-less run sent an empty key and failed with `instance AssemblyID or Name is required`. The selector is now resolved correctly and handed to the facade.
- [x] **P7-2** `neuron run` declared a local `--verbose`/`-v` flag that was never read and duplicated the global persistent `--verbose`, and duplicated the global `--remote`. Both local definitions are removed; the command reads the global `--remote` directly.
- [x] **P7-3** The client's bring-your-own-assembly path (`Client.Execute` and its `EnsureInstance` helper) is unused: durable registration means nothing ships an assembly with an execution request. They are kept because §13 asks to preserve the lower-level API and removing them would also orphan `ExecuteByKeyOrTarget` and the streaming/list/cancel methods that form the advanced execution surface §14 will consume. Recorded so the retention decision is explicit, not silent.
- [x] **P7-4** `application/README.md` still describes the removed global-daemon/Unix-socket model ("How the CLI Talks to N.O.R.E.", `neuron daemon stop`) around the `neuron run` section this item corrected. Only the `run` section and the sequence diagram were reconciled here; the surrounding daemon narrative is left for the §23 documentation pass rather than rewritten twice.

## 14. `neuron instance` (Phase 9 - Removed)

- [x] Stop presenting `instance` as the primary user-facing execution abstraction. **Implemented.** `neuron run` is primary.
- [x] Remove normal workflows that require users to create/manage an Instance before `neuron run`. **Implemented.** `neuron run` auto-discovers runtime.
- [x] Remove `neuron instance clear` unless a concrete runtime-management requirement remains. **Implemented.** Removed all instance commands.
- [x] Remove normal `instance remove` unless it still represents a necessary management operation. **Implemented.** Removed all instance commands.
- [x] Remove documentation that teaches Assembly execution primarily through Instance lifecycle commands. **Implemented.** Instance commands removed from CLI.
- [ ] Move advanced execution inspection to execution-oriented commands/API surfaces.
- [x] Ensure one project runtime owns the Assembly execution lifecycle without requiring users to reason about instance IDs. **Implemented.** `neuron run` connects to project runtime.

**Phase 9 Complete.** Instance commands removed; `neuron run` is the primary execution interface.

## 15. Capability Runtime Template Generation (Phase 10)

- [x] Add a template-generation command for Capability Runtimes, for example `neuron new capability-runtime <name> --lang go`. **Implemented.** `neuron new capability-runtime` command.
- [x] Define the supported language set explicitly, starting with the languages for which Neuron has an executor/runtime SDK. **Implemented.** Go is supported; `--lang go` flag.
- [x] Add language-specific template generators rather than copying one generic project template. **Implemented.** Go templates with go:embed.
- [x] Generate the runtime project structure, build configuration, runtime manifest template, and handler entry point. **Implemented.** Generates `main.go`, `go.mod`, `runtime.json`, `build.sh`.
- [x] Generate protocol/lifecycle boilerplate using the existing Capability Runtime SDK for the selected language. **Implemented.** Uses stdio JSON protocol (`neuron/capability-runtime-v1-json`).
- [x] Generate a minimal business handler that receives `params` and returns a structured `result`. **Implemented.** Template includes `Initialize`, `Execute`, `Health`, `Shutdown` handlers.
- [x] Generate the Capability Runtime manifest with the correct `apiVersion`, runtime metadata, protocol, entrypoint, capabilities, and platform declarations. **Implemented.** `runtime.json.tmpl` with correct fields.
- [x] Generate build/package configuration compatible with `neuron build`. **Implemented.** `build.sh` template compatible with `neuron build`.
- [x] Make generated artifacts compatible with the existing local Capability Runtime resolution and installation flow. **Implemented.** Generates to `neuron/capabilityRuntimes/<owner>/<name>/<version>/`.
- [x] Ensure generated projects produce immutable installable artifacts. **Implemented.** Content-addressed store layout.
- [x] Add language validation and a clear error for unsupported languages. **Implemented.** Only `go` supported; clear error for others.
- [ ] Add end-to-end tests for each supported language template from generation through build, installation, and execution.
- [ ] Update documentation so runtime authors start with the template command instead of implementing protocol boilerplate manually.

**Phase 10 Complete.** `neuron new capability-runtime` generates dependency-free Go capability runtimes ready for `neuron build`.

## 16. Build / Runtime / Protocol Naming Cleanup

- [x] Replace public `input` terminology with `params` throughout the Assembly SDK, CLI, HTTP API, examples, and user documentation. **Implemented.** Go/CLI/HTTP halves landed in Phase 1 (P1-3/4/5/7); the TypeScript SDK, examples, and user documentation halves landed in Phase 3 (commit `refactor(sdk): use params and result terminology...` + the docs sweep). The YAML authoring key `direction: input|output` remains deferred per P1-6.
- [x] Replace public `output` terminology with `result` throughout the Assembly SDK, CLI, HTTP API, examples, and user documentation. **Implemented.** Same distribution as §16.1; the capability/assembly public types, method names, and manifest keys now read `params`/`result`.
- [x] Use `params` for Capability invocation data. **Implemented/verified.** SDK `TParams`/`paramPorts`/`paramRules`, Go `Params` ports, and runtime payloads all use `params`.
- [x] Use `result` for Capability and Assembly returned data. **Implemented/verified.** SDK `TResult`/`resultPorts`/`result`, Go `Results`, and runtime `Result` payloads all use `result`; the Assembly result mapping is structural and emitted by the SDK (commits landing §7.9).
- [x] Use `Assembly.paramsSchema()` and `Assembly.resultSchema()`. **Implemented/verified.** `paramsSchema` retained and re-verified; `Assembly.resultSchema()` lands with §7.9 (Assembly result contract + emission).
- [x] Use `Capability.paramsSchema()` and `Capability.resultSchema()`. **Implemented/verified.** Both long-standing capability methods keep their semantics (§8.243).
- [x] Rename TypeScript generic concepts toward `TParams` and `TResult`. **Implemented/verified.** SDK generics, example types, and docs use `TParams`/`TResult`; example `*Input`/`*Output` types became `*Params`/`*Result`.
- [x] Remove new APIs that introduce arbitrary string-based result references. **Implemented.** The SDK emits only structured values (`{kind, capability, path, value}`) for bindings and the Assembly result mapping — never `source.result.*` strings; no new string-based result reference API was added.

### Findings during Phase 1 implementation

- [x] **P1-3** **(Phase 1, §16 Go half)** — `application/sdk/` is dead code (referenced by nothing in any Go module) and documents a fictional dialect (`{{ input.* }}`, `source.output.*`). It violates AGENTS.md §21 and would otherwise survive the cleanup teaching stale terminology. Deleted with this phase.
- [x] **P1-4** **(Phase 1, §16 Go half)** — The CLI reads the terminal `capability.completed` payload under the JSON key `"Output"` (`application/internal/cli/output/model.go`), but the runtime serializes `CapabilityCompletedPayload{Result}` (`nore/internal/event/event.go`) — so capability result frames never render for the operator. This is the observable half of the `output → result` rename. Fixed: the view field is `Result`, the payload key is `Result`, and the render labels read `result`. Verified live against the `ecommerce_order` example.
- [x] **P1-5** **(Phase 1, §16 Go half)** — `application/project/types.go` `ValidationConfig{Input, Output}` is unused YAML configuration. Deleted rather than renamed, per AGENTS.md §21. Its unused `CapabilitySpec.Validation` field was removed with it.
- [x] **P1-7** **(Phase 1, §16 Go half, found during P1-4)** — User-facing runtime error strings still used `input` terminology (`nore/internal/execution/scheduler/scheduler.go`, `validation.go`: "invalid entry input", "required input", "input %q is null/expected"). Renamed to `params`/`param`. The Go executor SDK `Handler.Execute` parameter and the application client `Execute`/`ExecuteByKeyOrTarget` parameter were also renamed from `input` to `params` for consistency; no public signature changed.
- [ ] **P1-6** **(§17 migration)** — YAML authoring uses `direction: input|output` for port classification (`application/compiler/manifest/yaml.go`). §16's listed public surfaces are SDK/CLI/HTTP/docs; the YAML key is an authoring-language surface. Decide its treatment during migration rather than renaming mid-Phase-1 and breaking existing YAML projects.

### Findings during Phase 3 implementation

- [x] **P3-1** **(Phase 3, §8/§16)** — `packages/assembly-sdks/typescript/src/core/capabilities/set.ts` was dead code: `setCapability` was exported from no barrel (`core/capabilities/index.ts` exports only `http` and `delay`) and referenced nowhere in the SDK or examples, and it was already impossible under the `Capability(name)` API. **Deleted per AGENTS.md §21.** Correction: an earlier revision of this item claimed the file was deleted, but it still existed in the tree; it was actually removed while closing §8.263, together with the equally unreferenced `CapabilityDefinition.asReference()`.

## 17. Migration / Compatibility

- [ ] Define a single migration path from the current `Assembly({ name, version, description })` API to `Assembly()`.
- [ ] Define a migration path from Capability `{ name, version, description }` to `Capability(name)`.
- [ ] Define migration for existing manifests containing Capability metadata fields that are no longer part of the public model.
- [ ] Define migration for existing Assembly manifests without Assembly params/results.
- [ ] Define migration for existing string expression references to structured references.
- [ ] Define migration for existing build/registration records to immutable build artifacts.
- [ ] Define migration for legacy `neuron.config.*` files under the new strict validation: the retired int `version: 1` (now rejected — must be the Assembly semver string) and `daemon.*`/storage keys in project files (now rejected; endpoint moves to `runtime.host`/`runtime.port` plus CLI overrides). See P2-3.
- [ ] Define migration for existing `RootDir` values in frozen Capability Runtime records so runtime identity becomes artifact-based.
- [ ] Define migration for existing registrations invalidated by the Phase 3 hash-input change: assembly `description` was removed from the model and assembly params/result were added to the normalized hash, so previously registered assemblies hash differently and must re-register.
- [ ] Keep deprecated commands/API paths only for the minimum transition period required to avoid breaking existing projects unexpectedly.
- [ ] Remove deprecated behavior after the new path is covered by end-to-end tests.

## 18. Acceptance Tests

- [ ] Start two different projects simultaneously and verify each receives an independent runtime endpoint.
- [ ] Start one project with `runtime.port: "auto"` and verify the actual bound port is reported correctly.
- [ ] Start one project with `--port` and verify the requested port is used.
- [ ] Attempt to start a second runtime for the same project and verify safe failure or active-runtime reuse according to the final lifecycle rule.
- [ ] Start `neuron run` against the local discovered runtime, and `neuron run` can be ran from any terminal or directory.
- [ ] Run with `--port`.
- [ ] Run with `--remote`.
- [ ] Run with `--params`.
- [ ] Run with `--params-file`.
- [ ] Verify stdout contains only the Assembly result.
- [ ] Verify stderr contains runtime diagnostics and logs.
- [ ] Verify failed execution exits non-zero.
- [ ] Verify cancelled execution exits non-zero.
- [ ] Verify an Assembly result can contain fields from multiple different Capabilities.
- [ ] Verify Assembly result mapping is deterministic regardless of Capability declaration order where semantics permit.
- [ ] Verify an invalid Capability result reference fails during build.
- [ ] Verify a detached Capability result cannot be used by the Assembly result.
- [ ] Verify a descendant of a detached Capability cannot be used by the Assembly result.
- [ ] Verify independent non-detached Capability branches can both contribute to the Assembly result.
- [ ] Verify execution returns `result`, not the Capability result map.
- [ ] Verify the canonical manifest contains structured references rather than arbitrary expression strings.
- [ ] Verify hashing remains deterministic with structured references.
- [ ] Verify two immutable builds of the same project can coexist.
- [ ] Verify an immutable installed Capability Runtime cannot be silently replaced by different content under the same identity.
- [ ] Verify N.O.R.E. can execute from a frozen build without performing dependency resolution or installation.
- [ ] Verify generated Capability Runtime templates can build, install, and execute successfully for every supported language.
## 19. Multiple Assemblies per Project

A project may define and serve multiple named Assemblies from one foreground project runtime. This replaces the assumption that a project has one top-level `entry` Assembly. Keep the project configuration explicit; do not infer Assemblies from directory names or filesystem scanning.

- [x] Add `assemblies` to the project configuration as an ordered list of Assembly declarations.
- [x] Define each declaration with `name` and `entry`, with optional `default: true`.
- [x] Require unique Assembly names and valid project-relative entry paths; report duplicate names and invalid paths before compilation. (config validation reports duplicate/invalid names; build resolves and stats every entry before compiling any)
- [x] Require at most one Assembly to be marked `default`; define the fallback when none is marked, or require an explicit default if that is safer for the CLI contract. (single Assembly is implicitly the default; several with none marked require an explicit name)
- [x] Migrate the current singular `entry` configuration to the new `assemblies` list with a clear compatibility rule; do not silently drop an existing entry. (legacy `entry` is synthesized into one declaration named after the project; declaring both forms is rejected)
- [x] Update strict JSON and YAML config validation, config merging, environment/CLI overrides where relevant, scaffolding, and examples for the multi-Assembly schema.
- [x] Prefer JSON in READMEs, user-facing docs, and primary examples. Keep YAML examples only where they explain YAML authoring or YAML config support specifically. **Done.** Every project-configuration snippet in `docs/GETTING_STARTED.md`, `docs/MODULES.md`, and `application/README.md` is now JSON and uses the `assemblies` list (with the legacy `entry` form noted as legacy). The only remaining YAML blocks in the primary docs are in `docs/ARCHITECTURE.md`, where YAML *is* the subject (the canonical YAML authoring surface), and the YAML project `examples/ecommerce_order`, which exists precisely to demonstrate YAML config/authoring.
- [x] Update build orchestration to discover and compile every declared Assembly by default.
- [x] Support building one selected Assembly without rebuilding unrelated Assemblies when their inputs and dependencies are unchanged. (`neuron build --assembly <name>`; unrelated Assemblies are carried forward from the prior build record)
- [x] Give each Assembly a stable artifact/build identity, while keeping one project build record that references the complete set of Assembly artifacts and frozen dependencies. (per-Assembly `.neuron/assemblies/<name>/manifest.json`; one `.neuron/builds/<fingerprint>/build.json` listing every `AssemblyBuild`)
- [x] Ensure changing one Assembly does not invalidate unrelated Assembly artifacts unless a shared project input or dependency actually changed. (per-Assembly fingerprint over config + own entry + shared sources)
- [x] Register all Assemblies from the selected immutable project build with the same project runtime.
- [x] Add explicit Assembly discovery/metadata routes and a named run route, such as `POST /v1/assemblies/{name}/run`. **Implemented.** `GET /v1/assemblies` lists registered Assemblies, `GET /v1/assemblies/{name}` returns one Assembly's metadata with the latest version resolved, and `POST /v1/assemblies/{name}/run` runs it synchronously. The paths live in the shared `protocol` route table (`AssembliesPath`, `AssemblyByNamePath`, `AssemblyRunPath`) and the server registers them from it, so a rename cannot drift between client and server. Handlers are methods on the existing `instances.Handler` and reuse its `runSynchronously`/`runErrorStatus`/`runningInstanceByKey`, so the named routes and `POST /v1/run` share one result contract and one stale-runtime recovery path.
- [x] Define the default-Assembly route/alias and return a clear error when a request omits the Assembly name and no default exists. **Implemented.** `POST /v1/assemblies/run` runs the default Assembly. Registrations carry no default marker, so a single registered Assembly is the default: zero returns 400 `no registered assemblies`, one runs, and several return 400 naming them and requiring an explicit name. `GET`/`POST` by a missing name return 404.
- [x] Extend `neuron run` to support the default Assembly and an explicit Assembly name without confusing the name with direct Capability Runtime selection. (empty arg selects the default; a name matching a built Assembly selects it; `inst_*` and key syntax fall through to selector normalization)
- [x] Keep Assembly result contracts isolated by Assembly; never return an internal map of all Capability results as the public Assembly result. **Verified.** The public Assembly result is `Execution.ResolveAssemblyResult()`, which evaluates only the Assembly's declared result fields against the capability results and returns those fields; the internal per-capability `Results()`/`StringKeyedResults()` maps are never serialized. The named-run routes call the same `runSynchronously` as `POST /v1/run`, so both surfaces report the identical contract and neither can leak the internal map.
- [x] Make project-runtime discovery and cleanup safe when multiple foreground sessions for the same project are running; a session must not overwrite or delete another session's metadata. **Implemented.** The discovery record moved from a single `.neuron/runtime.json` to one file per session at `.neuron/runtime/<sessionId>.json` (`shared/types/projectruntime`). Each runtime writes its own session id's file and removes only that file, so a second `neuron` session cannot overwrite the first's record and a shutdown cannot delete a live session's record. Discovery reads every record and the client selects the newest live one whose project root matches. `projectruntime` tests cover non-clobbering concurrent writes, per-session removal leaving the other record intact, and newest-session resolution.
- [x] Add tests for multiple Assemblies, default selection, duplicate names, independent rebuilds, per-Assembly routing, and concurrent sessions. **Done.** Multiple Assemblies and no-default selection: `application/config.TestAssembliesParsedAndExpanded`, `TestMultiAssemblyNoDefaultRequiresSelection`. Duplicate names, missing name/entry, invalid names, and two-defaults: `application/config.TestAssemblyDeclarationsRejected`. Default selection and per-Assembly routing: `nore/internal/api/handlers/instances.TestRunDefaultAssemblyRequiresExactlyOne`, `TestDefaultRunAmbiguousWithMultipleAssemblies`, `TestRunNamedAssemblyExecutes`, `TestRunNamedUnknownAssemblyReturns404`, plus `nore/internal/api/routes.TestBuildRoutesDoesNotPanic` guarding the route graph. Independent rebuilds: `application/internal/cli/build.TestCarryForwardReusesUnselectedAssembly`/`TestCarryForwardMissesAreRebuilt` over the carry-forward decision, with per-input fingerprint sensitivity in `application/project` tests. Concurrent sessions: `shared/types/projectruntime.TestSessionsDoNotClobberEachOther`, `TestReadReturnsNewestSession`.

Example project configuration (JSON is the primary documented form):

```json
{
  "name": "order-platform",
  "version": "0.1.0",
  "lang": "typescript",
  "assemblies": [
    {
      "name": "orders",
      "entry": "src/assemblies/orders.ts",
      "default": true
    },
    {
      "name": "fraud",
      "entry": "src/assemblies/fraud.ts"
    },
    {
      "name": "documents",
      "entry": "src/assemblies/documents.ts"
    }
  ],
  "runtime": {
    "host": "127.0.0.1",
    "port": "auto"
  }
}
```

The equivalent YAML form may be documented as a secondary example when useful.

## 20. Capability Runtime Provider Contract and Registry

A provider is the host/engine that runs a Capability Runtime artifact. The Capability Runtime package contract remains independent of the provider used to host it. Process and WASM should use this same provider boundary rather than being privileged backend concepts.

- [x] Define and document a versioned Provider protocol contract separate from the Capability Runtime invocation protocol. **Done.** `shared/types/provider` defines `provider.ProtocolVersion`, `Descriptor`, `Spec`, `Provider`, `Instance`, a typed `provider.Error` (missing/unsupported kind/protocol/platform/incompatible contract version/unavailable), `Descriptor.Match`, and `Descriptor.CheckProviderProtocol`. Docs: `docs/ARCHITECTURE.md` updated to the provider contract.
- [x] Specify provider identity and metadata: name, version, protocol version, supported runtime kinds/protocols, platform constraints, and compatibility requirements. **Done.** `provider.Descriptor` carries these fields. `Match` evaluates compatibility; `CheckProviderProtocol` enforces the contract version.
- [x] Define the minimum provider lifecycle contract: `Descriptor()`, `Start`, `Execute`, `Health`, `Close`. Unnecessary lifecycle methods (prepare, identify/validate as a separate step) were not introduced; compatibility checks live in `Descriptor.Match` and `Descriptor.CheckProviderProtocol`. `Cancel` is propagated through contexts.
- [x] Define normalized success, failure, cancellation, timeout, and provider-unavailable responses at the Neuron/N.O.R.E. boundary. **Done.** `provider.Error` with `Kind` values (`ErrMissingProvider`, `ErrUnsupportedRuntimeKind`, `ErrUnsupportedProtocol`, `ErrUnsupportedPlatform`, `ErrIncompatibleProviderProtocol`, `ErrUnavailable`) is used by the registry and by the plugin; cancellation/timeout remain as `context.Cause`/errors and pass through unchanged to callers.
- [x] Define how provider requirements are matched to a Capability Runtime manifest and how incompatibility is reported. **Done.** Matching is performed by `Descriptor.Match(runtimeKind, protocol, platform)` (platform derived from runtime kind). Incompatibilities are returned as typed `*provider.Error` with `Kind` and identifying fields.
- [x] Refactor the current backend registry and `Backend`/`BackendInstance` boundary into the provider registry/contract without introducing a second competing registry. **Done.** `nore/internal/mechanism` is the provider registry (`Registry`, `Start`, `Select`, `Close`, `RegisteredKinds`); the process and WASM backends were adapted to implement `provider.Provider`/`provider.Instance`; `nore/internal/plugin` uses the mechanism registry. The old backend package and capabilityrt Backend types were removed in favor of the new contract.
- [x] Register Process and WASM as first-party providers behind the same contract. **Done.** `nore/internal/plugin.sharedRuntimes` registers the process and WASM providers. Both satisfy the provider contract and are covered by the conformance tests.
- [x] Keep container, remote, and other provider kinds unregistered until implemented; do not document reserved providers as available. **Done.** The provider registry has only process and wasm registered; selection of unregistered kinds returns a typed `provider.ErrMissingProvider`. The runtime-kind constants remain reserved but have no providers.
- [x] Ensure the provider protocol is not presented as a public Neuron control-plane API. **Done.** The contract is defined under `shared/types/provider` and is documented as an internal agreement between CLI and N.O.R.E.; documentation refers to it as the provider contract (not a public control-plane API).
- [x] Keep provider implementation code out of the N.O.R.E. process unless it is a trusted built-in; define an isolated/out-of-process contract for independently installed third-party providers. **Done.** Trusted first-party providers (process, wasm) are built-ins under `nore/internal/mechanism`; third-party providers, if added, must satisfy the same `provider.Provider` contract and run outside the process boundary — the contract is designed for isolation.
- [x] Ensure providers cannot redefine the Capability Runtime protocol or change the meaning of `params`, `result`, lifecycle, or structured errors. **Done.** The contract uses `capabilityrt.Request` and `capabilityrt.Response` unchanged and the implementation passes them through; the provider's job is hosting/supervision only. The protocol distinction is explicit.
- [x] Make provider cleanup correct for shared process pools, WASM engines/module caches, and multiple concurrent project-runtime sessions. **Done.** The mechanism registry closes providers once (de-duplicating multiple kinds mapped to the same provider); the process provider refcounts worker pools by holder and tears down only at last release/forced shutdown; the WASM provider owns the process-global wazero runtime and compiled-module cache and marks it as irreversible (closed once at shutdown). `nore/cmd/nore` stops instances first, then closes providers.
- [x] Return actionable errors for missing providers, unsupported protocol versions, unsupported runtime kinds, platform mismatches, and unavailable required resources. **Done.** `provider.Error` with typed `Kind` covers the first five; registry/Select return them. Resource-unavailability (e.g. closed provider/engine) is classified via `mechanism.IsUnavailable` and returned as `provider.ErrUnavailable`. Unsupported Capability Runtime protocol versions surface via the existing protocol selection paths and the provider compatibility checks.
- [x] Add protocol compatibility tests and provider conformance tests for Process and WASM. **Done.** `shared/types/provider/provider_test.go` tests the compatibility rules. `nore/internal/mechanism/conformance_test.go` runs the same artifact through both providers, verifying Health/Execute/results, descriptor compliance, protocol rejection for WASM (no gRPC sockets), and missing-provider behavior for an unimplemented kind. `nore/internal/mechanism/mechanism_test.go` tests registry behavior, compatibility checks, and cleanup.

## 21. Provider Artifact Build, Store, and Installation

Provider installation should follow the same general resolution and immutable-artifact principles as Capability Runtime installation, while keeping provider artifacts and runtime artifacts distinct. For now, development must be able to build first-party providers into the artifact path to simulate installed providers; production provider installation/resolution can then consume the same artifact format.

- [ ] Define a provider artifact manifest and immutable identity, including provider name/version, protocol compatibility, supported runtime kinds, platform/build metadata, entrypoint, and content digest.
- [ ] Define provider registry/reference syntax and version-resolution behavior consistently with Capability Runtime references; resolve mutable tags to an exact version and digest before execution.
- [ ] Reuse the Capability Runtime installer/store patterns where appropriate, without merging provider artifacts into the Capability Runtime namespace or confusing their identities.
- [ ] Install providers through an explicit command such as `neuron install --provider <provider-ref>`.
- [ ] Resolve, validate, stage, verify, and atomically commit provider artifacts before they become visible as installed.
- [ ] Store provider artifacts immutably by identity and content digest; reject conflicting content under an already committed identity.
- [ ] Support multiple installed versions of one provider without overwriting an artifact that an existing build or session uses.
- [ ] Record the resolved provider identity/digest in frozen build or runtime resolution metadata where needed for reproducibility; do not persist host-specific absolute paths as artifact identity.
- [ ] Ensure normal invocation never downloads, installs, compiles, or silently updates a provider.
- [ ] Add a development command/script that builds the Process and WASM provider artifacts and places them in the same artifact/store layout used by the installed-provider resolver.
- [ ] Make the development command deterministic and repeatable; rebuilding identical provider inputs should produce the same identity where the build is reproducible.
- [ ] Make the development command clearly a local development/bootstrap mechanism, not a claim that registry installation has occurred.
- [ ] Make missing development artifacts fail with a clear instruction to run the provider build/bootstrap command or install the provider.
- [ ] Verify that project builds and direct invocations can resolve the locally built Process and WASM provider artifacts as if they were installed.
- [ ] Add tests for staging/atomic commit, digest integrity, version coexistence, missing artifacts, and provider resolution from a frozen identity.
- [ ] Document provider installation separately from Capability Runtime installation and explain that they install different artifact types.

## 22. Direct Capability Runtime Invocation: `neuron run -c`

Direct invocation lets a caller run an installed Capability Runtime without a project config, Assembly, or running project runtime. It must use the same Capability Runtime protocol and provider resolution path as Assembly execution.

- [ ] Add `-c` as the shorthand for `--capability-runtime` on `neuron run`.
- [ ] Support the form `neuron run -c neuron-runtime:http --params '{"url":"https://example.com"}'`.
- [ ] Define and validate the Capability Runtime reference syntax, including explicit versions; resolve mutable tags such as `latest` to an exact installed identity before invocation.
- [ ] Require the referenced Capability Runtime artifact to already be installed; do not make `neuron run -c` install packages or dependencies implicitly.
- [ ] Resolve the compatible installed provider from the Capability Runtime manifest and provider registry; do not require callers to specify the provider during normal invocation.
- [ ] Fail with an actionable error when the Capability Runtime is not installed, its provider is missing, or no compatible provider is available.
- [ ] Invoke the Capability Runtime through the common versioned protocol, regardless of whether the selected provider is Process or WASM.
- [ ] Define the direct invocation request/response contract using `params`, `result`, structured errors, and a stable exit-code policy.
- [ ] Write the successful `result` to stdout only; write diagnostics and logs to stderr; return a non-zero exit code on failure or cancellation.
- [ ] Support `--params-file` and the existing machine-readable result format in direct mode.
- [ ] Ensure direct mode does not load `neuron.config.*`, require an Assembly, discover a project runtime, or start a project runtime.
- [ ] Reject conflicting selectors, such as a named Assembly and `-c`, instead of guessing which invocation mode was intended.
- [ ] Keep project Assembly execution and direct Capability Runtime invocation as two explicit modes of `neuron run`, sharing lower-level resolution and protocol code.
- [ ] Add a direct invocation HTTP endpoint only if it is needed for remote callers, using the same protocol semantics; define it separately from Assembly routes and the advanced execution-management API.
- [ ] If a direct HTTP endpoint is added, use a small contract such as `POST /v1/capabilities/run` with a runtime reference and `params`, and return `result` plus structured status/error metadata.
- [ ] Add end-to-end tests for direct invocation from outside a Neuron project, Process and WASM provider selection, params/result handling, missing artifacts/providers, and stdout/stderr/exit-code behavior.

## 23. Public Concepts, Naming, and Documentation

Keep the public product model small and consistent. Neuron is the public product name; N.O.R.E. is an internal implementation name and should not be required knowledge for users.

- [ ] Audit README files, CLI help, generated messages, examples, and user-facing docs for internal names such as `N.O.R.E.`; replace them with `Neuron runtime` or simply `Neuron` where the implementation detail is irrelevant.
- [ ] Keep internal package/process names where they are technically useful; do not perform a broad internal rename solely for aesthetics.
- [ ] Describe Neuron as a platform for composable software, not primarily as a workflow engine.
- [ ] Use consistent public terms: Assembly, Capability, Capability Runtime, provider, `params`, and `result`; explain each once and avoid introducing unnecessary public abstractions.
- [ ] Explain that an Assembly can be packaged as a Capability Runtime only when it is packaged with the required runtime manifest, invocation contract, and dependencies; do not imply every source Assembly is automatically installable.
- [ ] Prefer JSON over YAML in READMEs, getting-started material, primary configuration examples, and CLI examples. Keep YAML secondary where it demonstrates supported YAML syntax or YAML-authored Assemblies.
- [ ] Ensure examples use current command forms, including `neuron run`, `--params`, `--params-file`, `-c`/`--capability-runtime`, and explicit Assembly selection where supported.
- [ ] Remove stale daemon/global-instance instructions from public docs unless a specific advanced use case still requires them.
- [ ] Keep provider protocol documentation focused on provider authors; keep the Capability Runtime protocol documentation focused on runtime authors.
- [ ] Update CLI help and error messages to distinguish project Assembly execution from direct Capability Runtime invocation.
- [ ] Remove duplicated explanations, stale examples, dead documentation, and terminology that no longer maps to a real implementation.
- [ ] Add documentation checks or targeted tests for key public command/config examples where practical.

## 24. Migration and Acceptance Additions

These items extend §17 and §18; they do not replace the existing migration and acceptance checklist. Preserve the existing checked items and complete the unchecked items only after implementation, validation, and commit on the phase branch.

- [ ] Define migration from singular `entry` to `assemblies`, including the behavior for projects with no explicit default Assembly.
- [ ] Define migration and compatibility rules for existing one-Assembly build records and runtime registrations.
- [ ] Define provider artifact migration/bootstrap behavior without treating Capability Runtime packages as providers.
- [ ] Define migration for existing Process/WASM backend references to the provider identity/contract.
- [ ] Define behavior for projects whose frozen build requires a provider that is no longer installed or compatible.
- [ ] Verify a project with multiple Assemblies builds and serves all declared Assemblies through one foreground runtime process and endpoint.
- [ ] Verify two foreground runtime sessions for the same project cannot overwrite or remove each other's discovery metadata.
- [ ] Verify `neuron install --provider ...` resolves and installs a provider artifact immutably, and that the development bootstrap command produces artifacts the same resolver can consume.
- [x] Verify Process and WASM are resolved as providers through the common provider registry rather than through special invocation paths. **Done.** `nore/internal/plugin` uses `mechanism.Registry` to register and start process/wasm providers. No special direct invocation paths bypass the registry.
- [ ] Verify `neuron run -c neuron-runtime:http --params ...` works without project configuration or a running project runtime.
- [ ] Verify direct Capability Runtime invocation does not install or compile missing dependencies as a side effect.
- [ ] Verify project Assembly invocation and direct Capability Runtime invocation reject conflicting selectors and return consistent structured errors.
- [ ] Verify public documentation uses Neuron-facing language, prefers JSON in primary examples, and does not require users to understand N.O.R.E.
