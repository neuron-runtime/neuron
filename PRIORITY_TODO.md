# Neuron Project Runtime + Execution Result Refactor — TODO

> Ignore the repository's existing `TODO.md`. This document defines the work for the new project-scoped runtime model and the related public API changes.

## How this document is used

This file is the **single source of truth** for the work below.

Rules that apply to every change:

- A checkbox is marked `[x]` only after the change is implemented, validated, and committed on its own branch.
- Never move on to the next item while the current one is unchecked.
- Never leave dead code, obsolete abstractions, or commented-out experiments behind. Removing them is part of the item that revealed them, not a follow-up.
- Findings that emerge while implementing are appended to the relevant phase section as new checklist items rather than being silently fixed or silently dropped.
- Each phase is one branch and one or more coherent commits (`feat(scope):`, `fix(scope):`, `refactor(scope):`, `test(scope):`, `docs(scope):`).

### Implementation order

The order below is forced by dependency, not by convenience.

```text
Phase 0   correctness + dead code      (no dependencies)
Phase 1   structured references        §9, §16 (Go half)
Phase 2   project config schema        §6
Phase 3   Assembly + Capability API    §7, §8, §16 (TS half)
Phase 4   immutable runtime store      §5
Phase 5   immutable builds             §4
Phase 6   Assembly result semantics    §10, §11
Phase 7   execution return + API       §12, §13
Phase 8   project runtime + run        §1, §2, §3
Phase 9   instance surface             §14
Phase 10  runtime templates            §15
Phase 11  migration + acceptance       §17, §18
```

Why this order:

- **Phase 1 is the keystone.** Sections 7, 8, 10, and 11 all depend on a *structured reference* type. Building them first means defining that contract twice.
- **Phase 4 precedes Phase 5.** A frozen build must reference an installed artifact by content digest. Phase 5 cannot be artifact-addressed until Phase 4 removes the host-specific `RootDir` from the frozen record.
- **Phases 1, 2, 3, and 5 precede Phase 8.** The project runtime hosts a build that already has a result contract and an artifact-addressed dependency set.
- **Phase 10 is independent** and may proceed in parallel from Phase 2 onward.
- **Phase 0 is first** because the refactor must land on a correct base, and because dead code must not survive into it.

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
- [ ] **P0-23** Key casing is inconsistent between the two variables of one Capability environment. `nore/internal/execution/engine/executor_engine.go` exposes the capability's own payload as `params` with authored keys intact, while `execution.params` is normalized to snake_case by `data.SnakeMap`. So `params.shippingAddress` and `execution.params.shipping_address` are both correct inside the same template, depending on which variable an author reaches for. Phase 1 replaces both with structured references over declared ports, which removes the question by construction. Until then do not paper over it by normalizing one side only.
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

- [ ] Make bare `neuron` start the current project's N.O.R.E. runtime in the foreground when a `neuron.config.json`, `neuron.config.yaml`, or `neuron.config.yml` is found.
- [ ] Resolve the project root once and use it consistently for configuration, build selection, endpoint discovery, and runtime lifecycle.
- [ ] Remove the current automatic global-daemon model from the normal CLI path.
- [ ] Stop using global Unix socket/PID/data paths as the identity or lifecycle mechanism for a project runtime.
- [ ] Treat one running `neuron` process in one project root as one project runtime.
- [ ] Keep the runtime lifecycle owned by that process: start, readiness, logs, graceful shutdown, cancellation, and cleanup.
- [ ] Prevent two `neuron` processes from incorrectly claiming the same project runtime.
- [ ] Define a project-runtime identity/discovery record that can safely detect an active runtime and reject stale records.
- [ ] Keep low-level N.O.R.E. transport details internal; users should not need to specify socket paths, PID files, or daemon paths.
- [ ] Ensure runtime startup does not mutate immutable build or installed Capability Runtime artifacts.

## 2. Project Runtime Endpoint / N.O.R.E. Port Discovery

- [ ] Add project configuration for `runtime.host`.
- [ ] Add project configuration for `runtime.port`, accepting an explicit integer or `"auto"`.
- [ ] Default local runtime binding to loopback rather than a publicly reachable interface.
- [ ] Implement automatic port allocation by asking the OS for an available port and retaining the bound listener rather than probing and binding separately.
- [ ] Report the actual bound port after the runtime is ready.
- [ ] Print one canonical local URL during startup, for example `http://127.0.0.1:6000` or `http:localhost:6000`.
- [ ] Add `neuron --port <port>` as a command-line override for the project runtime.
- [ ] Define precedence as CLI override > project configuration > default `auto`.
- [ ] Add project-local endpoint discovery metadata so `neuron run` can find the active runtime without requiring users to know the port.
- [ ] Write endpoint discovery metadata atomically.
- [ ] Include enough ownership information in discovery metadata to reject a stale or foreign process.
- [ ] Remove assumptions that the runtime is always reachable through the current global Unix socket.
- [ ] Make automatic port selection work correctly when multiple Neuron projects are running simultaneously.
- [ ] Add startup tests for `auto`, explicit ports, occupied ports, concurrent projects, stale discovery metadata, and clean shutdown.

## 3. `neuron run`

- [ ] Make `neuron run` a client of a project runtime; it must not own runtime startup or shutdown.
- [ ] Make `neuron run` resolve the current project's endpoint automatically.
- [ ] Add `--port <port>` for explicitly targeting the local project runtime.
- [ ] Add `--remote <url>` for explicitly targeting a remote Neuron runtime.
- [ ] Define `--remote` as mutually exclusive with automatic local endpoint discovery unless an explicit override is intentionally supported.
- [ ] Replace `--input` with `--params` everywhere in the public CLI, documentation, examples, tests, and client code.
- [ ] Add `--params-file <path>` for loading JSON params without shell quoting.
- [ ] Make the normal execution path wait for completion and return the Assembly result.
- [ ] Remove `--detach` from the normal `neuron run` interface.
- [ ] Remove `--build` from the normal `neuron run` interface; build/preparation should be explicit.
- [ ] Remove event-stream rendering from normal `run` output.
- [ ] Make stdout contain only the Assembly result in the selected result format.
- [ ] Send logs, diagnostics, runtime startup messages, and execution errors to stderr.
- [ ] Return a non-zero process exit code for failed or cancelled executions.
- [ ] Add a stable machine-readable result format such as `--format json`.
- [ ] Keep event streaming and detailed execution inspection available through advanced commands/APIs rather than the default `run` experience.

## 4. `neuron build`

- [ ] Separate source compilation, Capability Runtime preparation, dependency resolution, freezing, and runtime activation into explicit internal phases.
- [ ] Make `neuron build` produce a complete immutable build artifact for the project.
- [ ] Include the canonical manifest in the immutable build artifact.
- [ ] Include the frozen Capability Runtime dependency set in the immutable build artifact.
- [ ] Freeze exact Capability Runtime versions and content digests during build.
- [ ] Ensure N.O.R.E. never performs dependency resolution or installation during execution.
- [ ] Replace mutable build state with immutable build directories identified by a build/content hash.
- [ ] Use a mutable pointer such as `.neuron/current.json` only to select which immutable build is current.
- [ ] Remove duplicated mutable registration state such as `.neuron/register.json`.
- [ ] Make the current-build pointer safe to update atomically.
- [ ] Make `neuron run` consume the selected immutable build rather than mutating or rebuilding it.
- [ ] Define exactly when a new build is required: source changes, project configuration changes, Capability Runtime declaration changes, or dependency changes.
- [ ] Ensure build reproducibility from the frozen build alone.
- [ ] Ensure an existing immutable build remains executable after the project's source files change.
- [ ] Add tests proving two immutable builds can coexist and remain independently executable.

## 5. Immutable Capability Runtime Store

- [ ] Treat every installed Capability Runtime as an immutable artifact after commit.
- [ ] Keep shared installed Capability Runtime artifacts outside project runtime state so multiple projects can safely reuse them.
- [ ] Keep project-specific frozen dependency references separate from the shared installed artifact store.
- [ ] Use an identity that includes the artifact content digest, either directly or as part of the final store identity.
- [ ] Prefer a layout that prevents replacing the same logical artifact with different content, such as `<name>/<version>/<digest>/`.
- [ ] Stage the complete installation before commit.
- [ ] Include the final `install.json` and all referenced files in the staged tree before the atomic rename.
- [ ] Remove the current post-rename rewrite of the install record.
- [ ] Never rewrite a committed Capability Runtime directory in place.
- [ ] Make reinstalling the same identity succeed only when the existing artifact is byte/content-equivalent, otherwise fail with an integrity conflict.
- [ ] Make removal a cache/garbage-collection operation rather than an update operation.
- [ ] Store the artifact digest and immutable identity in the frozen dependency record.
- [ ] Stop persisting a host-specific `RootDir` as part of the frozen deployment identity.
- [ ] Resolve the local store path from the frozen artifact identity when N.O.R.E. starts.
- [ ] Add integrity tests that modify a committed artifact and verify that the digest/identity no longer matches.

## 6. Project Configuration Schema

- [ ] Update `neuron.config.*` to own the Assembly's project-level identity.
- [ ] Add `name`.
- [ ] Add `version`.
- [ ] Keep `lang`.
- [ ] Keep `entry`.
- [ ] Add `runtime.host`.
- [ ] Add `runtime.port`.
- [ ] Define `"auto"` as the default runtime port mode.
- [ ] Keep registry/cache configuration separate from project runtime identity.
- [ ] Reject internal storage, PID, socket, daemon, and other engine plumbing from normal project configuration.
- [ ] Define strict validation for unknown configuration keys.
- [ ] Update the existing YAML/JSON config loader and merge precedence rules.
- [ ] Update `neuron init` templates to generate the new configuration.
- [ ] Update configuration documentation and examples.

## 7. Assembly Public API

- [ ] Change the TypeScript Assembly constructor from `Assembly({ name, version, description })` to `Assembly()`.
- [ ] Remove Assembly `description` from the executable/public Assembly model.
- [ ] Keep Assembly `name` and `version` in the canonical manifest, but source them from `neuron.config.*`.
- [ ] Update the project/compiler boundary so project configuration supplies Assembly metadata before canonicalization/hashing.
- [ ] Keep Assembly version in the canonical artifact because it currently participates in Assembly identity and hashing.
- [ ] Ensure Assembly metadata is not duplicated between project configuration and authored TypeScript.
- [ ] Keep the existing `Assembly.paramsSchema(...)` API.
- [ ] Make Assembly params part of the canonical manifest rather than keeping `_inputPorts` only inside the TypeScript SDK.
- [ ] Add `Assembly.resultSchema<T>()`.
- [ ] Add `Assembly.result({...})` for defining the public Assembly result mapping.
- [ ] Allow Assembly `.result({...})` to reference results from multiple different Capabilities.
- [ ] Preserve the declared Assembly result field names exactly in the result contract.
- [ ] Validate every Assembly result mapping against the Assembly result schema.
- [ ] Validate every referenced Capability result field at compile time in TypeScript and again during canonical compilation.
- [ ] Ensure Assembly result mapping is represented structurally in the canonical manifest, not as arbitrary expression strings.
- [ ] Update Assembly hashing so params/result contracts and result mappings are part of the build identity.

## 8. Capability Public API

- [ ] Change `Capability({ name, version, description })` to `Capability(name)`.
- [ ] Keep Capability `name` as the logical Capability reference used by composition.
- [ ] Remove Capability `version` from the Assembly-facing Capability Definition.
- [ ] Remove Capability `description` from the Assembly-facing Capability Definition.
- [ ] Keep implementation/version/registry identity on the Capability Runtime/package side.
- [ ] Ensure the Capability Runtime package remains the source of truth for implementation version and artifact identity.
- [ ] Keep `.runtime(...)`, `.paramsSchema(...)`, `.resultSchema(...)`, `.withParams(...)`, `.bind(...)`, `.connect(...)`, and `.result` semantics where they remain useful.
- [ ] Update generated/imported Capability Packages so Assembly authors normally consume ready-to-use typed Capability Definitions rather than reconstructing runtime metadata.
- [ ] Update TypeScript SDK types and all examples to remove the deprecated metadata fields.

## 9. Structured Expressions and Compilation Protocol

- [ ] Remove the use of arbitrary string expressions as the canonical representation of Capability references.
- [ ] Replace values such as `"process-order.result.order.id"` with a structured reference object.
- [ ] Define one canonical structured reference type for params/result references across TypeScript SDK, manifest, compiler, protocol, and N.O.R.E.
- [ ] Represent at minimum the referenced Capability identity and the result field/path separately.
- [ ] Ensure nested result paths are represented as structured path segments rather than embedded in strings.
- [ ] Keep expression operators/conditions structured rather than encoding them as opaque strings wherever the protocol can support it.
- [ ] Define a source/reference union that can distinguish Assembly params, Capability results, project variables, literals, and supported expressions.
- [ ] Make the TypeScript SDK emit structured references directly.
- [ ] Make the compiler consume structured references directly.
- [ ] Make the canonical manifest serialize those references as objects.
- [ ] Update hashing/normalization to hash structured references deterministically.
- [ ] Update N.O.R.E. planning and transition evaluation to consume the structured representation.
- [ ] Remove string parsing logic that exists only to reconstruct Capability identity from expressions.
- [ ] Add compatibility handling only where existing persisted manifests require migration.
- [ ] Add tests for nested paths, multiple Capability sources, invalid references, literal values, and deterministic serialization.

### Findings during Phase 1 implementation

- [ ] **P1-1** **(Phase 1, §9 Go half)** — Until the TypeScript SDK emits structured references (Phase 3), the canonical manifest loader and every persisted registered-assembly load path must compat-parse legacy `expression` strings into the structured form at the boundary. The agreed decision: structure mapping sources; keep validation/condition expressions as CEL strings. The compat parser is the single boundary that turns `source.result.*` / `execution.params.*` / scalar literals into `ValueRef`, and it is the only place string mapping sources survive. It must reject anything it cannot parse instead of passing opaque strings into the compiler.
- [ ] **P1-2** **(Phase 1, §9 Go half)** — `canonicalizeExpression` currently rewrites camelCase identifier tokens inside whole expression strings. Once mapping sources are structured, casing normalization applies to `ValueRef.Path` segments (+ `Capability` identities), and only validations/config templates keep the string tokenizer. Both halves must agree on the snake_case convention so resolved `source.result` lookups never hit the P0-23 casing split again.

## 10. Assembly Result Semantics in N.O.R.E.

- [ ] Add an Assembly result contract to `core.Assembly`.
- [ ] Add Assembly result schema and result mappings to the canonical manifest.
- [ ] Compile the Assembly result mapping into a N.O.R.E. execution plan.
- [ ] Resolve each Assembly result field independently from its declared structured reference.
- [ ] Permit one Assembly result to read from one Capability and another Assembly result to read from a different Capability.
- [ ] Permit the Assembly result mapping to reference Capability results from multiple branches when those results are guaranteed to exist before Assembly completion.
- [ ] Reject result mappings that reference an unknown Capability.
- [ ] Reject result mappings that reference an undeclared Capability result field/path.
- [ ] Reject result mappings that cannot be guaranteed to exist at Assembly completion.
- [ ] Produce one final Assembly result object from the mapped fields.
- [ ] Keep internal per-Capability execution results available to the scheduler/planner only where required for execution.
- [ ] Stop exposing the full Capability result map as the normal execution result.

## 11. Capability Runtime `runtimeConfig.execution.mode`

- [ ] Preserve the existing Capability Runtime `runtimeConfig.execution.mode` semantics: `wait` and `detach`.
- [ ] Treat `detach` as a lifecycle boundary where the parent Assembly execution does not wait for that Capability branch.
- [ ] Define an Assembly-result rule that only values guaranteed to be completed inside the root execution may contribute to the final Assembly result.
- [ ] Reject an Assembly `.result({...})` reference that directly references a Capability whose effective `runtimeConfig.execution.mode` is `detach`.
- [ ] Reject an Assembly result reference to any descendant Capability whose execution is downstream of a detached Capability and therefore belongs to the detached branch.
- [ ] Perform this validation during compilation/build, not only at runtime.
- [ ] Make the validation understand the actual dependency graph rather than checking only the immediate Capability.
- [ ] Produce a clear compiler error identifying the Assembly result field, detached Capability, and offending result reference.
- [ ] Do not silently convert `detach` to `wait` to satisfy an Assembly result reference.
- [ ] Do not silently omit a detached result from the Assembly result.
- [ ] Keep detached Capability tasks independently observable through advanced execution APIs.
- [ ] Add tests covering direct detached references, detached descendants, mixed detached/non-detached branches, and valid independent Assembly result mappings.

## 12. Execution Return Contract

- [ ] Replace the current wait-mode response that exposes `execution.StringKeyedResults()` with an Assembly-level result response.
- [ ] Rename the response's result field to `result`.
- [ ] Return only the Assembly result contract for successful synchronous execution.
- [ ] Keep `executionId` and final `status` in the response for observability/correlation.
- [ ] Return a structured error/status response for failed or cancelled executions.
- [ ] Do not return execution history in the normal synchronous response.
- [ ] Do not return the entire internal execution context.
- [ ] Do not return the map of all Capability results.
- [ ] Update the terminal `execution.completed` event to carry the Assembly result instead of the entire Capability result collection where that event is used for client-visible completion.
- [ ] Keep internal scheduler state and Capability results private to N.O.R.E.
- [ ] Update the client API and CLI to consume `result`, not `results`.

## 13. Execution HTTP API

- [ ] Add a simple synchronous execution endpoint such as `POST /v1/run`.
- [ ] Define the request body using `params`.
- [ ] Define the response body using `result`.
- [ ] Keep Assembly identity/runtime selection outside the request body when the project runtime has exactly one configured Assembly.
- [ ] Preserve the existing lower-level execution API internally until the new facade is proven.
- [ ] Separate synchronous execution from asynchronous execution instead of overloading one request with `mode`.
- [ ] Keep `POST /v1/executions` as the advanced async execution resource if it remains necessary.
- [ ] Keep `GET /v1/executions/{id}` and event streaming as advanced execution-management APIs.
- [ ] Ensure the simple `/v1/run` contract cannot accidentally expose execution history.
- [ ] Update API protocol types, handlers, clients, tests, and documentation.

## 14. `neuron instance`

- [ ] Stop presenting `instance` as the primary user-facing execution abstraction.
- [ ] Keep the internal Instance model only where it is required by the N.O.R.E. engine during migration.
- [ ] Remove normal workflows that require users to create/manage an Instance before `neuron run`, and make the defaul output or render format to be json.
- [ ] Remove `neuron instance clear` unless a concrete runtime-management requirement remains.
- [ ] Remove normal `instance remove` unless it still represents a necessary management operation.
- [ ] Remove documentation that teaches Assembly execution primarily through Instance lifecycle commands.
- [ ] Move advanced execution inspection to execution-oriented commands/API surfaces.
- [ ] Ensure one project runtime owns the Assembly execution lifecycle without requiring users to reason about instance IDs.

## 15. Capability Runtime Template Generation

- [ ] Add a template-generation command for Capability Runtimes, for example `neuron new capability-runtime <name> --lang go`.
- [ ] Define the supported language set explicitly, starting with the languages for which Neuron has an executor/runtime SDK.
- [ ] Add language-specific template generators rather than copying one generic project template.
- [ ] Generate the runtime project structure, build configuration, runtime manifest template, and handler entry point.
- [ ] Generate protocol/lifecycle boilerplate using the existing Capability Runtime SDK for the selected language.
- [ ] Ensure the generated runtime uses the existing `capabilityruntime.Serve`/equivalent SDK rather than hand-written protocol transport code.
- [ ] Generate a minimal business handler that receives `params` and returns a structured `result`.
- [ ] Generate the Capability Runtime manifest with the correct `apiVersion`, runtime metadata, protocol, entrypoint, capabilities, and platform declarations.
- [ ] Generate build/package configuration compatible with `neuron build`.
- [ ] Make generated artifacts compatible with the existing local Capability Runtime resolution and installation flow.
- [ ] Ensure generated projects produce immutable installable artifacts.
- [ ] Add language validation and a clear error for unsupported languages.
- [ ] Add end-to-end tests for each supported language template from generation through build, installation, and execution.
- [ ] Update documentation so runtime authors start with the template command instead of implementing protocol boilerplate manually.

## 16. Build / Runtime / Protocol Naming Cleanup

- [ ] Replace public `input` terminology with `params` throughout the Assembly SDK, CLI, HTTP API, examples, and user documentation.
- [ ] Replace public `output` terminology with `result` throughout the Assembly SDK, CLI, HTTP API, examples, and user documentation.
- [ ] Use `params` for Capability invocation data.
- [ ] Use `result` for Capability and Assembly returned data.
- [ ] Use `Assembly.paramsSchema()` and `Assembly.resultSchema()`.
- [ ] Use `Capability.paramsSchema()` and `Capability.resultSchema()`.
- [ ] Rename TypeScript generic concepts toward `TParams` and `TResult`.
- [ ] Remove new APIs that introduce arbitrary string-based result references.

### Findings during Phase 1 implementation

- [ ] **P1-3** **(Phase 1, §16 Go half)** — `application/sdk/` is dead code (referenced by nothing in any Go module) and documents a fictional dialect (`{{ input.* }}`, `source.output.*`). It violates AGENTS.md §21 and would otherwise survive the cleanup teaching stale terminology. Deleted with this phase.
- [ ] **P1-4** **(Phase 1, §16 Go half)** — The CLI reads the terminal `capability.completed` payload under the JSON key `"Output"` (`application/internal/cli/output/model.go`), but the runtime serializes `CapabilityCompletedPayload{Result}` (`nore/internal/event/event.go`) — so capability result frames never render for the operator. This is the observable half of the `output → result` rename.
- [ ] **P1-5** **(Phase 1, §16 Go half)** — `application/project/types.go` `ValidationConfig{Input, Output}` is unused YAML configuration. Deleted rather than renamed, per AGENTS.md §21.
- [ ] **P1-6** **(§17 migration)** — YAML authoring uses `direction: input|output` for port classification (`application/compiler/manifest/yaml.go`). §16's listed public surfaces are SDK/CLI/HTTP/docs; the YAML key is an authoring-language surface. Decide its treatment during migration rather than renaming mid-Phase-1 and breaking existing YAML projects.

## 17. Migration / Compatibility

- [ ] Define a single migration path from the current `Assembly({ name, version, description })` API to `Assembly()`.
- [ ] Define a migration path from Capability `{ name, version, description }` to `Capability(name)`.
- [ ] Define migration for existing manifests containing Capability metadata fields that are no longer part of the public model.
- [ ] Define migration for existing Assembly manifests without Assembly params/results.
- [ ] Define migration for existing string expression references to structured references.
- [ ] Define migration for existing build/registration records to immutable build artifacts.
- [ ] Define migration for existing `RootDir` values in frozen Capability Runtime records so runtime identity becomes artifact-based.
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
