# Security final review — T-022-security-final

- Task: T-022 (as hardened by T-037 `f82ffa6` and T-038 `a78a366`)
- Agent: security
- Scope: timer storage substrate, ownership/fencing, migrations, backend parity
- Verdict: **approved**
- Postgres execution: **not executed** — no `NEURON_TEST_POSTGRES_DSN`; Postgres contract and migration tests skipped with explicit reason.

## Findings

- No critical or high findings in scope.
- Original security conditions are satisfied in code:
  - `PutTimer` fails closed with `ErrTimerLeased` under a live lease.
  - `RenewTimer`, `ClaimTimer`, and `DeleteTimerOwned` enforce owner equality and reject empty owners across Memory, SQLite, and PostgreSQL.
  - Migration v3 is immutable DDL-only; v4 backfills parseable legacy rows and quarantines the rest via `due_at IS NULL`.
  - Driver-specific SQL avoids a transaction-poisoning `?` probe on PostgreSQL.
  - Due discovery is indexed and bounded.
  - Postgres advisory lock uses a pinned connection; SQLite retry is bounded/cancellable.
- Carried T-024 conditions: generate unique high-entropy per-claim owner tokens; treat `RenewTimer == false` as lease loss; use `DeleteTimerOwned`; validate lease duration and clock margin.
- Low: test fake does not model `ErrTimerOwnerEmpty`.
- Low: cross-host lease decisions use local wall-clock time.
- Low/informational: unbounded `ListTimers`, Memory `DueTimers` copy-before-limit, v4 full scan, and index not covering lease columns; documented as non-blocking performance residuals.

## Evidence

- `cd nore && GOWORK=off go test -race -count=1 ./internal/storage/...` → PASS
- Postgres contract/migration tests → SKIP, DSN unavailable
- `git diff --stat f82ffa6..HEAD -- nore/internal/storage/` → only `migrate/**` changed after fencing commit
- `gofmt -l nore/internal/storage/` → empty
- `git diff --check 23aca93^..HEAD` → clean
- No manifest/lockfile changes in the storage hardening range.

## Residual risks

- Postgres runtime parity unverified without a DSN.
- Caller-supplied owner token and unvalidated lease duration are T-024 responsibilities.
- Clock skew, unbounded listing/admin paths, and test-fake divergence remain documented.

## Next safe action

Record T-022 approval in the ledger, carry T-024 fencing conditions forward, and execute DSN-gated Postgres suites when a test database is available.
