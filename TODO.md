# TODO

Personal development checklist for Neuron. This file is intentionally **not tracked in git**
(it is listed in `.gitignore`) — it is a working todo list, not documentation.

Tick items off as they are completed. Group by type: **Fixes**, **Improvements**,
**Distribution**, **Documentation**.

---

## Fixes

- [ ] **Long-running execution streams.** `application/connection` uses an `http.Client`
  ```
  with a fixed 60s timeout, so an SSE stream for an execution longer than a minute is
  cut with `context deadline exceeded while reading body`. implement websocket connection and 
  make the client subscribe to a room and also send structure json data with payload and actions.
  ```
- [ ] **API authentication.** N.O.R.E.'s API is unauthenticated and bound to a local socket
  ```
  by default. Define a token-based model before any loopback exposure.
  ```
- [ ] **Worker crash/restart recovery.** Exercise worker-pool restart behavior after repeated
  ```
  executor failures under load; fix whatever explodes.
  ```
- [ ] **Windows daemon lifecycle.** Verify socket-path handling and `neuron daemon` start/stop
  ```
  on Windows (best-effort so far: Linux/macOS only).
  ```
- [ ] **Storage path normalization.** `storage.directory` accepts relative paths (e.g.
  ```
  `./.neuron/data`) that are resolved against the daemon's working directory; resolve them
  against the project root and pass an absolute path to `--data-dir`.
  ```



## Improvements

- [ ] **Execution-history retention policies.** Implement `storage.executionHistory:
  ```
  none | memory | local`; execution semantics must not depend on persistence.
  ```
- [ ] `neuron execution` **inspection command.** Re-add as a real surface that lists
  ```
  executions across instances (currently only visible via `neuron instance list`).
  ```
- [ ] **Per-executor resource limits.** CPU, memory, file count, and runtime limits in the
  ```
  process runtime.
  ```
- [ ] **Stronger process isolation.** Sandboxing (seccomp, namespaces) or a dedicated runtime
  ```
  backend for external modules.
  ```
- [ ] **Concurrency backpressure.** Bound in-flight executions per instance and per daemon.
- [ ] **Execution record audit.** Decide which persisted record fields matter in the product
  ```
  model vs. internal use.
  ```
- [ ] **TLS for the opt-in TCP endpoint.** Document client-side verification.
- [ ] **Protocol negotiation.** Add explicit `neuron/executor-v1` version/capability negotiation
  ```
  checks in the runtimes' handshake.
  ```
- [ ] **Executor-go examples.** Add gRPC-only, JSON-only, and two-runtime reference executors.
- [ ] **Request deadlines end-to-end.** Propagate CLI-provided execution deadlines through the
  ```
  whole execution path instead of relying on transport timeouts.
  ```



## Distribution

- [ ] **Publish first official** `v0.1.0` **release.** `scripts/release.sh` and the tag-triggered
  ```
  workflow are ready; create the tag when CI is green.
  ```
- [ ] **Publish** `@neuron/sdk` to a package registry with `engines` metadata and clear versioning.
- [ ] **Publish the shared Go module** (`github.com/Muhammad-Jay/neuron/shared`) so consumers can
  ```
  depend on `executor-go` and the protocol contracts without vendoring.
  ```
- [ ] **Public module ecosystem.** Publish the reference `echo` module and a starter catalog
  ```
  reachable by the default `github` registry; document catalog conventions.
  ```
- [ ] **Additional registry runtimes.** Progress on `oci` and `remote` executor runtimes.



## Documentation

- [ ] **Design-notes sync.** `docs/executors/2026-09-05-*.md` must never contradict shipped
  ```
  registry/installer behavior; update as the ecosystem evolves.
  ```
- [ ] **Runtime deep dives.** Extend `docs/RUNTIME.md` with the execution plan, event breakdown,
  ```
  and recovery paths.
  ```
- [ ] **Changelog / migration notes.** Record breaking changes between 0.1.x releases.
- [ ] **Pre-1.0 API audit.** Audit every exported symbol in the SDK and `executor-go`; unexport
  ```
  anything that is not a deliberate contract.
  ```

