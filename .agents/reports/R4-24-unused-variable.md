# Implementation — R4-24-unused-variable

status: complete
lease: nore/internal/instance/recovery_test.go

## Changed
- nore/internal/instance/recovery_test.go:482 — Removed unused variable declaration `capabilityID := shared.ID("cap_retry")` from `TestRecoveryDuringRetryBackoff` function. The variable was declared but never referenced; the function uses the string `"cap_retry"` directly throughout.

## Behavior
- No functional change. The test continues to operate on the `"cap_retry"` capability as before, but without the now-unused variable binding.

## Verification
- `go build ./nore/internal/instance/...` — passes
- `go vet ./nore/internal/instance/...` — vet error at line 594 (`MarkCapabilityWaiting undefined`) is pre-existing and unrelated to this change

## Tests added or updated
- None needed. The existing `TestRecoveryDuringRetryBackoff` function behavior is unchanged.

## Risks and limitations
- None. This is a clean removal of dead code.

## Discovered but not fixed
- None.

## Next safe action
- Task complete. The unused variable has been removed.