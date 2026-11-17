# Review — T-038

status: changes_requested
scope: nore/internal/storage/migrate/{migrate.go,registry.go,migrate_test.go} @ ab23e5a
acceptance: partially met

## Summary

The direction is correct: v3 is restored to timer-substrate DDL, v4 performs the legacy
`due_at IS NULL` backfill/quarantine, SQL placeholder selection is now dispatched by
driver, the PostgreSQL transaction-aborting `?` probe is gone, and the SQLite busy retry
is bounded. I found no source-level correctness blocker in the reviewed code.

Changes are requested because required verification is missing or misleading: the
PostgreSQL DSN-gated migration tests are an unconditional skip placeholder, the new
SQLite retry path is never exercised by a test, no test guards v3 DDL-only immutability,
and the duplicate-column regression test would pass against the old implementation.
Acceptance criterion 4 is therefore not met; acceptance criterion 2 is implemented but
its concurrency/immutability evidence is incomplete (PostgreSQL not executed).

## Findings

### [major] PostgreSQL DSN-gated migration tests are a placeholder that never runs
- file:line: nore/internal/storage/migrate/migrate_test.go:193-199; ADR 0002:94,156,179
- problem: `TestMigratePostgresDSNGated` skips when `NEURON_TEST_POSTGRES_DSN` is unset
  and then unconditionally skips again when it is set ("not implemented in this lease").
  There is no PostgreSQL migration test anywhere in the repository. The ADR requires
  DSN-gated tests for populated-v2→v4, already-v3→v4 repair, concurrent startup, and
  idempotent re-run, plus coverage that the PostgreSQL path only uses `$n` placeholders.
- impact: PostgreSQL `$1/$2` backfill, advisory-lock lifetime, pinned-connection
  transaction boundaries, and concurrent startup remain verified by code inspection only,
  not by an executable test. The placeholder also makes `go test` report SKIP even in an
  environment that has a usable DSN, so "executed" status can never be recorded. This is
  a false sense of coverage for a migration path that runs automatically on startup.
- required change: implement the ADR-required DSN-gated PostgreSQL tests against a
  dedicated test database with table cleanup, skipping with an explicit message only when
  the DSN is absent. If that is intentionally a separate task, remove this placeholder and
  record the verification as an explicit limitation rather than presenting it as a test.
- blocks acceptance: yes (criterion 4: "Postgres migration tests are DSN-gated and record
  unavailable/executed status honestly" — only the unavailable half exists).

### [major] SQLite concurrency/retry behavior is not proven; runner still relies on caller DSN for immediate transactions
- file:line: nore/internal/storage/migrate/migrate.go:158-190; nore/internal/storage/migrate/migrate_test.go:127-171 (DSN at :142, `SetMaxOpenConns(1)` at :147); production DSN nore/internal/storage/sqlite/sqlite.go:52; ADR 0002:76-83,93,137,154
- problem: The new bounded `SQLITE_BUSY` retry is not exercised by any test. The only
  concurrency test opens every handle with `_txlock=immediate` and
  `SetMaxOpenConns(1)`, so it almost certainly serializes at the DSN level and never
  enters the retry loop. There is no test for `isSQLiteBusy` classification, non-busy
  error passthrough, context cancellation during backoff, or retry exhaustion. The ADR
  required the runner to force immediate/write-lock serialization, or to make the
  `_txlock=immediate` DSN contract explicit and add a test that omits it; the runner does
  neither and only the production DSN at `sqlite.go:52` carries the setting.
- impact: acceptance criterion 2's "safe against concurrent startup ... on SQLite" is
  supported only for the one production DSN, not by the migration package contract, and
  the newly added retry logic can regress silently. The code appears convergent (a stale
  read followed by a write conflict yields `SQLITE_BUSY`/`SQLITE_BUSY_SNAPSHOT`, the step
  transaction rolls back, and the retry re-reads the version), but that is reasoning, not
  evidence.
- required change: either enforce immediate/write-lock transactions inside
  `migrateSQLiteStep` through a driver-supported mechanism, or document and validate the
  `_txlock=immediate` DSN requirement at the `migrate` boundary. Add a concurrent-startup
  test using a plain DSN (no `_txlock=immediate`, no forced single connection) and
  focused tests for busy retry success, non-busy error passthrough, context cancellation,
  and exhausted retries.
- blocks acceptance: no (the existing concurrency test passes and the code path is
  bounded), but the ADR-required serialization contract is not met; fix before `done`.

### [major] No test proves v3 remains DDL-only
- file:line: nore/internal/storage/migrate/migrate_test.go:76-97,99-125; nore/internal/storage/migrate/registry.go:43-61
- problem: The populated-v2 test inserts fixtures and then jumps straight to v4
  (`MaxVersion: 4`); the v3→v4 test inserts `due_at NULL` rows after v3 has already been
  applied. Neither test asserts the state between v3 and v4. If backfill were reintroduced
  into v3, both tests would still pass because the final v4 state is identical.
- impact: the central acceptance invariant "v3 remains immutable DDL-only" (ADR 0002 §1)
  has no regression guard and is protected only by reading the current code. A future edit
  that makes v3 mutate legacy rows would not be caught, reproducing the exact
  history-split and non-repair problem the v4 migration exists to fix.
- required change: in the populated-v2 test, migrate only to v3, assert version 3 and that
  parseable/unparseable legacy rows still have `due_at IS NULL`, then migrate to v4 and
  assert repair/quarantine. Also assert an existing non-NULL `due_at` is never
  overwritten by v4.
- blocks acceptance: no (current code is DDL-only), but required test for criterion 2.

### [major] Duplicate-column regression test would pass without the change
- file:line: nore/internal/storage/migrate/migrate_test.go:173-191; nore/internal/storage/migrate/registry.go:139-151
- problem: The test triggers `no such table`, which contains neither `duplicate column
  name` (new condition) nor `already exists` (old broad condition). Both the old and new
  implementations return the error, so the test does not prove the suppression was
  narrowed, and it never exercises the duplicate-column branch that must be suppressed.
- impact: the ADR-required guarantee ("do not blanket-match 'already exists'; keep
  idempotence") is not regression-tested. A revert to the broad matcher would leave this
  test green.
- required change: test both branches: (a) a duplicate `ADD COLUMN` is tolerated, and
  (b) an unrelated failure that the removed `already exists` matcher would have swallowed
  is surfaced. If SQLite cannot produce such a message through `execAddColumnSQLite`,
  factor the classification into a helper and unit-test it with the exact candidate error
  strings.
- blocks acceptance: no.

### [major] Backfill parser edge cases that decide scheduling vs. quarantine are untested
- file:line: nore/internal/storage/migrate/registry.go:203-234; nore/internal/storage/migrate/migrate_test.go:244-257
- problem: v4 now applies legacy parsing to every database, including databases already at
  v3, but tests cover only an RFC3339 `waitUntil`, a missing field, and malformed JSON.
  Untested: RFC3339Nano, numeric epoch seconds, numeric millisecond epochs, alternate
  field names (`wait_until`/`dueAt`/`due_at`), empty/invalid strings, non-object JSON,
  null/zero/negative values, huge values, and preservation of an existing non-NULL
  `due_at`. These branches determine whether a legacy timer is scheduled or quarantined.
- impact: a wrong classification can silently schedule a timer at an unintended instant
  or leave it permanently inert. The acceptance contract ("backfilled where parseable,
  otherwise quarantined; no silent scheduling") is only partially evidenced.
- required change: add table-driven tests for each field/format/value case, including
  proof that a pre-existing non-NULL `due_at` is not overwritten and that negative/zero
  numeric values quarantine.
- blocks acceptance: no.

### [minor] Time-dependent fixture can fail on a clock before 2026-11-17T00:00:00Z
- file:line: nore/internal/storage/migrate/migrate_test.go:231-233,265,281
- problem: `legacyDueTime()` is a fixed absolute time, and `assertLegacyTimerBackfill`
  requires `due_at <= time.Now().UTC()` to count the row as schedulable. The previous test
  used `time.Now().Add(-time.Minute).UTC().Truncate(time.Second)`. The new test passes
  only because the authoring clock is after the fixed instant.
- impact: non-hermetic test; can fail on an earlier system clock or when checking out and
  running under a fixed/restored clock, and the failure would be confusing.
- required change: derive the fixture from `time.Now()` (past, truncated) or inject a
  clock; keep the fixed value only for timezone/format assertions.
- blocks acceptance: no.

### [minor] Unreachable retry-exhaustion return and confusing loop counter
- file:line: nore/internal/storage/migrate/migrate.go:164-182 (reset at :170, final return at :182)
- problem: The loop always returns before normal termination: a busy error on
  `attempt == maxBusyRetries-1` returns, and a successful step resets `attempt` to `-1`.
  The trailing `return fmt.Errorf("migrate: sqlite busy: exceeded retries")` is therefore
  unreachable. Reusing the loop variable as both retry counter and progress signal is
  non-obvious and makes the retry budget per-step rather than overall.
- impact: dead code and maintainability risk; future edits may assume a reachable
  exhaustion error or an overall bound.
- required change: use a separate per-step retry counter or an explicit helper/loop,
  remove or make the exhaustion path reachable, and document that the budget is per
  migration step.
- blocks acceptance: no.

### [minor] v3 is not textually identical to 23aca93
- file:line: nore/internal/storage/migrate/registry.go:50-57,113-116
- problem: SQLite v3 now routes `ADD COLUMN` through `execAddColumnSQLite`, and PostgreSQL
  v3 uses `ADD COLUMN IF NOT EXISTS`; 23aca93 used plain `ADD COLUMN` for both. ADR 0002
  step 1 permits keeping idempotent/duplicate-tolerant column handling if semantics are
  unchanged, so this is acceptable, but "restore to the 23aca93 contract" is not literal
  and the ADR itself incorrectly describes 23aca93 as already having `IF NOT EXISTS`.
- impact: low; already-migrated v3 databases are unaffected and fresh schemas are
  identical. Only partial/retried DDL behavior differs (more tolerant).
- required change: record the intentional deviation in ADR 0002 (or align it) so future
  maintainers do not treat textual v3 differences as an error.
- blocks acceptance: no.

## Acceptance criteria

- Legacy timer rows with `waitUntil` are backfilled where parseable, or explicitly
  quarantined with a recorded reason; no silent permanent invisibility — **met for SQLite;
  unverified for PostgreSQL**. `registry.go:164-201` backfills only `due_at IS NULL` rows
  and leaves unparseable rows NULL; v4 is registered for both drivers
  (`registry.go:62-68,126-132`). ADR 0002 §2 records the policy reason (NULL + ADR), and
  `ListTimers` still lists quarantined keys (`sqlite.go:567-569`,
  `postgres.go:563-565`) while `DueTimers`/claim exclude NULL (`sqlite.go:600-625`,
  `postgres.go:593-618`). Tests cover backfill/quarantine on SQLite
  (`migrate_test.go:76-125`). Caveat: there is no per-row reason, count, or log; that is
  explicitly the ADR's policy-level decision, not an implementation defect.
- Migration v3 remains immutable DDL-only; v4 performs legacy repair and is safe against
  concurrent startup on PostgreSQL and SQLite — **partially met**. v3 is DDL-only and
  v4 is ordered after it for both drivers (`registry.go:43-133`). SQLite concurrency test
  passes (`migrate_test.go:127-171`, observed). PostgreSQL lock/backfill structure is
  correct by inspection (`migrate.go:111-155`; `registry.go:191-199`) but not executed;
  SQLite runner does not itself enforce immediate transactions and no test exercises the
  new retry path. No test guards v3 DDL-only (finding 3).
- Populated-v2, v3-to-v4, fresh-to-latest, idempotent, and concurrent-migration tests pass
  — **met**. `migrate_test.go:21-38` (fresh to computed latest), `:40-54` (idempotent),
  `:76-97` (populated v2→v4 and rerun), `:101-125` (v3→v4), `:127-171` (concurrent);
  all observed passing, including `-race` and ten repetitions of the concurrency test.
- PostgreSQL migration tests are DSN-gated and record unavailable/executed status honestly
  — **not met**. `migrate_test.go:193-199` is a placeholder that always skips, including
  when the DSN is present; there is no PostgreSQL migration test.

## Boundary and dependency notes

- The change is confined to `nore/internal/storage/migrate`; it does not cross into
  compiler/executor/runtime/API/CLI boundaries and adds no dependencies. The schema
  version advance to 4 is consumed dynamically by both production constructors
  (`nore/internal/storage/sqlite/sqlite.go:65-72`,
  `nore/internal/storage/postgres/postgres.go:58-65`), so no hard-coded consumer was
  missed.
- Driver dispatch removes the unsafe PostgreSQL `?` probe and uses `$1/$2` only
  (`registry.go:191-199`); this is the correct direction. PostgreSQL lock acquisition
  precedes table/version reads and per-step transactions run on the pinned connection
  (`migrate.go:119-155,312-334`), with best-effort background-context unlock on the same
  connection (`migrate.go:122-126`). This is inspection only, not execution.
- Quarantine state is an existing storage concept (`due_at IS NULL`), not a competing
  representation. No new abstraction was added.
- Documentation consistency is unresolved: `c8bb501` added ADR 0002 but ADR 0001 still
  needs its v3-DDL/v4-backfill status update (ledger T-038 open risk). This is a
  coordinator/documenter follow-up, not a source defect in ab23e5a.

## Test assessment

Covered: fresh migration to v4; v2→v4 backfill/quarantine; v3→v4 repair; idempotent
re-run; concurrent SQLite startup under the production DSN mode; narrow duplicate-column
intent. Missing or weak:

- PostgreSQL migration tests do not exist (finding 1).
- The new SQLite retry path, context cancellation, and retry exhaustion are untested; the
  concurrency test uses `_txlock=immediate` + `SetMaxOpenConns(1)` and likely never enters
  the retry loop (finding 2).
- No test asserts the intermediate v3 state / v3 immutability (finding 3).
- `TestExecAddColumnSQLite_DoesNotSuppressUnrelatedErrors` would pass against the old
  implementation because `no such table` was never matched by either suppression string
  (finding 4).
- Parser coverage omits numeric epochs (seconds and milliseconds), RFC3339Nano, alternate
  field names, empty/invalid values, and non-NULL `due_at` preservation (finding 5).
- The scheduling assertion uses a fixed future-at-authoring-time date instead of a
  relative past time (finding 6).
- The `TestMigrateIdempotent` assertion checks only the recorded version, not that v4
  leaves an already-backfilled/non-NULL row unchanged; the v2 test partially covers the
  backfilled row but not the non-overwrite case.

## Verification observed

- `cd nore && GOWORK=off go test -count=1 ./internal/storage/migrate/...` — ok (0.057s).
- `cd nore && GOWORK=off go test -race -count=1 ./internal/storage/migrate/...` — ok (1.586s).
- `cd nore && GOWORK=off go test -count=10 -run TestMigrateConcurrentStartupSQLite ./internal/storage/migrate/` — ok (0.356s).
- `cd nore && GOWORK=off go test -count=1 ./internal/storage/...` — all packages ok
  (storage 1.637s, migrate 0.089s, postgres 0.023s with contract tests skipped, sqlite
  0.773s).
- `cd nore && GOWORK=off go test -race -count=1 ./internal/storage ./internal/storage/sqlite` — ok.
- `cd nore && GOWORK=off go build ./...` — ok.
- `gofmt -l nore/internal/storage/migrate` — no output.
- `go test -v ./internal/storage/migrate -run 'Postgres|Concurrent|AddColumn'` — concurrent
  and duplicate-column tests PASS; `TestMigratePostgresDSNGated` SKIPs with
  "not run — NEURON_TEST_POSTGRES_DSN unavailable".
- `scripts/script.sh` — not run. It runs `go mod tidy` across modules and the full
  workspace/pnpm gates; a read-only review should not mutate module files. Targeted
  migrate/storage tests and build were run instead.
- PostgreSQL runtime — not executed; no DSN available. No claim of PostgreSQL parity is
  made in this review.

## Residual risks

- PostgreSQL advisory-lock lifetime, pinned-connection transaction boundaries, `$1/$2`
  backfill, v2→v4/v3→v4 repair, and concurrent startup remain unexecuted. The code is
  structurally correct by inspection only.
- `migrateSQLite` retry is bounded, but with the production `_busy_timeout=5000` each
  attempt can block up to 5s and the per-step budget is 50 attempts; four steps can delay
  startup for many minutes before failing. No infinite loop was found, but the worst-case
  startup latency has not been measured.
- v4 selects every `due_at IS NULL` row, accumulates all parseable updates in memory, and
  performs one UPDATE per row inside a single migration transaction. On a large legacy
  timer store this can hold a write lock and grow memory; there is no batching or limit.
- Quarantine has no per-row reason, count, or log. Discoverability relies on
  `due_at IS NULL` / `ListTimers` and the ADR policy; operators have no startup
  diagnostic for how many rows were quarantined.
- ADR 0001 is not updated and the ledger still records T-038 as `needs_review`; the
  architect/security signoffs are pending.

## Verdict

The migration change is directionally correct and I found no source-level blocker in the
v3/v4 ordering, driver-dispatched SQL, PostgreSQL lock shape, bounded SQLite retry, or
narrowed duplicate-column handling. Request changes because the evidence required by
T-038 and ADR 0002 is incomplete: replace the PostgreSQL skip placeholder with real
DSN-gated migration tests (or record it as an explicit unimplemented limitation), add a
plain-DSN SQLite concurrency test plus retry/cancellation tests, add a v2→v3 assertion
that proves v3 is DDL-only, strengthen the duplicate-column test so it fails against the
old broad matcher, and extend parser coverage to numeric/alternate/invalid values and
non-NULL preservation. Acceptance criterion 4 is not met; criterion 2 is implemented but
not fully evidenced; PostgreSQL runtime must remain explicitly unverified.
