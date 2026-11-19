# Review — T-038-review2

status: approved
scope: commit a78a366 — nore/internal/storage/migrate/{migrate.go,registry.go,migrate_test.go,postgres_migrate_test.go}
acceptance: met (Postgres runtime execution remains an explicit DSN-unavailable limitation, not a pass)

## Findings

No blocking, critical, major, or minor defect was found in `a78a366`. Every prior
`changes_requested` finding that is within this task's scope is resolved in code
and/or tests, the tests are non-vacuous at the correct boundary, and the Postgres
execution limitation is reported honestly. The one remaining item from the prior
report (ADR note for v3 duplicate-column tolerance) is a coordinator/architect
documentation action outside this commit and is recorded under Residual risks.

## Prior findings disposition

| Prior finding | Status | Evidence |
|---|---|---|
| [major] Postgres DSN-gated tests were an always-skip placeholder | Resolved | Real 4-test file `postgres_migrate_test.go:111-234`; skip only when DSN empty (`:29-33`); present-but-unreachable DSN hard-FAILs (observed) |
| [major] SQLite concurrency/retry unproven; deferred-DSN contract dishonest | Resolved | Honest DSN contract `migrate.go:59-93`; bounded retry + classifier `:180-239`; production-DSN test `migrate_test.go:169-217`; deferred-DSN guard `:219-265`; retry unit tests `:414-453` |
| [major] No test proves v3 DDL-only | Resolved | `TestMigratePopulatedV2BackfillsOrQuarantinesLegacyTimers` `migrate_test.go:96-136` migrates v2→v3, asserts all legacy rows still `due_at IS NULL`, then v4 repair + non-NULL preservation |
| [major] Duplicate-column test passed against the old broad matcher | Resolved | Tolerated case `:267-283`; unrelated "already exists" case `:285-307` (fails against old matcher); classifier table `:309-328`; narrow matcher `registry.go:140-157` |
| [major] Parser edge cases untested | Resolved | 21-case table `migrate_test.go:332-377`; NaN/Inf/int64-overflow guard `registry.go:216-254` (guard at `:241`) |
| [minor] Fixed wall-clock fixture | Resolved | Relative fixture `migrate_test.go:29-31` |
| [minor] Unreachable retry exhaustion / loop counter | Resolved | `errSQLiteBusyExhausted` sentinel `migrate.go:26-29`, reachable at `:212-214`; separate `busyRetries` counter; test `migrate_test.go:414-453` |
| [minor] ADR note for v3 duplicate tolerance | Out of commit scope; in progress by coordinator/architect | Uncommitted `.agents/decisions/0002-timer-migration-strategy.md` (status `needs_changes`, T-038 architecture review section) |

## Acceptance criteria

- Legacy timer rows backfilled where parseable, otherwise quarantined; no silent invisibility — **met** (SQLite executed). `registry.go:159-214` backfills only `due_at IS NULL` rows with parseable legacy JSON; unparseable rows remain NULL/non-schedulable by ADR policy. Verified by `migrate_test.go:96-165`, `332-377`. PostgreSQL path unexecuted (DSN unavailable).
- v3 immutable DDL-only; v4 legacy repair safe against concurrent startup on PostgreSQL and SQLite — **met in code; SQLite executed, PostgreSQL inspection-only**. v3/v4 ordering `registry.go:44-69,107-134`; PG advisory lock + pinned connection `migrate.go:133-178`; SQLite bounded retry `migrate.go:180-294`; real v3 immutability guard `migrate_test.go:96-136`; production-DSN and deferred-DSN concurrency `migrate_test.go:169-265`.
- Populated-v2, v3-to-v4, fresh-to-latest, idempotent, and concurrent tests pass — **met**. All observed PASS, including `-race` and 15 repetitions of the concurrency/retry/backfill set.
- Postgres migration tests DSN-gated and record unavailable/executed status honestly — **met**. `postgres_migrate_test.go:29-108` skips only when `NEURON_TEST_POSTGRES_DSN` is empty (4 explicit SKIPs observed) and treats connection/setup failure as fatal (hard FAIL observed with a present-but-unreachable DSN); 4 real cases at `:111-234` cover v2→v4, v3→v4 repair, concurrent startup, and idempotent rerun with per-test isolated schemas pinned via `search_path`.

## Boundary and dependency notes

- Scope is confined to `nore/internal/storage/migrate/**`; no source outside the commit was changed. No new dependencies: `github.com/lib/pq` and `modernc.org/sqlite` are already direct dependencies of `nore/go.mod`.
- No boundary crossings, no CLI/API/runtime/persistence-layer changes, and no competing representation: quarantine remains the existing `due_at IS NULL` concept.
- The runner still does not force `BEGIN IMMEDIATE` in code; `a78a366` chose the DSN-contract option explicitly permitted by ADR 0002 required change 1. The contract is now documented (`migrate.go:59-93`) and test-guarded against both the declared production DSN and an unsupported deferred DSN. Architecture sign-off for `a78a366` is not recorded (architecture sign-off is explicitly out of scope for this re-review).
- The production DSN constant `migrate_test.go:24` matches `nore/internal/storage/sqlite/sqlite.go:52` exactly, so the "declared contract" test is honest.

## Test assessment

- Covered at stable boundaries: fresh/idempotent/newer-schema refusal; true v3 DDL-only immutability; v3→v4 repair; backfill/quarantine/schedulability; non-NULL preservation; duplicate-column tolerance and unrelated-error surfacing; 21 parser cases; backfill placeholder dispatch; busy-error classification; retry success/passthrough/exhaustion/cancellation/budget-reset; concurrent startup under the production DSN; deferred-DSN no-silent-corruption guard.
- Would fail without the change: the unrelated "already exists" test fails against the old broad matcher; the v2→v3 guard fails if v3 backfills; the exhaustion test fails without the sentinel/refactor. These are genuine regression guards, not vacuous assertions.
- Missing or unverifiable: PostgreSQL runtime paths (v2→v4, v3→v4, concurrent/advisory-lock, idempotent rerun) — tests exist and are honest but were not executed because no DSN is available. The deferred-DSN test is a negative guard (accepts classified busy/exhaustion/deadline and asserts integrity), not a forward-progress proof; that is consistent with the documented unsupported status. There is still no per-row quarantine reason/log (existing ADR policy decision, not a defect introduced here).
- No existing test was weakened, skipped, or deleted; the old unconditional Postgres placeholder was replaced with real tests.

## Verification observed

- `cd nore && GOWORK=off go test -count=1 ./internal/storage/migrate/...` — ok (0.157s).
- `cd nore && GOWORK=off go test -race -count=1 ./internal/storage/migrate/...` — ok (1.692s).
- `cd nore && GOWORK=off go test -count=1 -v ./internal/storage/migrate/...` — all 13 top-level tests PASS; 4 Postgres tests SKIP with "not run — NEURON_TEST_POSTGRES_DSN unavailable ...".
- `cd nore && GOWORK=off go test -count=15 -run 'TestMigrateConcurrentStartupSQLite|TestMigrateSQLiteWithRetry|TestMigratePopulatedV2|TestMigrateV3ToV4' ./internal/storage/migrate/` — ok (2.434s), no flakiness.
- `cd nore && GOWORK=off go test -race -count=5 -run 'TestMigrateConcurrentStartupSQLite' ./internal/storage/migrate/` — ok (3.256s).
- `NEURON_TEST_POSTGRES_DSN="postgres://u:p@127.0.0.1:1/none?connect_timeout=1" GOWORK=off go test -count=1 -run TestMigratePostgresPopulatedV2ToV4 -v ./internal/storage/migrate/` — FAIL at `create test schema ...: dial tcp 127.0.0.1:1: connect: connection refused`. Confirms a provided DSN never silently skips.
- `cd nore && GOWORK=off go test -count=1 ./internal/storage/...` — all packages ok (storage 0.915s, migrate 0.218s, postgres 0.011s, sqlite 0.480s).
- `cd nore && GOWORK=off go build ./...` — ok (BUILD_OK).
- `gofmt -l internal/storage/migrate` — no output; `git diff --check a78a366` — clean.
- Env check: no `NEURON_TEST_POSTGRES_DSN`/`PG*` variables set — Postgres execution not possible here.
- `scripts/script.sh` — not run (read-only review; the script runs `go mod tidy` across modules and mutates module files). Recorded as a verification limitation, not a pass.

## Residual risks

- PostgreSQL advisory-lock lifetime, pinned-connection transaction boundaries, `$1/$2` backfill, v2→v4/v3→v4 repair, concurrent startup, and idempotent rerun remain unexecuted; correctness there is inspection-only until a DSN is available. Not misrepresented by the coordinator or the tests.
- ADR 0002 status is still `needs_changes` and there is no architect or security sign-off recorded for `a78a366` (the uncommitted ADR review is of `ab23e5a` and refuses sign-off). ADR 0001 also needs its one-line status update. These are process follow-ups outside this re-review.
- With production `_busy_timeout=5000`, a busy step can block up to 5s before each of up to 51 attempts; worst-case startup latency is bounded but unmeasured.
- v4 accumulates all `due_at IS NULL` rows in memory and issues one UPDATE per row inside the migration transaction; no batching/limit. Residual scalability risk documented in ADR 0002.

## Verdict

Approve. Commit `a78a366` resolves all prior in-scope `changes_requested` findings with real, non-vacuous tests at the migration boundary, an honest SQLite DSN serialization contract, reachable bounded retry, a narrow duplicate-column matcher, driver-correct backfill SQL with an epoch-overflow guard, and genuine DSN-gated PostgreSQL tests that skip only when no DSN is supplied and hard-fail when a supplied DSN is unusable. No new defect, regression, or scope violation was found; all locally runnable checks pass, including race and repetition stress. Next safe action: the coordinator obtains architect/security sign-off for `a78a366`, lands the ADR 0002 note and ADR 0001 update, and records the DSN-gated PostgreSQL results when a test database is available; until then PostgreSQL parity must remain explicitly unverified.
