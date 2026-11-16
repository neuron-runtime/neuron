# Phase 5 Implementation Plan — Signals, Timers, and Long-Running Execution

Status: approved by coordinator after three read-only research reports and the Phase 5 ADR.
Branch: `feature/durable-signals-timers` (created from `feature/durable-execution` @ `6043255`).
Design authority: `.agents/decisions/0001-phase5-signals-timers.md`.
Backlog authority: `todos/TODO.md` Phase 5, plus the blocking Phase 3/4 items named below.

## Objective

Make durable execution genuinely suspend and resume:

- a durable execution can wait for an external signal, a timer, or a deadline across a runtime restart;
- a wait occupies no worker;
- duplicates are idempotent;
- signals are authenticated and bound to the intended execution and wait;
- every previously checked-but-inert Phase 5 item is either made real or corrected in the backlog.

## Scope

In scope: all Phase 5 items `R5-1..R5-28`, plus only the Phase 3/4 gaps that Phase 5 depends on:

- `F4-11` / `R4-14` step 5 — restore retry timers and suspended waits;
- `R4-6` and `R4-8` behavior that is marked complete but absent (attempt/deadline/dispatch-state fields, worker release during retry/wait);
- `R3-24` join-state persistence verification;
- the pre-existing failures found by the baseline run.

Out of scope: unrelated Phase 2 migrations/conformance, unrelated Phase 3 fan-in items, Phase 6 DX, and Phase 7 built-in runtimes. No TypeScript executor SDK is created.

## Verified baseline (before implementation)

Commands and evidence are in the three research reports and summarized here.

- Working tree clean on `feature/durable-signals-timers` at `6043255`.
- Go non-test source builds in `nore`, `shared`, `application`, and the Go executor SDK.
- Pre-existing failures:
  1. Four test packages do not compile because test fakes lack `WithTimer`/`ClaimTimer`:
     - `nore/internal/storage/fake_test.go`
     - `nore/internal/execution/store_failure_test.go`
     - `nore/internal/instance/metadata_store_test.go`
     - `nore/cmd/nore/main_test.go`
  2. `application/internal/acceptance` `TestR4_24SigKill*` hang for 10 minutes because
     `runtimeSession.stop` is not idempotent; the tests also assert nothing meaningful.
  3. `gofmt -l` reports `nore/internal/storage/memory.go` and
     `nore/internal/storage/postgres/postgres.go`.
- Phase 5 checkboxes are not supported by behavior. `CapabilityWaiting` is never entered,
  the signal handler mutates memory only, timer records have no consumers, recovery treats
  `WaitUntil == nil` as due, and waiting is counted in `inFlight` on the recovery path.
- Phase 3 join state/durable join restore is largely implemented but several boxes are stale.
  `R4-14` step 5 is genuinely open.
- Environment: Go 1.26.5, pnpm 10.33.0, dotnet 10.0.115 available. PostgreSQL DSN is not set,
  so Postgres parity tests skip; record that limitation rather than claiming parity.

## Adopted decisions

The full ADR is `.agents/decisions/0001-phase5-signals-timers.md`. The constraints that drive
the work split are:

1. One authoritative wait representation: execution snapshot is authoritative; `timer_store` is
   an indexed lease/index substrate, not a second domain model.
2. Wait entry/resolution is a single scheduler-owned committed transition. Handlers never mutate
   execution state.
3. `TimerTx` is one transaction over the main KV surface plus timer operations on every backend.
   SQLite and Postgres must not diverge.
4. `timer_store` gets a `due_at` column/index and a bounded `DueTimers(now, limit)` primitive;
   `ClaimTimer` claims only due, unleased/expired-lease timers.
5. Capability suspension is an additive, feature-negotiated v1 protocol outcome. Resume is a fresh
   `Execute` with a new attempt ID, not a stackful continuation. Go and .NET SDKs are updated;
   no TypeScript executor SDK exists.
6. Signal contract: required `deliveryId`; body `{deliveryId, payload, correlationKey?}`;
   duplicate same-payload delivery is an idempotent 200; different payload is 409; unmatched
   is 404; terminal/late is 410; cross-instance is 404; no buffering.
7. `WaitUntil == nil` means no timer deadline and is never due. Waiting is excluded from
   `inFlight`. `StatusWaiting` is derived only when no runnable work exists.
8. Default wait-deadline policy is `fail`, configurable to `cancel`/`continue`. Default execution
   max lifetime is 30 days, configurable; `0` disables it. Both are distinct from per-capability
   timeout.
9. Cancellation of a wait is terminal by default; wait metadata is retained for diagnostics;
   buffered join inputs are discarded; timers are deleted in the same transaction.
10. Durable retry replaces the in-process backoff sleep; non-durable modes keep current behavior.

## Workstreams, exact leases, and dependencies

One mutating owner per task. Paths below are leases; an owner must not edit outside its lease.

### Wave 0 — truthful baseline, repairs, and frozen protocol (parallel)

| Task | Owner | Lease | Depends | Acceptance | Verification |
|---|---|---|---|---|---|
| T-019 Correct Phase 5 backlog truthfulness | documenter | `todos/TODO.md` only | none | Re-open `R5-1..R5-8`, `R5-10`, `R5-11`; append findings that checked items lack behavior, with evidence references; add `F5-*` findings for the pre-existing failures; do not claim completion | `git diff -- todos/TODO.md`; every re-opened item is unchecked and every finding names evidence |
| T-020 M0 foundational repair | coder | `nore/internal/storage/fake_test.go`, `nore/internal/execution/store_failure_test.go`, `nore/internal/instance/metadata_store_test.go`, `nore/cmd/nore/main_test.go`, gofmt-only `nore/internal/storage/memory.go`, `nore/internal/storage/postgres/postgres.go` | none | Four test packages compile; `gofmt -l` empty for the two named files; no production behavior change | `(cd nore && GOWORK=off go test -count=1 ./internal/storage ./internal/execution ./internal/instance ./cmd/nore)`; `gofmt -l` |
| T-021 M0b acceptance truthfulness | bugfixer | `application/internal/acceptance/**` | none | `runtimeSession.stop` idempotent; `R4-24` tests assert real state and terminate; no hanging cleanup | `(cd application && GOWORK=off go test -count=1 -timeout 5m ./internal/acceptance)`; targeted `-run 'R4-24'` |
| T-023 M4a protocol/SDK suspend contract | coder | `shared/protocol/capabilityruntime/**`, generated Go/C# protocol files, `packages/executor-sdks/golang/**`, `packages/executor-sdks/dotnet/**`, `nore/internal/mechanism/conformance_test.go` | ADR contract; no production wiring yet | Additive `ExecuteResponse.suspend`, `ExecuteRequest.resume`, `"suspend"` feature; Go/.NET API updates; conformance for negotiated/unnegotiated/old-client/resume/duplicate | `(cd packages/executor-sdks/golang && GOWORK=off go test ./...)`; `dotnet test packages/executor-sdks/dotnet/Neuron.Executor.slnx --no-restore`; generation command evidence |

T-019, T-020, T-021, and T-023 are disjoint and may run concurrently.

### Wave 1 — storage/timer substrate

| Task | Owner | Lease | Depends | Acceptance | Verification |
|---|---|---|---|---|---|
| T-022 M1 storage timer substrate | coder | `nore/internal/storage/provider.go`, `key.go`, `memory.go`, `sqlite/**`, `postgres/**`, `migrate/**`, storage tests; plus the three storage-facing fakes only after T-020 releases | T-020 | `TimerTx.KV` unified; `due_at` migration/index; `PutTimer(dueAt)`; bounded `DueTimers`; lease-aware `ClaimTimer`; Memory/SQLite/Postgres parity; Memory race-safe; no full scan | `(cd nore && GOWORK=off go test -race -count=1 ./internal/storage/...)`; SQLite/migrate tests; Postgres tests or explicit DSN limitation |

### Wave 2 — execution wait state and transitions

| Task | Owner | Lease | Depends | Acceptance | Verification |
|---|---|---|---|---|---|
| T-024 M2 execution wait/transition/recovery/deadline core | coder | `nore/internal/execution/execution.go`, `snapshot.go`, `accessors.go`, `wait.go`, `store.go`, `dispatch_intent*.go`, `recovery*.go`, `scheduler/**`, `instance/**` (except `metadata_store_test.go`), `contracts/**`, `event/**`, `nore/internal/mechanism/**` until F3 | T-021, T-022, T-023 | Wait entry/resolution; derived `StatusWaiting`; nil-`WaitUntil` fixed; waiting excluded from `inFlight`; transactional wait + timer + delivery + dispatch intent; cancellation terminal with retained metadata; deadline policy/lifetime; durable retry state; snapshot v3; recovery restores waits/timers; join state verified; dispatch reconciler | `(cd nore && GOWORK=off go test -count=1 ./internal/execution/... ./internal/instance/...)`; `-race`; focused Wait/Signal/Recover/Retry/Timer tests |

### Wave 3 — engine, protocol adapter, API, and config (parallel, disjoint)

| Task | Owner | Lease | Depends | Acceptance | Verification |
|---|---|---|---|---|---|
| T-025 M2b engine suspend/retry/reconciler | coder | `nore/internal/execution/engine/**` only | T-024 | Suspend outcome maps to wait transition; durable retry does not sleep in worker; reconciler republishes ready intents; non-durable behavior unchanged; cancellation/deadline enforced | `(cd nore && GOWORK=off go test -count=1 ./internal/execution/engine/...)`; focused retry/suspend/cancel tests |
| T-026 M4b process adapter/conformance | coder | `nore/internal/mechanism/process/**` only (handoff after F3) | T-023, T-024 | Process adapter maps proto suspend/resume to contracts; old/feature-negotiated behavior; conformance round-trip | `(cd nore && GOWORK=off go test -count=1 ./internal/mechanism/...)`; conformance |
| T-027 M3 signal API/auth/query | coder | `nore/internal/api/handlers/instances/**`, `nore/internal/api/routes/**`, `shared/types/protocol/**`, API tests | T-024 | Execution-scoped route plus compatibility route; body/deliveryId/idempotency; payload validation; instance/execution binding; wait list/filter exposes reason/signal/correlation/deadline; precise 400/404/409/410 | `(cd nore && GOWORK=off go test -count=1 ./internal/api/...)`; security-sensitive cases |
| T-028 M5a config/deadline policy | coder | `shared/types/core/runtimeconfig.go`, `nore/internal/runtimeconfig/**`, `application/config/**`, `application/cmd/**`, `nore/cmd/**` (except `main_test.go`) | T-024 | Wait deadline policy default fail; max lifetime default 30d, `0` disables; poll interval/batch; validation/denial; defaults preserve non-durable behavior | `(cd nore && GOWORK=off go test ./internal/runtimeconfig/...)`; `(cd application && GOWORK=off go test ./config/... ./cmd/...)` |

T-025, T-026, T-027, and T-028 have disjoint leases and may run concurrently after T-024.

### Wave 4 — CLI and end-to-end validation

| Task | Owner | Lease | Depends | Acceptance | Verification |
|---|---|---|---|---|---|
| T-029 M5b CLI signal/wait/query | coder | `application/internal/cli/**`, `application/client/**`, `application/connection/**` if needed | T-027 | Commands to deliver a signal, cancel a waiting execution, list suspended executions with wait reason/deadline; errors are actionable; output invariants preserved | `(cd application && GOWORK=off go test -count=1 ./internal/cli/...)`; CLI tests |
| T-030 Phase 5 end-to-end tests | bugfixer | `application/internal/acceptance/**`, plus new integration test files it creates under `application/internal/acceptance/**` | T-025, T-026, T-027, T-028 | R5-22 suspend→restart→signal→resume; R5-23 timer after restart; R5-24 duplicate signal/timer one transition; R5-25 unauthorized/wrong execution; R5-26 waiting occupies no worker; R5-27 deadline policies; SIGKILL evidence | `(cd application && GOWORK=off go test -count=1 -timeout 15m ./internal/acceptance/...)`; targeted `-run 'Durable|R5'` |

T-029 and T-030 are disjoint and may run concurrently.

### Wave 5 — documentation, TODO closure, and gate

| Task | Owner | Lease | Depends | Acceptance | Verification |
|---|---|---|---|---|---|
| T-031 Documentation and TODO completion | documenter | `docs/**`, `README.md`, `application/README.md`, `packages/executor-sdks/golang/README.md`, `packages/executor-sdks/dotnet/README.md`, `todos/TODO.md` | T-029, T-030 | Docs describe verified suspend/resume, signal idempotency, timer/deadline/lifetime policy, config, SDK changes, no TS executor SDK; mark only verified Phase 5 items `[x]`; mark blocking Phase 3/4 items only with evidence; record limitations | `git diff --check`; documentation claims checked against code/tests |
| T-032 Final validation gate | coordinator | none (read-only) | T-031 | `scripts/script.sh` result recorded; all targeted suites pass; Postgres/.NET limitations named; no uncommitted changes; all task commits recorded | `scripts/script.sh`; final `git status` and commit log |

## Corrected Phase 5 closure map

`todos/TODO.md` titles are authoritative. This map fixes the real ownership.

| Item | Title area | Closing task(s) |
|---|---|---|
| R5-1 | Waiting state with wait metadata | T-024 |
| R5-2 | Waiting excluded from worker/in-flight accounting | T-024, T-030 |
| R5-3 | Wait state persisted transactionally | T-022, T-024 |
| R5-4 | General signal primitive with delivery ID | T-027, T-023 |
| R5-5 | Signal auth + execution/wait binding | T-027, T-030 |
| R5-6 | Persist signal before acknowledging, then resume | T-024, T-027 |
| R5-7 | Duplicate delivery idempotent | T-022, T-027, T-030 |
| R5-8 | Signal payload schema validation | T-024, T-027 |
| R5-9 | Signal after deadline/cancellation | T-024, T-027 |
| R5-10 | Durable scheduled retry/wake time | T-022, T-024, T-025 |
| R5-11 | Timer index/claim operation | T-022 |
| R5-12 | Timer poller claims due timers and resumes | T-024, T-025 |
| R5-13 | Clock changes/missed timers | T-022, T-024, T-030 |
| R5-14 | Separate capability timeout from max lifetime | T-024, T-028 |
| R5-15 | Wait deadline policy + execution max lifetime | T-024, T-028 |
| R5-16 | Transactional deadline expiry with reason | T-024 |
| R5-17 | Cancel waiting execution + list suspended | T-024, T-027, T-029 |
| R5-18 | Cancelled-wait resume/buffered-state semantics | T-024, T-031 |
| R5-19 | Capability requests suspension | T-023, T-025 |
| R5-20 | Version protocol, update every SDK/conformance | T-023, T-026, T-031 |
| R5-21 | Enforce cancellation/deadlines on suspended work | T-024, T-025 |
| R5-22 | Suspend approval, restart, signal, resume test | T-030 |
| R5-23 | Durable timer after restart test | T-030 |
| R5-24 | Duplicate signal/timer one-transition test | T-027, T-030 |
| R5-25 | Unauthorized/wrong-execution signal test | T-027, T-030 |
| R5-26 | Waiting occupies no worker test | T-024, T-030 |
| R5-27 | Deadline expiry policy tests | T-024, T-028, T-030 |
| R5-28 | `scripts/script.sh` recorded | T-032 |

Blocking Phase 3/4 items: `F4-11`/`R4-14` step 5 → T-022, T-024, T-030; `R4-6` → T-022, T-024; `R4-8` → T-024, T-030; `R3-24` → T-024 verification plus T-031 marking; `R4-24`/`R4-25` test truthfulness → T-021, T-030.

## Parallelism summary

```
Wave 0: T-019 ∥ T-020 ∥ T-021 ∥ T-023
Wave 1: T-022
Wave 2: T-024
Wave 3: T-025 ∥ T-026 ∥ T-027 ∥ T-028
Wave 4: T-029 ∥ T-030
Wave 5: T-031 → T-032
```

Read-only `reviewer`/`security` reviews run at each freeze point and may overlap any wave.
Overlapping leases are forbidden. If a task needs a path outside its lease, it stops and asks the
coordinator; the coordinator revises the lease before edits.

## Pre-existing errors and remediation

| Failure | Task | Rule |
|---|---|---|
| Four test fakes missing timer methods | T-020 | Fix the fakes; do not weaken the `storage.Store` interface |
| `gofmt` drift in `memory.go`/`postgres.go` | T-020 | Format only; no behavior change |
| Acceptance `stop` not idempotent + vacuous R4-24 | T-021 | Make stop idempotent; make tests assert real behavior |
| Postgres DSN unavailable | T-022/T-032 | Record as "not run"; do not claim parity |
| .NET toolchain/pre-existing artifacts | T-023/T-032 | Run if available; record limitation if not |

## Risks and limitations

- The capability suspend extension is wire-additive but changes SDK APIs; old clients must keep
  working. Conformance tests are mandatory.
- Durable dispatch remains at-least-once. Signal `deliveryId` and stable logical operation IDs
  make retries safe; exactly-once external side effects remain a capability concern.
- `WithTimer` serializes timer writes; measure contention and keep batches/transactions small.
- Postgres parity cannot be claimed without a DSN; the gate must report the limitation.
- Join-state durability is believed implemented; T-024 must verify it rather than assume it.
- Any pre-existing failure discovered later is added as a bounded task; it is never waived.

## Definition of done

- Every Phase 5 item `R5-1..R5-28` has verified evidence and is checked only after verification.
- Blocking `F4-11`/`R4-14` step 5, `R4-6`, `R4-8`, `R3-24` are closed or explicitly documented.
- Every pre-existing failure is fixed or has a user-approved documented limitation.
- Required reviews/sign-offs are recorded: reviewer for every non-trivial change, architect for
  boundaries/persistence/concurrency, security for protocol/auth/storage/timers.
- `scripts/script.sh` has run; exact result or exact limitation is recorded.
- Every completed task is committed on `feature/durable-signals-timers`; no push.
- `todos/TODO.md` reflects actual state and includes findings.
