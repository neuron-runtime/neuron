# 0002 — Timer migration hardening: immutability, v4 backfill, and startup serialization

- Status: accepted
- Date: 2026-11-17
- Accepted commit: a78a366 (T-038 migration hardening). Supersedes the refusals of d295bdf and ab23e5a recorded below.
- Owners: architect
- Related tasks: T-038 (primary), T-022 (23aca93), T-036 (ce13556), T-037 (disjoint), T-024 (out of scope)
- Supersedes: none (amends interpretation of ADR 0001 §D2 as applied to committed migration history; ADR 0001 must be updated for consistency after implementation)

## Context

### Observed facts

**Committed history / migration identity**
- `23aca93` (T-022) introduced migration v3 in `nore/internal/storage/migrate/registry.go:43-68` (SQLite) and `:106-131` (PostgreSQL): adds `due_at`, `owner`, `lease_expires_at`, and index `idx_timer_store_due_key (due_at, key)`. No legacy-row handling. `KVStoreMigrations` is the sole registry (`registry.go:13-136`).
- `ce13556` / ADR 0001 `.agents/decisions/0001-phase5-signals-timers.md:41` states: "Legacy rows: timer rows without `due_at` are **inert by decision**. Optional backfill may parse `waitUntil` where such rows exist, but it is **not silently assumed**." `:101` repeats this and binds v3 to the substrate.
- `d295bdf` (T-038, committed) **mutated the already-defined v3** to add backfill/quarantine and changed runner behavior: `migratePostgres` with session advisory lock and per-migration `applyMigration` (`migrate.go:63-96,98-155,291-313`); `migrateSQLite` with per-step `BEGIN IMMEDIATE`-style loop (`migrate.go:157-225`, but see correctness findings); `execAddColumnSQLite` duplicate-tolerant (`registry.go:138-148`); `backfillLegacyTimerDueAt` + `parseLegacyTimerDueAt` (`registry.go:150-226`). Tests added: populated-v2 backfill/quarantine and concurrent SQLite startup (`migrate_test.go:86-223`).
- Migrate records applied versions and refuses dirty/newer schemas (`migrate.go:14-24,131-140,189-197,269-289,315-326`). A database that has already recorded `schema_migrations.version=3` will not rerun v3 on upgrade because loops skip `m.Version <= current` (`migrate.go:147-155,204-218`). **Observed consequence (from code + stated):** databases already at v3 will never receive d295bdf’s in-v3 backfill/quarantine.

**Current d295bdf defects / risks (observed in code)**
- `backfillLegacyTimerDueAt` tries `UPDATE ... ?` then on any error falls back to `UPDATE ... $1` (`registry.go:183-191`). On PostgreSQL, the first failing `?` statement aborts the current transaction; the `$1` fallback executed in the same aborted transaction cannot succeed. This makes the shared helper unsafe for PostgreSQL inside a migration transaction.
- PostgreSQL runner takes a dedicated `*sql.Conn`, acquires `pg_advisory_lock($1)` and defers unlock via `conn.ExecContext(context.Background(), pg_advisory_unlock($1))` on the same connection (`migrate.go:110-125`), then reads/applies via that connection with `applyMigration` using `conn.BeginTx`-compatible path? `applyMigration` takes `txBeginner` and does `db.BeginTx` (`migrate.go:291-313`); `*sql.Conn` implements `BeginTx`, so per-migration transactions run on the locked session/connection. This is the correct session-lock shape, subject to runtime verification without DSN.
- SQLite runner uses `cfg.DB.BeginTx(ctx, nil)` per step (`migrate.go:173`) and comments claim `BEGIN IMMEDIATE` (`migrate.go:158-164`). **Observed:** Go `database/sql`/`modernc.org/sqlite` do not automatically issue `BEGIN IMMEDIATE` from `BeginTx` without driver/DSN-specific control (e.g. `_txlock=immediate`); the code does not set `sql.TxOptions`/`_txlock` itself. `TestMigrateConcurrentStartupSQLite` passes `_txlock=immediate` in DSNs it opens (`migrate_test.go:183-195`), which may prove serialization for that DSN but does not prove production configurations lacking it are safe. Write-lock serialization is therefore **not guaranteed by the runner as written** (inference from code/driver contract; not executed across DSN variants here).
- `execAddColumnSQLite` suppresses any error whose lowercase message contains `"already exists"` in addition to `"duplicate column name"` (`registry.go:138-148`). `"already exists"` is broader than SQLite’s duplicate-column condition and may mask unrelated DDL failures (e.g. table/object existence errors) — **risk**, not a proven incident.
- `MaxVersion` is honored as a target/ceiling in both drivers (`migrate.go:142-149,199-206`) and applied post-lock/post-current read (`migrate.go:127-140,184-197`). This preserves the existing refusal of newer schemas (`ErrNewerSchema`) and bounded apply; no semantic regression observed, but stepwise SQLite loop means partial progress can commit before hitting target logic only via normal per-step boundaries (acceptable).

**Acceptance constraints (T-038)**
- Legacy rows must be backfilled where safely parseable or explicitly quarantined with a recorded reason.
- Concurrent startup must be safe on PostgreSQL and SQLite.
- PostgreSQL DSN unavailable in this environment; PostgreSQL migration behavior cannot be executed here.

### Forces
- Migrations recorded in committed history and/or already applied are immutable identity points: changing their semantic effect after application is not observable and breaks trust in `schema_migrations`.
- ADR 0001 D2 fixed the v3 substrate contract (`due_at/owner/lease_expires_at` + index) and declared legacy rows inert by decision; hardening must not create competing truths for "version 3".
- Correctness across fresh and upgraded databases is mandatory; a fix that only helps fresh DBs or only helps DBs not yet at v3 is incomplete.
- Driver-specific SQL/transactions must be safe inside a migration transaction (especially PostgreSQL abort semantics).

## Decision

**Adopt option (b): restore v3 to its immutable `23aca93` semantic contract and add a new idempotent migration v4 for legacy backfill/quarantine and any data hardening, with runner/lock correctness fixes applied to the migration engine (not to v3 data semantics).**

### 1) Migration versioning and immutability (authoritative)

- **v3 is immutable after `23aca93`.** It MUST contain only the substrate DDL that `23aca93` defined: add `due_at`, `owner`, `lease_expires_at`, and create `idx_timer_store_due_key`. It MUST NOT perform data backfill/quarantine, because (a) v3 was committed and may be applied, (b) `schema_migrations` prevents rerun for DBs already at v3, and (c) ADR 0001 already states legacy rows without `due_at` are inert by decision.
- **v4 (new, next version) performs legacy timer handling:** for rows in `timer_store` where `due_at IS NULL`, set `due_at` from a safely parseable legacy JSON wait-until field when present; otherwise leave `due_at NULL` as an explicitly quarantined/inert row. v4 MUST be idempotent and safe on re-run/partial prior state (it operates only on `due_at IS NULL`).
- **Why v3 edit is rejected even though it “helps fresh DBs”:** it creates history split (a repo state where “v3” means two different things), does not repair DBs already recorded at v3 (the reported broken case), and violates migration immutability expectations for infrastructure. **Why v4 is required:** it is the only monotonic repair path visible to both fresh installs (run v3 then v4) and DBs already at v3 (run v4 only).

### 2) “Explicitly quarantined with a recorded reason”

- **Recorded reason is satisfied by a durable, repository-documented policy decision, not by a nullable column alone as an undocumented accident.** `due_at IS NULL` is the quarantine/inert *state* (and already excludes rows from `DueTimers`/claim/indexed discovery per substrate semantics); the *reason* MUST be recorded as: (i) ADR text (ADR 0001 D2 + this ADR) stating legacy rows without parseable due time are quarantined/inert-by-decision and not schedulable, and (ii) migration description/comments and code behavior that make the non-schedulable decision explicit.
- **No new schema column/table is required** for T-038 (minimal correct change). Adding `quarantine_reason` would be schema churn and is unnecessary because `NULL due_at` is a sufficient, queryable invariant and the policy is authoritative in ADRs/migration contract. If operators later require per-row diagnostics, that is a separate, deliberate schema ADR — out of scope here.
- v4 MUST NOT silently leave rows in an ambiguous state: every legacy row ends either backfilled with a valid `due_at` (schedulable per normal rules) or `due_at NULL` (explicitly quarantined/inert). No deletion.

### 3) Exact safe backfill strategy (driver-correct)

**Location:** perform parsing/selection in Go over rows fetched in the migration transaction; perform updates via parameterized SQL in the same transaction. (SQL-only JSON extraction differs across SQLite/Postgres versions and risks non-portable/fragile parsing; Go parsing is authoritative and bounded to `due_at IS NULL` rows.)

**Driver-specific parameterization — mandatory:**
- **PostgreSQL:** MUST use `$1, $2` placeholders only. It MUST NOT execute a `?`-placeholder probe inside a transaction (a failed statement aborts the transaction and poisons subsequent statements; observed in `registry.go:186-189`). Provide a Postgres-specific update path.
- **SQLite:** MUST use `?` placeholders.
- Implementation MUST dispatch by driver (e.g. `backfillLegacyTimerDueAt(ctx, tx, drv)` or split helpers) rather than try/fallback across placeholder syntaxes.

**Transaction boundaries:** v4 backfill runs inside the same migration transaction as its schema-recording unit for that step (PostgreSQL: `applyMigration`’s per-migration tx on the locked connection, `migrate.go:291-313`; SQLite: per-step tx, `migrate.go:172-225`). SELECT candidates and UPDATEs are in that tx; commit records schema_migrations for v4. Idempotence predicate `WHERE due_at IS NULL` (and optionally `AND key = ?`) ensures safe retry.

**Parsing (preserve current safe scope):** parse JSON object and accept only known legacy wait-until fields (`waitUntil`, `wait_until`, `dueAt`, `due_at`) with RFC3339/RFC3339Nano strings or numeric epoch as currently implemented (`registry.go:195-226`). Unparseable/absent → quarantine (no update). Do not invent values.

### 4) PostgreSQL advisory-lock correctness

**Observed correct elements:** dedicated `*sql.Conn` (`migrate.go:111-115`), session lock `pg_advisory_lock` on that conn (`:118-120`), deferred unlock on same conn with `context.Background()` so cancellation cannot skip unlock (`:121-125`), migrations applied via `applyMigration` which begins tx on that `txBeginner` conn (`:150-152,291-313`). Lock key derived from table via FNV (`:100-108`).

**Required fixes/confirmations:**
- Keep lock acquisition **before** reading `schema_migrations`/applying steps and release only after all intended migrations complete or on error (current structure is correct: lock held across `ensure/getCurrent/apply` loop).
- Ensure `ensureMigrationsTable`/version reads use the **same locked connection** (current: `conn` passed, correct `:127-135`).
- **Cancellation/failure behavior:** unlock must run on best-effort background context as now; if unlock fails, log/ignore cannot be relied on here but code must not panic — document residual risk of leaked session lock only if connection is returned broken (driver-dependent). On migration error, defer runs (lock released, conn closed) — correct.
- **Recommended hardening:** document that pool size/connection reuse must not bypass session lock (already satisfied by pinning `db.Conn`). Add a DSN-gated concurrency test (below) because behavior cannot be executed here.

### 5) SQLite concurrency

**Observed:** per-step loop opens tx via `cfg.DB.BeginTx(ctx, nil)` (`migrate.go:173`), commits each step, retries (`:157-169`). This gives per-step atomicity but **not an explicit immediate/write reservation** in code. Serialization depends on driver/DSN (`_txlock=immediate` / `BEGIN IMMEDIATE`) and busy timeout; default/deferred transactions can allow read-read then write conflict (`SQLITE_BUSY`) under concurrent starters.

**Required design:** guarantee write-lock serialization at the migration boundary:
- Prefer opening/using a connection configured for immediate transactions for migration steps (e.g. DSN includes `_txlock=immediate` for `modernc.org/sqlite`, or issue `BEGIN IMMEDIATE` via `Exec` on a pinned connection/tx path supported by driver). The runner or documented/constructed DSN contract must make this explicit; do not rely on incidental test DSNs.
- Keep `busy_timeout`/retry policy bounded and documented (existing test uses `_busy_timeout=5000`).
- `TestMigrateConcurrentStartupSQLite` (`migrate_test.go:168-205`) currently proves serialization for connections opened with `_txlock=immediate` and `SetMaxOpenConns(1)` — **insufficient to prove default/production DSN safety**. Strengthen as specified below.

### 6) `execAddColumnSQLite` error suppression and `MaxVersion`

- **`execAddColumnSQLite` (`registry.go:138-148`):** current suppression of `"already exists"` is **too broad**. Restrict tolerated duplicates to SQLite’s known duplicate-column signal(s) (e.g. `"duplicate column name"`; verify exact driver string) and do **not** blanket-match `"already exists"` which can mask unrelated failures. Keep idempotence for concurrent/partially-applied retries without swallowing real errors.
- **`MaxVersion`:** semantics remain correct (ceiling/target, refuse newer schema). Ensure v4 is included in `KVStoreMigrations` so `MaxVersion=4` (and default latest) applies it; `MaxVersion=3` must stop at 3 (allowing controlled staged rollout) — preserve current comparison logic. No change to meaning.

### 7) Tests and verification (required)

- **Unit/integration (SQLite, runnable):** `TestMigratePopulatedV2BackfillsOrQuarantinesLegacyTimers` adapted for v4 path (apply to v2, upgrade to latest/v4, assert parseable backfilled, unparseable/quarantined NULL, idempotent re-run). Add **fresh-vs-upgraded matrix**: (a) fresh DB → migrate to latest includes v3+v4, (b) DB at v3-with-legacy-NULL-rows (simulate post-23aca93 state) → migrate to v4 backfills/quarantines, (c) DB already at v4 → no-op/idempotent.
- **SQLite concurrency:** test concurrent starters with the same DSN/connection mode the runner will rely on in production (and/or assert runner enforces immediate). If runner cannot force immediate generically, document and require `_txlock=immediate` in storage DSN construction and add a test that omits it to demonstrate/guard behavior (expected: either serialized by explicit runner control or a clearly handled retry/error, not silent corruption). At minimum, make the existing test assert the production configuration contract.
- **PostgreSQL (DSN-gated):** `NEURON_TEST_POSTGRES_DSN`-gated tests for: populated-v2→v4 backfill/quarantine, DB-already-at-v3→v4 repair, concurrent startup serialization/advisory-lock contention, and idempotent re-run. Must be skipped with an explicit message when DSN absent and recorded as unverified.
- **Runner safety:** a regression test that a PostgreSQL-targeted backfill path never issues `?` placeholders (code-level dispatch/coverage) and/or uses `$n` only.
- **Error handling:** test that non-duplicate SQLite DDL errors are not swallowed by `execAddColumnSQLite`.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| (a) Retain v3 edit as-is (d295bdf) | Violates migration immutability; does not run for DBs already recorded at v3; PostgreSQL backfill uses a transaction-aborting `?` probe then `$1` fallback in same tx (`registry.go:186-189`); fresh-vs-upgraded correctness not achieved. |
| (c) Out-of-band/manual repair script only | Not a schema-migration guarantee; easy to miss on deployment/startup paths; does not satisfy “concurrent startup safe”/automatic upgrade acceptance for infrastructure. Could complement but not replace v4. |
| Add `quarantine_reason` column/table in v4 | Explicit recorded reason is satisfied by `due_at IS NULL` invariant + ADR/migration contract; extra schema is unnecessary churn now. Rejected as non-minimal; can be reconsidered by future ADR if per-row diagnostics required. |
| SQL-only JSON backfill in migration | Non-portable across SQLite/Postgres and version/extension differences; higher correctness risk than bounded Go parsing of `due_at IS NULL` rows. |
| Keep placeholder try/fallback but “fix” ordering | PostgreSQL transaction is already aborted after first failed statement; fallback cannot be made safe in-place. Must dispatch by driver. |

## Consequences

### Positive
- Restores a single, immutable meaning for v3 across history and applied databases.
- Makes legacy repair observable and automatic for all upgrade paths via v4.
- Eliminates PostgreSQL transaction-abort hazard by driver-correct parameterization.
- Clarifies quarantine semantics as an explicit, durable policy state.

### Negative / obligations
- Requires a follow-up coder change set (revert v3 data logic, add v4, fix runner/backfill) and ADR 0001 consistency update.
- Until implemented, DBs already at v3 remain with inert legacy rows per ADR 0001 decision (known state), and d295bdf’s in-v3 backfill is ineffective for them.
- SQLite serialization guarantee depends on explicit immediate-tx/DSN contract; must be enforced, not assumed.

### Neutral
- Storage/execution semantics unchanged: `due_at NULL` remains non-schedulable.

## Boundary and dependency impact

- **Modules affected:** `nore/internal/storage/migrate/registry.go` (restore v3, add v4), `nore/internal/storage/migrate/migrate.go` (runner locking/tx-mode correctness as needed), `nore/internal/storage/migrate/migrate_test.go` (matrix + concurrency), DSN-gated `nore/internal/storage/postgres` migration/contract tests if colocated there; `.agents/decisions/0001-phase5-signals-timers.md` consistency note (coordinator/documenter-owned per process, but required).
- **Edges:** no new dependency edges; migration remains internal to storage persistence boundary. No runtime/execution/API/CLI/public SDK surface change.
- **Public/internal contract:** schema version advances to v4; `MaxVersion` behavior preserved.

## Migration plan

Ordered, each step leaves the system in a working, explicable state (for implementer/coder):

1. **Restore v3 immutability (DDL-only):** in `KVStoreMigrations` (`registry.go`), revert v3 `Up` for both drivers to `23aca93` semantics — add columns + create index only, no `backfillLegacyTimerDueAt` call, no data mutation. Keep idempotent/duplicate-tolerant column handling only if it does not change semantics and is driver-correct (but see step 3). Ensure descriptions match immutable intent.
2. **Add v4 migration (data/backfill):** append `Version: 4, Description: "backfill/quarantine legacy timer rows with null due_at"` for SQLite and PostgreSQL. `Up` calls a driver-aware, transaction-safe backfill that only updates `WHERE due_at IS NULL` and records quarantine-by-null for unparseable rows (idempotent). No schema changes required.
3. **Fix backfill implementation:** replace shared placeholder try/fallback (`registry.go:183-191`) with driver-dispatched parameterization (`?` for SQLite, `$1/$2` for PostgreSQL). Keep parsing in Go (`parseLegacyTimerDueAt`) with safe, bounded candidate scan. Remove/avoid executing a failing probe statement in a PG tx.
4. **Harden runner:** keep PostgreSQL session advisory lock on dedicated conn, same-conn migration txs, background-context unlock (verify/fix as needed). Make SQLite write serialization explicit (immediate transaction mode via supported driver mechanism or enforced DSN contract) rather than relying on `BeginTx` default; preserve per-step atomicity and bounded busy/retry behavior.
5. **Tighten error suppression:** in `execAddColumnSQLite` (`registry.go:138-148`) suppress only verified duplicate-column errors; do not match generic `"already exists"`. Retain `MaxVersion`/`ErrNewerSchema` semantics.
6. **Update tests:** revise `migrate_test.go` for v4/fresh-vs-upgraded-v3 matrix and concurrency guarantees; add DSN-gated Postgres tests (skipped/unverified without DSN). Ensure tests prove repair for DBs already at v3.
7. **ADR consistency:** update ADR 0001 D2 to state v3 = substrate DDL (immutable) and v4 = legacy backfill/quarantine hardening; restate Postgres parity env-gated and quarantine reason recorded by policy+NULL invariant. (Requires coordinator/documenter action outside this lease if source edits restricted; record as required follow-up.)
8. **Validation:** run targeted migrate/storage tests; record Postgres DSN-unavailable limitation explicitly.

## Exact ordered source changes for coder

| # | File | Symbol/function | Change |
|---|---|---|---|
| 1 | `nore/internal/storage/migrate/registry.go` | `KVStoreMigrations` (SQLite v3, ~`:43-67`) | Restore to 23aca93 contract: only `ADD COLUMN due_at/owner/lease_expires_at` (duplicate-tolerant only if narrowly correct) + `CREATE INDEX IF NOT EXISTS idx_timer_store_due_key`; remove `backfillLegacyTimerDueAt` call and data backfill from v3. |
| 2 | `nore/internal/storage/migrate/registry.go` | `KVStoreMigrations` (Postgres v3, ~`:106-130`) | Same restore for Postgres (`IF NOT EXISTS` columns as 23aca93) + index only; no backfill in v3. |
| 3 | `nore/internal/storage/migrate/registry.go` | `KVStoreMigrations` (both drivers) | Add **v4** migration after v3: `Up` invokes driver-aware legacy backfill/quarantine over `due_at IS NULL` rows (idempotent). Update descriptions. |
| 4 | `nore/internal/storage/migrate/registry.go` | `backfillLegacyTimerDueAt` (~`:150-193`) | Change to driver-dispatched SQL (no `?` then `$1` fallback). Signature may become `(ctx, tx, drv Driver)` or split `backfillLegacyTimerDueAtSQLite` / `...Postgres`; Postgres path uses `$1/$2` only, SQLite `?` only, both `WHERE ... due_at IS NULL`. |
| 5 | `nore/internal/storage/migrate/registry.go` | `parseLegacyTimerDueAt` (~`:195-226`) | Retain safe parsing scope; optional small hardening only if evidence-based (no behavior expansion without tests). |
| 6 | `nore/internal/storage/migrate/registry.go` | `execAddColumnSQLite` (~`:138-148`) | Narrow tolerated errors to verified duplicate-column condition(s); remove generic `"already exists"` suppression. |
| 7 | `nore/internal/storage/migrate/migrate.go` | `migratePostgres`/`applyMigration` (~`:110-155,291-313`) | Preserve dedicated-conn session advisory lock, same-conn txs, background unlock; adjust only if needed for correctness/clarity. Ensure lock spans full apply. |
| 8 | `nore/internal/storage/migrate/migrate.go` | `migrateSQLite`/`migrateSQLiteStep` (~`:157-225`) | Enforce explicit immediate/write-lock serialization (driver-supported mechanism or pinned-conn `BEGIN IMMEDIATE`) so concurrent startup is safe independent of incidental DSN; keep per-step commit/idempotence. |
| 9 | `nore/internal/storage/migrate/migrate_test.go` | existing tests (~`:16-223`) + new tests | Rework populated-v2 test to target v4; add DB-already-at-v3→v4 repair test, fresh-install latest test, idempotence, concurrency under production contract, and non-duplicate-error-not-swallowed case. |
| 10 | `nore/internal/storage/postgres/*_test.go` (or appropriate DSN-gated location) | new DSN-gated tests | `NEURON_TEST_POSTGRES_DSN`-gated: v2→v4 backfill/quarantine, v3→v4 repair, concurrent startup, idempotent rerun. Skip with explicit reason if unset. |
| 11 | `.agents/decisions/0001-phase5-signals-timers.md` | D2 / implementation update (`:36-42,79-105`) | Consistency edit: v3 immutable DDL; v4 performs legacy backfill/quarantine; quarantine reason recorded (NULL + ADR/policy). (If this lease cannot edit ADR 0001, coder/coordinator must do it in a follow-up task.) |

## Risks

| Risk | Likelihood | Impact | Mitigation | Owner |
|---|---|---|---|---|
| PostgreSQL migration/lock/backfill behavior unverified here (no DSN) | Certain (in this env) | High if untested before release | Add DSN-gated tests; run in CI/env with DSN; record limitation until executed. | coder + gate |
| DBs already at v3 retain inert legacy rows until v4 is deployed | High (given 23aca93/d295bdf history) | Medium (timers inert, not corrupted) | v4 auto-repairs on next startup; document state; prioritize rollout. | coder/coordinator |
| SQLite serialization depends on driver/DSN semantics | Medium | High (startup races/corruption risk) | Enforce immediate tx in runner or hard DSN contract + tests proving it. | coder |
| Narrowing `execAddColumnSQLite` could surface previously-masked real errors | Low | Low/Medium (fails fast, desired) | Intentional; covered by tests; improves correctness. | coder |
| Mis-parsing legacy JSON fields | Low | Medium (wrong backfill vs quarantine) | Restrict to known fields/formats, only `due_at IS NULL`, idempotent, tests with fixtures. | coder |

**Residual risk (explicit):** PostgreSQL concurrent-startup, advisory-lock session behavior, `$n` backfill correctness, and v3→v4 repair on Postgres **cannot be claimed verified without a DSN**. Any report must state: "Postgres migration behavior: not executed (DSN unavailable)".

## Verification

### Exact acceptance tests (to add/change)
- `TestMigratePopulatedV2BackfillsOrQuarantinesLegacyTimers` (adapt): v2 populated → migrate to v4/latest; parseable `waitUntil` backfilled; missing/unparseable quarantined (`due_at NULL`); only backfilled rows discoverable by `due_at IS NOT NULL AND due_at <= now`; idempotent rerun.
- `TestMigrateV3ToV4RepairsLegacyNullDueAt`: simulate DB at v3 with legacy rows `due_at NULL` → migrate to v4 repairs/quarantines as specified.
- `TestMigrateFreshToLatestIncludesV4`: empty DB → latest applies v1-v4.
- `TestMigrateConcurrentStartupSQLite`: uses production-equivalent immediate/locked configuration; N starters all succeed; schema at v4 and tables usable.
- `TestExecAddColumnSQLite_DoesNotSuppressUnrelatedErrors` (or equivalent): confirms narrow duplicate handling.
- DSN-gated Postgres: `TestMigratePostgres_PopulatedV2ToV4`, `TestMigratePostgres_V3ToV4Repair`, `TestMigratePostgres_ConcurrentStartup`, `TestMigratePostgres_Idempotent` (names illustrative) — all `t.Skip` with explicit reason when `NEURON_TEST_POSTGRES_DSN` empty.

### Verification commands
```bash
gofmt -l nore/internal/storage/migrate nore/internal/storage/sqlite nore/internal/storage/postgres
cd nore && GOWORK=off go test -count=1 ./internal/storage/migrate/...
cd nore && GOWORK=off go test -race -count=1 ./internal/storage ./internal/storage/sqlite
cd nore && GOWORK=off go build ./...
# DSN-gated (record "not run, DSN unavailable" if unset):
cd nore && NEURON_TEST_POSTGRES_DSN="$NEURON_TEST_POSTGRES_DSN" GOWORK=off go test -count=1 ./internal/storage/postgres ./internal/storage/migrate
```

## Sign-off

**status at time of original decision: needs_review (implementation/refusal pending). SUPERSEDED — see "T-038 architecture re-review — a78a366" below, where architecture sign-off is granted.**

- I **approve the design direction** specified above for T-038: **(b) new v4 backfill/quarantine + restore immutable v3**, driver-dispatched parameterization (no PG `?` probe in tx), explicit quarantine-by-policy+NULL, PostgreSQL session-lock preservation, and explicit SQLite write-serialization.
- I **refuse to sign off** the current d295bdf state as correct/complete because: v3 was mutated post-commit (`registry.go:43-131` vs 23aca93 contract), DBs already at v3 will not be repaired, PostgreSQL backfill fallback is transaction-unsafe (`registry.go:186-189`), SQLite immediate-lock guarantee is not established by runner code (`migrate.go:173`), and `execAddColumnSQLite` suppression is overly broad (`registry.go:144-146`).
- **Implementation sign-off** (architect/security, per ownership/ledger) may be granted only after the ordered changes and acceptance tests above land and evidence is recorded, with the Postgres DSN limitation stated explicitly if unrun.

## T-038 architecture review — `ab23e5a` (2026-11-17)

- verdict: **needs_review (architecture sign-off refused)**
- scope reviewed (read-only): `nore/internal/storage/migrate/registry.go`, `nore/internal/storage/migrate/migrate.go`, `nore/internal/storage/migrate/migrate_test.go`, `nore/internal/storage/sqlite/sqlite.go:52`, `.agents/decisions/0001-phase5-signals-timers.md`, `.agents/decisions/0002-timer-migration-strategy.md`, commit `ab23e5a`.
- evidence: migrate tests pass (`cd nore && GOWORK=off go test -count=1 ./internal/storage/migrate/...` 0.112s; `-race` 1.571s), storage/build reported passing by task context. **Postgres runtime behavior not executed (no `NEURON_TEST_POSTGRES_DSN`).**

### Decision on required questions

1. **v3 duplicate-column tolerance vs exact plain `ALTER TABLE` from `23aca93` (`registry.go:49-60`, helper `:139-151`; Postgres `:112-123` uses `IF NOT EXISTS`) — acceptable, not an immutability violation.** v3 remains DDL-only and preserves the committed substrate contract (same columns/index, no data mutation/backfill, legacy handling deferred to v4 `:62-67,126-131`). Narrow duplicate-column suppression is limited to `"duplicate column name"` (`:147`) and `execAddColumnSQLite_DoesNotSuppressUnrelatedErrors` guards unrelated failures (`migrate_test.go:173-191`), so partial/retried DDL converges without masking real errors. Immutability applies to migration *semantic identity/effect*, which is preserved.
2. **SQLite serialization (`migrate.go:68-70,158-186,193-246`) — bounded `SQLITE_BUSY` retry + production `_txlock=immediate` is a sufficient *DSN-contract* mechanism for current production code, but insufficient as a runner-enforced guarantee.** `nore/internal/storage/sqlite/sqlite.go:52` opens with `_txlock=immediate` and `db.SetMaxOpenConns(1)` (`:57`), and `TestMigrateConcurrentStartupSQLite` proves convergence under the same mode (`migrate_test.go:127-171`, DSN at `:142`). However, the runner itself still uses `BeginTx(ctx, nil)` (`migrate.go:194`) and does not force `BEGIN IMMEDIATE` on a pinned connection; with a deferred-tx DSN concurrent starters can observe read-read/write conflict behavior that is only probabilistically mitigated by retry. **Required change (blocking):** either (a) enforce write reservation in code for SQLite migrations (e.g. pin a connection and issue/guarantee `BEGIN IMMEDIATE`/equivalent supported by `modernc.org/sqlite` before migration work, or otherwise make step transactions immediate independent of caller DSN), or (b) make the `_txlock=immediate` (+ bounded busy/retry) contract explicit and test-guarded by adding a runner-level test that proves deterministic behavior under the declared production DSN contract and documenting that non-immediate DSNs are unsupported for migration/startup safety. Option (a) is preferred for infrastructure correctness.
3. **`TestMigratePostgresDSNGated` (`migrate_test.go:193-199`) — blocks sign-off.** When DSN is absent it skips correctly, but when a DSN is set it still skips (`"DSN-gated migration cases require a dedicated test database; not implemented in this lease"`), so it never executes any Postgres case. ADR 0002 required DSN-gated v2→v4 backfill/quarantine, DB-already-at-v3→v4 repair, concurrent startup/advisory-lock serialization, and idempotent rerun, all skipped with an explicit reason when unset and reported as unverified. **Minimum acceptable implementation:** real DSN-gated tests covering (i) populated v2 → v4 backfill/quarantine + schedulability, (ii) schema-at-v3 with legacy `due_at IS NULL` rows → v4 repair, (iii) concurrent `Migrate` starters converge at v4 without dirty/newer-schema errors, (iv) idempotent rerun at v4; use a dedicated isolated schema/database per test (or unique table-prefix/lock namespace) so tests do not collide with shared state; `t.Skip` with explicit reason only when `NEURON_TEST_POSTGRES_DSN` is empty. Until executed, results must be recorded as "Postgres migration behavior: not executed (DSN unavailable)".
4. **v4 full scan/load of `due_at IS NULL` rows (`registry.go:164-203`) — acceptable for a one-time, idempotent migration, with noted residual risk.** It selects only quarantined legacy candidates (not all timers), parses in Go, and issues targeted per-row updates `WHERE ... due_at IS NULL` inside the migration transaction/step, which is rollback-safe per step and bounded by the legacy-null population. No streaming/batching is required for sign-off, but **required documentation/test follow-up (non-blocking to architecture if recorded):** note that memory scales with null-due rows and consider batching if a deployment can have an extreme legacy backlog; correctness is preserved because rerun is idempotent.
5. **PostgreSQL advisory-lock/runner shape (`migrate.go:101-156,291-334`) — design acceptable; runtime unverified.** Dedicated `*sql.Conn` pinned (`:112-116`), `pg_advisory_lock` on that session (`:118-120`), deferred `pg_advisory_unlock` on the same conn with `context.Background()` (`:121-126`), and migrations/`schema_migrations` reads applied through that same locked connection/tx path (`:128-155`, `applyMigration` `:312-334`, `txBeginner` `*sql.Conn` satisfies `BeginTx`). Driver-dispatched `$1/$2` backfill avoids the prior transaction-aborting `?` probe (`registry.go:191-198`). `MaxVersion`/`ErrNewerSchema`/dirty refusal semantics preserved (`migrate.go:132-149,209-218,269-289,315-326`). **Explicit limitation:** not executed against Postgres in this environment.
6. **Quarantine reason policy (`registry.go:153-163,185-189`, ADR 0002 §2/`registry.go:47-48,110-111`) — consistent and acceptable.** Quarantine state is `due_at IS NULL` (non-schedulable/excluded from due discovery by substrate contract); the reason is durably recorded by repository policy (ADR 0001 D2 + ADR 0002, v4 description "backfill/quarantine legacy timer rows with null due_at", and code comments stating unparseable rows remain explicitly quarantined/non-schedulable). No extra schema column required.

### Per-step SQLite commit/retry correctness
- Per migration step: read state, validate, apply one migration `Up` and record version in the same tx, then commit (`migrate.go:193-246`) — correct step atomicity. Busy contention is retried boundedly (`:163-186`, `isSQLiteBusy` `:188-191`) and `ctx` is honored. `applied==true` resets the retry budget (`:170`) which is reasonable for forward progress but should remain bounded overall by migration count/ctx in implementation; not a current correctness defect. No `ErrDirty`/partial version record is written on failure because step tx rolls back on error (`:199-203,228-236`).

### Ordered required changes (blocking)

1. **SQLite migration serialization must be deterministic, not best-effort retry-only:** enforce immediate/write-reserved migration transactions in runner code for SQLite (preferred) or codify+test the exact production DSN contract so non-immediate configurations cannot silently rely on contention luck. Update `migrate.go` and/or storage DSN construction/docs/tests accordingly.
2. **Implement real DSN-gated PostgreSQL migration tests** (replacing the unconditional skip in `migrate_test.go:193-199`) for v2→v4, v3→v4 repair, concurrent startup/advisory-lock convergence, and idempotent rerun; isolate state per test; skip with explicit reason only when DSN is absent. Record executed/not-executed status as evidence.
3. **After (1)-(2), re-run and record:** `cd nore && GOWORK=off go test -count=1 ./internal/storage/migrate/...`, `-race` as applicable, SQLite storage/migrate suites, and Postgres DSN-gated suites (or explicit "DSN unavailable, not executed" limitation). Update ADR 0002 status to `accepted` only with that evidence.
4. **If sign-off is later granted:** update ADR 0001 D2/implementation notes to state v3 immutable DDL-only, v4 legacy backfill/quarantine landed (commit), T-037 lease renewal/fencing landed, and Postgres migration/runtime parity unverified without DSN (consistency with T-036/T-037/T-038). *(Not performed now because sign-off is refused.)*

### Explicit Postgres limitation statement
**PostgreSQL migration behavior (advisory lock, `$n` backfill, v2→v4, v3→v4 repair, concurrent startup, idempotence) was NOT executed in this review environment because `NEURON_TEST_POSTGRES_DSN` is unavailable; parity/correctness on PostgreSQL remains unverified.**

## T-038 architecture re-review — `a78a366` (2026-11-17)

- verdict: **approved — architecture sign-off granted for `a78a366`** (supersedes the `ab23e5a` refusal). This is architecture sign-off only; reviewer/security sign-off and ledger state remain coordinator-owned, and Postgres runtime remains unexecuted (see limitation).
- scope reviewed (read-only): commit `a78a366` diff vs `ab23e5a`; `nore/internal/storage/migrate/migrate.go`, `registry.go`, `migrate_test.go`, `postgres_migrate_test.go`; production DSN `nore/internal/storage/sqlite/sqlite.go:52,57`; `nore/go.mod:8`. No source edits made by this review.
- diff is confined to `nore/internal/storage/migrate/**` (4 files; +745/−78). No new dependency (`math` is stdlib; `github.com/lib/pq v1.10.9` at `nore/go.mod:8` was already present). No new boundary edge: the change stays inside the storage persistence boundary; no compiler/execution/API/CLI/SDK surface changed. No competing representation or duplicated migration logic was introduced.

### Blocker 1 — SQLite deterministic serialization: RESOLVED via option (b), explicit + test-guarded DSN contract

- `migrate.go:59-67` documents the exact `Config` requirement: `_txlock=immediate` + bounded `_busy_timeout` + `SetMaxOpenConns(1)`, and explains why the runner cannot force transaction mode (`Migration.Up` receives `*sql.Tx` whose `BEGIN` is issued by `database/sql` at `BeginTx`; only the opening DSN controls it).
- `migrate.go:76-93` documents the concurrency contract and failure behavior: deterministic serialization is a caller DSN contract; deferred/plain DSNs are explicitly unsupported and "must never be treated as serialized"; concurrent starters either converge or return an explicit busy/exhaustion error.
- The declared-contract test `TestMigrateConcurrentStartupSQLite` (`migrate_test.go:165-213`) runs 8 concurrent starters using the exact production DSN string (`sqliteProductionDSN` at `:24`) plus `SetMaxOpenConns(1)` (`:189`) and requires every starter to succeed, then asserts a usable schema. The production DSN is verified at `sqlite.go:52` (`_txlock=immediate`, `_busy_timeout=5000`) and `:57` (`SetMaxOpenConns(1)`) — the test constant matches it verbatim.
- The test that omits the contract exists: `TestMigrateConcurrentStartupSQLiteDeferredDSN` (`migrate_test.go:215-265`) runs 6 starters on a plain/deferred DSN, requires that any failure is a bounded busy/exhaustion/deadline error (not silent corruption), and asserts `PRAGMA integrity_check`, no dirty rows, and a recorded version in range (`:541-576`).
- `errSQLiteBusyExhausted` (`migrate.go:26-29`) is now reachable at `migrate.go:212-214` (previously dead code) and proven by the `exhaustion is reachable` subtest (`migrate_test.go:450-462`). Retry logic was extracted into `migrateSQLiteWithRetry` (`migrate.go:194-222`) with an injected delay, enabling deliberate unit coverage of busy-then-success, non-busy passthrough, exhaustion, cancellation, and per-step budget reset (`migrate_test.go:414-502`). Busy classification is expanded at `migrate.go:224-239` and table-tested at `migrate_test.go:390-412`.
- This satisfies ADR 0002 §7 and the `ab23e5a` required change (1) option (b): the contract is explicit, documented, and test-guarded, and non-immediate DSNs have defined, non-corrupting failure behavior.

### Blocker 2 — Real DSN-gated Postgres tests: RESOLVED

- The unconditional-skip placeholder is gone. `setupPostgresMigrationTest` (`postgres_migrate_test.go:26-71`) skips **only** when `NEURON_TEST_POSTGRES_DSN` is empty (`:31-34`); a supplied DSN that fails to connect/setup is fatal (`:42-69`), so "executed" status can now be recorded when a DSN exists.
- All four ADR-required cases exist: `TestMigratePostgresPopulatedV2ToV4` (`:111-132`), `TestMigratePostgresV3ToV4Repair` (`:137-159`), `TestMigratePostgresConcurrentStartup` (6-way; `:164-211`), `TestMigratePostgresIdempotent` (`:215-234`).
- Isolation is per test: a unique schema `neuron_migrate_test_<pid>_<nanos>` is created (`:37-49`), validated by `isSafeSQLIdentifier` (`:95-106`), dropped on cleanup (`:50-59`), and pinned on every connection through `search_path` in the DSN by `withSearchPath` (`:61,73-93`) — including connections opened during `Migrate` and advisory-lock handling. This prevents collision with shared/public schema state.
- Postgres `$n`-only placeholder dispatch is additionally guarded by `TestLegacyBackfillUpdateSQLUsesDriverPlaceholders` (`migrate_test.go:379-388`).
- Observed skip behavior (DSN unset): all four Postgres tests SKIP with the explicit reason `not run — NEURON_TEST_POSTGRES_DSN unavailable` (verbose run output). This is the required honest "unavailable" state.

### Other acceptance criteria — assessed

- **v3 immutability (DDL-only):** `registry.go:44-62` (SQLite) and `:107-126` (Postgres) add only the three columns plus `idx_timer_store_due_key`; no data mutation. Regression guard added in `TestMigratePopulatedV2BackfillsOrQuarantinesLegacyTimers` (`migrate_test.go:93-134`): migrate to v3, assert version 3 and all legacy rows still `due_at IS NULL` (`:108-115`), then v4 repairs/quarantines (`:121-133`). The prior reviewer finding "no v3 guard" is closed.
- **Backfill correctness:** driver-dispatched SQL via `legacyBackfillUpdateSQL` (`registry.go:206-214`) — no PG `?` probe in a transaction. `parseLegacyTimerDueAt` (`registry.go:216-254`) now rejects non-finite and int64-overflowing epochs (`:241`) and normalizes to UTC (`:246,249`); 21 table cases at `migrate_test.go:332-377` cover RFC3339/Nano, seconds/fraction/ms, all four field names, and invalid/huge/zero/negative values. Non-NULL `due_at` preservation is proven at `migrate_test.go:117-126`. The time-dependent fixture was made relative (`migrate_test.go:26-31`), closing the prior minor finding.
- **Advisory lock:** unchanged from the reviewed `ab23e5a` shape (`migrate.go:133-178`): pinned `*sql.Conn`, `pg_advisory_lock` on that session, background-context deferred unlock on the same conn, per-migration tx on the locked connection. Design acceptable; execution still gated on DSN (below).
- **Idempotence:** SQLite rerun at v4 (`migrate_test.go:128-133`) and Postgres rerun (`postgres_migrate_test.go:127-131,220-233`) prove no-op behavior and non-overwrite of an existing non-NULL `due_at`.
- **Duplicate-column narrowing:** `isSQLiteDuplicateColumnError` (`registry.go:148-157`) tolerates only `"duplicate column name"`; both branches are now genuinely tested — tolerated duplicate (`migrate_test.go:267-283`) and surfaced unrelated `already exists` (`:285-307`) — plus a direct classification table (`:309-330`). This closes the prior finding that the old test would pass against the old matcher.
- **Retry exhaustion reachability:** fixed (`migrate.go:212-214`), no dead code.

### New architectural defects introduced by `a78a366`

- None found. The changes are localized hardening plus extraction of testable helpers (`migrateSQLiteWithRetry`, `isSQLiteDuplicateColumnError`, `legacyBackfillUpdateSQL`); no new public surface, no interface change, no boundary crossing, no speculative abstraction, and no folder/layout change. The chosen option (b) is the ADR-sanctioned alternative, not new drift.

### Postgres execution limitation (explicit)

**PostgreSQL migration behavior (advisory lock, `$n` backfill, v2→v4, v3→v4 repair, 6-way concurrent startup, idempotence) was NOT executed in this review environment because `NEURON_TEST_POSTGRES_DSN` is unavailable.** The tests are real and will execute when a DSN is supplied; they are not a placeholder. Structural correctness only is claimed for Postgres. This is an accepted verification limitation, not an architecture blocker; full parity must be executed in a DSN-enabled environment before release.

### Evidence recorded

- `cd nore && GOWORK=off go test -count=1 ./internal/storage/migrate/...` → ok, 0.160s.
- `cd nore && GOWORK=off go test -race -count=1 ./internal/storage/migrate/...` → ok, 1.649s.
- Verbose targeted run: SQLite v3-immutability, deferred-DSN concurrency, duplicate-column, 21 parser cases, and 5 retry subtests PASS; 4 Postgres tests SKIP with explicit reason (DSN unset).
- Coordinator-reported (not re-run here): `-count=10` concurrency ok, `./internal/storage/...` ok, `go build ./...` ok.
- Production DSN contract verified at `sqlite.go:52,57`; dependency verified at `nore/go.mod:8`.

### Residual risks (non-blocking, recorded)

- SQLite serialization is a DSN contract (option b), not runner-enforced; any non-production SQLite opener must set `_txlock=immediate` + `_busy_timeout` + `SetMaxOpenConns(1)` or accept explicit busy/exhaustion failure. Documented at `migrate.go:59-93`.
- Worst-case startup latency under sustained contention (50 retries × up to 5s busy timeout) is not measured; bounded, no infinite loop.
- v4 loads all `due_at IS NULL` rows into memory and issues one `UPDATE` per row in a single migration transaction; no batching. Acceptable for a one-time idempotent migration; batch only if a deployment has an extreme legacy backlog (ADR 0002 §7 already records this).
- Advisory lock key derives from the table name (`migrate.go:121-131`), so concurrent tests in different isolated schemas share one lock key; this serializes rather than collisions — acceptable.

## Open questions

- No blocking architecture questions remain. ADR 0001 has been updated for consistency (v3 immutable DDL-only, v4 backfill/quarantine, T-037 fencing, Postgres unverified).
- Coordinator follow-ups (not architecture decisions): record commit `a78a366` and the accepted sign-off in ledger T-038; obtain reviewer/security sign-off; execute the DSN-gated Postgres tests in a DSN-enabled environment and record the result; run the full validation gate before closing T-038.