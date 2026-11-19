# Security re-review — T-038-security2

- Task: T-038
- Agent: security
- Scope: commit `a78a366` delta in `nore/internal/storage/migrate/**`
- Verdict: **approved**
- Postgres execution: **not executed** — `NEURON_TEST_POSTGRES_DSN` unavailable; all four DSN-gated tests skipped with explicit reason.

## Findings

- No critical, high, or medium findings in the delta.
- No new dependency or trust boundary.
- SQL identifiers are not attacker-controlled; test schema names are validated; row values use `?`/`$n` placeholders.
- No DSN or secret is logged or interpolated into errors.
- Error suppression is narrowed to SQLite `duplicate column name`; unrelated `already exists` errors surface.
- SQLite retry is bounded and context-cancellable; exhaustion is explicit and fail-closed.
- Postgres test isolation uses a unique schema, pinned `search_path`, and drops only that schema.
- Prior medium remains: v4 materializes all `due_at IS NULL` rows and issues per-row updates in one transaction; documented as non-blocking for a one-time idempotent migration.

## Low / informational

- `isSQLiteBusy` matches error text; a timer key containing a busy-like phrase could be retried to bounded exhaustion. Fail-closed.
- Postgres test cleanup uses `context.Background()` without a timeout; test-only availability concern.

## Evidence

- `cd nore && GOWORK=off go test -count=1 ./internal/storage/migrate/...` → ok
- `cd nore && GOWORK=off go test -race -count=1 ./internal/storage/migrate/...` → ok
- Verbose run: SQLite tests pass; four Postgres tests skip with DSN-unavailable reason.
- `git diff --check a78a366^ a78a366` → clean.

## Residual risks

- Postgres advisory lock, `$n` backfill, v2→v4/v3→v4 repair, concurrent startup, and idempotence remain inspection-only until run with a DSN.
- v4 full-scan/materialization remains a documented scalability residual.

## Next safe action

Record approval in the ledger; run the DSN-gated Postgres suite in a DSN-enabled environment before release.
