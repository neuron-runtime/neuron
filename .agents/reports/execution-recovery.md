# Implementation — execution recovery

status: complete
lease: nore/internal/execution/execution.go

## Changed
- file: nore/internal/execution/execution.go:436-454 — CapabilityWaiting case

## Behavior
- When a capability in `CapabilityWaiting` state resumes because `WaitUntil` has elapsed (or is nil), the wait-related fields are now cleared upon resumption:
  - `state.WaitUntil = nil`
  - `state.WaitReason = ""`
  - `state.CorrelationKey = ""`
  - `state.WaitType = ""`
  - `state.ExpectedSignal = ""`
  - `state.PayloadSchema = ""`
- This ensures a clean transition to `CapabilityReady` without stale wait state carrying over.

## Verification
- `go build ./nore/internal/execution/...` — passes
- `go test ./nore/internal/execution/... -count=1 -timeout 120s` — all 3 test suites pass
- `go vet ./nore/internal/execution/...` — passes
- `gofmt -l ./nore/internal/execution/execution.go` — no issues

## Risks and limitations
- This is a defensive cleanup; no new runtime behavior is introduced beyond resetting fields that should not persist across resumption.