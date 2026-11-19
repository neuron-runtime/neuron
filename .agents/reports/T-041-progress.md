# Defect repair — T-041a

status: needs_review
lease: nore/internal/execution/execution.go; nore/internal/execution/store.go (comments only); nore/internal/execution/scheduler/scheduler.go; nore/internal/execution/scheduler/timer_poller.go; nore/internal/execution/scheduler/wait.go; .agents/reports/T-041-progress.md

Branch `feature/durable-signals-timers`, HEAD `29c3ab0`. This pass completed only the interrupted `scheduler.go` work. No tests were edited, no commit was made.

## Reproduction
- `cd nore && GOWORK=off go build ./internal/execution/...` — exact failure before fix:
  - `scheduler.go:198:6: s.failExecutionTransition undefined (type *Scheduler has no field or method failExecutionTransition)`
  - `scheduler.go:224:68: too many arguments in call to s.completeIfDrained (have (context.Context, *execution.Execution, int, *pendingEvents), want (context.Context, *execution.Execution, int))`
  - `scheduler.go:244:7`, `:251:7`, `:261:6`, `:267:7`: `s.failExecutionGuarded undefined`
  - `scheduler.go:339:57: too many arguments in call to s.completeIfDrained`
  - Same command after fix: no output, exit 0.

## Root cause
- `nore/internal/execution/scheduler/scheduler.go:198,224,244,251,261,267,339` — the prior agent changed callers to a new guarded-commit protocol (`failExecutionTransition`, `failExecutionGuarded`, `completeIfDrained(..., *pendingEvents)`) but never implemented those helpers or wrapped the two `Run`-loop handlers, leaving unresolved symbols, a stale two-arg helper signature, and direct publishes reachable from unguarded handlers.

## Fix
Only `nore/internal/execution/scheduler/scheduler.go` was modified in this pass (ranges below are final file lines):
- `454-526` — `onExecutionStarted`: one `RunTransition` at `:475` covers scope bind, `Start`, entry validation, `MarkCapabilityReady`, and `commit` (`:514`); `CapabilityReady` events are appended via `events.add` (`:518`) and published by `events.publish` at `:525`, outside the guard.
- `528-617` — `onCapabilityCompleted`: one `RunTransition` at `:544` covers terminal check, transition evaluation, `CompleteCurrentAndSchedule`, readiness, commit (`:605`) and `completeIfDrained` (`:603`); readiness events are appended at `:609` and published at `:616`.
- `619-655` — `completeIfDrained`: signature now accepts `*pendingEvents`; `ExecutionFailed`/`ExecutionCompleted` are appended at `:644`/`:651` instead of published; still guard-oblivious (no `RunTransition`).
- `657-704` — `failExecutionTransition`: replaces the old by-ID `failExecution`; guard-oblivious, commits the failure, uses `isDurableRace` + `reloadInto` on conflict (`:687-697`, nil wait-store check prevents a panic in committer-only mode), logs non-race commit failures as before, and appends `ExecutionFailed` at `:703`.
- `706-732` — `failExecutionGuarded`: top-level load + single `RunTransition` at `:722` calling the guard-oblivious helper, then publishes at `:731` (publish error ignored, preserving the old `failExecution` semantics).

Pre-existing unmodified work verified in this pass: `execution.go` (`transitionMu`/`RunTransition` at `:163-173`, `:250-266`), `store.go` (comment-only `:32-45`), `wait.go` (guards at `:185`, `:231`), `timer_poller.go` (guard at `:120`). No other lease path was edited; `execution.go` and `store.go` are unchanged by this pass.

## Regression test
- `nore/internal/execution/scheduler/concurrent_wait_test.go` (untracked, authored by the prior agent; not edited here) — `TestConcurrentWaitDriverRollbackCannotPoisonCommittedPeer` could not build before this fix (package compile failure) and now passes: `cd nore && GOWORK=off go test -race -count=1 ./internal/execution/scheduler -run 'TestConcurrentWaitDriverRollbackCannotPoisonCommittedPeer' -v` → `--- PASS (0.10s)`.

## Guard coverage proof (grep audit)
Grep of `RunTransition(` under `nore/internal/execution/**/*.go` found 10 hits: one definition (`execution.go:262`) and exactly nine committer guards:
1. `wait.go:185` — `EnterWait`
2. `wait.go:231` — `ResolveSignal`
3. `timer_poller.go:120` — `processDueTimer`
4. `scheduler.go:171` — `RecoverExecution`
5. `scheduler.go:313` — `onCapabilityDetached`
6. `scheduler.go:401` — `CancelExecution`
7. `scheduler.go:475` — `onExecutionStarted`
8. `scheduler.go:544` — `onCapabilityCompleted`
9. `scheduler.go:722` — `failExecutionGuarded` (the `capabilityFailed` commit path)

Commit sites (grep `SaveWaitTransition`, `s.commit(`): `wait.go:192/279`, `timer_poller.go:218`, `scheduler.go:416/421/212/335/514/605/642/687` — every one is inside one of the nine guards (`:642` and `:687` are in guard-oblivious helpers called only from guards). No helper calls `RunTransition`. The remaining `SaveWaitTransition` grep hits are test-only fake/override definitions or delegating calls (`cancel_wait_test.go:30/33`, `wait_test.go:115/117`, `concurrent_wait_test.go:74/87`, `timer_poller_test.go:68/72/216`) plus a comment (`timer_poller.go:203`); none is a production commit site.

Publish sites (grep `s.bus.Publish`, `events.publish`, `publishReady`): `wait.go:311` (after guard), `timer_poller.go:168/177/183/192` (closures invoked only at `:260` after guard), `scheduler.go:229/344/439/525/616/731` (all after guard), `scheduler.go:644/651/703/518/609` (event collection only, no bus call). No direct publish inside a `RunTransition` callback.

## Verification
- `cd nore && GOWORK=off go build ./internal/execution/...` → exit 0, no output.
- `cd nore && GOWORK=off go test -count=1 ./internal/execution/...` → ok (execution 0.134s, engine 0.350s, scheduler 1.004s).
- `cd nore && GOWORK=off go test -race -count=1 ./internal/execution/scheduler -run 'Signal|Timer|Recover|Wait|Cancel'` → ok, 1.799s.
- `cd nore && GOWORK=off go test -race -count=1 ./internal/execution/...` → ok (execution 2.189s, engine 1.381s, scheduler 2.072s).
- `cd nore && GOWORK=off go vet ./internal/execution/...` → exit 0, no output.
- `gofmt -l` on all five leased Go files → no output.
- `git diff --check` → no output (DIFF_CHECK_OK).
- `scripts/script.sh` → not run, per task instruction; final gate remains with coordinator.

## Related paths checked
- `nore/internal/execution/scheduler/wait.go` — guarded already; no change needed this pass; no missing guard found.
- `nore/internal/execution/scheduler/timer_poller.go` — guarded already; publish deferred outside guard; no change needed this pass.
- `nore/internal/execution/execution.go` — `RunTransition` and lock-order contract already present and correct; not edited.
- `nore/internal/execution/store.go` — comment-only prior change accurate; not edited.
- `nore/internal/instance/**`, `nore/internal/contracts/**`, storage, SDK, config, CLI, ledger, decisions — untouched (outside lease).

## Risks and limitations
- Production durable mode supplies `WithTransitionCommitter` without `WithWaitStore`; `failExecutionTransition` therefore skips `reloadInto` when no wait store is configured. This avoids a nil dereference but means conflict refresh is only available when the wait store is configured, matching the existing wait-driver assumption.
- Event publication is still a buffered channel send after guard release; no synchronous callback can re-enter a guard, but no performance measurement was taken (out of scope).
- No new test was added in this pass because the brief forbids editing tests; the pre-existing untracked concurrency regression test was exercised, not authored or modified here.

## Discovered but not fixed
- `CancelExecution` (`scheduler.go:424-428`) calls `isDurableRace`/`reloadInto` without checking `s.waitStore != nil`; a durable conflict in committer-only mode could panic. Pre-existing from the interrupted work; changing it is outside the five required completion items — recommend a follow-up bounded fix.
- ADR 0004 test plan items (dedicated execution-boundary guard test, Run-loop-handler-vs-wait-driver regression, injected-conflict rollback test, `-race -count=50`) are not yet implemented; test files are out of scope for this pass.
- T-042's `TestStoppedCapabilityPublishesCancellationNotFailure` flake remains uncharacterized and is unrelated (ledger `:506`).

## Next safe action
- Coordinator: review this diff, then have a coder/tester add the ADR 0004 regression tests (execution-boundary guard overlap, Run-loop handler vs guarded wait driver, injected conflict after a peer commit), run the full `-race -count=50` pattern, obtain reviewer/architect/security sign-off, address the `CancelExecution` nil-wait-store note above, and run `scripts/script.sh` at the final gate.
