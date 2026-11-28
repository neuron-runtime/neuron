# Decision: R5-24 duplicate-timer proof boundary

Date: 2026-11-27

Context: `TestR5_24_DuplicateTimerFireCausesOneResume` attempted to prove duplicate-timer idempotency through two recovered runtimes sharing durable storage. It failed after 45s with the execution still `waiting` on a timer. Read-only diagnosis showed independent runtime caches/event buses plus a 30s timer lease create a serial lease-delay cycle, not a deterministic duplicate fire. No public timer claim/lease-expiry/delivery injection route exists.

Decision: Remove the nondeterministic acceptance test. Prove duplicate-fire fencing deterministically at the scheduler boundary with a test-only short-lease/no-renew store wrapper: first poller claims with a short lease and is gated before commit; second poller takes over after expiry; stale first commit loses fencing; assert exactly one `CapabilityReady` event, one timer deletion, and one ready dispatch intent. Keep R5-24/TODO evidence explicit that this is scheduler-boundary proof, not a public acceptance API test.

Rationale: User selected `Scheduler boundary (Recommended)` when asked. This preserves the behavioral guarantee without adding a production/test-harness injection API or weakening the test.

Consequences: T-079 scope includes `application/internal/acceptance/**` and `nore/internal/execution/scheduler/**`; R5-24 must not be described as public acceptance coverage; T-032 final validation remains required.
