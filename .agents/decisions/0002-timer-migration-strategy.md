# 0002 — Timer migration hardening: immutability, v4 backfill, and startup serialization

- Status: proposed
- Date: 2026-11-17
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

**status: needs_review (architecture decision issued; implementation/refusal pending coder work).**

- I **approve the design direction** specified above for T-038: **(b) new v4 backfill/quarantine + restore immutable v3**, driver-dispatched parameterization (no PG `?` probe in tx), explicit quarantine-by-policy+NULL, PostgreSQL session-lock preservation, and explicit SQLite write-serialization.
- I **refuse to sign off** the current d295bdf state as correct/complete because: v3 was mutated post-commit (`registry.go:43-131` vs 23aca93 contract), DBs already at v3 will not be repaired, PostgreSQL backfill fallback is transaction-unsafe (`registry.go:186-189`), SQLite immediate-lock guarantee is not established by runner code (`migrate.go:173`), and `execAddColumnSQLite` suppression is overly broad (`registry.go:144-146`).
- **Implementation sign-off** (architect/security, per ownership/ledger) may be granted only after the ordered changes and acceptance tests above land and evidence is recorded, with the Postgres DSN limitation stated explicitly if unrun.

## Open questions

- None blocking this decision. Implementation must choose the concrete `modernc.org/sqlite` mechanism to guarantee `BEGIN IMMEDIATE`/equivalent in `migrateSQLiteStep` consistent with deployed DSN policy (code-enforced preferred over convention) and document it in tests/comments.
- Coordinator follow-up: update ADR 0001 for consistency (step 11) and record T-038 evidence/commits in ledger once implemented.