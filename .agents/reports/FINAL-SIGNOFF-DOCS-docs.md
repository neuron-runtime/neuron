# Documentation — FINAL-SIGNOFF-DOCS

status: complete
verdict: APPROVE

No blocking documentation issues found. This is a read-only final sign-off: no documentation, source, test, ledger, or TODO files were changed by this task.

## Updated
- `.agents/reports/FINAL-SIGNOFF-DOCS-docs.md` — this sign-off record only.

## Verified against

T-031 documentation acceptance (`.agents/ledger.yaml:1217-1252`):
- `README.md:44`; `docs/ARCHITECTURE.md:470,479,756-778,1016,1018,1024-1031`; `application/README.md:183,233,443-447,499-566`; `packages/executor-sdks/golang/README.md:234-279`; `packages/executor-sdks/dotnet/README.md:134-182` — describe suspend/resume, signal idempotency (delivery ID replay/conflict), wait-deadline policy, max lifetime, timer-poll config, and SDK API changes.
- `docs/ARCHITECTURE.md:479`; `docs/MODULES.md:291,301,324`; `README.md:44`; both SDK READMEs (`:406`, `:224`) — state the protocol extension is additive/feature-gated and no TypeScript executor SDK exists. `packages/executor-sdks/` contains only `dotnet/` and `golang/`.
- `todos/TODO.md:760-871` — only items with recorded closing evidence are checked; `R5-5`, `R5-7`, `R5-24`, `F5-2`, `F5-10` remain open where evidence does not close them.
- `README.md:44`; `docs/ARCHITECTURE.md:778,984,1018`; `todos/TODO.md:773,785,871,879,882`; `packages/executor-sdks/dotnet/README.md:229` — PostgreSQL DSN limitation and the absence of an end-to-end .NET conformance claim are recorded; the .NET README records its 38 SDK tests separately.
- `git diff -- README.md docs/ARCHITECTURE.md application/README.md packages/executor-sdks/golang/README.md packages/executor-sdks/dotnet/README.md` — stale claims that suspended waits/timers were not resumed and that the reconciler was unwired were replaced; no unrelated documentation was rewritten.

T-080 TODO acceptance (`.agents/ledger.yaml:1723-1737`):
- `todos/TODO.md:845-857` — `R5-24` stays unchecked; duplicate-signal idempotency is attributed to T-030, duplicate-timer fencing to T-079's scheduler-boundary test, and acceptance-level duplicate-timer delivery is explicitly out of scope. `.agents/decisions/0011-r5-24-duplicate-timer-proof-boundary.md:5-11` and `.agents/reports/T-079.md:16-37` support that wording.
- `todos/TODO.md:868-871` — `R5-28` checked with the exact gate result and separate limitations.

T-032 final-gate documentation (`.agents/ledger.yaml:1254-1275`):
- `scripts/script.sh:9-14,79` — the gate covers the four Go modules and pnpm packages and its exact final line is `All modules built and tested successfully.`; there is no `dotnet` step.
- `.agents/reports/T-081.md:19-24` — `./scripts/script.sh` passed with that exact final line; full acceptance suite passed; no stray SDK tarball remained.
- `README.md:44`; `docs/ARCHITECTURE.md:778,1018`; `todos/TODO.md:767-768,834,856-857,868-871,879,881-882` — the pass is recorded verbatim, not as a paraphrase; PostgreSQL parity and .NET are not claimed by the gate.

T-082 CLI behavior (`.agents/ledger.yaml:1582-1607`, required documenter sign-off):
- `application/internal/cli/bootstrap/runtime.go:21,91-110` — `SetupInstanceClient` honors an explicit configured endpoint, otherwise uses the discovered project runtime; the no-runtime error text is exactly the string documented at `application/README.md:447`.
- `application/README.md:5,183,233,443-447` and `docs/ARCHITECTURE.md:778,1016` — instance commands prefer the discovered live project runtime when no explicit remote is set; explicit `--remote`/socket-daemon workflows remain supported; no empty-daemon fallback is promised.
- `.agents/reports/FINAL-SIGNOFF-SEC-CLI-security.md:20-25,37-38` — security verified token adoption is header-only, explicit endpoints are preserved, no token appears in errors/logs, and no public API/protocol files changed (APPROVE).

Contract spot-checks behind documented claims:
- `shared/protocol/capabilityruntime/v1/capability_runtime.proto:64-70,94-112,129-176` — `suspend` feature negotiation, additive `resume`/`suspend` fields, attempt/logical identity.
- `packages/executor-sdks/golang/suspend.go:16-25,39-118`; `capabilityruntime.go:101-111,312-330`; `stdio.go:39-44` — SDK type names, validation, feature gating, and stdio refusal match the Go README.
- `packages/executor-sdks/dotnet/src/Neuron.Executor/ExecuteContext.cs:25-40`; `ExecutorConstants.cs:21-36`; `SuspendOutcome.cs:20-44`; `Grpc/GrpcExecutorService.cs:134-166` — .NET README field names and rules match the code.
- `shared/types/core/runtimeconfig.go:266-284`; `application/config/defaults.go:33-37`; `application/config/operational_policy_test.go:16-26` — documented defaults/bounds (`fail`, `720h`, `1s`/`100ms`–`1m`, `256`/`4096`) match code and tests.
- `git diff --check` — exit 0, no output.

## Not updated
- `README.md`, `docs/**`, `application/README.md`, SDK READMEs, `todos/TODO.md` — read-only sign-off; all were already synchronized and need no blocking correction.
- `.agents/ledger.yaml` — coordinator-owned; T-031 is still `in_progress` with a documenter lease, T-032 `not_started`, T-080/T-082 `needs_review`. This sign-off supplies the documenter verdict; the coordinator must reconcile statuses and evidence.
- Source, tests, scripts, ADRs — read-only evidence; no edits.

## Unverified or ambiguous
- `./scripts/script.sh` was not rerun by this read-only sign-off. The pass and exact final line are taken from the coordinator brief and `.agents/reports/T-081.md:22`, and the final line was confirmed in `scripts/script.sh:79`. The script also mutates modules via `go mod tidy`, so rerunning it is a coordinator validation action, not a documentation check.
- `git status --short --branch` on `feature/phase5-engine-wiring` showed uncommitted source/test/doc changes and untracked test files. T-032's "final git status clean" acceptance item is coordinator-owned and was not satisfied at inspection time; this is a repository-state item, not a documentation defect.
- `docs/ARCHITECTURE.md:1018` says "duplicate-delivery ... acceptance tests pass." Verified as duplicate-*signal* delivery (`todos/TODO.md:847-848`); duplicate-timer proof is scheduler-boundary only (`todos/TODO.md:848-857`, decision 0011). The sentence is not a duplicate-timer claim, but sharpening it to "duplicate-signal delivery" would remove a possible misreading. Non-blocking.
- `packages/executor-sdks/golang/capabilityruntime.go:98-100` has a stale source comment claiming `Serve` never returns nil, while the implementation returns nil on graceful stop (`:146`) and one-shot success (`:152-166`) and `packages/executor-sdks/golang/README.md:227` documents that behavior. Source comments are outside this read-only lease; non-blocking for documentation acceptance.

## Links and examples checked
- `[Waiting and signals](./docs/ARCHITECTURE.md#waiting-and-signals)` from `README.md:44` — target heading exists at `docs/ARCHITECTURE.md:756`.
- `application/README.md` instance command usage/flags, Go and .NET suspend examples, and cited paths (`nore/internal/execution/scheduler/timer_poller_test.go`, `packages/executor-sdks/*`, `scripts/script.sh`) — all exist and match current interfaces.
- No new commands, flags, defaults, paths, or error semantics were introduced by this sign-off.

## Next safe action
- Coordinator: record this APPROVE as T-082's documenter sign-off and as the final documentation sign-off for T-031/T-080/T-032, reconcile ledger statuses/evidence, complete the remaining sign-offs and clean-tree/commit gate, then close Phase 5.
