# Security review — T-037

- Status: approved with conditions
- Scope: `nore/internal/storage/{provider.go,memory.go,sqlite/sqlite.go,postgres/postgres.go,storagetest/contract.go}`
- Commits: `8a01be9`, `f82ffa6`

## Findings

- Critical `PutTimer` fail-open: closed. `RowsAffected()==0` now returns `ErrTimerLeased`; no racy pooled recheck.
- High empty-owner bypass/divergence: closed. Claim/Renew/DeleteOwned reject empty owner consistently.
- Medium: owner fencing token is caller-supplied; T-024 must generate a unique high-entropy token per claim and never reuse worker IDs.
- Medium: lease duration is not validated; non-positive/absurd leases can produce instantly reclaimable claims. T-024 must validate and size leases above worst-case latency.
- Low: unfenced `DeleteTimer` remains public and documented as admin/cleanup.
- Low: lease decisions use local wall-clock time; multi-host skew remains a duplicate-execution risk.
- Low: execution/instance test fake does not model `ErrTimerOwnerEmpty`.
- Informational: SQL is parameterized; new operations are single-row primary-key operations; `DueTimers` remains `LIMIT`-bounded.

## Verdict

Approved for the storage layer, conditional on T-024 unique per-claim tokens, treating `RenewTimer==false` as lease loss, using only `DeleteTimerOwned` for completion, and validating lease duration/clock margin. Postgres parity remains unverified without a DSN and must not be claimed.
