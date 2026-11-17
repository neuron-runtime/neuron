# 0003 — Recovery semantics for durable `StatusWaiting` executions

- Status: accepted
- Date: 2026-11-17
- Owners: architect (design authority); coordinator (adoption); bugfixer (T-040 implementation)
- Related tasks: T-040; T-024 / T-024c1a (`059cae7`); ADR 0001 D5
- Branch evidence: `feature/durable-signals-timers`, HEAD `7fcf97f`

## Context

### Observed structure and behavior (HEAD `7fcf97f`)

- Execution statuses include both `StatusWaiting` and `StatusRetryWait`; capability statuses include `CapabilityWaiting` and `CapabilityRetryWait` (`nore/internal/execution/execution.go:16-26`, `:28-51`).
- `StatusRetryWait` is declared once and never assigned anywhere in the repository; it is currently a dead constant. Durable retry waits are represented as `CapabilityRetryWait` plus execution-level `StatusWaiting`.
- `Execution.RecoverableDispatch` returns `nil` unless `e.status == StatusRunning` (and a blueprint is attached) (`nore/internal/execution/execution.go:442-447`). It already contains elapsed-time logic: `CapabilityRetryWait` resumes when `NextAttemptAt` is non-nil and elapsed (`:462-469`); `CapabilityWaiting` resumes only for timer/deadline waits with a non-nil, elapsed `WaitUntil` via `isElapsedTimerWait` (`:470-477`, `:490-498`). It rebuilds `inFlight` from recovered states (`:486`).
- `RecoverableDispatch` mutates capability state and `inFlight` but does **not** call `refreshStatusLocked`; its own doc says it "prepares a running execution" (`execution.go:420-488`).
- `Scheduler.RecoverExecution` returns early for any non-Running, non-Pending, non-terminal status (`nore/internal/execution/scheduler/scheduler.go:143-162`), so a fully waiting execution never reaches `RecoverableDispatch`.
- `RecoverExecution` has a corruption branch that fails an execution when `len(dispatch)==0 && InFlight()==0 && UnfinishedCapabilityCount()>0` (`scheduler.go:164-168`). A healthy non-due signal wait satisfies all three conditions.
- `UnfinishedCapabilityCount` counts every non-terminal capability status, including `CapabilityWaiting` and `CapabilityRetryWait` (`nore/internal/execution/accessors.go:45-61`). `WaitingCapabilities` returns the authoritative list of waiting and retry-wait capabilities (`:90-104`).
- `StatusWaiting` is a **derived** execution status: `refreshStatusLocked` sets it only when waiting work exists and no runnable work remains, and reverts to `StatusRunning` otherwise (`nore/internal/execution/wait.go:402-416`, helpers `:418-439`). It is applied on wait entry/resume/retry transitions (`:204`, `:239`, `:260`, `:290`). ADR 0001 D5 records the same invariant (`.agents/decisions/0001-phase5-signals-timers.md:183-189`, especially `:186`).
- Status is part of the durable snapshot (`nore/internal/execution/snapshot.go:21`, `:77`) and is restored verbatim (`:101`, `:178`). Recovery therefore loads `StatusWaiting` from storage.
- `Instance.RecoverExecution` attaches the recompiled blueprint, adopts the execution into the live store, then delegates to `Scheduler.RecoverExecution` (`nore/internal/instance/instance.go:462-494`). The instance is an integration boundary, not a second recovery implementation.
- Durable timer rows are written at wait entry with an explicit due time derived from `WaitUntil` (`nore/internal/execution/scheduler/wait.go:154-176`) inside `SaveWaitTransition` (`nore/internal/execution/store.go:967-1070`). They survive restart; there is nothing to re-arm.
- `Scheduler.PollDueTimers` discovers, claims, fences, resolves, and deletes due timer rows (`nore/internal/execution/scheduler/timer_poller.go:28-56`, `:58-205`), but **has no production caller at HEAD**; the only callers are tests (`scheduler/timer_poller_test.go`, `scheduler/wait_test.go:186`). It is therefore latent until the host loop is wired.
- `SaveTransition`, which recovery uses via `s.commit` (`scheduler.go:113-121`), writes snapshot + intents only and has no timer effects (`store.go:149-202`).
- `processDueTimer` returns without deleting when the loaded capability is no longer `CapabilityWaiting` (`timer_poller.go:107-116`). `DueTimers` excludes only timers with a live lease (`nore/internal/storage/memory.go:700-729`, `nore/internal/storage/sqlite/sqlite.go:615-628`), so such a row resurfaces after lease expiry.
- Read-only verification passed: `cd nore && GOWORK=off go test -count=1 ./internal/execution/... ./internal/instance/... -run 'Recover|Wait|Retry|Timer'` → all four packages `ok`. No existing test drives a durable `StatusWaiting` execution through `Scheduler.RecoverExecution`; the defect is latent at this HEAD because durable wait mutators have no production callers yet.
- **Workspace note (parallel work, not part of this ADR's evidence):** during this review, uncommitted edits appeared in `nore/internal/execution/scheduler/wait.go` and `nore/internal/execution/store.go`, adding `WaitTimerKeys(ctx, executionID, capabilityID)` to the `WaitStore` interface and an `ExecutionStore` implementation. This architect did not make or inspect those edits for sign-off. They strengthen the D4 follow-up by giving the poller a bounded, scoped way to enumerate timer keys; they do not change any D1-D3 decision or status semantics. All `file:line` citations above refer to committed HEAD `7fcf97f`.

### Intended behavior

From ADR 0001 D5: `WaitUntil == nil` is never due; timer/deadline waits resume when elapsed; signal waits do not resume from time; waiting work is excluded from `inFlight`; `StatusWaiting` is derived; recovery resumes only eligible waits (`.agents/decisions/0001-phase5-signals-timers.md:183-189`). T-040 adds that a fully waiting execution must be reconciled, not skipped or failed (`.agents/ledger.yaml:412-436`).

### Constraints

One authoritative wait-state model; no duplicated recovery logic; no manufactured duplicate timers; no conversion of a healthy wait into failure; no Postgres claims without a DSN; no source edits in this task.

## Decision

### D1 — `RecoverableDispatch` handles `StatusWaiting` directly; do not pre-normalize to `StatusRunning`

Broaden the guard at `execution.go:445` to accept `StatusRunning` **and** `StatusWaiting` (terminal and `StatusPending` remain excluded). At the end of `RecoverableDispatch`, call the existing internal `e.refreshStatusLocked()` so the returned execution's status matches its post-recovery capability set. Update the doc comment at `execution.go:420-441` to state that the method is single-shot per recovery and re-derives status.

Rationale: `StatusWaiting` is derived from capability states (`wait.go:402-416`; ADR 0001 `:186`). Normalizing it to `Running` before dispatch would create a second, competing representation that the next `refreshStatusLocked` would immediately invert for a non-due signal wait. The domain method already owns the wait model and already mutates capability state and `inFlight`; re-deriving status there keeps one owner and one edge.

### D2 — A non-due signal wait remains durably and visibly `StatusWaiting`

After recovery of a non-due signal wait (nil or future `WaitUntil`), the persisted and operator-visible status is `StatusWaiting`, not `StatusRunning` and not a transient `Running`. `refreshStatusLocked` keeps `StatusWaiting` because waiting work exists and no runnable work remains (`wait.go:409-411`), and `s.commit` persists that snapshot (`scheduler.go:180-182`; `store.go:161-201`). No `CapabilityReady` event is published for a wait that did not resume.

**Public/operator-visible status semantics: unchanged.** This restores the status that was already durable before the restart (`snapshot.go:21`, `:77`, `:101`). No status value, wire field, or query contract changes. The only observable change is that a previously skipped/failed execution is now correctly reported as waiting.

### D3 — Guard the corruption branch with authoritative capability state, not derived status

At `scheduler.go:165`, fail only when there is unfinished work that is **neither running nor legitimately waiting**:

```go
if len(dispatch) == 0 && execution.InFlight() == 0 &&
    execution.UnfinishedCapabilityCount() > 0 &&
    len(execution.WaitingCapabilities()) < execution.UnfinishedCapabilityCount() {
    s.failExecution(...)
}
```

Use the existing authoritative accessors (`accessors.go:49-61`, `:90-104`); do not add a new wait-model predicate and do not use `IsWaiting()` alone. `IsWaiting()` is derived status and could mask a stuck non-waiting unfinished capability; the count comparison preserves the existing corruption detection. A healthy wait then proceeds to the normal commit path with zero intents and zero publications; `completeIfDrained` cannot complete it because `MarkCompleted` requires `StatusRunning` (`execution.go:621-632`; `scheduler.go:510-513`).

### D4 — No timer re-arming; split recovery and poller by responsibility

- Timers are armed once at wait entry and are durable (`scheduler/wait.go:154-176`). **Neither recovery nor the poller re-arms them.**
- **Recovery** owns status/identity/in-flight reconciliation and immediate catch-up dispatch: elapsed `CapabilityRetryWait` via `NextAttemptAt` (`execution.go:462-469`) and elapsed timer/deadline waits (`execution.go:470-477`, `:490-498`). This is required at HEAD because `PollDueTimers` has no production caller; deferring elapsed timers to it would stall them. It also preserves ADR 0001 D5 (`.agents/decisions/0001-phase5-signals-timers.md:184`, `:189`).
- **Timer poller** owns the durable timer-row lifecycle (discovery, fresh per-claim owner, renew, fenced delete, deadline policy) for waits that are still `CapabilityWaiting` (`timer_poller.go:28-205`). Its capability-state check (`:107-116`) plus claim fencing (`:74-94`) make it a no-op for a wait recovery already resumed, so dispatch is not duplicated.
- **Residual obligation:** `SaveTransition` has no timer effects (`store.go:149-202`), so a timer row consumed by recovery is not deleted there. `processDueTimer` currently leaves such a stale row claimed and re-surfaces it after lease expiry (`timer_poller.go:107-110`; `memory.go:700-729`; `sqlite.go:615-628`). This is **not** a duplicate-dispatch defect (the state check prevents dispatch), but it can accumulate stale due timers and crowd the bounded batch once the poller is wired. Required fix **before** the poller is connected to production: make `processDueTimer` fence-delete a stale due timer when the capability is no longer `CapabilityWaiting`, using the scoped key enumeration now available from `WaitTimerKeys` (see workspace note above) where a conflicting future deadline must be distinguished. Preferred over adding a recovery→wait-store commit edge because it keeps all timer-row ownership in the poller and adds no new interface to T-040. Owner: T-025/T-030; record in `todos/`. It is outside T-040's current lease.

### D5 — Mandatory regression tests at three boundaries

1. **Execution boundary** (`nore/internal/execution/wait_test.go` / a focused recovery test file): restored `StatusWaiting` execution with an elapsed timer/deadline wait resumes exactly once, wait metadata cleared, `inFlight` correct, and status re-derived to `StatusRunning`; elapsed retry wait via `NextAttemptAt`; non-due signal wait stays `CapabilityWaiting`/`StatusWaiting` with empty dispatch and `inFlight == 0`; nil `WaitUntil` never due (extend `TestNilWaitUntilIsNeverDue` at `wait_test.go:266-292` with the durable-status assertion).
2. **Scheduler boundary** (`nore/internal/execution/scheduler/`): `RecoverExecution` on a restored `StatusWaiting` execution (a) does not publish `ExecutionFailed`, (b) persists `StatusWaiting` with no `CapabilityReady` for a non-due signal wait, (c) publishes exactly one `CapabilityReady` and one committed intent per elapsed wait, including retry, and (d) mixed waits dispatch only the elapsed one. Capture events/commits with the existing fakes (`scheduler/transition_commit_test.go:34`, `scheduler/wait_test.go:115`).
3. **Instance boundary** (`nore/internal/instance/recovery_test.go`): recovery of an execution loaded with durable `StatusWaiting` leaves it waiting (not failed, not running) until a delivery arrives, and a subsequent elapsed timer/resume dispatches once. This is the persistence integration check; instance must not acquire its own wait logic (`instance.go:462-494`).

## Alternatives considered

- **Normalize `StatusWaiting` → `StatusRunning` in the scheduler before `RecoverableDispatch`.** Rejected: contradicts the derived-status invariant (`wait.go:402-416`; ADR 0001 `:186`), changes durable/operator-visible status for a healthy wait, and creates a competing representation that reverts on the next refresh.
- **Keep the guard and special-case only non-due waits with a separate "kept waiting" path.** Rejected: leaves elapsed timers stalled and duplicates wait-state logic outside the execution model.
- **Guard the failure branch with `IsWaiting()`.** Rejected: derived status can mask a stuck non-waiting unfinished capability; the authoritative count comparison is stricter and reuses existing accessors.
- **Move all elapsed timer/deadline resume to the poller.** Rejected at HEAD: `PollDueTimers` has no production caller, so missed timers would never fire; also conflicts with ADR 0001 D5 `:189`.
- **Have recovery commit `WaitEffects.DeleteTimers` through `SaveWaitTransition` inside T-040.** Deferred: expands the lease into `store.go` and committer/interface changes; the poller self-heal is smaller and keeps timer-row ownership single-owned. Recorded as an obligation above, not silently dropped.

## Consequences

- Positive: fully waiting executions are reconciled and observable as waiting; elapsed waits resume without a second wait model; healthy waits are not failed; no new package, interface, dependency, or status value.
- Negative: `RecoverableDispatch` now changes execution status, so callers/tests that assumed it is a pure dispatch query must assert the new status. It remains single-shot: its `CapabilityReady` case re-dispatches on a second call (`execution.go:459-461`); exactly-once is therefore guaranteed by the scheduler's single `ClearDispatchIntents` + commit + publish sequence (`scheduler.go:169-193`), not by method idempotence. Document this.
- Neutral: `StatusRetryWait` remains an unused constant; its removal is unrelated cleanup.

## Boundary and dependency impact

- `scheduler → execution`: unchanged edge; `RecoverExecution` continues to call `RecoverableDispatch` (`scheduler.go:164`) and the existing accessors (`accessors.go`). No new edge.
- `execution` internal: `RecoverableDispatch` reuses `refreshStatusLocked` (`wait.go:405`); no new dependency and no new exported symbol.
- No new abstraction, no new package, no folder move, no manifest/config/API/protocol change.
- `store.go`, storage backends, and `contracts` are **out of scope** for T-040. Timer-row self-heal stays with the poller as a sequenced follow-up.
- Public SDK/API surface: none changed.

## Migration plan (ordered, reversible, each step keeps the system working)

1. In T-040, broaden the `RecoverableDispatch` guard and add internal `refreshStatusLocked`; update its doc. Running-status behavior is unchanged; the dormant `StatusWaiting` path becomes correct. Add execution-boundary tests first.
2. In T-040, guard the `scheduler.go:165` failure branch with the waiting-vs-unfinished comparison; add scheduler-boundary tests for non-failing healthy waits and exactly-once elapsed dispatch.
3. In T-040, add the instance-boundary recovery test; run the ledger's focused and race suites (`ledger.yaml:429-431`).
4. Before the timer poller is wired to production (T-025/T-030), add stale-timer fenced delete in `processDueTimer` and a test that a recovered/resumed wait's timer is reclaimed without a new dispatch.
5. T-030 restart acceptance tests (R5-13/R5-22/R5-24) confirm the behavior end to end.

## Verification

- Executed read-only during this review: `cd nore && GOWORK=off go test -count=1 ./internal/execution/... ./internal/instance/... -run 'Recover|Wait|Retry|Timer'` → `ok` for `execution` (0.040s), `execution/engine` (0.012s), `execution/scheduler` (0.067s), `instance` (0.129s). Re-run after the parallel `WaitTimerKeys` edits appeared → still `ok` for all four packages (0.052s / 0.022s / 0.071s / 0.093s). This confirms the suite is green **without** covering the defect — coverage must be added. No claim is made about the uncommitted parallel edits' correctness; they were not the subject of this review.
- Required after implementation: the two ledger verification commands (`.agents/ledger.yaml:429-431`), plus `go build ./...` and `gofmt -l` on touched files.
- Architectural property check: grep confirms no second status-normalization path and no new timer write in recovery; `RecoverableDispatch` remains the only domain recovery dispatcher.

## Risks

- **Risk:** tests that call `RecoverableDispatch` on a restored `StatusWaiting` execution may now observe `StatusRunning`/`StatusWaiting` re-derivation where they previously observed no change. **Likelihood:** medium. **Impact:** low. **Mitigation:** update those assertions; `snapshot_v3_test.go:41` asserts status before recovery and remains valid.
- **Risk:** accidental duplicate dispatch if recovery is invoked twice for the same execution. **Likelihood:** low. **Impact:** high. **Mitigation:** keep single invocation at `instance.go:494`/`scheduler.go:143`; assert exactly one committed intent and one published event in scheduler tests; retain `ClearDispatchIntents` (`scheduler.go:170-179`).
- **Risk:** stale due-timer accumulation after recovery-resumed waits once the poller is live. **Likelihood:** medium. **Impact:** medium (bounded batch starvation, no duplicate work). **Mitigation:** sequenced poller self-heal (D4); owner T-025/T-030.
- **Risk:** PostgreSQL parity claims. **Likelihood:** n/a. **Impact:** n/a. **Mitigation:** none claimed; no DSN was available, so no Postgres behavior is asserted here.

## Sign-off recommendation for T-040

**Architecture design: approved with conditions.** The ADR decision is accepted and the proposed fix boundary (execution + scheduler + tests only) is architecturally sound. Final architecture sign-off on the implementation is conditional on: (1) status re-derivation inside `RecoverableDispatch` rather than forced normalization; (2) the authoritative waiting-vs-unfinished guard at `scheduler.go:165`; (3) persisted status `StatusWaiting` for non-due signal waits; (4) all three test boundaries in D5; and (5) the stale-timer obligation recorded in `todos/` before poller wiring. No source edits were made by this task.

## Open questions

- Should T-040's lease be expanded to include the poller self-heal, or should it stay with T-025/T-030? This is a scope/lease decision for the coordinator; the ADR recommends the latter.
- Should the dead `StatusRetryWait` constant (`execution.go:25`) be removed as separate cleanup? It is unrelated to T-040 and is recorded here only as an observation.
