# Neuron Implementation Priorities — Durable Execution and Composition Roadmap

> This document replaces the previous project-runtime roadmap. The work tracked here is the
> foundation needed for durable, long-running, real-world compositions: correctness of
> persistence, pluggable storage, fan-in, durable execution, signals/timers, and a coherent
> developer surface.

## How this document is used

This file is the **single source of truth** for the work below.

Rules that apply to every change:

- A checkbox is marked `[x]` only after the change is implemented, validated with
  `scripts/script.sh`, and committed on its own branch.
- Never move on to the next phase while the current phase's exit criteria are unmet.
- Never leave dead code, obsolete abstractions, commented-out experiments, or misleading
  settings behind. Removing them is part of the item that revealed them.
- Findings that emerge while implementing are appended to the owning phase's
  **Findings** section as new checklist items. Do not silently fix and do not silently drop.
- Keep completed items as historical implementation records. Add new work to the owning phase
  rather than duplicating a task across sections.
- Each phase is one branch and one or more coherent commits
  (`feat(scope):`, `fix(scope):`, `refactor(scope):`, `test(scope):`, `docs(scope):`).
- Line references are evidence captured during investigation. They may drift; re-verify before
  editing.

## Relationship to the previous roadmap

The previous roadmap (project-scoped runtime, immutable builds, multi-assembly projects,
providers, direct invocation, and built-in capability runtimes) is **substantially complete**
through its Phase 16. Its history remains in git. This document does not re-open that work.

The one unfinished area from the previous roadmap — the production built-in capability
runtimes formerly numbered §25–§34 / Phase 17 — is **carried forward as Phase 7 and deferred**
until Phases 1–6 meet their exit criteria. It is not dropped.

The feedback that produced this roadmap was verified against the codebase before being
accepted. The evidence is recorded in the next section so future work does not have to
re-litigate it.

---

## Verified current-state evidence

Investigated with subagents against the repository at the time of writing.

| Claim from feedback | Verdict | Evidence |
| --- | --- | --- |
| Storage provider is configured as `"file"` but SQLite is constructed directly | Confirmed | `application/config/defaults.go:39-42`; `application/config/config.go:176-187`; `nore/cmd/nore/main.go:131` and `:410` |
| Storage cannot be selected through configuration | Confirmed | `storage` is rejected in every config file: `application/config/validate.go:33-47` |
| Generic runtime code imports a SQLite package | Confirmed | `nore/internal/execution/store.go:9`; `nore/internal/event/store.go:11`; `nore/internal/instance/store.go:10` all import `storage/sqlite` for `SanitizeKey` |
| `ExecutionStore.Add`/`Save` mutate memory before persisting | Confirmed | `nore/internal/execution/store.go:27` then `:36`; `:53` then `:62` |
| Persistence failures silently become in-memory/partial results | Confirmed | `nore/internal/execution/store.go:83-85`, `:121-123`, `:139-141`; malformed events skipped at `nore/internal/event/store.go:65-71` |
| Persistence uses `context.Background()` on important paths | Confirmed | `nore/internal/execution/store.go:36,74,80,112,139`; `nore/internal/instance/drain.go:90` |
| Restored executions are non-resumable; unfinished work is failed | Confirmed (deliberate current behavior, not a bug) | `nore/internal/execution/snapshot.go:96-100`; `nore/internal/instance/restore.go:38-68`; `nore/internal/instance/drain.go:35-45` |
| Snapshot does not include the executable blueprint | Confirmed | `nore/internal/execution/snapshot.go:11-25`; blueprint is recompiled, `nore/internal/instance/instance.go:179-191` |
| Fan-in is rejected in the TypeScript SDK | Confirmed | `packages/assembly-sdks/typescript/src/assembly.ts:310-315` |
| Fan-in is rejected by the planner | Confirmed | `nore/internal/planner/compiler.go:85-88` |
| Scheduler assumes single-predecessor readiness | Confirmed | `nore/internal/execution/scheduler/scheduler.go:240-292`; `nore/internal/execution/execution.go:131-148` |
| Detached-scope ownership also depends on the single-parent rule | Confirmed (not in feedback; additional constraint) | `nore/internal/planner/detach.go:20-25`, `:117-143` |
| Re-registering an assembly version prunes old content-hash artifacts | Confirmed, same version only | `nore/internal/assembly/repository.go:32-36`, `:62-66`, `:248-265` |
| Retries are in-memory only and have no failure classification | Confirmed | `nore/internal/execution/engine/invocation.go:104-158`, `:232-244`; `shared/types/core/runtimeconfig.go:78-112` |
| No durable suspension/signal/timer exists | Confirmed by the state model and scheduler | Statuses in `nore/internal/execution/execution.go:15-46`; no waiting state; no timer persistence anywhere |
| `neuron dev` does not exist; `neuron run` does not start a runtime | Confirmed | `application/internal/cli/cli.go:77-92`; `application/internal/cli/run/run.go:117-132` |
| There is no `config validate/show/explain` command | Confirmed | no such command is registered; validation is implicit in `config.Load` |
| Config contains a legacy singular `entry` alongside `assemblies` | Confirmed | `application/config/config.go:43`, `:48`; `application/config/assemblies.go:32-50` |
| HTTP built-in has its own retry logic separate from the runtime policy | Confirmed | `nore/internal/builtins/http/http.go:244-290`, `:364-392` |

### Corrections and nuances to the feedback

- **Pruning is per `(assemblyID, version)`.** Distinct versions coexist; only a same-version
  re-registration deletes the previous content-hash artifact.
- **Restoring as `failed` is a deliberate correctness choice**, not a defect. The defect is the
  absence of a durable execution mode, not the conservative restore behavior.
- **The assembly hash is recorded but not honored.** `Repository.Resolve` claims a fully
  specified hash maps to its artifact, but the implementation ignores `key.Hash`
  (`nore/internal/assembly/repository.go:94-112`, `:143-184`). This is a real defect and a
  prerequisite for durable execution.
- **Storage being non-configurable is currently intentional**, which is why the `Provider`
  field is misleading rather than user-visible. It must still be resolved before selecting
  providers.

---

## Locked architectural decisions

These decisions constrain every phase. If implementation reveals a conflict, stop and resolve
the decision before coding.

1. **Composition stays the central abstraction.** Neuron composes capabilities; it does not
   become a database, broker, cloud platform, or integration catalog.
2. **Observability, persistence, and durable execution are distinct guarantees.** History is
   persisted by default; resumability is an explicit execution mode with defined recovery
   semantics.
3. **Fan-in is part of the core execution model.** It is authored by the SDK, validated by the
   compiler/planner, and enforced by the scheduler with explicit join semantics.
4. **Storage is a runtime provider chosen at deployment time.** Backend selection never lives
   in an assembly, and project configuration never carries deployment secrets.
5. **Long-running execution uses durable suspension, not a sleeping worker.** Signals, timers,
   and deadlines are first-class execution primitives.
6. **The simple path stays simple.** Common commands and configuration use sensible defaults;
   advanced controls never become prerequisites for normal operation.
7. **The storage backend is never leaked into generic runtime code.** Only
   `nore/internal/storage` and its backend packages may know a specific database exists.
8. **A durable execution references an immutable assembly artifact.** A retained execution
   must never resume against a graph it did not start with.
9. **Do not adopt full event sourcing.** Use durable snapshots plus transactional state
   transitions; keep the event log for observability and diagnostics.
10. **Durable dispatch is at-least-once.** Exactly-once external side effects are a capability
    concern expressed through idempotency keys, not a claim the runtime can make alone.

---

## Implementation order

The order is forced by dependency, not convenience.

```text
Phase 1   persistence correctness and storage contract        R1
Phase 2   storage providers and migrations (SQLite, Postgres) R2
Phase 3   graph correctness and fan-in                        R3
Phase 4   durable execution and recovery                      R4
Phase 5   signals, timers, and long-running execution         R5
Phase 6   developer experience and integration ecosystem      R6
Phase 7   production built-in capability runtimes (deferred)  R7
```

Why this order:

- **Phase 1 precedes everything.** Durable execution, fan-in persistence, and PostgreSQL all
  require a storage contract whose failures are explicit and whose writes can be transactional.
- **Phase 2 precedes Phase 4.** Durable transitions need transactions and a backend that can
  enforce them. Provider selection must be real before the runtime depends on durability.
- **Phase 3 precedes Phase 4.** Recovery must restore join state. Fan-in and durability share
  the same execution-state model; building them separately would produce two representations.
- **Phase 4 precedes Phase 5.** Signals and timers are durable waits layered on the resumable
  state machine.
- **Phase 6 accompanies Phases 2–5.** CLI and configuration simplification can proceed in
  parallel, but its exit criteria depend on the runtime features being stable.
- **Phase 7 is last.** The built-in runtimes are a public surface; shipping them before the
  execution model settles would churn that contract twice.

---

## Phase 1 — Persistence Correctness and Storage Contract

Branch: `fix/persistence-correctness`

Goal: persistence failures are explicit, generic runtime code is backend-neutral, storage
operations that must be atomic are atomic, and assembly artifacts are addressable by content
hash. This phase defines the contract Phase 4 will rely on.

Exit criteria:

- The runtime's observable state is identical whether a storage operation succeeds normally or
  fails; failures are never converted into false "not found" results or partial listings.
- No package outside `nore/internal/storage` imports a concrete storage backend.
- Assembly resolution by content hash actually returns that hash's artifact.
- Storage supports transactions with contract tests.

### 1.1 One storage initialization path

- [x] **R1-1** Remove the misleading provider default. `application/config/defaults.go:39-42`
      sets `Storage.Provider: "file"`, while `nore/cmd/nore/main.go:131` and `:410` hardcode
      `sqlite.New(...)` and ignore it. Either delete the field until Phase 2 selects a backend,
      or make the default truthful (`sqlite`) and route initialization through a factory.
- [x] **R1-2** Add a backend-neutral factory, for example
      `storage.Open(ctx, storage.Config) (storage.Store, error)`, in `nore/internal/storage`.
      It must validate the configured provider/driver and return an actionable error for an
      unknown one instead of falling back silently.
- [x] **R1-3** Route both daemon mode and project mode through the same factory so there is one
      construction path (`nore/cmd/nore/main.go:131`, `:410`). Delete the direct `sqlite.New`
      calls from the command.
- [x] **R1-4** Define store lifecycle explicitly: open, ping/health, migrate, close. A failed
      open must not leave a half-initialized store that later reports missing records.
- [x] **R1-5** Keep the `storage.Store` interface as the only type runtime components receive.
      No component may construct its own store.

### 1.2 Remove backend-specific dependencies from generic code

- [x] **R1-6** Move `SanitizeKey` out of `nore/internal/storage/sqlite`. It is not a SQLite
      concept. Put key construction in the backend-neutral `nore/internal/storage` (for example
      `storage.Key(parts ...string)`) and update:
      - `nore/internal/execution/store.go:9`, `:153`
      - `nore/internal/event/store.go:11`, `:58`, `:130`, `:135`
      - `nore/internal/instance/store.go:10`, `:80`
- [x] **R1-7** Add a guard test that fails if `nore/internal/execution`, `nore/internal/event`,
      `nore/internal/instance`, `nore/internal/assembly`, or `nore/internal/planner` depend on
      `nore/internal/storage/sqlite` (or any future concrete backend). A `go list -deps` check
      in a test is sufficient and catches transitive leakage.

### 1.3 Explicit persistence error semantics

- [x] **R1-8** `ExecutionStore.Add` and `Save` must not leave memory ahead of durable state.
      Today `Add` calls `s.mem.Add` before `s.store.Put`
      (`nore/internal/execution/store.go:27` vs `:36`) and `Save` does the same at `:53` vs
      `:62`. Persist first, then update memory, or roll memory back on a failed write.
      Document the chosen ordering because callers depend on it.
- [x] **R1-9** Distinguish outcomes on every read path:
      `ErrNotFound`, backend/unavailable, and decode failure.
      - `ExecutionStore.Get` currently delegates to `loadFromStore`, which collapses every
        error into `(nil, false)` (`nore/internal/execution/store.go:138-150`).
      - `metadataStore.Get` does the same (`nore/internal/instance/store.go:47-57`).
      Return an error (or a typed result) instead of reporting a decode/backend failure as
      absence.
- [x] **R1-10** Remove silent in-memory fallbacks on list failures:
      `ExecutionStore.List` returns `s.mem.List()` when the store list fails
      (`nore/internal/execution/store.go:83-85`), and `ListByInstance` returns partial memory
      results (`:121-123`). A storage failure must surface; it must not masquerade as a
      complete execution list.
- [x] **R1-11** `event.Store.List` must not silently skip malformed events
      (`nore/internal/event/store.go:65-71`). Either fail with the offending key or return a
      typed partial result that names skipped records. Silent skipping turns corruption into
      missing history.
- [x] **R1-12** Add sentinel errors and wrapping helpers in `nore/internal/storage`
      (`ErrNotFound` already exists) so callers use `errors.Is` instead of inspecting strings.
- [x] **R1-13** Make save conflicts observable. Add a revision/updated-at guard to the persisted
      execution record and return a typed conflict when a stale copy is written. Two schedulers
      must not silently overwrite each other once Phase 2 introduces transactional backends.

### 1.4 Cancellation-aware persistence

- [x] **R1-14** Replace `context.Background()` in request-scoped persistence with the caller's
      context:
      `nore/internal/execution/store.go:36,74,80,112,139` and
      `nore/internal/instance/drain.go:90`.
      Where an operation intentionally outlives cancellation (shutdown sweep, detached drain),
      use a bounded context with a documented timeout instead of an unbounded background one.
- [x] **R1-15** Add tests proving a cancelled context aborts the persistence call and does not
      produce a half-applied state.

### 1.5 Transactional storage contract

- [x] **R1-16** Extend the storage contract with transaction support, for example
      `WithTx(ctx context.Context, fn func(Tx) error) error` where `Tx` exposes the same
      Get/Put/Delete/List operations bound to one transaction. Keep the plain `Store` usable for
      reads and non-atomic writes.
- [x] **R1-17** Implement transactions for SQLite (`BEGIN`/`COMMIT`/`ROLLBACK`). The SQLite
      store currently serializes with a mutex and one connection
      (`nore/internal/storage/sqlite/sqlite.go:16-35`); the transaction implementation must not
      deadlock on that mutex.
- [x] **R1-18** Define which operations require a transaction and record the decision in
      `docs/ARCHITECTURE.md`:
      - assembly registration: artifact write, prune, and version-index update
        (`nore/internal/assembly/repository.go:37-86`, `:248-265`)
      - execution transition plus dispatch intent (Phase 4)
      - timer claim (Phase 5)
- [x] **R1-19** Add backend-parameterized contract tests for transaction commit, rollback,
      nested/re-entrant calls (define the rule), and cancellation.
- [x] **R1-20** Make assembly registration atomic in SQLite now that transactions exist. A
      partial registration (artifact written, index not updated, or prune before index write)
      must be impossible.

### 1.6 Content-hash-pinned assembly resolution

- [x] **R1-21** Fix `Repository.Resolve` so a key carrying a `Hash` loads exactly
      `assemblies/{id}/{version}/{hash}/{env}/manifest`. Today it ignores the hash and returns
      the newest artifact for the version (`nore/internal/assembly/repository.go:94-112`,
      `:143-184`), contradicting its own documentation.
- [x] **R1-22** Define and test the failure modes: unknown hash, hash present but artifact
      absent, and hash present with a mismatched stored hash. A missing pinned artifact is an
      error, never a silent fallback to current content.
- [x] **R1-23** Record the current same-version replacement behavior as an explicit retention
      limitation. Phase 4 (R4-2) removes it for artifacts referenced by durable executions.
      Until then, documentation must not claim content-addressed immutability that the
      repository does not provide.
- [x] **R1-24** Add tests: resolve by exact hash, resolve after a same-version rebuild, resolve
      after `--force`/`Delete`, and confirm distinct versions remain independently resolvable.

### 1.7 Document the execution guarantee contract

- [x] **R1-25** In `docs/ARCHITECTURE.md`, state the three guarantees separately:
      observability (you can inspect a run), persistence (history survives restart), and
      durable execution (unfinished work resumes). State that only the first two exist today.
- [x] **R1-26** Document the current restore behavior truthfully: instances restore
      metadata-only and unfinished executions are failed
      (`nore/internal/instance/restore.go:38-68`, `nore/internal/instance/drain.go:35-45`).
      Present it as the current non-durable mode, not as a defect.
- [x] **R1-27** Define the durable execution mode by name and semantics here, so Phase 4
      implements a documented contract rather than an emergent behavior.

### 1.8 Validation

- [x] **R1-28** Add failure-injection tests for every storage operation used by the runtime:
      Get, Put, Delete, List, and transaction begin/commit/rollback. Each test asserts that a
      failure is reported and never converted into absence, partial data, or success.
- [x] **R1-29** Add a concurrent save/claim test proving the revision guard rejects a stale
      writer.
- [x] **R1-30** Run `scripts/script.sh` and record the result in this phase. Result: passed after
      the Phase 1 changes (Go tidy/vet/tests/builds for `nore`, `shared`, `application`, and the
      Go SDK; pnpm typecheck/tests/build for the TypeScript SDK and example projects).

### Findings during Phase 1 implementation

Append new findings here as checklist items. Each finding states the defect, the evidence, and
the fix.

- [x] **Finding: SQLite re-entrant operations deadlocked instead of reporting a transaction.**
  Evidence: `Store.Get`/`Put`/`Delete`/`List` checked `txActive` only after taking `s.mu`, which
  `WithTx` already held; a plain operation from inside the callback waited forever. Fix: check
  `txActive` before acquiring the mutex, retain the post-lock check for the race window, and
  cover re-entrant `Get`/`Put` and nested `WithTx` in the storage contract suite.

- [x] **Finding: event deletion could leave a partially deleted history.**
  Evidence: `event.Store.DeleteExecution` listed keys and deleted them one at a time through
  separate `Store.Delete` calls; a failure mid-loop left earlier events gone and later events
  present. Fix: run the list and deletes inside one `storage.WithTx`, with a failure-injection
  test proving a failed delete rolls back the whole history.

- [x] **Finding: engine/scheduler tests could not exercise the repository error contract.**
  Evidence: `execution.MemoryStore` kept the old `Add(*Execution) error` / `Get(id) (*Execution,
  bool)` shape while `contracts.ExecutionRepository` became context- and error-aware, so tests
  used a looser type than production. Fix: make `MemoryStore` implement the full repository
  interface and migrate scheduler/engine tests to it.

- [x] **Finding: the effective storage provider never reached the N.O.R.E. process.**
  Evidence: `application/config/defaults.go` defaulted to `file`, and neither
  `application/internal/cli/cli.go` nor `application/daemon/config.go` passed the selected
  provider to `nore`. Fix: default to `sqlite` and forward `--storage-driver` from both launch
  paths.

- [x] **Finding: instance removal is not atomic across executions, events, and metadata.**
  Evidence: `Manager.removeInstance` deletes each execution, each execution's events, and the
  metadata record through separate storage calls; a failure leaves a partially removed
  instance and the maps intact for retry, but the durable state is inconsistent until retry
  succeeds. Fix: perform all deletions (executions, events per execution, metadata) in a single `storage.WithTx`, delete in-memory state only after successful commit; maps removed only on success to preserve retry semantics.

---

## Phase 2 — Storage Providers and Migrations

Branch: `feature/storage-providers`

Goal: storage backend selection is real, SQLite and PostgreSQL implement the same contract,
schema changes are versioned, and the same conformance suite runs against both.

Exit criteria:

- Selecting `sqlite` or `postgres` through deployment configuration changes the backend without
  any code path in `nore/internal/execution`, `event`, `instance`, or `assembly` knowing which
  one is active.
- The shared contract suite passes against both backends.
- Schema migrations are versioned, recorded, and applied atomically where the backend supports
  it.

### 2.1 Provider selection

- [x] **R2-1** Define the storage configuration surface. Proposed shape:

      ```yaml
      storage:
        driver: sqlite | postgres
        path: ./.neuron/neuron.db          # sqlite
        dsn: ""                            # postgres; never committed
      ```

      Storage remains a **deployment/machine-level** setting. It stays rejected in project
      configuration files (`application/config/validate.go:33-47`) so an assembly never selects
      or embeds a database.
- [x] **R2-2** Provide environment and CLI overrides consistent with the existing precedence
      model: `NEURON_STORAGE_DRIVER`, `NEURON_DATABASE_URL`, and explicit runtime flags. Precedence
      remains defaults < global < project < env < CLI, with storage only reachable from the
      machine/deployment layers.
- [x] **R2-3** Reject an unsupported driver during configuration load with a message naming the
      supported drivers. Do not silently default after Phase 2 lands.
- [x] **R2-4** Keep credentials out of committed configuration. A DSN containing a password must
      be redacted in any diagnostic output (see R6-10).
- [x] **R2-5** Update `application/config/defaults.go`, `config.go`, `loader.go`, and the config
      documentation so there is exactly one description of the storage settings.

### 2.2 PostgreSQL backend

- [x] **R2-6** Implement `nore/internal/storage/postgres` against the Phase 1 contract,
      including transactions.
- [x] **R2-7** Use a real schema rather than an opaque blob-only table, or at minimum add the
      indexes required for prefix listing and key equality. The SQLite backend uses a single
      `kv_store` table (`nore/internal/storage/sqlite/sqlite.go:50-59`); PostgreSQL must not
      make `List(prefix)` a full scan of every row in a large table.
- [x] **R2-8** Configure connection pooling and timeouts explicitly. Document the expected
      deployment shape: PostgreSQL enables shared state, but horizontal execution also requires
      safe work claiming (Phase 4).
- [x] **R2-9** Implement health/readiness checks for the backend and expose them through the
      runtime's health surface.

### 2.3 Migrations

- [ ] **R2-10** Add a versioned migration framework shared by both backends, with a
      `schema_migrations` record and an ordered, idempotent migration set.
- [ ] **R2-11** Apply migrations during store initialization (Phase 1 factory) and fail startup
      with a precise error when a migration cannot be applied.
- [ ] **R2-12** Define downgrade/upgrade policy. At minimum: refuse to start against a newer
      schema than this binary understands rather than corrupting it.
- [ ] **R2-13** Document backup and restore for SQLite and PostgreSQL, and the supported upgrade
      path from an existing SQLite database created by the previous schema.
- [ ] **R2-14** Add a migration test that applies the full set to an empty database and asserts
      the resulting schema is usable by the contract suite.

### 2.4 Conformance

- [ ] **R2-15** Extract the storage contract tests into a suite parameterized by backend.
      Run it against SQLite in normal CI and against PostgreSQL when a DSN is provided
      (skip with a clear message otherwise).
- [ ] **R2-16** Include failure-injection and transaction semantics in the shared suite, not
      only in SQLite-specific tests.
- [ ] **R2-17** Add a test that proves no storage-specific logic leaks: the execution, event,
      instance, and assembly packages compile and pass against a fake/in-memory store that
      implements only the neutral contract.

### 2.5 Typed repositories (staged decision)

- [ ] **R2-18** Keep the generic key/value contract for this phase. Record the staged plan:
      Phase 4 introduces transaction-aware repository operations for execution transitions,
      readiness, timers, and dispatch. Do not turn every entity into a relational model now.
- [ ] **R2-19** If list/claim performance requires it after measurement, add indexed query
      operations behind the neutral backend interface rather than exposing SQL upward.
      Measure before optimizing.

### 2.6 Validation

- [ ] **R2-20** Run the same end-to-end execution through SQLite and PostgreSQL and assert
      identical observable results.
- [x] **R2-21** Run `scripts/script.sh` and record the result in this phase. Result: passed.

### Findings during Phase 2 implementation

Append new findings here as checklist items.

---

## Phase 3 — Graph Correctness and Fan-In

Branch: `feature/graph-fan-in`

Goal: the compiled graph and scheduler support multiple incoming dependencies with an explicit,
deterministic `all` join policy. This is a core execution-model change, not a validation
relaxation.

Exit criteria:

- Independent branches can converge, and each join executes exactly once with complete,
  validated inputs.
- Authoring works in the TypeScript SDK and the canonical/YAML path.
- Failure propagation and detached-scope ownership have defined semantics under fan-in.
- Join state is represented in the execution state (so Phase 4 can persist it).

### 3.1 Canonical execution model

- [ ] **R3-1** Add explicit incoming edges to the compiled graph. `types.ExecutionNode` currently
      carries only `Next` transitions (`nore/internal/planner/compiler.go:57-97`). Add the
      predecessor set (or derive it once and store it) so readiness does not have to be inferred
      from other nodes.
- [ ] **R3-2** Add an explicit join policy field with exactly one supported value for now:
      `all` (a target becomes ready only when every required predecessor is satisfied).
      Do not implement `any`, `quorum`, or conditional joins yet; leave room in the type without
      implying support.
- [ ] **R3-3** Define deterministic merge order for inputs from multiple predecessors. The order
      must not depend on map iteration or completion timing. Stable ordering by binding ID is
      acceptable; document the rule.
- [ ] **R3-4** Reject conflicting target-field mappings at build time. Two incoming bindings must
      not silently write the same target path. Either fail with the conflicting paths or require
      an explicit merge operation (not implemented in v1).
- [ ] **R3-5** Validate the merged input against the target capability's declared params before
      execution, using the same required/type validation already applied to single bindings
      (`nore/internal/execution/scheduler/scheduler.go:272-275`).
- [ ] **R3-6** Include join policy and incoming edges in canonicalization and hashing so a graph
      with fan-in has a distinct, deterministic identity.

### 3.2 TypeScript SDK

- [ ] **R3-7** Remove the fan-in rejection in `collectBindings`
      (`packages/assembly-sdks/typescript/src/assembly.ts:310-315`).
- [ ] **R3-8** Define the authoring form. The minimum is: a node whose references name multiple
      source capabilities emits one binding per source, and the target is implicitly an
      all-required join. Add an explicit join declaration only if the implicit form is ambiguous.
- [ ] **R3-9** Emit deterministic bindings and structured references for every incoming edge.
      Preserve the existing structured-reference rules (no opaque expressions).
- [ ] **R3-10** Validate that two incoming edges do not target the same param field, mirroring
      R3-4 at authoring time.
- [ ] **R3-11** Update type tests, examples, and the SDK README. Include a fan-in example.

### 3.3 YAML and canonical compilation

- [ ] **R3-12** Confirm and test that YAML bindings can target one capability from multiple
      sources, and that the canonical manifest represents them unambiguously.
- [ ] **R3-13** Ensure the canonical compiler applies the same duplicate-target and merge-order
      rules as the SDK. One authority per rule: prefer the canonical compiler as the enforcer
      and mirror only for authoring ergonomics.

### 3.4 Planner and detached scopes

- [ ] **R3-14** Remove the multiple-incoming rejection in the planner
      (`nore/internal/planner/compiler.go:85-88`).
- [ ] **R3-15** Build the predecessor set per node and validate the join policy. Update
      acyclicity and reachability to reason over explicit edges.
- [ ] **R3-16** Resolve detached-scope ownership under fan-in. Today detached compilation relies
      on "exactly one incoming binding"
      (`nore/internal/planner/detach.go:20-25`, `:117-143`). Decide and document:
      - a detached branch may not participate in a join, or
      - a join spanning detached and non-detached work is rejected, or
      - scope ownership is redesigned for multiple parents.
      The first option is the smallest correct rule; do not leave the invariant implicit.
- [ ] **R3-17** Keep the Assembly result detachment rule consistent: a join whose inputs include
      a detached branch must be rejected if the join is inside the root execution
      (`nore/internal/planner/result_detach.go`).

### 3.5 Execution state and scheduler

- [ ] **R3-18** Add join state to the execution model: which predecessors have completed, which
      mapped inputs have arrived, and whether the target has been dispatched. Store it in a form
      that Phase 4 can persist without redesign.
- [ ] **R3-19** Change readiness: `onCapabilityCompleted` must not immediately mark a
      multi-predecessor target ready
      (`nore/internal/execution/scheduler/scheduler.go:258-290`). It must record the completed
      edge, then mark the target ready only when the join is satisfied.
- [ ] **R3-20** Guarantee the join runs once. Two completions arriving concurrently must not
      dispatch the target twice. Use the execution's lock/state machine as the single authority.
- [ ] **R3-21** Fix in-flight accounting for fan-in. `CompleteCurrentAndSchedule` currently
      decrements one and adds the number of directly scheduled targets
      (`nore/internal/execution/execution.go:259-270`); a blocked join must not be counted as
      scheduled, and satisfying a join must increment exactly once.
- [ ] **R3-22** Define failure behavior. Required predecessors use fail-fast: a permanently
      failed required branch prevents the join. Retryable failures remain retryable. Document
      the rule next to the existing "one failure fails the execution" behavior.
- [ ] **R3-23** Handle cancellation with a pending join: buffered inputs are discarded and the
      join never fires.
- [ ] **R3-24** Persist join state in the execution snapshot (extends R3-18; full durability
      lands in Phase 4).

### 3.6 Validation

- [ ] **R3-25** Test completing predecessor branches in every order; assert one join execution
      with complete inputs.
- [ ] **R3-26** Test duplicate/out-of-order completion events and prove the join still runs once.
- [ ] **R3-27** Test conflicting target mappings fail at build time.
- [ ] **R3-28** Test detached-plus-fan-in combinations against the rule chosen in R3-16.
- [ ] **R3-29** Test cancellation with a partially satisfied join.
- [ ] **R3-30** Run `scripts/script.sh` and record the result in this phase.

### Findings during Phase 3 implementation

Append new findings here as checklist items.

---

## Phase 4 — Durable Execution and Recovery

Branch: `feature/durable-execution`

Goal: an opt-in durable execution mode that reconstructs unfinished work after a restart and
continues according to defined recovery rules, built on the Phase 1 storage contract, Phase 2
providers, and Phase 3 join state.

Exit criteria:

- After forcibly terminating (`SIGKILL`) and restarting the runtime, an unfinished durable
  execution restores its completed work and continues without rerunning completed capabilities.
- Recovery works with fan-out and fan-in.
- A durable execution always resumes against the immutable assembly artifact it started with.
- Durable state transitions are atomic and dispatch is safe under duplicate delivery.

### 4.1 Immutable assembly reference and retention

- [x] **R4-1** Persist the immutable assembly identity on the execution record. Today a snapshot
      stores only `InstanceID` (`nore/internal/execution/snapshot.go:11-25`) and the assembly is
      reached indirectly through instance metadata
      (`nore/internal/instance/store.go:17-27`). Store `{assemblyID, version, hash, env}` (or a
      single artifact reference) directly on durable executions.
- [x] **R4-2** Stop pruning an assembly artifact that a non-terminal durable execution
      references. Replace unconditional same-version pruning
      (`nore/internal/assembly/repository.go:62-66`, `:248-265`) with reference-aware retention
      or an explicit GC that refuses to delete a referenced artifact.
- [ ] **R4-3** Define artifact retention policy and make it configurable (for example retain by
      count, age, or while referenced). Removal must be a GC operation, never an implicit
      consequence of re-registration.
- [ ] **R4-4** Ensure builds do not silently invalidate durable executions. The CLI reuses
      `config.Version` across builds (`application/internal/cli/build/build.go:214-222`), so a
      same-version graph change is the dangerous case. Either retain per-hash artifacts (R4-2)
      or require a version bump when the graph changes; document the rule.
- [ ] **R4-5** Define `--force`/`Delete` behavior for live durable executions. It must not
      silently delete execution history that a running durable execution depends on; require an
      explicit destructive flag and report what will be affected.

### 4.2 Recoverable execution state

- [x] **R4-6** Version the execution snapshot schema and add a migration path
      (`nore/internal/execution/snapshot.go`). Add at least:
      - immutable assembly reference (R4-1)
      - per-capability state including `pending`, `ready`, `running`, `retry_wait`, `waiting`,
        `completed`, `failed`, `cancelled`
      - satisfied predecessor edges and buffered join inputs (Phase 3)
      - attempt count, last failure classification, and `next_attempt_at`
      - wait state (signal/timer correlation, deadline) for Phase 5
      - dispatch state that prevents duplicate dispatch
      - execution deadline distinct from per-capability timeouts
- [ ] **R4-7** Decide how the blueprint is recovered. Preferred: persist the immutable assembly
      reference and recompile from the retained artifact. Do not copy the whole graph into every
      execution unless measurement shows recompilation is a problem.
- [x] **R4-8** Define status semantics precisely. `waiting` is an execution that is suspended,
      not a capability that is running. `retry_wait` is a capability awaiting a future attempt.
      Neither may occupy a worker.

### 4.3 Transactional transitions and dispatch

- [x] **R4-9** Make the execution state machine own transitions. The scheduler currently mutates
      state and publishes events in several steps
      (`nore/internal/execution/scheduler/scheduler.go:240-314`). Introduce a transition
      operation that validates the precondition, applies the change, and persists it atomically.
- [x] **R4-10** Persist the state transition and the dispatch intent in one transaction (or a
      transactional outbox). A crash must not leave "capability completed" recorded without the
      resulting ready work being discoverable.
- [x] **R4-11** Define commit ownership: a successful durable transition means the required
      persistent record was committed, not merely updated in memory.
- [x] **R4-12** Add safe work claiming for multiple schedulers/replicas: a lease or a
      compare-and-set claim with an expiry, so the same ready work is not executed twice.
      SQLite is single-host; PostgreSQL must not assume a single scheduler.
- [x] **R4-13** On startup, reconcile pending work against persisted state rather than relying on
      in-memory event channels. In-memory events remain a low-latency wake-up mechanism only.

### 4.4 Recovery protocol

- [x] **R4-14** Implement recovery for unfinished durable executions:
      1. load the execution record
      2. resolve the immutable assembly artifact
      3. restore capability, join, attempt, and wait state
      4. identify ready-but-undispatched work and requeue it
      5. restore retry timers and suspended waits
      6. handle invocations whose outcome is unknown after interruption
      7. continue until completion, failure, cancellation, or another durable wait
- [ ] **R4-15** Keep non-durable behavior unchanged: when durability is off, interrupted
      executions are still failed rather than resumed
      (`nore/internal/instance/drain.go:35-45`). The mode decides which sweep applies.
- [x] **R4-16** Define interrupted-invocation handling. A capability that was `running` at the
      moment of interruption has an unknown external outcome. The default is at-least-once
      redispatch with an idempotency key; a per-capability policy may require reconciliation
      instead. Document both.
- [x] **R4-17** Never rerun a capability recorded as `completed`. Prove this in recovery tests.

### 4.5 Attempt identity and idempotency

- [x] **R4-18** Define stable execution ID, logical operation ID, and per-invocation attempt ID.
      Expose an idempotency key to the capability runtime so a downstream service can deduplicate.
- [ ] **R4-19** Extend the capability runtime protocol only if needed to carry the attempt/
      idempotency identity. Any protocol change is versioned and documented for every SDK
      (Go, .NET, TypeScript). Do not require a specific implementation language.
- [ ] **R4-20** Document at-least-once dispatch as the runtime guarantee. Exactly-once external
      side effects remain a capability/service responsibility.

### 4.6 Execution durability mode

- [x] **R4-21** Add an explicit execution durability setting, for example
      `execution.durability: none | history | durable`, aligned with the AGENTS.md storage
      policy guidance. Define what each mode retains and whether it resumes.
- [x] **R4-22** Default to the current behavior (history/observability) so enabling durability is
      a deliberate deployment choice. Durable mode requires a transactional backend and must
      fail startup with an actionable error if the backend cannot support it.
- [x] **R4-23** Ensure execution semantics never depend on durable storage being enabled, except
      for the resume guarantee itself.

### 4.7 Validation

- [ ] **R4-24** Add process-level tests that terminate the runtime at multiple points (`SIGKILL`,
      not graceful shutdown) and verify recovery:
      - after a capability completes but before its downstream is dispatched
      - between two fan-in branches
      - during a retry backoff
      - during an external wait (covered in Phase 5)
- [ ] **R4-25** Test that completed capabilities are not rerun and that a join preserved one
      branch's result while the other was unfinished.
- [ ] **R4-26** Test an execution that suspends, then a rebuild changes the same version's graph;
      verify the execution resumes against its original artifact (or fails explicitly if the
      artifact was deliberately deleted under an allowed policy). This is the core of R4-1/R4-2.
- [ ] **R4-27** Test duplicate dispatch/claim: two workers, one should win.
- [ ] **R4-28** Test that non-durable mode still fails interrupted executions and never reports
      them as running.
- [ ] **R4-29** Run `scripts/script.sh` and record the result in this phase.

### Findings during Phase 4 implementation

Append new findings here as checklist items.

---

## Phase 5 — Signals, Timers, and Long-Running Execution

Branch: `feature/durable-signals-timers`

Goal: a durable execution can suspend for hours or days without occupying a worker and resume
when an authenticated external signal, timer, or deadline fires.

Exit criteria:

- An execution can wait for an external event across a runtime restart without losing state.
- Duplicate signals and duplicate timer fires are idempotent.
- Waiting executions occupy no worker.
- Signals are authenticated and bound to the intended execution and wait.

### 5.1 Waiting state

- [ ] **R5-1** Add a defined `waiting` execution state with wait metadata: reason, correlation
      key, expected signal/timer, deadline, and the payload schema the execution expects.
- [ ] **R5-2** Ensure waiting executions are excluded from in-flight/worker accounting. A wait
      must not look like a running capability.
- [ ] **R5-3** Persist wait state transactionally with the transition that enters the wait, so a
      crash cannot produce a "waiting" execution with no wait record.

### 5.2 Signals

- [ ] **R5-4** Define a general signal primitive, for example
      `POST /v1/executions/{executionId}/signals/{signalName}`, with a unique delivery event ID
      in the payload. This is a runtime primitive, not a Telegram/Slack-specific integration.
- [ ] **R5-5** Authenticate signals with the existing API token mechanism and authorize them
      against the specific execution and wait state. A signal for an execution that is not
      waiting for that signal returns a precise error.
- [ ] **R5-6** Persist the signal before acknowledging receipt, then resume scheduling. A signal
      must not be lost between HTTP acceptance and execution resumption.
- [ ] **R5-7** Handle duplicate delivery idempotently: the same signal ID must not advance the
      execution twice.
- [ ] **R5-8** Define signal payload validation against the declared wait schema.
- [ ] **R5-9** Define behavior for a signal arriving after the wait deadline or after
      cancellation.

### 5.3 Durable timers

- [ ] **R5-10** Persist scheduled work with a due time (`next_attempt_at`, wake-at) rather than
      sleeping a goroutine. Retry backoff becomes durable scheduling, replacing the in-memory
      sleep at `nore/internal/execution/engine/invocation.go:232-244` for durable executions.
- [ ] **R5-11** Add a timer index/claim operation so a restarted runtime can find due and
      future timers efficiently and claim them safely.
- [ ] **R5-12** Implement a timer poller that claims due timers and resumes the associated
      executions. Define the polling interval and its interaction with latency.
- [ ] **R5-13** Handle clock changes and missed timers: a timer whose due time passed while the
      process was down fires on recovery.

### 5.4 Deadlines and lifetime

- [ ] **R5-14** Separate per-capability timeout from the maximum lifetime of an execution. A
      multi-day composition must not inherit a 30-minute capability timeout as its lifetime.
- [ ] **R5-15** Add a wait deadline policy (fail, cancel, or continue) and an execution-level
      maximum lifetime.
- [ ] **R5-16** On deadline expiry, apply the policy transactionally and record why.

### 5.5 Cancel and resume

- [ ] **R5-17** Allow an authorized caller to cancel a waiting durable execution and to list
      suspended executions with their wait reason and deadline.
- [ ] **R5-18** Define whether a cancelled wait can be resumed and how buffered state is handled.
      Prefer terminal cancellation unless a concrete need appears.

### 5.6 Capability protocol support

- [ ] **R5-19** Define how a capability requests suspension. Prefer a general runtime primitive
      (the capability returns a "suspend" outcome with a correlation key and expected signal)
      over embedding approval logic in a built-in.
- [ ] **R5-20** If the protocol is extended, version it and update every SDK README and the
      conformance tests. Keep it language-independent.
- [ ] **R5-21** Ensure the engine still enforces cancellation and deadlines on suspended work.

### 5.7 Validation

- [ ] **R5-22** Test suspending for approval, restarting the runtime, delivering the signal, and
      resuming the execution with the approval payload.
- [ ] **R5-23** Test a durable timer firing after a restart.
- [ ] **R5-24** Test duplicate signal delivery and duplicate timer fires; assert one state
      transition each.
- [ ] **R5-25** Test unauthorized signal rejection and signal-to-wrong-execution rejection.
- [ ] **R5-26** Test that a waiting execution does not occupy a worker (observe pool usage while
      suspended).
- [ ] **R5-27** Test deadline expiry policies.
- [ ] **R5-28** Run `scripts/script.sh` and record the result in this phase.

### Findings during Phase 5 implementation

Append new findings here as checklist items.

---

## Phase 6 — Developer Experience and Integration Ecosystem

Branch: `feature/dx-and-integrations`

Goal: the common path is simple and predictable, advanced controls are available but not
required, configuration is inspectable, and external integrations have a clear boundary without
Neuron becoming an integration catalog.

Exit criteria:

- A developer can create, build, run, inspect, and extend a project without understanding the
  internal runtime architecture.
- The documented command and configuration surfaces match the implementation.
- Execution inspection answers the practical questions about a failed or stuck execution.
- Integration guidance covers secrets, triggers, and versioning without service-specific logic
  in the core.

### 6.1 CLI coherence

- [ ] **R6-1** Define one obvious development path. The proposed surface is
      `neuron init`, `neuron dev`, `neuron build`, `neuron run`. `neuron dev` does not exist
      today; `neuron run` requires a separately started `neuron` runtime
      (`application/internal/cli/run/run.go:117-132`). Decide whether `dev` runs the runtime and
      the assembly, or whether `run` gains an explicit auto-start mode.
- [ ] **R6-2** Group commands by the concept the developer manages: project, execution, modules,
      runtime providers, storage, configuration. Preserve existing commands as aliases during
      migration and emit deprecation notices rather than breaking silently.
- [ ] **R6-3** Resolve the capability-runtime versus provider terminology in command names and
      help. Today `add`/`remove`/`capability runtime` manage capability runtimes while
      `install --provider`/`provider build` manage providers
      (`application/internal/cli/cli.go:77-92`); the distinction should be obvious from the help.
- [ ] **R6-4** Remove hidden or dead command surfaces that no longer map to a real operation
      (`register` is a hidden alias for `build`,
      `application/internal/cli/register/register.go:25`). Document any retained compatibility
      path and its removal condition.
- [ ] **R6-5** Add execution-oriented inspection commands: list, show, cancel, retry, resume,
      and events. Today `instance list/remove/clear` is the partial inspection surface
      (`application/internal/cli/instance/instance.go:10-27`). Expose execution IDs, state,
      assembly/version, current or waiting capability, attempts, and failure reason.
- [ ] **R6-6** Ensure errors state what went wrong, why it matters, and what to do next. Missing
      dependencies should name the module and the locations searched; invalid execution
      configuration should fail at build where possible.

### 6.2 Configuration

- [ ] **R6-7** Separate project definition from runtime/deployment configuration in the
      documentation and schema. Project configuration owns identity, language, assemblies, and
      variables; deployment owns storage, concurrency, listeners, retention, and recovery.
      Storage stays machine-level (Phase 2).
- [ ] **R6-8** Remove `application/internal/cli/config/config.go`. It is orphaned dead code
      (not registered or imported) and emits a divergent template; the live scaffold is
      `application/internal/cli/initcmd/init.go`. This violates the no-dead-code rule.
- [ ] **R6-9** Add `neuron config validate`, `neuron config show`, and `neuron config explain`.
      `validate` reports the first invalid setting with its path and constraint; `show` renders
      the effective merged configuration with secrets redacted; `explain` reports which layer
      supplied each value.
- [ ] **R6-10** Redact secrets in every diagnostic, log, and config rendering path (database
      DSNs, tokens, credentials). A secret must never appear in output because a configuration
      command printed the effective config.
- [ ] **R6-11** Publish a JSON Schema for `neuron.config.*` for editor validation and completion.
- [ ] **R6-12** Preserve the legacy singular `entry` migration for compatibility
      (`application/config/assemblies.go:32-50`) and document its removal at a major boundary.
      Do not remove it in this phase.
- [ ] **R6-13** Fix the documentation drift identified during investigation:
      - README `entry` examples that conflict with the primary `assemblies` schema
      - the claim that the CLI auto-starts the runtime (it does not)
      - the claim that project config rejects runtime endpoint plumbing (it is `daemon` that is
        rejected)
      Verify each against code before editing.

### 6.3 Observability and diagnostics

- [ ] **R6-14** Make execution inspection answer: which assembly and version ran, which
      capability is running or waiting, attempt count and failure reason, the next retry time,
      unsatisfied dependencies, why a join is not ready, and why an execution was recovered,
      failed, or retried.
- [ ] **R6-15** Add structured logs with correlation and execution IDs, plus health/readiness
      endpoints that include storage health.
- [ ] **R6-16** Add metrics for capability latency, retries, queue delay, execution failures,
      timer lag, and storage errors. Keep them optional and backend-agnostic.
- [ ] **R6-17** Ensure normal `run` output remains stable: result on stdout, diagnostics on
      stderr, non-zero exit on failure or cancellation. Preserve the invariants already covered
      by the acceptance suite.

### 6.4 Integration model

- [ ] **R6-18** Document how to author, package, validate, test, version, and install an
      external capability runtime. Treat integrations as versioned capability runtimes, not core
      built-ins. Do not build a provider-specific catalog.
- [ ] **R6-19** Define the secrets contract: the runtime decides which secrets a capability can
      access, how they are passed, and how they are redacted from logs and results. Service
      authentication (OAuth, token refresh, pagination) belongs to the integration, not the core
      HTTP runtime.
- [ ] **R6-20** Define the event/trigger boundary for external sources. An adapter may start or
      signal an execution through the authenticated execution API. A durable trigger records the
      event before acknowledging it, so an event is not lost between receipt and dispatch.
- [ ] **R6-21** Keep provider-specific behavior outside the composition: the assembly declares
      the capability contract; the module implements the integration; a model or service can be
      replaced without changing the composition.

### 6.5 Validation

- [ ] **R6-22** Add CLI tests for the actual command tree, defaults, precedence, invalid
      settings, and migration from older configurations.
- [ ] **R6-23** Add security tests proving external capabilities cannot bypass HTTP host
      restrictions, filesystem roots, command policies, API authentication, or signal
      authorization.
- [ ] **R6-24** Add documentation checks or targeted tests for the primary command/config
      examples where practical.
- [ ] **R6-25** Run `scripts/script.sh` and record the result in this phase.

### Findings during Phase 6 implementation

Append new findings here as checklist items.

---

## Phase 7 — Production Built-in Capability Runtimes (Deferred)

Branch: `feature/production-builtin-runtimes`

These items are carried forward verbatim from the previous roadmap. They remain pending and are
**deferred until Phases 1–6 meet their exit criteria**. They are listed here so they are not
dropped. Each keeps its original identifier.

### 7.1 Built-in contract

- [ ] **P17-1** Define the built-in contract in `docs/BUILTINS.md`.
- [ ] **P17-2** Built-ins are versionless; SDK stops emitting version/registry; `run -c`
      rejects versioned built-ins.
- [ ] **P17-3** Shared input contract in `nore/internal/builtins/input` (merge rule, typed
      accessors, output-format convention).
- [ ] **P17-4** Options come from capability input, never `RuntimeConfig`.
- [ ] **P17-5** Documented defaults for every built-in.
- [ ] **P17-6** Specific, actionable errors; no silent coercion or truncation.
- [ ] **P17-7** One Go package per built-in under `nore/internal/builtins/`.

### 7.2 Security policy

- [ ] **P17-8** Define the policy contract in `shared/types/builtins`.
- [ ] **P17-9** Compiled-in ceilings live in `shared`; `nore` re-validates.
- [ ] **P17-10** Add a `builtins` config section that can only tighten defaults.
- [ ] **P17-11** Convey the effective policy across the module boundary (`--builtin-policy`).
- [ ] **P17-12** A built-in never reads its limits from the assembly.

### 7.3 `neuron:core:http`

- [ ] **P17-13** Replace the demo-grade HTTP runtime (hard-coded 30s timeout, simulated latency,
      per-request client).
- [ ] **P17-14** Full request contract (`url`, `method`, `headers`, `query`, `body`,
      `contentType`, `auth`, `timeout`, `followRedirects`, `retry`, `format`).
- [ ] **P17-15** Block SSRF by default (loopback, private, link-local, metadata); allowlist
      through policy.
- [ ] **P17-16** Redirects off by default; bounded and re-checked per hop when enabled.
- [ ] **P17-17** Retries off by default; bounded; method opt-in only.
- [ ] **P17-18** Bound request and response bodies; oversized is a failure, not truncation.
- [ ] **P17-19** Return `status_code`, `headers`, `body`, `content_type`, `duration_ms` with
      structured JSON parsing.

### 7.4 `neuron:core:command`

- [ ] **P17-20** Fix start-failure reporting (`exit_code: 0, success: true`) and cancellation
      returning nil.
- [ ] **P17-21** Executable plus args array; reject spaces in `command`; no shell.
- [ ] **P17-22** Environment allowlist plus explicit `env`; never inherit the runtime
      environment.
- [ ] **P17-23** Working directory confined to a declared root.
- [ ] **P17-24** Memory-bounded output with an explicit `truncated` flag.
- [ ] **P17-25** Non-zero exit fails by default (`failOnNonZeroExit`).
- [ ] **P17-26** Cancellation and deadlines terminate the whole process group.

### 7.5 `neuron:core:filesystem`

- [ ] **P17-27** Add a filesystem capability runtime.
- [ ] **P17-28** Version 1 is read-only (`read`, `list`, `stat`).
- [ ] **P17-29** Recursive `list` bounded by a maximum entry count.
- [ ] **P17-30** JSON output by default; `format: text` as plain string.
- [ ] **P17-31** Paths resolve against a declared root; `~` does not bypass confinement.
- [ ] **P17-32** Reject traversal and symlink escapes.
- [ ] **P17-33** Bound reads and listings; report truncation.
- [ ] **P17-34** Default root is the project root; fall back to working directory when absent.

### 7.6 `neuron:core:set` and built-in removal

- [ ] **P17-35** Align `set` input contract with other built-ins (params override configuration).
- [ ] **P17-36** `set` returns a structured object; no output-format option.
- [ ] **P17-37** Remove `neuron:core:ai`.
- [ ] **P17-38** Remove `neuron:core:delay`.
- [ ] **P17-39** Remove `neuron:core:log`.
- [ ] **P17-40** Hard removal with an actionable error naming existing built-ins.
- [ ] **P17-41** Separate "is a core namespace" from "is an existing built-in".
- [ ] **P17-42** One authoritative built-in name list (currently duplicated between
      `shared/types/core.CoreTypes` and `nore/internal/registry`).

### 7.7 Built-in CLI and SDK surface

- [ ] **P17-43** Add `neuron builtins` and `neuron builtins <name>`.
- [ ] **P17-44** No running runtime required; the contract is shared data.
- [ ] **P17-45** `--verbose` adds full descriptions.
- [ ] **P17-46** SDK default runtime becomes `{ name: "neuron:core:set" }` (versionless, no
      registry).
- [ ] **P17-47** `./core` exports wrappers for `set`, `http`, `command`, `filesystem`; drops
      `delay`; migration entry, README update, type tests.
- [ ] **P17-48** HTTP result keeps `status_code` and `body`; new fields are additive.

### 7.8 Documentation and acceptance

- [ ] **P17-49** Update `docs/ARCHITECTURE.md`, `docs/GETTING_STARTED.md`, `docs/MODULES.md`,
      `docs/MIGRATION.md`, `application/README.md`, and the TypeScript SDK `README.md`.
- [ ] **P17-50** Add an acceptance test that drives a built-in end to end through the engine.
- [ ] **P17-51** Add unit tests for every built-in at its own boundary.

### Findings during Phase 7 implementation

Append new findings here as checklist items.

---

## Cross-phase acceptance matrix

These scenarios are the definition of done for the roadmap as a whole. Each maps to the phase
that owns it.

| Scenario | Owner | Required assertion |
| --- | --- | --- |
| Storage portability | R2 | Same contract tests pass against SQLite and PostgreSQL |
| Storage failures | R1 | Failed reads/writes/migrations/transactions are never reported as missing or success |
| Transaction atomicity | R1/R2 | A multi-write registration or transition is all-or-nothing |
| Fan-in ordering | R3 | Different completion orders produce one join with complete data |
| Fan-in recovery | R3/R4 | Restart after one branch completes; the completed result is preserved and the join stays blocked |
| Durable execution | R4 | Kill and restart; completed capabilities are not rerun |
| Interrupted operation | R4 | Unknown external outcome follows the configured retry or reconciliation policy |
| Durable waits | R5 | Restart while waiting; the wait is recovered and resumes on signal/timer |
| Duplicate events | R5 | Repeated signals/claims cause one state transition |
| Definition upgrades | R4 | A waiting execution resumes against its original immutable artifact |
| CLI/configuration | R6 | Command tree, defaults, precedence, invalid settings, and migration are tested |
| Security | R6 | Capabilities cannot bypass host, filesystem, command, API, or signal restrictions |

---

## Non-goals for this roadmap

- Do not build an integration catalog or ship a provider-specific integration for every service.
- Do not copy social-network or content-generation logic into the core runtime.
- Do not adopt full event sourcing as a prerequisite for durability.
- Do not implement `any`, `quorum`, or conditional fan-in policies in the first fan-in version.
- Do not make persistence mandatory for execution semantics.
- Do not expose internal scheduling steps as the public user model.

---

## Reporting requirements

When a phase completes, report:

- what changed and which files/boundaries it touched
- what was validated (`scripts/script.sh` result) and what tests were added
- which findings were appended and which were fixed
- remaining risks and any follow-up items promoted into a phase
