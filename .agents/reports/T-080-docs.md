# Documentation — T-080

status: complete

## Updated
- `todos/TODO.md:844-855` — replaced the stale R5-24 note claiming duplicate-timer coverage was "tracked in T-038 and remains pending final validation" with verified scheduler-boundary evidence: T-079's deterministic test, decision 0011's proof boundary, and the explicit statement that acceptance-level duplicate-timer delivery is unproven and out of scope. R5-24 stays unchecked; duplicate-signal coverage remains attributed to T-030.

## Verified against
- `.agents/ledger.yaml:1723-1737` — T-080 scope, acceptance, and open risk that R5-24 must be reconciled after T-079.
- `.agents/decisions/0011-r5-24-duplicate-timer-proof-boundary.md:5-11` — scheduler-boundary proof decision; no public injection hook; R5-24 must not be described as public acceptance coverage.
- `.agents/reports/T-079.md:12-17,32-38` — nondeterministic acceptance test removed; scheduler test added; acceptance-level proof out of scope.
- `nore/internal/execution/scheduler/timer_poller_test.go:991-1145` — `TestTwoPollersWithStaleTimerLeaseCommitOneResume` asserts one `CapabilityReady` (0 stale / 1 replacement), owner-fenced deletes with distinct claim owners (`1082-1084`, `1124-1126`), timer removed (`1127-1129`), and exactly one claimable dispatch intent (`1131-1137`).
- `application/internal/acceptance/durable_signal_security_test.go:20-24` — duplicate-signal acceptance test still present, attributed to T-030 (`2d9fda5`).
- `git diff -- todos/TODO.md` — only the R5-24 block was changed by this task (the diff also shows earlier uncommitted T-080 edits already present in the worktree).
- `git diff --check` — exit 0, no output.

## Not updated
- `README.md`, `docs/**` — no public-surface or behavior claims about duplicate-timer proof are present there; out of lease and out of scope.
- `.agents/ledger.yaml` — coordinator-owned; T-080/T-079 state and sign-offs are not documenter lease.
- Source and test files — read-only evidence; no edits.

## Unverified or ambiguous
- T-032 final Phase 5 validation is still pending; the R5-24 note now says so rather than claiming a completed gate.
- T-079 ledger status is still `in_progress` and its reviewer/architect sign-offs are pending; this documentation records the test evidence, not sign-off completion.

## Links and examples checked
- `nore/internal/execution/scheduler/timer_poller_test.go` — path exists and contains the named test.
- Decision 0011 and T-079 report — paths exist and were read in full.
- No new commands, flags, paths, or defaults were introduced in the documentation.

## Next safe action
- Coordinator: obtain outstanding T-079/T-080 sign-offs, reconcile ledger evidence, and run the T-032 final validation gate.
