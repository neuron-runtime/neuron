# Review — T-037

- Status: approved
- Scope: `nore/internal/storage/provider.go`, `memory.go`, `sqlite/sqlite.go`, `postgres/postgres.go`, `storagetest/contract.go`
- Commits: `8a01be9`, `f82ffa6`

## Verdict

Approved. No blocking findings in scope.

## Acceptance

- SQL `PutTimer` now returns `ErrTimerLeased` directly on `RowsAffected()==0`; the prior pooled-connection recheck that could report success without a write is removed (`sqlite/sqlite.go:481-505`, `postgres/postgres.go:477-501`).
- Memory matches via `putTimerLocked`/`leaseLive` (`memory.go:637-647`).
- `ErrTimerOwnerEmpty` is documented as a required non-empty opaque per-claim token and enforced for claim/renew/delete-owned across Memory, SQLite, PostgreSQL, and TimerTx paths.
- Stale owners cannot renew, delete, or overwrite a reclaimed live lease.
- Expired/unleased re-put clears stale owner/lease.
- Contract tests cover live-lease Put refusal, expiry/reclaim, renew ownership, delete-owned fencing, empty-owner rejection, TimerTx atomicity/fencing, and due ordering/limits.
- `f82ffa6` changes only the five leased storage files.

## Limitations

- Postgres runtime parity not independently verified without `NEURON_TEST_POSTGRES_DSN`.
- Local wall-clock lease decisions remain a clock-skew risk.
- The storage test fake does not model `ErrTimerOwnerEmpty`; test-only divergence.
