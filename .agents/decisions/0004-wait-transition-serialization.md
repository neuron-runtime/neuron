# 0004 — Serialization of shared wait transitions

- Status: accepted (design authority); T-041 implementation sign-off pending
- Date: 2026-11-17
- Owners: architect (design authority); bugfixer (T-041 implementation); coordinator (adoption)
- Related tasks: T-041 (`.agents/ledger.yaml:459-483`); T-040a / ADR 0003; reviewer finding T-024c1b1-review; T-042 (pre-existing test flake); ADR 0001 D5
- Branch evidence: `feature/durable-signals-timers`, HEAD `cf3e6a6`

## Context

### Observed structure and behavior (verified at HEAD `cf3e6a6`)

**The live execution object is shared.**

- `ExecutionStore.Get` returns the live cached `*Execution` from `s.mem.Get` whenever the execution is live, falling back to a durable load only on a cache miss (`nore/internal/execution/store.go:99-108`). Signal delivery and the timer poller therefore operate on the same pointer when the execution is live.
- Recovery deliberately adopts a durable snapshot into the live cache so the scheduler and engine share one `Execution` value (`store.go:204-219`; `nore/internal/instance/instance.go:462-495`, adoption at `:485`, delegation at `:494`).

**Commit serialization is narrower than the transition.**

- `Execution` owns `revision` and `saveMu`; the field comment states that two writers sharing the same value "cannot produce a false conflict; only a distinct copy with an older revision triggers `storage.ErrConflict`" (`nore/internal/execution/execution.go:159-166`). `Revision()`/`setRevision` are the accessors (`:744-756`).
- `ExecutionStore`'s own concurrency contract repeats this: `saveMu` + revision compare "serialize writes for one Execution value", and two writers sharing the value "cannot conflict with one another" (`store.go:32-45`).
- `saveMu` is acquired **only inside** store methods: `Add` (`store.go:58-59`), `Save` (`:121-122`), `SaveTransition` (`:165-166`), `SaveWaitTransition` (`:993-994`). Nothing holds it while the caller validates or mutates domain state.
- The revision precondition is read from the same object being mutated: `writeExecutionRevision` compares `execution.Revision()` to the stored revision and writes R+1 (`store.go:464-482`, comparison at `:469-472`, `CompareAndSwap` at `:478`). The caller then applies `setRevision(nextRevision)` on that same object (`store.go:142`, `:196`, `:1113`) and only afterwards replaces the cache (`:143`, `:198`, `:1115`).

**Three external drivers read, mutate, and commit one shared value.**

- `Scheduler.ResolveSignal` reads `CapabilityState`, checks `state.Status != CapabilityWaiting` (`:234-243`), snapshots `before` (`:245`), mutates with `ResolveWait` (`:246`), commits with `SaveWaitTransition` (`:266-269`), restores `before` on any error (`:270-273`), and publishes `CapabilityReady` only after commit (`:282-285`) (`nore/internal/execution/scheduler/wait.go:206-286`).
- `Scheduler.PollDueTimers` / `processDueTimer` loads the live object (`timer_poller.go:96`), checks capability status and `WaitUntil` (`:112-149`), snapshots `before` (`:151`), mutates via `ResolveWait`/`ExpireWait`/`MarkFailed`/`MarkCancelled` (`:156-189`), commits (`:209-212`), restores `before` on error (`:213-231`), and publishes after commit (`:235-238`) (`nore/internal/execution/scheduler/timer_poller.go:35-239`).
- `Scheduler.RecoverExecution` mutates through `Execution.RecoverableDispatch` (`scheduler.go:166`; domain mutation at `execution.go:449-496`, including capability transitions at `:462`, `:471`, `:480`, `inFlight` at `:493`, status re-derivation at `:494`), resets intents (`:178-187`), commits (`:188-190`), and publishes (`:191-201`) (`nore/internal/execution/scheduler/scheduler.go:145-203`). Recovery is documented as running before the scheduler/engine start (`:143-144`), but it adopts the same live object and is therefore a driver of it.
- Two further wait drivers share the same pattern: `EnterWait` (`wait.go:143-197`; mutate `:186`, commit `:189`, `Restore` `:193`) and `CancelExecution` (`scheduler.go:340-389`; `MarkCancelled` `:364`, `SaveWaitTransition` `:376`, publish `:385`).

**Per-mutator guards are not a transition guard.**

- `ResolveWait` and `ExpireWait` each hold `e.mu` and recheck `state.Status != CapabilityWaiting` (`nore/internal/execution/wait.go:212-241` at `:213-214` and `:223`; `:329-377` at `:330-331` and `:337`). Those locks serialize two mutations of the *same capability* on one object, but they are released before the store call, cover neither the preceding read nor the commit, and do not cover different capabilities or terminal mutators such as `MarkFailed`/`MarkCancelled` (`timer_poller.go:172`, `:180`).
- `Snapshot` reads `e.revision` (`nore/internal/execution/snapshot.go:45-49`) and `Restore` writes `e.revision = snapshot.Revision` (`:97-102`). A `before` snapshot taken before a peer commits can therefore roll the shared object — including its revision — backwards.

### Intended behavior

From ADR 0001 D5 and ADR 0003: `StatusWaiting` is derived from capability state, one authoritative wait-state model, elapsed timer/deadline waits resume, non-due signal waits stay waiting, and a transition is committed before it is published (`.agents/decisions/0001-phase5-signals-timers.md:183-189`; `.agents/decisions/0003-wait-recovery-semantics.md:40-71`). T-041 adds that concurrent drivers of one execution must not both commit and publish from one snapshot (`.agents/ledger.yaml:470-475`).

### Refinement of the reviewer finding

The reviewer finding (`.agents/reports/T-024c1b1.md:96-100`; ledger `T-041` evidence at `.agents/ledger.yaml:481`) is **valid at the composite-boundary level** and is confirmed here as a latent correctness defect. One detail is refined by inspection: two drivers mutating the *same capability on the same object* cannot both pass `wait.go:223`/`:337` because `e.mu` is held across those checks (`wait.go:213-214`, `:330-331`). The defect is still real, in three forms:

1. **Defeated loser rejection (observed code path, inferred outcome).** When two drivers commit the same shared object, the first advances that object's revision (`store.go:1113`); the second then reads the advanced revision as its CAS precondition (`store.go:469`). The conflict path is unreachable for same-object writers by design (`store.go:36-41`, `execution.go:163-165`). Different-capability transitions (signal for one capability, deadline expiry for another) therefore both commit and both publish; a fail/cancel deadline can publish `ExecutionFailed`/`ExecutionCancelled` alongside a `CapabilityReady` + dispatch intent for a resumed sibling.
2. **Stale rollback (observed code path, inferred outcome).** `before` is captured before the commit (`wait.go:245`, `timer_poller.go:151`) and restored on error (`wait.go:271`, `timer_poller.go:219`). Because the first committer already advanced the shared object's revision and cache, a failing second driver can restore an older revision and state over it (`snapshot.go:97-102`), poisoning the live copy until reload.
3. **No composite atomicity (structural fact).** Even where `e.mu` prevents a double mutation, the read → validate → mutate → commit sequence is unprotected, so a driver can validate against state that a peer changes before the commit.

No existing test exercises signal-vs-poller or recovery-vs-signal concurrency; the defect is latent because durable wait mutators have no production callers yet (ADR 0003 `:24`, `:71`; `.agents/reports/T-024c1b1.md:92`). This is why it must be fixed before T-025/T-027 production wiring.

### Constraints and non-goals

- One authoritative wait-state model; no second status representation or duplicated wait logic.
- No nested transactions; no publish inside a transaction (`store.go:156-157`).
- No stale rollback over a committed peer.
- No store/contract/storage/backend/SDK/operator-API change; no new package; no folder move.
- No Postgres claims (no DSN available in this environment).
- Design only; no source edits in this task.

## Decision summary (answers to the required questions)

| Question | Decision |
|---|---|
| A vs B vs C | **A** — per-execution composite transition serialization — with the existing durable revision CAS retained as the cross-value backstop. B alone is rejected. See D1. |
| Where the lock lives | On the shared `Execution` value, as `transitionMu`, exposed only through one internal method `Execution.RunTransition`; not on a scheduler instance and not in a store-keyed registry. See D2. |
| How recovery enters | `Scheduler.RecoverExecution` wraps its whole mutation+commit+publish composite in `RunTransition` on the adopted object; adoption stays outside the guard. See D3. |
| Rollback semantics | Capture `before` inside the guard; never restore after a successful commit; on `ErrConflict`/replay use durable `Reload` + `Restore(fresh.Snapshot())`, not `Restore(before)`. See D4. |
| Test boundaries | Execution unit boundary + scheduler regression boundary + rollback boundary; `-race -count=50`. See D5 and "Test plan". |
| Store/storage/contracts/public API | No changes to store behavior, storage backends, `contracts`, SDK, or operator contract. Only two concurrency doc comments are corrected. See D6. |
| Blockers and steps | One test-infrastructure blocker (T-042 flake) and ordered steps below. See "Blockers" and "Migration plan". |

## Decision

### D1 — Serialize the composite transition; keep CAS as the cross-value backstop

Introduce one explicit serialization boundary around **read authoritative state → validate → mutate domain → durably commit → update cache**. Publish remains after the commit and outside the guard. The two mechanisms have disjoint, documented roles:

- The per-value guard provides mutual exclusion for drivers sharing one live `*Execution`. It is what makes the loser fail its precheck before it mutates or publishes.
- The existing store `saveMu` + revision CAS (`store.go:993-994`, `:464-482`) continues to reject a genuinely distinct, stale copy (the case already covered by `store_cas_test.go:90-200`). It is not removed and not changed.

Publishing outside the guard is safe because only the guard-winner reaches publish: a loser fails the guarded precheck and returns before commit or publish. It also keeps I/O out of the critical section. The known `RecoverableDispatch` property that re-dispatches an already-`CapabilityReady` capability (ADR 0003 `:90`; `execution.go:466-468`) is a separate concern, recorded under "Open questions", and is not solved by this guard.

### D2 — The guard lives on the `Execution` value

Add `transitionMu sync.Mutex` to `Execution` beside `saveMu` (`execution.go:159-166`) and one exported-but-internal method in package `execution`:

```go
// RunTransition serializes one composite state transition on this Execution
// value. fn must validate, mutate, and commit exactly once. RunTransition is
// non-reentrant: fn must not call RunTransition again.
func (e *Execution) RunTransition(fn func() error) error
```

Rationale: the shared mutable `*Execution` is the authority that all drivers already share (`store.go:99-108`, `store.go:204-219`). A lock on the value travels with it, so two scheduler instances, a poller loop, and recovery all serialize without a shared registry or a new interface. A scheduler-local lock map would create a second scheduler *instance* with a second map and silently fail to serialize (coupling violation); a store-keyed lock map would add a new store/contract method while the CAS already covers distinct-store races (more API surface, no benefit).

Lock order is fixed and must be documented: **`transitionMu` → store `saveMu` → `e.mu`**. Store commit methods never acquire `transitionMu`; domain methods never call the store; no code acquires `saveMu` before `transitionMu`. The two mutexes are distinct, so the nested `store.SaveWaitTransition` call does not deadlock.

### D3 — Recovery enters the same guard on the object it adopts

`Scheduler.RecoverExecution` wraps `RecoverableDispatch` + `ClearDispatchIntents` + `s.commit` + publish + `completeIfDrained` in one `execution.RunTransition(...)` call on the execution passed to it (`scheduler.go:145-203`). Instance adoption (`instance.go:485`) stays **outside** the guard: it decides which object becomes shared, and a driver holding the pre-adoption object is rejected by the existing CAS rather than by the guard. This preserves one recovery implementation (ADR 0003 D1/D4) and adds no instance-side wait logic.

### D4 — Rollback never crosses a commit

1. Capture `before` **inside** `RunTransition`, immediately before the first domain mutation; never outside it.
2. `Restore(before)` is permitted only when no durable commit succeeded — i.e. the guard is still held and the store returned an error. A terminal transition that already signalled `Done` keeps the truthful in-memory outcome and is not restored (`timer_poller.go:213-226`).
3. On `storage.ErrConflict`, `WaitTransitionResult.Replay`, `ErrSignalDeliveryConflict`, or any reload path, use the existing durable reload (`store.go:363-369`, `wait.go:288-298`) and `execution.Restore(fresh.Snapshot())` **instead of** `Restore(before)`. This refreshes instead of poisoning the live copy.
4. After a successful commit, no rollback path exists. `setRevision`/cache replacement already happened (`store.go:1113-1117`); publish failure returns `DispatchPublishError` and is reconciled from the committed intent (`wait.go:95-108`, `:300-313`).
5. The same rule applies to `EnterWait` (`wait.go:185-195`) and `CancelExecution` (`scheduler.go:345-388`).

### D5 — Required tests are at the scheduler boundary plus one execution-boundary guard test

See the "Test plan" section. The primary regression is signal-vs-poller on one shared `*Execution`; no new store test is needed because cross-store/CAS behavior is already covered (`store_cas_test.go:24-200`).

### D6 — No contract or operator change

`contracts.WaitCommitter` (`nore/internal/contracts/wait-committer.go:18-34`) and `contracts.TransitionCommitter` (`transition-committer.go:25-43`) are unchanged. `WaitStore` (`scheduler/wait.go:78-88`) is unchanged. Store/storage/backend signatures are unchanged. No status value, event type, wire field, CLI command, config key, or query contract changes. The only observable difference is the absence of duplicate/inconsistent transition publications; that is a correctness fix, not a contract change.

## Alternatives considered

1. **B alone — durable re-read + strict expected-revision CAS.** Rejected as the sole fix: it does not stop two goroutines from mutating one live object, because `before`/`Restore` and the domain mutations would still interleave outside any store lock, and the CAS precondition would still be read from an object whose revision a peer advances (`store.go:469`, `:1113`). It also cannot prevent a stale `before` from being restored after a peer commits. Retained only as the cross-value backstop for which it is already proven (`store_cas_test.go:147-200`).
2. **Copy-on-transition (stronger B): always `Reload`, mutate a private copy, commit with CAS, then adopt.** Rejected: it replaces the shared-object model wholesale, duplicates the live-cache/adopt lifecycle in every driver, widens the diff into `instance.go` and the engine, and can replace a fresher cached object with a stale adoption. It solves more than T-041 needs and creates a second representation of "the live execution".
3. **Store-keyed per-ID lock map with a `WithExecutionTransition` callback API.** Rejected: adds a method to the store and to `WaitStore`/committer contracts, and its authority is the store instance rather than the shared object; the CAS already handles distinct-store races. More API surface for the same guarantee.
4. **Reuse `e.mu` instead of adding `transitionMu`.** Rejected: `e.mu` is held inside every domain mutator and Go mutexes are not reentrant, so wrapping the composite would deadlock; refactoring every mutator into an `...Locked` variant is a larger, riskier change than one additional mutex.
5. **Add a scheduler-local mutex map.** Rejected outright: two scheduler instances would have independent maps and would not serialize (violates "no hidden coupling" and the required boundary test).

## Consequences

- Positive: one composite transition per execution value at a time; losers rejected before mutation and publication; rollback cannot overwrite a committed peer; recovery and external drivers use one guard; no new package, interface, contract, or status value.
- Negative: callers must be audited so every shared-execution driver enters the guard; a missed call site keeps a partial second concurrency regime. The guard is non-reentrant, so nested wraps must be removed rather than added.
- Neutral: `saveMu` and the CAS remain; direct store unit tests are unchanged. The already-observed T-042 dispatch-claim flake is unrelated and is not fixed here.

## Boundary and dependency impact

- `execution` internal: add `transitionMu` and `RunTransition`; no new import, no new dependency, no new exported type. Domain methods remain in `wait.go`/`execution.go`.
- `scheduler → execution`: unchanged direction; drivers call the existing execution value's guard.
- `instance → scheduler`: unchanged; adoption stays at `instance.go:485`.
- No new edge to `store.go`, storage backends, `contracts`, `event`, or `api`.
- Files expected to change in T-041 (implementation, not this task): `execution.go`, `store.go` (doc comments only), `scheduler/wait.go`, `scheduler/timer_poller.go`, `scheduler/scheduler.go`, and scheduler/execution test files. Folder layout unchanged.

## Exact implementation boundaries (T-041 lease)

In scope: `nore/internal/execution/execution.go`, `nore/internal/execution/store.go` (comments only), `nore/internal/execution/scheduler/wait.go`, `nore/internal/execution/scheduler/timer_poller.go`, `nore/internal/execution/scheduler/scheduler.go`, `nore/internal/execution/*_test.go`, `nore/internal/execution/scheduler/**/*_test.go`.

Out of scope: `nore/internal/storage/**`, `nore/internal/contracts/**`, `nore/internal/instance/**`, `nore/internal/api/**`, all SDKs, configuration, CLI, and T-042's dispatch-claim flake. `T-040b` instance recovery work (`nore/internal/instance/recovery_wait_test.go`, currently uncommitted) has a disjoint lease and is not touched.

Drivers that must enter the guard (every committer to a shared `*Execution`): `EnterWait` (`wait.go:143-197`), `ResolveSignal` (`wait.go:206-286`), `processDueTimer` (`timer_poller.go:61-239`), `CancelExecution` (`scheduler.go:340-389`), `RecoverExecution` (`scheduler.go:145-203`), and the four top-level `Run`-loop event entry points that own the five `s.commit` sites: `onExecutionStarted` (`:400-463`, commit `:454`), `onCapabilityCompleted` (`:465-547`, commit `:538` or `completeIfDrained` `:536`), `onCapabilityDetached` (`:258-314`, commit `:306` or `completeIfDrained` `:313`), and the `capabilityFailed` select case (`:226-234`, via `failExecution` `:620`). `completeIfDrained` (`:554-581`, commit `:569`) and `failExecution` (`:601-627`, commit `:620`) stay guard-oblivious: they never acquire `RunTransition` and run inside the caller's guard. No new file or path is added to the lease; this decision only changes which existing handlers must call the guard.

## Test plan

1. **Execution boundary** — `nore/internal/execution/` (new focused test, e.g. `transition_guard_test.go`): concurrent `RunTransition` callbacks on one `*Execution` do not overlap; a `Restore` inside a guarded failure cannot lower a revision committed by a preceding guarded success; `RunTransition` is non-reentrant (documented, not tested by deadlock).
2. **Scheduler boundary — primary regression** — `nore/internal/execution/scheduler/` (new `concurrent_wait_test.go`, using `waitHarness` at `wait_test.go:17-65`, `recordingBus` at `:120-128`, `recordingWaitStore` at `:110-118`):
   - Park one execution in `CapabilityWaiting` with `WaitTypeSignal` and an elapsed `WaitUntil`; start `ResolveSignal` and `PollDueTimers` at a shared barrier; assert **exactly one** `CapabilityReady` publish and one committed transition; the loser returns a non-waiting/replay error without publishing.
   - Same-capability variant: signal vs deadline-expiry (`fail` policy) → assert no `CapabilityReady` after `ExecutionFailed` and no dispatch intent for a failed execution.
   - Different-capability variant on one object: signal resumes capability A while capability B's deadline fails → assert one terminal event and no inconsistent ready event/intent.
   - Recovery variant: `RecoverExecution` vs `ResolveSignal` on the adopted object → exactly one dispatch/publish.
   - Use `sync.WaitGroup` + a `start` channel; collect results in buffered channels; never call `t.Fatalf` from a goroutine; assert after `Wait`. This matches the existing cross-store race-test shape (`store_cas_test.go:37-79`).
3. **Rollback boundary** — scheduler fake that injects `ErrConflict`/`ErrWaitTimerLeaseLost`/replay after a peer commits (pattern: `commitFaultStore` at `timer_poller_test.go:216`; `cancelCaptureStore` at `cancel_wait_test.go:30`): assert the cached object's revision/status equal the winner's durable snapshot, not the loser's `before`.
4. **Race execution**: `cd nore && GOWORK=off go test -race -count=50 ./internal/execution/scheduler -run 'Concurrent|Signal|Timer|Recover'` and the full `./internal/execution/... ./internal/instance/...` race suite from `.agents/ledger.yaml:477-478`.
5. **No nested transaction / no publish-in-transaction assertion**: code review of the diff against `store.go:156-157`, `:1033`, and the publish sites (`wait.go:282`, `timer_poller.go:235`, `scheduler.go:191`, `:385`).

## Operator / public contract impact

None. No public SDK API, CLI, configuration, protocol, event schema, status value, or query field changes. Operator-visible status semantics remain those of ADR 0003 D2 (`.agents/decisions/0003-wait-recovery-semantics.md:46-50`). The change removes an incorrect second publish; it does not add or rename an observable operation.

## Migration plan (ordered, reversible, each step keeps the system working)

1. Add `transitionMu` and `RunTransition` to `Execution` with the lock-order and non-reentrancy contract; add the execution-boundary unit test. No caller changed yet, so behavior is unchanged.
2. Wrap `ResolveSignal` (move `before` inside the guard; reload on conflict/replay; publish outside the guard only on success). Run the focused signal tests.
3. Wrap `processDueTimer` (claim/renew stay before the guard because they are timer leases, not execution state; `before` inside; reload on conflict/replay; publish outside). Run focused timer tests.
4. Wrap `EnterWait` and `CancelExecution` using the same rollback rule; run wait/cancel tests. Cancellation's intentional scope cancel-before-mutate (`scheduler.go:360-367`) is preserved.
5. Wrap `RecoverExecution` including `ClearDispatchIntents` and `completeIfDrained`; adoption remains in the instance. Run recovery tests.
6. Add the concurrent regression and rollback tests; run the two ledger verification commands (`ledger.yaml:477-478`), `go build ./...`, `gofmt -l`, `git diff --check`.
7. Correct the two concurrency doc comments (`store.go:32-45`, `execution.go:163-165`) to state that same-value writers are serialized by `RunTransition` and that CAS is the cross-value backstop. No signature change.
8. Coordinator integration: reviewer + architect + security sign-off, then ledger evidence. Do not wire T-025/T-027 production callers until steps 1-7 are verified.

## Blockers

- **B1 (test-infrastructure, not a design blocker):** the exact T-041 verification pattern `-run 'Concurrent|Signal|Timer|Recover'` also selects `TestConcurrentClaimDispatchIntentWinnerLoserAndRecovery`, a known flake owned by T-042 (`.agents/ledger.yaml:484-505`). Observed in this review: first run **FAIL** ("more than one concurrent claim won", `dispatch_intent_test.go:262`), immediate rerun **PASS**, narrow pattern excluding `Concurrent` **PASS**. Impact: T-041's stated verification command can fail for an unrelated reason. Resume condition: T-042 fixes the flake, or the coordinator records a scoped verification command for T-041 that excludes that test and records the T-042 failure separately. Do not attribute that failure to T-041.
- **B2 (sequencing):** T-040b is in parallel with disjoint lease (`nore/internal/instance/**`). T-041 step 5 touches recovery behavior at the scheduler boundary; integration must re-run instance recovery tests after T-040b lands.
- No source, dependency, or environment blocker to the design itself.

## Risks

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| A driver commit site is missed and keeps a second concurrency regime | medium | high | Enumerate all nine committers in "Exact implementation boundaries" (five wait drivers, recovery, four `Run`-loop entry points); grep `SaveWaitTransition`/`s.commit` across the whole scheduler; reviewer checks each; no residual regime |
| Accidental nested `RunTransition` deadlock | medium | high | Document non-reentrancy; wrap once at driver entry, not per store call; grep for nested wraps in review |
| Holding the guard across blocking event publish | low (decision keeps publish outside) | medium | Review the publish placement; no performance claim without measurement |
| `Restore(before)` still used on a conflict path | medium | high | Step 4 rule D4.3; test asserts cached revision equals winner's durable revision |
| T-042 flake obscures T-041 verification | high | low | B1; scoped command + explicit ledger record |
| PostgreSQL parity claims | n/a | n/a | None asserted; no DSN available |

## Sign-off conditions for T-041 implementation

Architecture sign-off requires all of: (1) `transitionMu`/`RunTransition` on the `Execution` value with documented lock order and no scheduler-local lock registry; (2) every committer in the "Exact implementation boundaries" section enters one guard — the five wait drivers, recovery, and the four `Run`-loop event entry points — while `completeIfDrained` and `failExecution` remain guard-oblivious (no nested `RunTransition`); (3) `before` captured inside the guard and reload-on-conflict replacing stale restore; (4) publish outside both guard and transaction, only by the committing winner (Run-loop handlers collect events and publish after the guard is released); (5) the signal-vs-poller, rollback, and Run-loop-handler-vs-wait-driver regressions under `-race -count=50`; (6) no change to store/contract/storage/SDK/operator contracts; (7) B1 recorded honestly. Security sign-off is required because this is a concurrency/lifecycle change (`.agents/ownership.yaml:49-70`, `:394-403`).

## Verification performed for this ADR (read-only)

- `git status --short --branch` → `## feature/durable-signals-timers`; `git rev-parse --short HEAD` → `cf3e6a6`; 5 uncommitted paths (`todos/TODO.md`, `.agents/decisions/0004-wait-transition-serialization.md`, `.agents/reports/T-005-todos-docs.md`, `.agents/reports/T-040b.md`, `nore/internal/instance/recovery_wait_test.go`), none outside the coordination/report surface and the disjoint T-040b lease.
- T-040b (B2): read-only inspection of `nore/internal/instance/recovery_wait_test.go:148-279` at this HEAD confirms the two instance-boundary recovery tests assert the ADR 0003 D5.3 contract (non-due signal wait stays `StatusWaiting`/`CapabilityWaiting` with zero publish/intents; elapsed timer and retry waits dispatch exactly once) through `Instance.RecoverExecution`, with no production instance code changed. Architect sign-off granted; no source, ledger, or report edited here.
- `cd nore && GOWORK=off go test -race -count=1 ./internal/execution/... -run 'Signal|Timer|Recover|Concurrent'` → **FAIL**: `--- FAIL: TestConcurrentClaimDispatchIntentWinnerLoserAndRecovery (0.19s)` / `dispatch_intent_test.go:262: more than one concurrent claim won`; `engine` and `scheduler` ok.
- Immediate rerun of the identical command → **PASS** (execution 1.351s, engine 1.087s, scheduler 1.281s) — confirms flake, not a deterministic T-041 failure.
- `cd nore && GOWORK=off go test -race -count=1 ./internal/execution/... -run 'Signal|Timer|Recover'` → **PASS** (execution 1.170s, engine 1.052s [no tests to run], scheduler 1.199s).
- `scripts/script.sh` not run: this is a read-only design task and the script mutates module files (same reason recorded in `.agents/reports/T-024c1b1.md:26`). No Postgres behavior asserted.

## Open questions

- **Resolved (architect, T-041-design-close): the five `Run`-loop `s.commit` sites (`scheduler.go:306`, `:454`, `:538`, `:569`, `:620`) are in T-041 scope — no residual second regime.** Observed at `cf3e6a6`: the four `Run` select cases (`:212-242`) are single-threaded among themselves but commit the same live `*Execution` driven by the four external wait drivers; guarding only those drivers would leave the exact cross-driver TOCTOU/stale-rollback defect live. Feasibility confirmed: one wrap per top-level entry point works without nesting if `completeIfDrained` (`:554-581`) and `failExecution` (`:601-627`) stay guard-oblivious and `failExecution`'s by-ID reload (`:602`) moves to the guarded entry point so the guard is taken on the same shared object. **Completion condition:** all four entry points enter `RunTransition` on the loaded execution; helpers never self-wrap; publishes occur after guard release (bus delivery is a buffered channel send, `nore/internal/event/bus.go:34-46`, so no callback reentrancy but I/O stays out of the critical section); one regression test proves a Run-loop handler racing a guarded wait driver yields one commit and no duplicate publish. No path is added to the lease.
- The `RecoverableDispatch` re-dispatch of an already-`CapabilityReady` capability (`execution.go:466-468`; ADR 0003 `:90`) can still produce a second `CapabilityReady` if recovery is invoked after a live dispatch. Recovery is documented as startup-only (`scheduler.go:143-144`), so this ADR does not change it; track separately if recovery ever becomes concurrent with live execution.
- `StatusRetryWait` remains an unused constant (`execution.go:25`); unrelated cleanup, not in scope.
