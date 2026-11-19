# 0001 — Durable signals, timers, and long-running execution (Phase 5)

- Status: accepted
- Date: 2026-11-16
- Owners: architect (design authority); coordinator (decisions adopted)
- Related tasks: Phase 5 `R5-1..R5-28`; `R4-14` step 5 / `F4-11`; `R4-6` / `R4-8`
- Supersedes: none

## Context

### Observed current state

**Execution model and wait metadata**

- `nore/internal/execution/execution.go:16-26` defines execution statuses including `StatusWaiting` and `StatusRetryWait`; `:28-51` defines capability statuses including `CapabilityWaiting` and `CapabilityRetryWait`.
- `CapabilityExecutionState` already carries `NextAttemptAt`, `WaitUntil`, `WaitReason`, `CorrelationKey`, `WaitType`, `ExpectedSignal`, `PayloadSchema`, `DispatchState`, and `Deadline` (`execution.go:53-102`). These fields are serialized because they live inside `ExecutionSnapshot.States` (`nore/internal/execution/snapshot.go:25`); they are not top-level snapshot fields.
- `ExecutionSnapshot` is schema version `"2"` (`snapshot.go:69`, `:156-167`).
- `Execution.Signal` (`execution.go:258-290`) only mutates memory: it clears wait metadata, sets `CapabilityReady`, and increments `inFlight`. It does not persist, publish, create a dispatch intent, deduplicate, or verify execution ownership.
- `wait.go:8-36` exposes only terminal `Done()`/`Wait()`; there is no wait entry or wait resolution API.
- `RecoverableDispatch` (`execution.go:443-499`) treats a waiting capability with `WaitUntil == nil` as resumable (`:476`) and counts waiting capabilities as active (`:488`). Both are incorrect for durable waits.
- `UnfinishedCapabilityCount` (`nore/internal/execution/accessors.go:48-60`) is the drain/removal accounting.

**Advancement, persistence, and dispatch ownership**

- The scheduler owns committed transitions and commit-before-publish: `nore/internal/execution/scheduler/scheduler.go:17-40`, `:95-100`, `:335-398` (start), `:400-482` (completion), `:489-516` (drain/complete), `:536-562` (failure), `:287-324` (cancel).
- The engine mutates `MarkCapabilityRunning`/`Completed`/`Failed` on the repository object and publishes events; durable commits happen through the scheduler's `TransitionCommitter` (`nore/internal/execution/engine/executor_engine.go:134-264`, `:278-303`, `:374-411`).
- Dispatch claims are lease-based but not renewed (`executor_engine.go:28-35`), and `dispatchCompleteTimeout` is 5s (`:41`).
- Retry backoff is an in-process sleep (`nore/internal/execution/engine/invocation.go:132-139`, `:232-244`); `StatusRetryWait`, `CapabilityRetryWait`, and `NextAttemptAt` have no writer.
- Durable recovery restores executions and calls `RecoverExecution` (`nore/internal/instance/manager.go:87-183`, `:232-324`); recovery is gated on durability mode (`:42-45`, `:98`, `:124-126`).

**Storage and timers (pre-substrate observations retained for rationale)**

- Pre-`23aca93`, `TimerTx.KV` diverged by backend (SQLite `kv_store`, PostgreSQL `timer_store`), `ClaimTimer` did not reject live leases, `timer_store` had no due-time/index, and `PutTimer`/discovery lacked bounded due-time semantics. These defects motivated D1/D2.
- Pre-`23aca93` baseline also included gofmt drift in `memory.go`/`postgres.go` and storage/execution/instance/cmd test-fake build breakage reported by the coordinator.

**Implementation update — timer substrate (commit `23aca93`, 2026-11-17)**

- **D1 (implemented in code):** `TimerTx.KV` is unified to the main KV surface on every backend, and `TimerTx` remains the internal storage transaction surface over main KV plus the timer surface (`nore/internal/storage/provider.go`; `nore/internal/storage/postgres/postgres.go`, `sqlite/sqlite.go`, `memory.go` in `23aca93`).
- **D2 substrate (implemented in code):** migration **v3** adds `due_at`, `owner`, `lease_expires_at`, and index `idx_timer_store_due_key (due_at, key)` for SQLite and PostgreSQL (`nore/internal/storage/migrate/registry.go` in `23aca93`). `Timer` records, `PutTimer(ctx, key, dueAt, data)` with explicit due time, bounded `DueTimers(now, limit)` ordered by due time then key, and live-lease-aware `ClaimTimer`/claim semantics are implemented across the storage contract and Memory/SQLite/PostgreSQL backends (`provider.go`, `memory.go`, `sqlite/sqlite.go`, `postgres/postgres.go`, and storage contract tests in `23aca93`).
- **Parity/verification (not overclaimed):** PostgreSQL implements the same timer substrate in code, but PostgreSQL contract/parity execution is environment-gated by DSN availability and remains **unverified without a DSN** (ledger T-022 evidence records Postgres contract tests skipped with no DSN).
- **Legacy rows:** timer rows without `due_at` are **inert by decision**. Optional backfill may parse `waitUntil` where such rows exist, but it is **not silently assumed**.
- **Not claimed by `23aca93`:** lease renewal/ownership fencing and migration/concurrency hardening are **follow-up hardening** (tracked as `T-037`/`T-038`), not delivered by this commit. The storage-vs-scheduler boundary is unchanged: the timer store is an indexed lease/index mechanism; the execution snapshot remains authoritative.

**Implementation update — lease fencing and migration hardening (commits `f82ffa6`, `a78a366`, 2026-11-17)**

- **T-037 (`f82ffa6`) — lease renewal/ownership fencing landed.** `RenewTimer` extends only a live lease owned by the caller; `DeleteTimerOwned` deletes only on owner match; `PutTimer` rejects overwriting a live lease with `ErrTimerLeased` (fail-closed). Owner is a required per-claim fencing token. This closes the follow-up left open by `23aca93` (D1/D2 above); reviewer/security approved (ledger T-037).
- **T-038 (`a78a366`) — migration hardening landed; architecture sign-off granted in ADR 0002.** Migration **v3 remains immutable DDL-only** (`due_at`, `owner`, `lease_expires_at`, `idx_timer_store_due_key`). New **v4** backfills parseable legacy `waitUntil`/`wait_until`/`dueAt`/`due_at` values into `due_at` and leaves unparseable rows explicitly quarantined as `due_at IS NULL` (non-schedulable). The quarantine reason is recorded by repository policy (this ADR D2 + ADR 0002) and the `due_at IS NULL` invariant; no per-row reason column was added. v4 is idempotent (`WHERE due_at IS NULL`) and repairs databases already recorded at v3.
- **SQLite startup serialization is a documented, test-guarded DSN contract:** `_txlock=immediate` + `_busy_timeout` + `SetMaxOpenConns(1)` as in production (`nore/internal/storage/sqlite/sqlite.go:52,57`). Deferred/plain DSNs are unsupported for concurrent-startup safety and fail explicitly (bounded busy/exhaustion) rather than silently corrupting. See the ADR 0002 re-review of `a78a366`.
- **The earlier "inert by decision / optional backfill" legacy-row wording above describes the `23aca93` state only.** Current authoritative behavior is v4 backfill/quarantine as recorded in ADR 0002.
- **Postgres migration parity is environment-gated and remains UNVERIFIED without a DSN.** Real DSN-gated migration tests exist (`nore/internal/storage/migrate/postgres_migrate_test.go`) for v2→v4, v3→v4 repair, 6-way concurrent startup/advisory-lock contention, and idempotent rerun, isolated per test by a unique schema pinned via `search_path`; they skip with an explicit reason only when `NEURON_TEST_POSTGRES_DSN` is empty. No DSN was available in the review environment, so none executed. No Postgres migration/runtime parity is claimed.

**API, protocol, and SDKs**

- The only signal route is instance-scoped: `POST /v1/instances/{id}/executions/{execID}/signals/{signalName}` (`shared/types/protocol/endpoints.go:31`), registered at `nore/internal/api/routes/routes.go:56`.
- The handler (`nore/internal/api/handlers/instances/instance_executions.go:257-328`) selects the first capability whose `ExpectedSignal` matches (`:285-295`), dereferences `exec.Blueprint.Nodes` without a nil check (`:288`), ignores payload, `deliveryId`, and `correlationKey`, and mutates via `exec.Signal` (`:301`).
- Execution DTOs: `shared/types/protocol/instance.go:135-152`, `:168`, `:174`; `ExecutionItem` at `shared/types/protocol/execute.go:9-21`.
- The capability protocol is v1 with `ExecuteResponse{result, error, metadata}` and no suspend outcome (`shared/protocol/capabilityruntime/v1/capability_runtime.proto:101-114`). `InitializeResponse.features` exists (`:63-65`).
- Go executor SDK: `packages/executor-sdks/golang/capabilityruntime.go:1-44`. .NET executor SDK: `packages/executor-sdks/dotnet/src/Neuron.Executor/ExecutorHandler.cs:15-36`, `ExecutionResult.cs:11-19`. **No TypeScript executor SDK exists**; this ADR does not authorize one.
- Runtime configuration groups and defaults: `shared/types/core/runtimeconfig.go:28-36`, `nore/internal/runtimeconfig/runtimeconfig.go:31-38`, `application/config/defaults.go:22-26`, `application/config/validate.go:34-58`.

**Known pre-existing breakage (historical snapshot; may be partially addressed by later commits)**

- Coordinator reported four test-fake/tooling failures: `nore/internal/storage/fake_test.go`, `nore/internal/execution/store_failure_test.go`, `nore/internal/instance/metadata_store_test.go`, and `nore/cmd/nore/main_test.go`, plus gofmt drift in `nore/internal/storage/memory.go` and `nore/internal/storage/postgres/postgres.go`.
- Coordinator reported acceptance harness stop non-idempotency in `application/internal/acceptance/harness_test.go` and vacuous/hanging `R4-24` tests.

### Forces

- Execution semantics must not depend on persistence except when durability mode explicitly promises resumption.
- External executors are untrusted and isolated; the suspend extension must be additive and negotiated, never silently reinterpreted.
- Horizontal scaling forbids per-process timer assumptions.
- There must be one authoritative representation of a wait. The execution snapshot is the authoritative state; the timer store is an index/lease mechanism, not a second domain model.
- Every durable side effect must be committed before a client or event consumer can observe it.

## Decision

### D1 — Timer transaction atomicity (adopted; substrate implemented in `23aca93`)

`TimerTx` is one transaction over **the main KV surface plus the timer surface**. `TimerTx.KV` addresses the same main KV table on every backend (the pre-`23aca93` SQLite `kv_store` / PostgreSQL `timer_store` divergence is fixed). `TimerTx` continues to expose the timer-specific operations.

Consequence: one `WithTimer` transaction can atomically write an execution snapshot, a signal delivery record, and a timer record, delete the timer, and commit dispatch intents. No wait resolution may require two transactions.

`TimerTx` remains internal to the storage boundary. It must not be passed into scheduler/engine code directly; the transition committer owns the transaction.

Lease renewal/ownership fencing and migration/concurrency hardening were **follow-up hardening** (`T-037`/`T-038`) not claimed by `23aca93`; both have since landed (`f82ffa6`/`a78a366`) — see the implementation update above.

### D2 — Due-timer substrate (adopted; substrate implemented in `23aca93`)

Migration **v3** adds `due_at`, `owner`, `lease_expires_at`, and index `idx_timer_store_due_key (due_at, key)` to `timer_store` for SQLite and PostgreSQL (`nore/internal/storage/migrate/registry.go`, commit `23aca93`) and is **immutable DDL-only**; migration **v4** (`a78a366`) performs legacy backfill/quarantine (see ADR 0002). The bounded storage primitive and record are implemented in the storage contract (`nore/internal/storage/provider.go`, `23aca93`):

```go
type Timer struct {
    Key            string
    DueAt          time.Time
    Owner          string
    LeaseExpiresAt time.Time
    Data           []byte
}

// DueTimers returns at most limit timers whose due_at <= now and whose
// lease is unowned or expired, ordered by due_at, then key.
DueTimers(ctx context.Context, now time.Time, limit int) ([]Timer, error)
```

- `PutTimer` is `PutTimer(ctx, key string, dueAt time.Time, data []byte) error` on both `Store` and `TimerTx`, persisting an explicit due time (no production consumers at the time of change).
- `ClaimTimer` claims only when the timer is due/unowned or its lease is expired; a live lease returns `(false, nil)`. It uses compare-and-set/atomic claim semantics on the timer record.
- `DueTimers` must never return a timer with a live lease.
- The poller interval defaults to 1s and is configurable. Batch size defaults to 256 and is configurable. (Scheduler/poller consumption remains governed by later Phase 5 execution work; this ADR preserves the storage-vs-scheduler boundary.)
- No full-table scan is permitted for due discovery. Prefix listing remains for cleanup/listing only.
- **Legacy rows:** pre-existing timer rows without `due_at` were **inert by decision** in `23aca93`; current behavior is v4 backfill of parseable `waitUntil`/`wait_until`/`dueAt`/`due_at` and explicit quarantine (`due_at IS NULL`) of unparseable rows, per ADR 0002 and the implementation update above.
- `MemoryStore` implements the same substrate semantics (implemented in `23aca93`).

**Parity/verification:** SQLite and PostgreSQL implement the substrate in code with migration v3 and the due-key index; Memory implements parity semantics. PostgreSQL contract/parity execution is environment-gated by DSN availability and remains **unverified without a DSN** (see T-022 evidence), including the v2→v4/v3→v4 migration and concurrent-startup tests added in `a78a366` (none executed without a DSN). **Not claimed by `23aca93`:** lease renewal/ownership fencing and migration/concurrency hardening were **follow-up hardening** (`T-037`/`T-038`); both subsequently landed (`f82ffa6`/`a78a366`).

### D3 — Capability suspend protocol (adopted)

Additive, feature-negotiated v1 extension. Field numbers are frozen once merged.

```proto
message ExecuteResponse {
  map<string, Value> result = 1;
  string error = 2;
  map<string, string> metadata = 3;
  SuspendOutcome suspend = 4;   // additive
}

message SuspendOutcome {
  string correlation_key = 1;
  string wait_type = 2;         // "signal" | "timer"
  string expected_signal = 3;
  int64 deadline_unix_ms = 4;   // 0 = no wait deadline
  string payload_schema = 5;
}

message ExecuteRequest {
  // existing fields 1..6 unchanged
  ResumeInput resume = 7;       // additive
}

message ResumeInput {
  string cause = 1;             // "signal" | "timer" | "deadline"
  string signal_name = 2;
  string delivery_id = 3;
  map<string, Value> payload = 4;
}
```

- `InitializeResponse.features` must contain `"suspend"` for N.O.R.E. to accept a suspend outcome. If a runtime returns `suspend` without negotiating the feature, the capability fails with an explicit `unsupported_suspend_outcome` error. It is never treated as an empty successful result.
- Suspend is **not** a stackful continuation. The invocation ends. Resume is a fresh `Execute` with the same `logical_operation_id`, a new `attempt_id`, and `resume` populated. Capabilities must be idempotent and reconstruct state from params and resume input.
- Signal suspend requires `expected_signal`; `correlation_key` is required when more than one concurrent wait on the same signal name can exist within one execution. If omitted, the engine derives it from the capability ID.
- Timer suspend requires `wait_type="timer"` and a positive `deadline_unix_ms`.
- Old N.O.R.E. clients that do not understand field 4 ignore it; only feature-negotiated runtimes may emit it.
- Update the Go and .NET SDKs. Add conformance tests for: old client/no feature, negotiated suspend, unnegotiated suspend rejection, signal resume, timer resume, and duplicate resume delivery.

### D4 — Signal API semantics (adopted)

Primary route: `POST /v1/executions/{id}/signals/{name}`. The existing instance-scoped route remains as a compatibility surface and delegates to the same operation after verifying that the execution belongs to the instance.

Body:

```json
{ "deliveryId": "string", "payload": { }, "correlationKey": "optional" }
```

- `deliveryId` is required. Missing or invalid → 400.
- `payload` is optional (default empty object). `correlationKey` is optional.
- Duplicate `deliveryId` with the same payload → 200 idempotent replay; no state change; the original `capabilityId`/status is returned.
- Duplicate `deliveryId` with a different payload → 409 `signal_delivery_conflict`.
- Several matching waiting capabilities and no `correlationKey` → 409 `ambiguous_wait`.
- No matching waiting capability on a live execution → 404 `no_matching_wait`.
- Unknown execution → 404.
- Execution belongs to a different instance than the addressed instance → 404 (no existence leak). Authorization is the existing API token plus execution/instance binding.
- Execution already terminal, or the wait deadline passed → 410 `signal_expired`; if the execution was cancelled, the same 410 with a cancellation reason. A successful delivery replayed after the execution became terminal still returns the original 200.
- No signal buffering. A 404 means the sender must retry. `deliveryId` makes retries safe.
- The handler must never mutate execution state directly. It validates and calls the instance/scheduler boundary.
- Delivery acceptance and the wait resolution are committed in one `WithTimer` transaction before the 200 is returned. The `CapabilityReady` event is published after commit. If publish fails, the durable dispatch intent is the source of truth and the dispatch reconciler republishes it.

Delivery records are stored at `signals/<executionID>/<deliveryID>` and contain the payload hash, signal name, correlation key, capability ID, and result status. Unmatched attempts are not recorded, so a retry after the capability starts waiting can succeed.

### D5 — Wait entry, resolution, and accounting (adopted)

- Capability-requested suspend and authored/runtime waits use the same scheduler-owned committed transition. There is one entry point, one resolution path, and one event family.
- A wait is entered when a capability is `Running` (runtime suspend) or is declared to wait before dispatch; the transition is `Waiting` with `WaitType`, `ExpectedSignal`, `CorrelationKey`, `WaitUntil`, `WaitReason`, `PayloadSchema`, and a cleared `DispatchState`.
- `WaitUntil == nil` means "no timer deadline" and must **never** be treated as due. Signal waits without a deadline wait indefinitely until a signal, cancellation, or the execution max lifetime.
- `WaitUntil != nil` produces a timer record. Timer and deadline waits resume when `WaitUntil <= now` and the execution is not terminal.
- Waiting capabilities are excluded from `inFlight` and worker accounting. They remain counted by `UnfinishedCapabilityCount`.
- Execution-level `StatusWaiting` is derived: set when no capability is runnable (no `Ready`/`Running`/pending-with-satisfied-input work) and at least one capability is `Waiting` or `RetryWait`. A capability waiting while independent work runs remains `CapabilityWaiting`, and the execution remains `Running`.
- Resolution transitions are `Waiting → Ready` (signal/timer/deadline continue), `Waiting → Failed` (deadline policy `fail`, or retry exhausted), or `Waiting → Cancelled` (deadline policy `cancel`, operator cancel). Each resolution commits execution state, deletes the timer, persists the signal delivery record when applicable, and creates the next dispatch intent in one `WithTimer` transaction.
- A resumed dispatch advances `attempt_id` but preserves `logical_operation_id`. The previous dispatch intent is completed or removed before the new one is created.
- `RecoverableDispatch` must be corrected: `Waiting` is resumable only for `WaitType` `timer`/`deadline` with a non-nil elapsed `WaitUntil`; `signal` waits with nil or future `WaitUntil` are not dispatched. Waiting/retry-wait states must not increment `inFlight`.
- Add internal events `CapabilityWaiting`, `ExecutionWaiting`, and `CapabilityResumed` (or reuse `CapabilityReady` with a cause) with payloads sufficient for observability. Event consumers may never be required for correctness.
- A dispatch reconciler periodically claims ready dispatch intents with a bounded lease and republishes `CapabilityReady`. This closes the commit-then-publish crash window and is required for horizontal scaling.

### D6 — Cancellation, deadlines, lifetime, and durable retry (adopted)

- Cancellation of a wait is terminal by default. The execution becomes `Cancelled`; wait metadata (`WaitUntil`, `WaitType`, `ExpectedSignal`, `CorrelationKey`, `WaitReason`, `PayloadSchema`) is retained for diagnostics. The execution cannot be resumed.
- On cancellation, all timers for the execution are deleted in the same transaction as the terminal state. Buffered join inputs are discarded.
- Wait deadline policy is `fail` by default; `cancel` and `continue` are configurable.
  - `fail`: the waiting capability fails with `wait_timeout`; the execution fails (unless a future retry policy explicitly resumes it).
  - `cancel`: the execution is cancelled.
  - `continue`: the capability becomes `Ready` and resumes with `resume.cause="deadline"`.
- Execution max lifetime defaults to 30 days (`720h`) and is configurable; `0` disables it. It is persisted as an execution deadline in the snapshot and is distinct from per-capability `execution.timeout` and from per-wait `WaitUntil`.
- Durable retry: a retryable failure in durable mode does not sleep in `invocation.run`. The engine enters `CapabilityRetryWait` with `NextAttemptAt`, persists it, schedules a retry timer, decrements `inFlight`, and publishes a retry notice. The poller wakes it. Non-durable modes keep the existing in-process sleep.
- Retry-wait resolution uses the same timer/claim/resolution machinery as waits.
- Snapshot schema becomes `"3"`: `ExecutionDeadline *time.Time` is added at the top level. `"2"` and `"1"` remain readable; missing deadline means nil. Unknown newer versions remain rejected.

### D7 — Execution-scoped authorization (adopted)

- Add an execution-scoped signal route. The handler resolves the execution, reads its persisted `InstanceID`, and requires that the addressed instance (when present in the path) matches. A mismatch is 404, not 403, because the API token is not instance-scoped and a 403 would leak existence.
- Cancel is subject to the same binding. Cross-instance signal/cancel by execution ID is impossible.
- Do not trust only the in-memory cache for ownership; ownership comes from the durable execution record (or a live execution whose `InstanceID` is authoritative).
- The existing instance-scoped route validates the execution belongs to the instance and then delegates.
- The handler must not iterate `Blueprint.Nodes` to find waits when the blueprint may be absent (metadata-only restored instances). Wait lookup belongs to the scheduler/service, which loads the live execution and handles nil blueprints explicitly.

### D8 — Module and lease design (adopted)

See “Migration plan” and “Module and lease plan” below. Eight mutating modules, one owner each, disjoint file sets, explicit sequential handoffs only where an interface must change.

### D9 — Phase 5 closure mapping (adopted)

See “Phase 5 item closure map” below. The map is authoritative for lease assignment; the coordinator reconciles item titles against `todos/TODO.md` before dispatch.

### D10 — Verification (adopted)

See “Verification” below. Every module has commands; final validation is `scripts/script.sh` plus the durable-restart acceptance test. Postgres DSN and .NET toolchain availability are recorded, not assumed.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Keep `TimerTx.KV` backend-specific and perform two transactions for wait resolution | Cannot guarantee atomic execution-state + timer + signal acceptance; a crash can strand a capability or lose a delivery. |
| Store timers only in the execution snapshot and scan executions | Unbounded O(all executions) polling; no lease/claim for multi-process; couples the poller to the execution store. |
| Store timers only in `timer_store` and make the snapshot non-authoritative | Creates two competing representations of a wait and a reconciliation problem; violates one authoritative model. |
| Add a `due_at` column but derive it by parsing backend-specific JSON in storage | Couples storage to a domain record schema; duplicates the wait model in the storage layer. |
| Bump the capability protocol to v2 and require new runtimes | Breaks old runtimes unnecessarily; feature negotiation in v1 is additive and safe. |
| Treat suspend as an error sentinel from `CapabilityRuntime.Execute` | Uses errors as control flow; hides a first-class outcome; makes SDKs and middleware error-prone. |
| Treat a suspend response as an empty successful result for old clients | Silently completes work that intended to wait; correctness failure. |
| Buffer signals that arrive before a wait is recorded | Adds an unbounded, expiring second representation; introduces delivery-ordering semantics not in the contract. Clients can retry with `deliveryId`. |
| Match signals by capability state alone | Ambiguous with concurrent waits on the same signal; loses correlation. `correlationKey` resolves ambiguity. |
| Persist wait metadata only in memory and rebuild on restart | Loses waits across restarts and violates durability mode. |
| Keep retry backoff as an in-process sleep in durable mode | A crash loses the retry schedule; blocks a worker for the backoff duration. |
| Set `StatusWaiting` whenever any capability waits | Misreports executions that still have runnable work; breaks drain and operator queries. |
| Treat nil `WaitUntil` as due | Immediately resumes signal waits that have no deadline; the current recovery bug (`execution.go:476`). |
| Clear wait metadata on cancellation | Loses diagnostics; makes late-delivery conflicts indistinguishable from unknown waits. |
| Use a single global timer poller goroutine with no lease | Doubles work in multi-process deployments and has no crash recovery boundary. |
| Add a TypeScript executor SDK now | Out of scope; no existing TS executor SDK and no consumer. Documented as a non-goal. |
| Reorganize storage/scheduler packages as part of Phase 5 | Unnecessary boundary churn; the existing package layout already reflects the responsibilities. |

## Consequences

### Positive

- One authoritative wait model in the execution snapshot, with `timer_store` reduced to an indexed lease/index mechanism.
- Atomic transition boundaries: execution state, timer, signal delivery, and dispatch intent commit together.
- Bounded, horizontally scalable timer polling with lease claims.
- Additive capability protocol; old runtimes and old N.O.R.E. clients remain valid.
- Signal delivery is idempotent, auditable, and authorization-bound.
- Durable retry no longer consumes a worker during backoff.
- Clear ownership: HTTP, scheduler, storage, and protocol each keep their boundary.

### Negative

- `storage.Store`/`TimerTx` is a larger internal contract; all fakes and backends must be updated together.
- `WithTimer` serializes writes under the store gate. Polling and wait transitions share that gate; contention must be measured.
- Snapshot schema moves to `"3"`; readers must handle `"1"`, `"2"`, and `"3"`.
- `CapabilityRuntime.Execute` changes to return an outcome type; every in-repo runtime and test double must be updated.
- Suspend semantics impose idempotency requirements on capability authors.
- Signal replay requires retaining delivery records; retention policy is required to bound growth.

### Neutral

- Internal events grow but remain observability/diagnostics, not public execution semantics.
- The dispatch reconciler adds a background component, bounded by configuration.
- The existing instance-scoped signal route remains for compatibility.

### New obligations

- Document suspend semantics and idempotency in SDK docs and capability authoring docs.
- Add retention/cleanup for `signals/*` and terminal timer rows.
- Add metrics: due timers, claimed timers, wait entries/resolutions, signal replays/conflicts, reconciler lag.
- Security review for signal authorization, delivery retention, and timer claim abuse.
- Every mutating module must update its tests and the shared contract tests before handoff.

## Boundary and dependency impact

### Edges added

| Edge | Direction | Justification |
|---|---|---|
| HTTP handler → Instance signal operation | API → instance | Handlers must not reach into execution internals; the instance owns its scheduler. |
| Instance → Scheduler `EnterWait`/`ResolveWait`/`DeliverSignal` | composition → execution policy | Scheduler owns transitions and scopes. |
| Scheduler → `TransitionCommitter` wait methods | policy → persistence contract | Commit-before-publish boundary. |
| `TransitionCommitter` → `storage.Store.WithTimer`/`DueTimers` | contract → storage mechanism | Atomic wait transition. |
| Engine → `contracts.CapabilityRuntime` outcome | execution → capability contract | Suspend is a first-class outcome. |
| Process adapter → generated proto | transport → protocol | Wire mapping stays at the edge. |
| Timer poller → `DueTimers`/`ClaimTimer` | execution/instance → storage | Bounded, leased timer source. |

### Edges forbidden

- `storage` must not import `execution`, `scheduler`, `api`, or `instance`.
- `execution`/`scheduler` must not import `api` or HTTP types.
- `compiler` must not import storage timers or signal code.
- The capability protocol must not leak Go types or runtime-specific behavior.
- Handlers must not write execution snapshots or timer records directly.

### Public surface changes

| Surface | Change | Compatibility |
|---|---|---|
| `storage.Store` / `TimerTx` (internal) | `PutTimer` gains `dueAt`; `DueTimers` added; `ClaimTimer` lease semantics fixed; `TimerTx.KV` unified | Internal; update all implementations/fakes in the same module. |
| `contracts.CapabilityRuntime` | `Execute` returns `(Outcome, error)` with optional `Suspend` | Internal; all in-repo runtimes updated together. |
| `contracts.TransitionCommitter` | `SaveWaitEntry`, `SaveWaitResolution` (or one explicit wait-transition method) | Internal; fakes updated with the contract. |
| Capability proto v1 | Additive `ExecuteResponse.suspend`, `ExecuteRequest.resume`; `"suspend"` feature | Wire-compatible; feature-gated. |
| HTTP API | Execution-scoped signal route; signal body/status semantics; wait-list filter | Additive route; existing route retained. |
| Go/.NET SDKs | Suspend/resume types and handler surface | Additive where possible; documented. |
| Runtime config | Wait deadline policy, max lifetime, timer poll interval/batch | Defaults preserve current behavior except where durability is enabled. |

## Migration plan

### Module and lease plan

Exactly one mutating owner per module. Paths are exact scopes; no two active modules share a file. Sequential ownership handoffs are explicitly named and occur only after the previous owner releases its lease.

| Module | Owner (role) | Exact path scope | Depends on | Exit criteria |
|---|---|---|---|---|
| **M0 — Foundational repair** | `coder-repair` | `nore/internal/storage/fake_test.go`, `nore/internal/execution/store_failure_test.go`, `nore/internal/instance/metadata_store_test.go`, `nore/cmd/nore/main_test.go`; gofmt-only `nore/internal/storage/memory.go`, `nore/internal/storage/postgres/postgres.go` | none | Tree compiles; `gofmt -l` clean for the two named files; all four packages compile. No production behavior change. |
| **M0b — Acceptance truthfulness** | `coder-acceptance` | `application/internal/acceptance/harness_test.go`; the `R4-24` test files under `application/internal/acceptance/**` (coordinator confirms exact names) | M0 | Harness stop is idempotent; `R4-24` tests assert real behavior and terminate under a timeout. |
| **M1 — Storage/timer substrate** | `coder-storage` | `nore/internal/storage/provider.go`, `nore/internal/storage/memory.go`, `nore/internal/storage/sqlite/**`, `nore/internal/storage/postgres/**`, `nore/internal/storage/migrate/**`, storage tests; **handoff after M0**: the three storage-facing test fakes named in M0 for the interface change | M0b | Interface frozen; due-time migration; SQLite/Postgres/Memory parity; `TimerTx.KV` unified; claim respects live leases; no full scan. |
| **M2 — Execution wait/transition/engine/recovery** | `coder-execution` | `nore/internal/execution/execution.go`, `snapshot.go`, `accessors.go`, `wait.go`, `store.go`, `dispatch_intent*.go`, `recovery*.go`, `nore/internal/execution/scheduler/**`, `nore/internal/execution/engine/**`, `nore/internal/contracts/**`, `nore/internal/instance/**` (except `metadata_store_test.go`), `nore/internal/event/**`, `nore/internal/builtins/**`, `nore/internal/mechanism/**` until F3 | M1 | Wait entry/resolution, derived `StatusWaiting`, nil-`WaitUntil` fix, `inFlight` exclusion, cancellation semantics, durable retry, deadlines/lifetime, dispatch reconciler, recovery. |
| **M3 — Signal API, authorization, query** | `coder-api` | `nore/internal/api/handlers/instances/**`, `nore/internal/api/routes/**`, `shared/types/protocol/**`, API tests | M2 | Execution-scoped route, body/status contract, idempotency, authorization binding, wait-list filter, no direct mutation. |
| **M4 — Capability protocol, transport, SDKs, conformance** | `coder-runtime` | `shared/protocol/capabilityruntime/**`, generated Go/C# protocol files, `packages/executor-sdks/golang/**`, `packages/executor-sdks/dotnet/**`, `nore/internal/mechanism/conformance_test.go`; **handoff after F3**: `nore/internal/mechanism/process/**` | M2 | Additive suspend/resume, feature negotiation, Go/.NET SDK updates, conformance, old-client behavior. |
| **M5 — Policy, configuration, CLI** | `coder-config` | `shared/types/core/runtimeconfig.go`, `nore/internal/runtimeconfig/**`, `application/config/**`, `application/cmd/**`, `nore/cmd/**` (except `nore/cmd/nore/main_test.go`), `application/internal/**` except `application/internal/acceptance/**` | M3, M4 | Deadline policy, max lifetime, poll interval/batch defaults; CLI signal/wait/query; validation and denial rules. |
| **M6 — Documentation** | `documenter` | `docs/**`, `README.md`, `application/README.md` | M5 | Suspend/resume semantics, idempotency, signal contract, timer/deadline policy, config, SDK docs; no TypeScript executor SDK note. |
| **Gate — Final validation** | coordinator (read-only) | none | M6 | `scripts/script.sh` run; evidence recorded; unresolved limitations named. Not a mutating module. |

**High-contention files with exactly one owner**

| File | Owner | Notes |
|---|---|---|
| `nore/internal/storage/provider.go` | M1 | Interface freeze; no other module edits. |
| `nore/internal/storage/memory.go` | M1 | M0 gofmt-only before M1; no concurrent writes. |
| `nore/internal/storage/sqlite/**`, `postgres/**`, `migrate/**` | M1 | Includes due-time migration and parity. |
| `nore/internal/execution/execution.go`, `snapshot.go`, `accessors.go`, `wait.go`, `store.go`, `dispatch_intent*.go`, `recovery*.go` | M2 | Wait model, snapshot v3, dispatch. |
| `nore/internal/execution/scheduler/**` | M2 | Transition ownership. |
| `nore/internal/execution/engine/**` | M2 | Suspend consumption, retry-wait, reconciler. |
| `nore/internal/contracts/**` | M2 | Freeze outcome/committer interfaces. |
| `nore/internal/instance/**` (except `metadata_store_test.go`) | M2 | Signal forwarder, recovery, execution lookup. |
| `nore/internal/event/**` | M2 | New internal event payloads. |
| `nore/internal/api/handlers/instances/**`, `nore/internal/api/routes/**` | M3 | HTTP only. |
| `shared/types/protocol/**` | M3 | DTOs, routes, filters. |
| `shared/protocol/capabilityruntime/**`, generated files | M4 | Freeze field numbers/features. |
| `packages/executor-sdks/**` | M4 | Go/.NET code and tests. |
| `nore/internal/mechanism/process/**` | M2 → M4 | Handoff at F3; no overlap. |
| `shared/types/core/runtimeconfig.go`, `nore/internal/runtimeconfig/**`, `application/config/**`, `application/cmd/**`, `nore/cmd/**` (except `main_test.go`) | M5 | Config/CLI. |
| `docs/**`, `README.md`, `application/README.md` | M6 | Markdown only. |
| `application/internal/acceptance/**` | M0b | Harness and R4-24. |
| `nore/internal/storage/fake_test.go`, `nore/internal/execution/store_failure_test.go`, `nore/internal/instance/metadata_store_test.go`, `nore/cmd/nore/main_test.go` | M0 → M1 | M0 repairs to current interface; M1 claims the three storage-facing fakes only after M0 releases, for the interface change. |

**Freeze points**

| Freeze | After | Frozen contract |
|---|---|---|
| F0 | M0 | Tree compiles; fakes compile against the current interface. |
| F1 | M0b | Acceptance harness is truthful and stop-idempotent. |
| F2 | M1 | `storage.Store`/`TimerTx` method set, `PutTimer(dueAt)`, `DueTimers`, claim/lease semantics, migration v3. |
| F3 | M2 | `contracts.CapabilityRuntime` outcome, `contracts.TransitionCommitter` wait methods, scheduler wait API, instance signal forwarder, snapshot v3. |
| F4 | M3 | HTTP DTOs, routes, status codes, filters. |
| F5 | M4 | Proto field numbers, `"suspend"` feature string, SDK types. |
| F6 | M5 | Config keys, defaults, validation/denial rules, CLI surface. |

**Concurrency**

```
M0  →  M0b  →  M1  →  M2  →  M3 ∥ M4  →  M5  →  M6  →  Gate
```

- `M3 ∥ M4` is the only parallel mutating pair. Their file sets are disjoint: M3 owns HTTP/shared protocol DTOs; M4 owns capability proto/SDKs/transport. They may both depend on M2 but must not edit M2 files.
- `M1` and `M4` must not overlap: M4's process adapter consumes M2's frozen outcome type and generated proto; starting M4 before M2 would force an interface rewrite.
- `M5` is sequential after both M3 and M4 because CLI commands consume frozen routes and protocol config.
- `M6` is sequential after M5 because docs must describe verified behavior.
- Read-only review/security may run in parallel with any module, but must review the actual diff at the freeze point.

### Ordered steps (each keeps the system working)

1. **M0**: repair the four test fakes and gofmt drift. No semantics change. Commit: `fix(test): repair storage fakes and gofmt drift`.
2. **M0b**: make acceptance stop idempotent and repair vacuous/hanging `R4-24` tests. Commit: `test(acceptance): make stop idempotent and assert R4-24`.
3. **M1**: storage contract + migration + backend parity. Add `PutTimer(dueAt)`, `DueTimers`, fix `ClaimTimer`, unify `TimerTx.KV`, implement Memory parity. Commit: `feat(storage): add due-time timer substrate`.
4. **M2**: execution wait model, scheduler transitions, engine suspend/retry/deadline, snapshot v3, recovery fixes, dispatch reconciler, instance forwarder. Commit: `feat(execution): add durable waits and wait transitions`.
5. **M3**: execution-scoped signal route, DTOs, idempotency, authorization, wait query. Commit: `feat(api): add execution-scoped durable signals`.
6. **M4**: protocol suspend/resume, Go/.NET SDKs, transport mapping, conformance. Commit: `feat(protocol): add negotiated suspend and resume`.
7. **M5**: config policy, max lifetime, poll interval/batch, CLI signal/wait/query. Commit: `feat(config): expose wait deadline and timer policy`.
8. **M6**: docs. Commit: `docs: document durable waits, signals, and timers`.
9. **Gate**: run `scripts/script.sh` and the durable-restart acceptance test; record evidence.

## Phase 5 item closure map

`todos/TODO.md` remains the product source for item titles. This table fixes the closure owner so the coordinator can dispatch without overlapping leases. Before dispatch, the coordinator must confirm that each item's title falls in the assigned area; a mismatch reassigns the item to that area's module, never splits a file lease.

| Item | Closure area | Primary module | Closure evidence required |
|---|---|---|---|
| R5-1 | Storage/timer contract | M1 | Interface + parity tests |
| R5-2 | Timer table due-time migration | M1 | SQLite/Postgres migration tests |
| R5-3 | SQLite timer semantics | M1 | SQLite timer/claim/parity tests |
| R5-4 | PostgreSQL timer semantics | M1 | Postgres timer/claim/parity tests |
| R5-5 | Memory timer semantics | M1 | Memory parity + race tests |
| R5-6 | Timer contract tests | M1 | Shared contract suite |
| R5-7 | Execution wait state | M2 | Wait state tests |
| R5-8 | Wait entry transition | M2 | Scheduler transition tests |
| R5-9 | Wait resolution transition | M2 | Signal/timer resolution tests |
| R5-10 | Derived execution waiting status | M2 | Status/inFlight tests |
| R5-11 | Recovery of waiting executions | M2 | Restart/recovery tests |
| R5-12 | Durable retry wait | M2 | Retry-wait + restart tests |
| R5-13 | Wait deadlines | M2 | Deadline policy tests |
| R5-14 | Execution max lifetime | M2 | Lifetime expiry tests |
| R5-15 | Signal API and idempotency | M3 | Handler contract tests |
| R5-16 | Signal authorization/ownership | M3 | Cross-instance tests |
| R5-17 | Execution-scoped signal route | M3 | Route + client tests |
| R5-18 | Waiting-execution query | M3 | List/filter tests |
| R5-19 | Capability suspend outcome | M4 | Proto + transport tests |
| R5-20 | Resume input | M4 | Transport + SDK tests |
| R5-21 | Go SDK suspend/resume | M4 | Go conformance |
| R5-22 | .NET SDK suspend/resume | M4 | .NET conformance |
| R5-23 | Wait deadline policy configuration | M5 | Config validation tests |
| R5-24 | Timer poll interval/batch configuration | M5 | Config/daemon tests |
| R5-25 | CLI signal/wait/query | M5 | CLI tests |
| R5-26 | Documentation | M6 | Docs review |
| R5-27 | End-to-end durable signal/timer acceptance | Gate + M0b/M2 | SIGKILL restart evidence |
| R5-28 | Final validation | Gate | `scripts/script.sh` + recorded limitations |

**Re-opened items:** `R5-1..R5-8`, `R5-10`, and `R5-11` were previously marked done without sufficient evidence. They may not be marked done by a single module unless the table's evidence for the corresponding area exists. In particular, a storage-only change cannot close an execution-wait item and a scheduler-only change cannot close a timer-substrate item. The coordinator must re-verify each acceptance criterion at the gate.

**Pre-existing blockers**

| Item | Primary module | Notes |
|---|---|---|
| `R4-14` step 5 / `F4-11` | M0b + M2 | Harness restart/stop truthfulness plus durable recovery behavior. |
| `R4-6` | M1 | Storage durability/transaction blocker; closure requires parity and migration evidence. |
| `R4-8` | M2 | Execution recovery/wait blocker; closure requires restart evidence. |

## Risks

| Risk | Likelihood | Impact | Mitigation | Owner |
|---|---|---|---|---|
| Suspend is mistaken for a stackful continuation | High | High | Protocol docs, SDK docs, conformance tests, explicit resume semantics; capabilities must be idempotent. | M4 + M6 |
| At-least-once signal delivery causes duplicate side effects | Medium | High | `deliveryId` idempotency, `logical_operation_id` stable across resume, conformance tests. | M3 + M4 |
| Duplicate timer firing under multiple processes | Medium | High | Due-time index, live-lease-aware `ClaimTimer`, bounded `DueTimers`, idempotent resolution. | M1 + M2 |
| `WithTimer` gate contention under polling | Medium | Medium | Small batches, short transactions, configurable interval, measure before tuning. | M1 + M2 |
| Backend parity drift (SQLite/Postgres/Memory) | Medium | High | Shared contract tests run against all three; explicit `TimerTx.KV` parity test. | M1 |
| Nil `WaitUntil` resumes signal waits | High (current bug) | High | Explicit test; recovery only resumes non-nil elapsed timer/deadline waits. | M2 |
| Cancellation resumes or loses diagnostic state | Medium | High | Terminal cancellation, retained wait metadata, timer deletion in the same transaction. | M2 |
| Interface churn breaks fakes repeatedly | High | Medium | M0 repair first; M1/M2 freeze interfaces; sequential handoffs; no parallel edits. | Coordinator |
| Commit succeeds but event publish fails | Medium | Medium | Dispatch reconciler republishes ready intents; signal acceptance is durable before 200. | M2 + M3 |
| Signal retention grows without bound | Medium | Medium | Retention policy for `signals/*`; cleanup on terminal execution/removal. | M3 + M5 |
| Cross-instance signal/cancel leak | Low | High | Durable ownership check; 404 on mismatch; security sign-off. | M3 + security |
| Postgres test environment unavailable | Medium | Medium | Record the check as not run; do not claim parity verified. | Gate |
| .NET toolchain unavailable | Medium | Medium | Record the check as not run; do not claim .NET conformance verified. | Gate |
| Snapshot v3 reader bug | Low | High | Explicit v1/v2/v3 decode tests, nil deadline handling. | M2 |

## Verification

Commands are run from the named module directory unless stated otherwise. Do not report a check as passed if it skipped or could not run.

### M0

```bash
gofmt -l nore/internal/storage/memory.go nore/internal/storage/postgres/postgres.go   # must print nothing
cd nore && go build ./...
cd nore && go test ./internal/storage ./internal/execution ./internal/instance ./cmd/nore
```

Acceptance: the four packages compile; no production behavior change; `gofmt -l` is empty for the two files.

### M0b

```bash
cd application && go test ./internal/acceptance/... -count=1 -timeout 10m
cd application && go test ./internal/acceptance/... -run 'R4-24' -count=1 -timeout 5m
```

Acceptance: stop is idempotent; `R4-24` tests assert real behavior and terminate; no hanging test.

### M1

```bash
cd nore && go test ./internal/storage/... -count=1
cd nore && go test -race ./internal/storage/... -count=1
cd nore && go test ./internal/storage/sqlite/... -run 'Timer|Migration' -count=1
cd nore && go test ./internal/storage/postgres/... -run 'Timer|Migration|Parity' -count=1   # requires the repository's Postgres test DSN
cd nore && go test ./internal/storage/migrate/... -count=1
```

Acceptance: `TimerTx.KV` parity test passes on SQLite and Postgres; `DueTimers` never returns a live-lease timer; `ClaimTimer` rejects a live lease; migration v3 applies cleanly; Memory parity passes under `-race`.

If the Postgres DSN is unset, record: "Postgres timer/parity: not run (DSN unavailable)" and do not mark the module fully verified.

### M2

```bash
cd nore && go test ./internal/execution/... ./internal/instance/... -count=1
cd nore && go test -race ./internal/execution/... ./internal/instance/... -count=1
cd nore && go test ./internal/execution/... -run 'Wait|Signal|Cancel|Recover|Retry|Timer|Dispatch' -count=1
```

Acceptance: nil `WaitUntil` never resumes; waiting excluded from `inFlight`; `StatusWaiting` derived correctly; cancellation retains wait metadata and deletes timers; durable retry survives restart; dispatch reconciler republishes; snapshot v1/v2/v3 decode tests pass.

### M3

```bash
cd nore && go test ./internal/api/... -count=1
cd nore && go test ./internal/api/... -run 'Signal|Execution|Authorization' -count=1
cd nore && go test ./internal/api/handlers/instances/... -count=1
```

Acceptance: 400/404/409/410 cases; duplicate same payload → 200 replay; duplicate different payload → 409; ambiguous wait → 409; cross-instance → 404; terminal/late → 410; no direct execution mutation.

### M4

```bash
cd nore && go test ./internal/mechanism/... -count=1
cd packages/executor-sdks/golang && go test ./... -count=1
cd packages/executor-sdks/golang && go test -race ./... -count=1
cd packages/executor-sdks/dotnet && dotnet test
```

Acceptance: negotiated suspend accepted; unnegotiated suspend rejected; resume signal/timer round-trips; old client ignores suspend; Go and .NET conformance pass. If `dotnet` is unavailable, record: ".NET conformance: not run (toolchain unavailable)".

Proto generation: use the repository's existing generation command. If no generation command exists, record the limitation and do not hand-edit generated files.

### M5

```bash
cd application && go test ./config/... ./cmd/... -count=1
cd nore && go test ./internal/runtimeconfig/... -count=1
cd application && go test ./... -count=1
```

Acceptance: defaults preserve current behavior; wait deadline policy validates; max lifetime `0` disables; poll interval/batch bounds enforced; denied keys include operational timer settings; CLI signal/wait/query works against the frozen API.

### M6

```bash
git diff --check
```

Acceptance: docs describe suspend/resume, idempotency, signal contract, deadline/lifetime policy, config, and SDK changes; the absence of a TypeScript executor SDK is explicit; no code claims that were not verified.

### Gate

```bash
scripts/script.sh
```

Plus the durable-restart acceptance test added by M2/M0b:

```bash
cd application && go test ./internal/acceptance/... -run 'Durable' -count=1 -timeout 15m
```

Gate acceptance:

- `scripts/script.sh` completed successfully, or the exact failing/skipped check and reason are recorded.
- SIGKILL restart evidence: an instance with a suspended capability is killed, restarted, receives a signal, resumes, and completes; no duplicate dispatch and no lost delivery.
- SQLite and Postgres results are reported separately. Postgres parity is not claimed if the DSN was unavailable.
- .NET conformance is not claimed if the toolchain was unavailable.
- Every `R5` item's evidence exists; `R5-1..R5-8`, `R5-10`, `R5-11` are re-verified independently.
- No module is marked done on a delegate's claim alone.

## Open questions

None blocking this ADR. The following are coordinator confirmations, not design decisions:

1. Confirm the exact titles of `R5-1..R5-28` and `R4-6`/`R4-8` before dispatch so the closure map can be reconciled without changing module boundaries.
2. Confirm the exact file names of the `R4-24` tests under `application/internal/acceptance/**`.
3. Confirm `nore/cmd/nore/main_test.go` is the fourth broken test file and record its failure mode in the M0 report.
4. Confirm the Postgres test DSN convention and the repository's proto generation command; if either is unavailable, the gate records a verification limitation.
