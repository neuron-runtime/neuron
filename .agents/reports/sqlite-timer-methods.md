# Implementation — sqlite-timer-methods

status: complete
lease: nore/internal/storage/sqlite/sqlite.go

## Changed
- `nore/internal/storage/sqlite/sqlite.go` — multiple fixes to implement timer methods on the SQLite Store type

## Behavior
- `Store.WithTimer` now matches the `Store` interface signature `WithTimer(ctx context.Context, fn func(storage.TimerTx) error) error`, using `storage.TimerTx` instead of the local `TimerTx` interface
- `escapeLike` function added to escape SQL LIKE special characters (`\`, `%`, `_`), matching the PostgreSQL backend pattern
- `timerTx` struct added that implements `storage.TimerTx`, enabling timer operations (GetTimer, PutTimer, DeleteTimer, ListTimers, ClaimTimer) within a `WithTimer` callback that participate in the same database transaction
- `ClaimTimer` now correctly passes `lease duration` to the internal `claimTimer` function
- `claimTimer` function now accepts `lease time.Duration` as a parameter, fixing an undefined variable usage
- All timer operations within `WithTimer` use the transaction-bound `*sql.Tx` instead of the plain `*sql.DB`, ensuring proper atomicity

## Verification
- `go build ./nore/internal/storage/sqlite/` — passes
- `go vet ./nore/internal/storage/sqlite/` — passes
- `go test ./nore/internal/storage/sqlite/ -count=1` — all tests pass (15 tests)
- `gofmt` formatting applied

## Risks and limitations
- The `timerTx` struct stores a reference to the `Store` and the transaction `*sql.Tx`; callers must not use the `timerTx` outside the `WithTimer` callback, as the transaction gate enforces exclusive ownership
- The `escapeLike` function mirrors the PostgreSQL backend's escaping logic; both backends must maintain compatible LIKE patterns
- The `ClaimTimer`/`claimTimer` lease extension logic was previously broken (lease not passed between methods); this is now fixed but has not been extensively regression-tested

## Discovered but not fixed
- None

## Next safe action
- The task is complete. All acceptance criteria verified through build, vet, and test.