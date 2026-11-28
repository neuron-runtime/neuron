# 0010 — Fresh per-invocation attempt identity stable across wait keys

- Status: accepted
- Date: 2026-11-26
- Owners: architect (design authority); coordinator (adoption, ledger, lease)
- Related tasks: T-075 (resume descriptor), T-076 (rotation), T-077 (locked identity reads), T-078 (wire identity)
- Depends on: ADR 0006 (typed outcomes / dispatch claims), ADR 0009 (instance-safe reconciler routing, "no schema change" precedent)
- Branch observed: `feature/phase5-engine-wiring`; uncommitted T-034 resume diff present in contracts, execution, scheduler, engine, plugin
- Lease used for this decision: `.agents/decisions/0010-per-invocation-attempt-identity.md` only. No source, test, config, ledger, report, or git-index change was made by this task.

## Context

### Observed facts (verified against the current tree)

**Protocol contract already requires rotation; N.O.R.E. does not implement it.**

- `shared/protocol/capabilityruntime/v1/capability_runtime.proto:94-97` defines `attempt_id` as the "per-invocation attempt identifier" used for idempotency; it is optional and legacy runtimes receive an empty string.
- `capability_runtime.proto:99-103` defines `logical_operation_id` as a stable identifier "across execution restarts".
- `capability_runtime.proto:105-112` is the binding statement: the resume extension is additive, and "A resumed invocation is a fresh Execute with the same logical_operation_id as the suspended invocation and a new attempt_id."
- `capability_runtime.proto:162-177` defines `ResumeInput.cause`, `signal_name`, `delivery_id`, and `payload`; `:170-173` makes `delivery_id` the client-supplied idempotency key that capabilities must dedupe on.
- `packages/executor-sdks/golang/capabilityruntime.go:302` already forwards `req.AttemptId` into the SDK invocation. No SDK or `.proto` change is needed for this decision.

**There is one persisted attempt identity, documented as rotating but never rotated.**

- `nore/internal/execution/execution.go:194-197` documents `LogicalOperationID` as stable across restarts; `:199-201` documents `AttemptID` as the "per-invocation attempt identifier" that "increments when a capability is re-attempted after a failure or wait".
- `nore/internal/execution/execution.go:239-240` generates both IDs once in `NewExecution`.
- A repo-wide assignment search found **no production write** to `Execution.AttemptID` after construction: only `snapshot.go:116`, `snapshot.go:203` (restore) and `snapshot.go:205` (v1 fallback generation). The "increments" statement at `execution.go:199-201` is therefore **intended behavior, not observed behavior**.
- `nore/internal/contracts/capabilityruntime.go:66-74` exposes both IDs on `ExecutionContext`; the uncommitted T-034 diff adds `Resume *CapabilityResume` at `:79-81`.
- `nore/internal/execution/engine/executor_engine.go:241-253` reads `execution.AttemptID` and `execution.LogicalOperationID` once per `executeCapability` and passes them into each `cr.Execute` call. `nore/internal/plugin/plugin.go:213-224` (diff) puts them into context; `nore/internal/mechanism/process/worker.go:598-633` puts them on the wire.

**`AttemptID` is a durable key root, not merely a wire value.**

- `nore/internal/execution/store.go:917-925` — `DispatchIntent` carries `AttemptID`; `:932-934` and `:942-944` build `dispatch_intents/<executionID>/<capabilityID>/<attemptID>` and `dispatch_ready/<executionID>/<capabilityID>/<attemptID>`; `:968-980` (`NewDispatchIntent`) states the `(execution, capability, attempt)` triple is the intent's stable identity so replaying readiness maps to the same intent.
- `nore/internal/execution/store.go:1347-1356` — `WaitTimerKey(executionID, capabilityID, attemptID)`; its comment at `:1349-1350` says "A resumed capability gets a new attempt ID, so its next wait writes a new timer rather than reusing a stale one." This is a second statement of **intended** rotation.
- `nore/internal/execution/store.go:1361-1369` — `WaitTimerPayload.AttemptID` is persisted in the timer record.
- `nore/internal/execution/engine/executor_engine.go:319-355` — `claimDispatch` reconstructs the intent from `(execution.ID, capabilityID, execution.AttemptID)`, claims it, and the deferred completion closure at `:345-355` calls `CompleteDispatchIntent(..., execution.AttemptID, e.owner)`. Both reads are from the live `execution` at their respective times.
- `nore/internal/execution/scheduler/scheduler.go:190-224` — recovery calls `execution.RecoverableDispatch()`, clears prior intents (`:203-207`), rebuilds intents with `execution.AttemptID` (`:208-210`), commits, then publishes `CapabilityReady`.
- `nore/internal/execution/scheduler/wait.go:116-176` (`EnterWait`) and `:214-271` (`BeginRetryWait`) default `AttemptID` from `req.Execution.AttemptID` and write `WaitTimerKey` plus `WaitTimerPayload`.
- `nore/internal/instance/wait_lifecycle.go:43-67` loads a **fresh execution from the repository** for `EnterWait` and `BeginRetryWait`, so these writers observe the persisted attempt ID, not the engine's in-memory copy.

**Resolution paths compute effect keys after mutating the domain state.**

- `nore/internal/execution/scheduler/wait.go:346-370` calls `ResolveWaitWithResume` at `:352`, *then* computes `attemptID := req.AttemptID; if empty -> req.Execution.AttemptID` at `:355-358`. That value is used for the timer delete key at `:361-368` and for `NewDispatchIntent` at `:370`.
- `nore/internal/execution/scheduler/timer_poller.go:140-224` mutates via `ResumeRetryWait` (`:161`), `ResolveWait` (`:188`), or `ExpireWait` (`:194`), then at `:231-240` builds the timer delete from `timer.Key` and the dispatch intent with `attemptID := payload.AttemptID` (fallback `execution.AttemptID` at `:237`).
- `nore/internal/execution/execution.go:450-532` (`RecoverableDispatch`) describes recovery as at-least-once dispatch "under the same attempt identity" (`:453-456`); the T-034 diff adds resume causes at `:506` and `:513-516`.
- `nore/internal/execution/engine/invocation.go:106-145` retries the same `Execute` closure inline (non-durable mode) under one attempt context; durable retries are deferred by `executor_engine.go:260-276` through `BeginRetryWait`.

**Atomicity and idempotency machinery already exists.**

- `nore/internal/execution/execution.go:267-271` (`RunTransition`) serializes the mutation, snapshot, and commit; lock order is documented at `:263-266`.
- `nore/internal/execution/store.go:1493-1524` (`SaveWaitTransition`) persists the execution revision together with timer, delivery, and intent effects in one `WithTimer` transaction. Signal dedupe by `DeliveryID` + `PayloadHash` is at `:1544-1589`; a duplicate returns `Replay` with no writes.
- `nore/internal/execution/scheduler/wait.go:388-395` and `:423-429` reload durable state on replay; `:400-408` suppresses publication on replay. Failed commits restore the pre-mutation snapshot (`:207`, `:385`) or reload on durable race (`:377-383`).
- `nore/internal/execution/snapshot.go:38-42,92-93,112-117,199-205` persists and restores both IDs; `:70` writes schema version "3"; `:156-168` migrates v1/v2 to v3 and rejects unknown versions. `CapabilityExecutionState.Resume` (diff at `execution.go:110-113`) is an exported field with no custom marshaller, so it serializes inside the `States` map without a schema change.
- `nore/internal/execution/accessors.go:39-45` clones `Resume` on read so callers cannot mutate durable state.
- ADR 0009:13-17,60-66 established a binding preference for internal signature changes with **no durable migration** where the persisted key layout can be kept coherent.

### Intended vs observed vs proposed

- **Intended** (protocol + code comments): fresh attempt ID per resumed invocation; stable logical operation ID; new timer per new wait generation.
- **Observed**: one persisted `Execution.AttemptID`, generated once and never rotated; T-034 carries resume metadata but reuses that ID on every invocation.
- **Proposed** (this ADR): rotate the persisted attempt ID inside the same domain transition that creates a fresh dispatch, and correct effect-key capture order so no timer, intent, claim, or completion key is invalidated.

---

## Decision

### D1 — Identity semantics

| Concept | Owner | Lifetime | Rotation |
|---|---|---|---|
| `LogicalOperationID` | `Execution` (`execution.go:197`) | Whole logical operation, including restarts | **Never** after `NewExecution` (`execution.go:239`) |
| `AttemptID` | `Execution` (`execution.go:201`) | One dispatch generation (one wait/retry/recovery wave) | **Rotates atomically** at every transition that announces a fresh invocation |
| `Resume` | `CapabilityExecutionState` (`execution.go:113`) | One resumed invocation, then cleared when the next wait begins | Set on resolution; cleared by `EnterWait` (`wait.go:244`), `BeginRetryWait` (`wait.go:356`), `clearWaitMetadataLocked` (`wait.go:571`) |

Rotation points (all inside the domain mutator, under `e.mu`, inside the caller's `RunTransition`):

1. **First invocation** — `NewExecution` already generates a fresh ID (`execution.go:240`). No change.
2. **Signal resolution** — `resolveWaitWithResumeLocked` (`wait.go:276-295`).
3. **Timer / deadline resolution** — the same method via `ResolveWait` (`wait.go:259-261`), and deadline-continue via `ExpireWait` (`wait.go:432-435`).
4. **Retry resumption** — `ResumeRetryWait` (`wait.go:322-327`).
5. **Recovery re-dispatch** — `RecoverableDispatch` (`execution.go:482-532`), rotated **once per call** only when at least one capability is returned, so all intents rebuilt by `scheduler.go:208-210` share one coherent generation.
6. **No rotation** at `EnterWait` (`wait.go:195-254`), `BeginRetryWait` (`wait.go:336-357`), `discardClaimedTimer` paths, or inline non-durable retries (`invocation.go:111-143`). These either begin a new wait for the current generation or have no durable transition at which rotation could be made atomic.

`AttemptID` is an **execution-wide generation**, not a per-capability invocation token: capabilities dispatched in the same wave share it. This is the existing model (`store.go:968-980`, `scheduler.go:208-210`) and is not widened here. The wire contract is satisfied because each resumed/retried wave carries a value distinct from the wave that suspended or failed.

### D2 — Rotation mechanism (single authoritative implementation)

Add one private domain helper, e.g. `rotateAttemptIDLocked()`, that sets `e.AttemptID = shared.NewID("attempt_")`. `core.NewID` is entropy-backed (`shared/types/core/identity.go:12-22`, cited in ADR 0009:47), so successive generations cannot collide with persisted timer or intent keys.

Call it only from the five rotation points in D1. Do **not** expose a public `RotateAttemptID` method: every valid rotation is already coupled to a domain state transition, and a public method would permit a rotation with no persisted dispatch (an orphaned generation). Keep `LogicalOperationID` untouched.

Rotation is committed by the *existing* `SaveWaitTransition` transaction (`store.go:1493-1524`) or by the scheduler's existing recovery commit (`scheduler.go:212`). There is no separate write, no second phase, and no background cleanup.

### D3 — Key derivation: capture-before for deletes/completions, use-after for new work

Rotation changes the value of a key root, so effect builders must be explicit about which generation they address. The rule is:

> **Delete or complete under the generation that created the record; create the next timer, intent, and wire context under the generation produced by the transition.**

| Call site | Observed evidence | Required key source after this ADR |
|---|---|---|
| `scheduler/wait.go:346-370` (`ResolveSignal`) | `attemptID` computed after mutation at `:355-358` | Capture `priorAttemptID := req.Execution.AttemptID` **before** `ResolveWaitWithResume`; use it for `WaitTimerKey` delete at `:361-368`. Use post-rotation `req.Execution.AttemptID` for `NewDispatchIntent` at `:370`. `req.AttemptID`, when supplied, remains the explicit key of the wait being resolved (test/injection override), not the new dispatch identity. |
| `scheduler/timer_poller.go:231-240` | `attemptID := payload.AttemptID` at `:235` | Keep `timer.Key` for the owned delete (it already names the old generation). Build `NewDispatchIntent` from post-rotation `execution.AttemptID`, **not** `payload.AttemptID`. The fallback at `:236-238` becomes unnecessary for the intent but may remain for non-dispatch bookkeeping. |
| `engine/executor_engine.go:331-355` (`claimDispatch`) | claim at `:335`, deferred complete at `:348` both read live `execution.AttemptID` | Capture `attemptID := execution.AttemptID` once at the top of `claimDispatch` and use the captured value for both claim and `CompleteDispatchIntent`. Otherwise a concurrent capability's resume can rotate the shared field between claim and completion and orphan the ready entry. |
| `engine/executor_engine.go:241-253` (`executeCapability`) | wire IDs read at `:245` | Keep reading both IDs once at invocation start. Add a comment that this is the invocation's generation snapshot and must not be re-read inside `policy.run`. A concurrent rotation must not change an in-flight invocation's wire identity. |
| `scheduler/scheduler.go:203-210` (recovery) | clear intents then rebuild with `execution.AttemptID` | Keep `ClearDispatchIntents` before intent construction; after D1 rotation in `RecoverableDispatch`, the rebuilt intents use the new generation and stale keys are removed. |
| `scheduler/wait.go:148-186`, `:231-271` (`EnterWait`, `BeginRetryWait`) | default from `req.Execution.AttemptID` | No change. These write the timer for the generation current at call time, which `wait_lifecycle.go:43-67` loads from the repository. |

`ResumeCause` derived at `wait.go:294` and `execution.go:506,513` is orthogonal to attempt identity: it describes *why* the new invocation exists; the rotated ID describes *which* invocation it is.

### D4 — Idempotency and duplicate delivery

- Signal dedupe remains authoritative in `SaveWaitTransition` (`store.go:1544-1589`, keyed by `DeliveryID` + `PayloadHash`). Rotation occurs in the in-memory mutation before the commit, but every non-commit path discards it: `Restore(before)` on ordinary error (`wait.go:385`) and `reloadInto` on replay or durable race (`wait.go:377-383`, `:388-395`, `:423-429`). No replay persists a second rotation or a second dispatch intent.
- `ResumeInput.DeliveryID` (`capability_runtime.proto:170-173`) is the capability-side dedupe key. A capability runtime **must not** treat `attempt_id` as a cross-attempt dedupe key: it intentionally changes on every resume/retry. Cross-restart correlation uses the unchanged `logical_operation_id` together with `delivery_id`.
- `DispatchIntent` fencing is unchanged: release and completion remain owner-fenced (ADR 0009 D2, `store.go:1147-1149,1190-1192`), and the reconciler still never completes intents (ADR 0006 D5). Rotation must not be used as a second fencing authority.
- The ready index remains one-to-one with intents (`store.go:936-966`); because D3 uses the post-rotation value for creation and the pre-rotation value for deletion/completion, no entry is left pointing at a generation the engine can no longer claim.

### D5 — Compatibility, migration, and rollback

- **No protocol change.** `attempt_id` and `logical_operation_id` already exist and are optional (`capability_runtime.proto:94-103`). Wire consumers that ignore them are unaffected; consumers that use `attempt_id` observe the clarified semantics.
- **No SDK change.** `packages/executor-sdks/golang/capabilityruntime.go:302` already forwards the field.
- **No snapshot migration.** `ExecutionSnapshot.AttemptID`/`LogicalOperationID` already exist with `omitempty` (`snapshot.go:38-42`) and restore paths already handle empty values (`snapshot.go:112-117`, `:199-205`). `CapabilityExecutionState.Resume` is an additional optional exported field; JSON unmarshal of older `States` yields nil and `Resume == nil` is the correct first-invocation value. Schema version stays `"3"` (`snapshot.go:70,156-168`). A version bump would force a migration that buys nothing and would break the explicit v3-only tests at `snapshot_v3_test.go:23-24,130-132`.
- **No key layout change.** Timer, intent, and ready-key formats (`store.go:932-944,1351-1356`) are unchanged; only the value of the attempt component changes, atomically with the transition that writes the keys. This follows the ADR 0009:13-17 precedent.
- **Rollback is a plain code revert.** Every persisted attempt ID is accompanied by the snapshot revision that committed it; old code reads that same ID for key derivation and continues to work. There is no dual-read window, no backfill, and no cleanup artifact to reverse. After rollback, resumes reuse the persisted ID (the pre-T-035 defect) — a documented, degraded behavior, not corrupted state.

### D6 — Verification obligations

Tests must be added at the boundary that owns each rule, not only at integration level:

1. Domain (`nore/internal/execution/wait_test.go`, `recovery_wait_test.go`, `recovery_dispatch_test.go`): rotation on `ResolveWait`/`ResolveWaitWithResume`, `ResumeRetryWait`, `ExpireWait`-continue, and `RecoverableDispatch`; no rotation on `EnterWait`/`BeginRetryWait`; `LogicalOperationID` byte-identical throughout; `Resume` cause retained and cleared when the next wait begins.
2. Scheduler (`scheduler/wait_test.go`, `retry_wait_test.go`, `cancel_wait_test.go`, `timer_poller_test.go`, `concurrent_wait_test.go`): old timer key deleted, new intent key created, no orphan ready entry; duplicate `DeliveryID` replay leaves attempt ID and intent set unchanged; a due timer carrying a stale `payload.AttemptID` still produces an intent under the rotated ID.
3. Engine (`engine/dispatch_claim_test.go`, `wait_lifecycle_test.go`): completion uses the attempt ID captured at claim even if another capability rotates the generation; wire `ExecutionContext.AttemptID` changes across resume/retry while `LogicalOperationID` does not; `Resume == nil` on a first invocation.
4. Plugin/transport (`nore/internal/plugin/*`, `nore/internal/mechanism/process/*`): `ResumeInput` contains cause/signal/delivery/payload from the descriptor and the wire attempt ID equals the context value.
5. Snapshot (`snapshot_v3_test.go`): round-trip a rotated attempt ID plus a non-nil `Resume`; a legacy v3 snapshot without `resume` restores to nil without a version bump.
6. Acceptance (`application/internal/acceptance/durable_suspend_test.go`): signal and timer resumes observe a fresh attempt ID, stable logical operation ID, and correct resume input end to end.

Verification commands (from ledger T-035:1655-1658):

```
cd nore && GOWORK=off go build ./... && GOWORK=off go test -run '^$' ./...
cd nore && GOWORK=off go test -count=1 ./internal/execution/... ./internal/plugin/...
cd nore && GOWORK=off go test -race -count=1 ./internal/execution/... ./internal/plugin/...
gofmt -l <changed Go files>; git diff --check
```

## Alternatives considered

- **A — Keep the persisted ID as a key-root and introduce a separate rotating wire attempt ID.** Rejected: creates two competing attempt identities with no authoritative mapping, contradicts `store.go:1349-1350` and `execution.go:199-201`, and still needs stale-timer isolation. A boundary should not exist only to preserve a field name.
- **B — Derive the wire attempt ID from `LogicalOperationID` + `Revision` without persisting it.** Rejected: a second derivation authority; replay and recovery would need a guaranteed mapping from revision to wire value, and the dispatch-intent key would disagree with the wire value.
- **C — Rotate at the plugin/transport boundary.** Rejected: the transport layer does not own dispatch or wait lifecycle; it cannot keep claim/complete and timer keys coherent, and it violates dependency direction (edge policy at the edge only).
- **D — Rotate on every inline non-durable retry inside `invocation.run`.** Deferred and explicitly out of scope: there is no durable transition to make the rotation atomic, and unpersisted fresh IDs would weaken idempotency. Revisit with measurement and a separate ADR if per-`Execute` uniqueness is required.
- **E — Schema migration that rewrites timer and intent keys.** Rejected: unnecessary because rotation is committed in the same transaction as key writes; ADR 0009 already established no-migration as the preferred path for coherent internal identity changes.
- **F — Rotate per capability (state-local attempt ID).** Rejected as larger than the problem: it requires a new persisted field per capability state, engine reads from state rather than execution, and a migration story for existing states, while the protocol requirement (fresh value on resume) is already met by a per-wave generation.

## Migration plan

Implementation is bounded, reversible, and keeps the system working at each step. No durable migration exists.

1. Land the ADR (this document). No source change.
2. Add the private rotation helper and call it from the domain mutation points (D1/D2) **with their tests**. At this step the live code has not yet been given consistent effect-key capture, so do not publish: the helper must ship in the same coherent change as step 3.
3. Update effect-key capture at the sites in D3 (signal resolve, timer poller, engine claim/complete, recovery clear-then-create) in the same lease as step 2. Build and focused domain/scheduler/engine tests must pass before the lease is released.
4. Add snapshot/round-trip and plugin/transport boundary tests (D6 items 4-5).
5. Run the full verification set (D6). Reviewer, architect, and security sign-off remain mandatory (ledger T-035:1646).
6. Rollback, if needed: revert steps 2-4 as one unit. No data operation is required.

## Consequences

**Positive**

- The wire contract at `capability_runtime.proto:108-109` is honored: every resume/retry/recovery invocation carries a new `attempt_id` and the unchanged `logical_operation_id`.
- `execution.go:199-201` and `store.go:1349-1350` stop being aspirational comments and become true behavior.
- Stale timers from a previous generation cannot wake a new wait; the intent claim/complete fence is preserved across concurrent rotations.

**Negative**

- `claimDispatch` and effect builders become order-sensitive: capturing the wrong generation silently loses an intent. D3 and its tests are mandatory, not advisory.
- Capability runtimes that (incorrectly) dedupe on `attempt_id` will now reprocess resumed work. The protocol's `delivery_id` is the documented dedupe key; this must be stated in user-facing docs by a documenter task.
- Inline non-durable retries still share one attempt ID (D, deferred). This is a known, documented boundary of the semantics.

**Neutral**

- No public API, proto, SDK, snapshot schema, or key-layout change.
- The attempt ID remains execution-wide per wave rather than per capability, matching the current storage identity model.

## Boundary and dependency impact

- No package edge added or removed. Domain (`execution`) continues to own attempt identity; `contracts` continues to alias/declare ports (`capabilityruntime.go:60`); the engine continues to translate domain state into `ExecutionContext`; the plugin continues to map context to the wire request.
- No new abstraction. `rotateAttemptIDLocked` is a private helper inside the domain mutators, not a port or service.
- Responsibility stays single-owner: the execution domain rotates and persists identity; effect builders only choose which generation a key addresses.

## Risks

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Timer delete uses the rotated ID, orphaning the old deadline timer | Medium if D3 step 3 is skipped | Spurious resume of an already-resolved wait | Capture pre-rotation ID in `ResolveSignal`; scheduler test asserts the old key is gone and no extra `CapabilityReady` is published. |
| Intent completed under a different ID than it was claimed with | Medium under concurrent resumes | Orphaned ready entry and duplicate dispatch | Capture once in `claimDispatch` (D3); engine race test with two capabilities. |
| Timer poller builds intent from stale `payload.AttemptID` | High without edit | Engine cannot claim the intent; work stalls | Use post-rotation `execution.AttemptID` at `timer_poller.go:239`; due-timer test asserts claimable intent. |
| Recovery rotates more than once per call | Medium if rotation is placed in the per-capability loop | Intents built with a generation no longer in the snapshot | Rotate once per `RecoverableDispatch` call only when `len(dispatch) > 0`; recovery test asserts single new ID across all returned capabilities. |
| Snapshot compatibility broken by adding `Resume` | Low | Restore failures on existing executions | Keep version "3"; omitempty optional field; legacy snapshot test in D6 item 5. |
| Duplicate delivery rotates twice | Low (replay guard exists) | Duplicate dispatch | Verify `Restore`/`reloadInto` paths discard the optimistic rotation; replay test in D6 item 2. |

## Verification

- Static: `gofmt -l`, `git diff --check`, `go build ./...`, compile-only `go test -run '^$' ./...` in `nore`.
- Focused: domain, scheduler, engine, plugin, snapshot, and acceptance tests enumerated in D6.
- Concurrency: `-race` over `internal/execution/...` and `internal/plugin/...`, including the two-capability rotation case.
- Architectural property checked at review: every timer delete / intent completion uses the generation that created the record, and every timer write / intent creation / wire context uses the generation produced by the committing transition. Any test that only checks "an attempt ID changed" without checking key coherence is insufficient.

## Open questions

- Does the coordinator want per-`Execute` uniqueness for inline non-durable retries? This ADR says no (alternative D) because there is no durable boundary; answering yes requires a new measured decision and likely a persisted per-capability counter.
- Documentation of the runtime-side dedupe rule (`logical_operation_id` + `delivery_id`, not `attempt_id`) is a user-facing change; it needs a documenter task before Phase 5 is declared complete.
