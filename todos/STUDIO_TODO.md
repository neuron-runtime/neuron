# Neuron Studio — Implementation Roadmap

> This document tracks the implementation of Neuron Studio as a local-first graphical
> workspace for creating, configuring, authoring, building, running, and inspecting Neuron
> projects. It assumes the existing Neuron application, CLI, authoring pipeline, runtime
> protocol, and N.O.R.E. execution model have reached the completed state described by the
> main implementation roadmap.

## How this document is used

This file is the **single source of truth** for Neuron Studio implementation work.

Rules that apply to every change:

- A checkbox is marked `[x]` only after the deliverable is implemented, its acceptance tests
  pass, `scripts/script.sh` passes where applicable, and the work is committed on its own
  branch.
- Never move to the next phase while the current phase's exit criteria are unmet.
- Each phase is one branch and one or more coherent commits
  (`feat(studio):`, `fix(studio):`, `refactor(studio):`, `test(studio):`, `docs(studio):`).
- Every task must leave a reviewable artifact: code, tests, schema, generated contract,
  documentation, or an explicit recorded decision. "Investigate" is not a deliverable unless
  the result is written down and accepted.
- Do not mark a task complete because a UI mock, route stub, interface, or happy-path demo
  exists. The stated behavior, failure cases, security properties, and tests must exist.
- Findings discovered during implementation are appended to the owning phase's **Findings**
  section. Each finding must state the observed behavior, evidence, impact, chosen fix, and
  regression test. Do not silently fix or silently drop findings.
- Keep completed items as historical implementation records. Add new work to the owning phase
  rather than duplicating a task across sections.
- Do not introduce a second implementation of project semantics, configuration parsing,
  compilation, runtime scheduling, persistence, or execution protocol to make a screen easier
  to build.
- If an existing Neuron application service is not reusable, extract or repair that service at
  its proper application boundary before adding Studio-specific orchestration. Do not invoke
  CLI commands as subprocesses as a substitute for application services.
- Validate actual behavior in both web and desktop presentation modes. A frontend-only test is
  not evidence that the Go server, runtime client, packaging, or process lifecycle works.
- Record skipped tests, unavailable infrastructure, platform limitations, and unverified
  behavior explicitly. An attempted command is not a passing test.

## Relationship to the main implementation roadmap

This is a new roadmap for Studio, not a continuation or replacement of the main `TODO.md`.

For planning purposes, assume the main roadmap's existing phases and their unfinished items
have been completed and integrated before Studio work begins. Studio may consume the resulting
application services, CLI behavior, configuration contracts, authoring pipeline, runtime
protocol, and durable execution semantics.

Do not copy the main roadmap's runtime implementation tasks into this file. If Studio exposes
a runtime behavior that does not yet have a stable application service or public N.O.R.E.
protocol contract, record that as a blocking integration dependency and resolve it in the
owning Neuron layer before implementing a Studio-specific substitute.

Studio is an additional interface to Neuron. It does not change the ownership of the existing
project, build, provider, runtime, or execution domains.

---

## Locked architectural decisions

These decisions constrain every phase. If implementation reveals a conflict, stop and resolve
the decision before coding.

1. **Studio is an application client and local management environment.** It is not a second
   Neuron runtime, compiler, scheduler, persistence layer, or execution protocol.
2. **Neuron remains the authority for project semantics and operations.** Studio delegates
   project creation, configuration interpretation, authoring compilation, validation, build,
   dependency resolution, provider operations, and runtime requests to existing application
   services and contracts.
3. **N.O.R.E. remains the authority for execution.** Execution state, event history, scheduling,
   cancellation, recovery, waits, signals, and timers are read or changed through the existing
   N.O.R.E. API and its typed client/protocol boundary.
4. **The filesystem is the project source of truth.** A Studio database must not become
   authoritative for project configuration, Assembly source, build records, runtime state,
   execution history, or events.
5. **One frontend serves web and desktop.** React, TypeScript, and Vite provide one application.
   Tauri is a thin desktop shell, not a second frontend or backend.
6. **One local Go server serves both modes.** Web and desktop use the same Studio API, session
   model, application services, runtime client, and frontend build.
7. **The public launch surface is explicit.** `neuron --studio` launches desktop Studio;
   `neuron --studio --web` launches browser Studio. `--web` without `--studio` is invalid.
   Bare `neuron` and all existing subcommands retain their established behavior.
8. **Local-first is the initial deployment model.** Bind to loopback, use an ephemeral port,
   require no account or hosted control plane, and do not expose the server to the LAN by
   default. Remote runtime support is deferred.
9. **Runtime target identity is consistent across operations.** Build registration, Assembly
   listing, execution, cancellation, health, and inspection must all refer to the same
   explicitly selected target. Never silently switch between a project runtime and global
   daemon.
10. **Credentials stay in the Go process.** The frontend receives only a short-lived Studio
    session credential. N.O.R.E. tokens, provider secrets, database credentials, and other
    machine credentials must never be returned to browser code.
11. **Filesystem access is workspace-scoped.** The backend derives canonical roots from
    server-side workspace state, rejects traversal and symlink escapes, and never treats a
    browser-supplied path as authorization.
12. **File edits are conflict-aware and atomic.** A Studio write must be based on a known
    revision, validated, previewable where appropriate, and rejected or reconciled if an IDE
    or another process changed the file.
13. **Visual authoring must preserve source semantics.** Studio may visually edit only formats
    and constructs that can be represented and round-tripped without information loss.
    Arbitrary TypeScript remains source-first; compiled graph views may be read-only.
14. **The canonical manifest/compiler defines graph meaning.** Studio's graph canvas is an
    authoring and visualization surface, not an alternate graph model or validator.
15. **Editor metadata is non-semantic.** Node positions, viewport state, and other visual
    preferences live in Studio-only metadata and never affect manifests, hashes, builds, or
    execution.
16. **Generated contracts have one authority.** N.O.R.E. runtime DTOs and routes originate in
    the shared Go protocol. Studio API contracts originate in the Studio server. TypeScript
    contracts must be generated or checked against those authorities rather than manually
    drifting copies.
17. **No arbitrary shell execution from the UI.** Frontend actions map to named backend
    operations. Any required process launch uses fixed executables, validated arguments,
    controlled environment, explicit working directory, cancellation, and bounded output.
18. **Closing Studio is not stopping N.O.R.E.** Studio may stop only processes it started and
    owns, and only under an explicit lifecycle policy. Closing a window must not stop an
    unrelated daemon or attached runtime.
19. **Guided and developer workflows share the same data and operations.** They are views over
    one project model, not separate products or separate persistence paths.
20. **Remote project management is out of scope.** A future remote runtime target does not
    grant remote filesystem access and must not be used to imply remote project editing.

---

## Implementation order

The order is forced by architectural dependencies and user-visible acceptance.

```text
Phase 1   application integration and reusable operation boundaries
Phase 2   Studio server, session security, workspace and process lifecycle
Phase 3   frontend foundation and project onboarding
Phase 4   configuration and safe project-file workspace
Phase 5   Assembly authoring, dependencies, providers, build and registration
Phase 6   runtime workspace and execution inspector
Phase 7   desktop/web packaging, hardening, documentation and release acceptance
```

Why this order:

- **Phase 1 comes first.** Studio must consume stable application operations. Building UI against CLI internals or duplicated logic would harden the wrong boundary.
- **Phase 2 establishes the trusted local boundary.** Workspace isolation, authentication, lifecycle, and runtime target ownership must exist before exposing filesystem or execution actions.
- **Phase 3 delivers a usable shell and project journey.** It proves the same frontend works in browser and desktop modes before feature complexity accumulates.
- **Phase 4 establishes safe project editing.** Configuration and file changes need revision checks, validation, provenance, and conflict handling before the Assembly editor writes source.
- **Phase 5 depends on the canonical authoring and build pipeline.** The graph editor and build UI must produce exactly the artifacts that CLI builds produce.
- **Phase 6 depends on stable runtime target selection and public runtime contracts.** The inspector must display actual runtime state, not browser-maintained approximations.
- **Phase 7 proves the complete product can be installed, launched, upgraded, secured, and used on supported platforms.**

---

## Phase 1 — Application Integration and Reusable Operation Boundaries

Branch: `feat/studio-application-services`

Goal: every operation required by Studio is available through reusable application services or
existing typed runtime clients. The CLI and Studio invoke the same operations; no Studio handler
depends on CLI command internals or reimplements Neuron behavior.

Exit criteria:

- Studio packages do not import `application/internal/cli` or execute Neuron CLI subprocesses
  for project, config, build, provider, dependency, or runtime operations.
- Every Studio operation has an explicit application-service owner and a typed request/result
  contract.
- The CLI continues to use the same application operations for any behavior extracted or
  refactored by this phase.
- Runtime operations use the existing N.O.R.E. client and shared protocol; no parallel runtime
  route or DTO set is introduced.
- Unit and integration tests prove the extracted operations preserve existing CLI behavior.

### 1.1 Integration inventory and ownership

- [ ] **S1-1** Inventory every user journey required by the initial Studio release:
      project discovery/open/create, configuration read/validate/write, Assembly discovery and
      authoring, dependency inspection/install, provider configuration, build/registration,
      runtime target discovery/start/stop, Assembly listing, execution create/list/show/cancel,
      event replay/live streaming, and health. For each operation record the current package,
      current entry point, required inputs, outputs, side effects, and error behavior in
      `studio/INTEGRATION.md`. Do not mark an operation reusable based only on a CLI command
      existing.
- [ ] **S1-2** Create an ownership matrix mapping each operation to an existing application
      package or a proposed focused application service. The matrix must identify which layer
      owns validation, filesystem access, process creation, runtime transport, and persistence.
      Reject any design in which a Studio HTTP handler owns domain logic.
- [ ] **S1-3** Inspect package import boundaries before implementation. Add an automated
      dependency test that fails if `studio/server` imports `application/internal/cli`,
      `nore/internal` implementation packages, or concrete runtime storage backends.
      Runtime access must go through the application client/protocol boundary.
- [ ] **S1-4** Define the supported initial feature scope and explicit deferred items in
      `studio/README.md`. Do not expose placeholder navigation or controls for operations that
      have no implementation contract. Each visible action must either work or be explicitly
      disabled with a reason.

### 1.2 Project and configuration operations

- [ ] **S1-5** Verify that project discovery, project-root resolution, project scaffolding, and
      project validation can be invoked without constructing a Cobra command. Extract focused
      application operations where necessary. The CLI `init` path and Studio create-project path
      must produce equivalent project structures and configuration.
- [ ] **S1-6** Expose configuration loading and validation through an application-facing
      interface that returns the effective configuration plus source/provenance metadata.
      Do not add a Studio configuration parser or copy the precedence rules into Studio.
- [ ] **S1-7** Define a safe configuration mutation operation that accepts a typed patch or
      validated document plus an expected file revision. It must preserve unrelated keys and
      formatting where supported, validate against the authoritative schema, and reject stale
      writes. If the existing config package cannot preserve a document safely, document the
      limitation and use a controlled whole-document replacement with a diff.
- [ ] **S1-8** Add tests proving configuration values and validation errors are identical when
      loaded through the CLI and Studio application entry points, including defaults, global
      settings, project settings, environment overrides, and invalid values.

### 1.3 Build and authoring operations

- [ ] **S1-9** Verify that the canonical authoring builder registry can be invoked without CLI
      dependencies. Keep language selection and builder resolution in the application/build
      layer; Studio must not select or invoke a compiler by inspecting file extensions itself.
- [ ] **S1-10** Extract or expose a reusable build orchestration service for project validation,
      local dependency preparation, authoring compilation, canonical manifest generation,
      compiler validation, dependency/provider freezing, runtime registration, and build-record
      persistence. The service must accept explicit project and runtime-target inputs and return
      a structured result.
- [ ] **S1-11** Define structured operation progress and diagnostics for the build service:
      stable operation ID, stage, status, completed/total where meaningful, message, diagnostic
      severity/code/location, and terminal result. Progress must not be emitted as terminal
      strings that Studio has to parse.
- [ ] **S1-12** Ensure build orchestration has no dependency on Cobra, terminal writers, spinners,
      stdout, or stderr. Keep CLI rendering in the CLI adapter and add tests proving both CLI and
      Studio adapters consume the same service.
- [ ] **S1-13** Define cancellation semantics for long-running application operations. Cancellation
      must reach child processes and network/runtime calls where supported; a cancelled build
      must report which stages completed and whether registration or other irreversible side
      effects already occurred.

### 1.4 Runtime target and client boundary

- [ ] **S1-14** Define an application-owned runtime target model that distinguishes project-local
      runtime, local daemon, and future remote target without exposing credentials. Include
      stable session identity, target kind, endpoint metadata safe for display, health, process
      ownership, and lifecycle state.
- [ ] **S1-15** Add an explicit authenticated client/connection constructor for a discovered
      project-runtime endpoint and credential. Do not pass a project token through process-wide
      environment variables or mutate global client configuration to switch targets.
- [ ] **S1-16** Ensure all runtime operations accept an explicit selected target/client. Build
      registration, Assembly discovery, execution, cancellation, event streaming, and health
      must not independently resolve a default runtime.
- [ ] **S1-17** Define runtime process ownership states: started by this Studio session, attached
      to an existing project runtime, attached to shared daemon, or externally managed. Add
      tests proving shutdown only terminates a process owned by the current Studio session.
- [ ] **S1-18** Verify that the existing runtime API exposes every field required by the initial
      Studio inspector. If a required field is absent, add it through the shared protocol and
      runtime client/server implementation in the owning runtime layer. Do not serialize private
      N.O.R.E. structs directly or add a Studio-only runtime endpoint.

### 1.5 Validation

- [ ] **S1-19** Add application-service tests for project creation/discovery, config loading and
      mutation, validation, build progress/cancellation, runtime target resolution, and runtime
      client selection. Use fakes at explicit interfaces and integration tests for real
      filesystem/compiler/runtime boundaries.
- [ ] **S1-20** Run the CLI regression suite and `scripts/script.sh`. Record exact commands,
      results, skipped tests, and any behavior changes in this phase. Existing CLI commands must
      remain compatible unless a separately documented breaking change is approved.
- [ ] **S1-21** Review the import graph and public interfaces. Remove obsolete CLI-only wrappers,
      duplicate orchestration, dead code, and temporary compatibility layers introduced during
      extraction. Update `studio/INTEGRATION.md` with final service owners and contracts.

### Findings during Phase 1 implementation

Append new findings here as checklist items. Each finding states the observed behavior, evidence,
impact, fix, and regression test.

---

## Phase 2 — Studio Server, Session Security, Workspace, and Lifecycle

Branch: `feat/studio-local-server`

Goal: a secure local Go server provides one stable management API for both frontend modes,
isolates filesystem access to an explicitly selected workspace, and has deterministic startup
and shutdown behavior.

Exit criteria:

- `neuron --studio --web` starts the Studio server on loopback with an ephemeral port and
  authenticated session; `--web` alone is rejected.
- Every API request is authenticated and protected against cross-origin state changes.
- Filesystem operations cannot escape the selected workspace through traversal or symlinks.
- Runtime credentials never reach the frontend or logs.
- Server shutdown is bounded and terminates only Studio-owned child processes.

### 2.1 Package structure and server lifecycle

- [ ] **S2-1** Establish the Studio Go package boundary under `studio/server`. Keep server
      startup, API transport, session security, workspace resolution, filesystem operations,
      and frontend asset serving separate from application-domain services. Document package
      ownership in `studio/README.md`; do not create packages that merely wrap one function
      without a distinct responsibility.
- [ ] **S2-2** Implement a server constructor that receives explicit dependencies: application
      services, runtime-target resolver, workspace manager, session manager, asset handler,
      logger, and lifecycle context. Avoid package-level mutable globals and implicit singleton
      initialization.
- [ ] **S2-3** Bind to `127.0.0.1` on an OS-assigned ephemeral port by default. Return the actual
      bound URL to the launcher. Reject wildcard/LAN binding unless a future explicit and
      separately secured mode is implemented. Add tests that inspect the listener address.
- [ ] **S2-4** Implement graceful shutdown with a bounded timeout. Stop accepting requests,
      close event streams, cancel in-flight cancellable operations, wait for owned child
      processes, and close resources. A timeout must be surfaced in logs and shutdown results;
      it must not block forever.
- [ ] **S2-5** Add startup rollback. If session creation, route registration, asset resolution,
      or frontend launch fails after the listener starts, close the listener and release every
      resource already acquired. Test each startup failure point.
- [ ] **S2-6** Add health endpoints for Studio server readiness and liveness that do not disclose
      filesystem paths, credentials, environment values, or runtime secrets. Readiness must not
      claim a selected runtime is healthy unless that runtime has actually been checked.

### 2.2 Studio API contract

- [ ] **S2-7** Define the versioned Studio management namespace `/api/studio/v1/`. Keep it
      distinct from N.O.R.E. runtime routes. Group endpoints by session, workspace, projects,
      files/configuration, authoring/build operations, and runtime-target selection.
- [ ] **S2-8** Publish a machine-readable Studio API schema from the Go server. Generate the
      TypeScript client contracts from this schema or add automated compatibility checks.
      Hand-maintained duplicate request/response interfaces are not an acceptable long-term
      contract.
- [ ] **S2-9** Define consistent response envelopes and error codes. Errors must distinguish
      invalid input, missing resource, permission/workspace denial, revision conflict,
      validation failure, operation conflict, runtime unavailable, cancellation, and internal
      failure. Never return raw stack traces or secret-bearing underlying errors.
- [ ] **S2-10** Make handlers thin. Each handler may authenticate, decode and validate transport
      input, resolve server-side identifiers, invoke one application service, and map its result.
      Add tests or package-level review checks preventing business logic, compiler calls,
      SQL/storage access, or process orchestration from entering handlers.
- [ ] **S2-11** Add request IDs and operation IDs to structured logs and error responses. Ensure
      IDs are propagated into application operations where supported, while avoiding project
      source contents, credentials, and sensitive input values in logs.
- [ ] **S2-12** Define pagination, filtering, sorting, and maximum response sizes for collection
      endpoints. Reject unbounded event/file/list requests and test boundary values.

### 2.3 Session authentication and browser protections

- [ ] **S2-13** Generate a cryptographically unpredictable per-launch Studio session secret.
      Keep the authoritative value in Go memory and deliver it only through a deliberate
      bootstrap mechanism. Do not place it in command-line arguments, URLs, frontend build
      variables, project files, or logs.
- [ ] **S2-14** Authenticate every non-public API route. Use a secure session transport suitable
      for loopback web and Tauri modes; define cookie flags or an equivalent header mechanism,
      rotation, expiry, and invalidation. Add tests for missing, malformed, expired, and
      revoked sessions.
- [ ] **S2-15** Validate request `Origin` and `Host` against the actual Studio origin and bound
      listener. Reject unexpected origins and host-rebinding attempts. Do not use permissive
      wildcard CORS as a workaround for Vite development.
- [ ] **S2-16** Protect state-changing requests against CSRF. Require the chosen anti-CSRF
      mechanism on every mutation and test cross-origin form and fetch attempts.
- [ ] **S2-17** Apply restrictive security headers, including a production Content Security
      Policy compatible with the bundled frontend. Avoid `unsafe-eval` and broad remote
      script/connect sources. Document any development-only relaxation and prove it is not
      enabled in production builds.
- [ ] **S2-18** Redact session tokens, N.O.R.E. credentials, provider secrets, DSNs, authorization
      headers, and secret-bearing configuration fields from request logs, panic reports, and
      error serialization. Add automated redaction tests using sentinel secret values.

### 2.4 Workspace and filesystem confinement

- [ ] **S2-19** Define a server-side workspace model with one active workspace initially.
      Workspace identifiers must be opaque session-scoped IDs; the browser must not choose an
      arbitrary root path on each request.
- [ ] **S2-20** Resolve and canonicalize the workspace root when it is opened. Validate project
      roots as descendants of an explicitly authorized workspace root. Resolve symlinks and
      reject any path that escapes the root, including symlink changes between validation and
      use where the platform permits race-safe handling.
- [ ] **S2-21** Expose named file operations rather than a generic unrestricted filesystem API.
      Define allowlisted read/write operations and file classes. Reject absolute paths,
      traversal segments, device files, sockets, and unsupported special files.
- [ ] **S2-22** Enforce file-size limits, encoding rules, and operation timeouts. Binary assets
      must use explicit supported operations; do not silently decode arbitrary binary data as
      text.
- [ ] **S2-23** Add security tests for `..` traversal, encoded traversal, alternate separators,
      absolute paths, symlink escape, nested symlink, nonexistent parent, and workspace switch
      races. Tests must run on every supported operating system where path semantics differ.
- [ ] **S2-24** Keep recent-project metadata and Studio preferences outside the project source
      of truth. Store only non-secret identifiers/paths and UI preferences in the established
      user data location; document retention and deletion behavior.

### 2.5 Operation progress and events

- [ ] **S2-25** Define a typed operation event contract for progress, diagnostics, completion,
      failure, and cancellation. Events must include operation ID, monotonic sequence or
      cursor, timestamp, stage, and structured payload. Do not make clients parse human-readable
      messages to determine state.
- [ ] **S2-26** Implement progress delivery with reconnect behavior. A client reconnecting after
      a dropped stream must either replay from a cursor or retrieve the authoritative operation
      status; it must not silently lose the final result.
- [ ] **S2-27** Bound retained operation output and event buffers. Define what is retained in
      memory, what is persisted (if anything), and how old operation state expires. Do not
      create a second durable event store for N.O.R.E. execution history.

### 2.6 Validation

- [ ] **S2-28** Add server tests for authentication, origin/CSRF checks, workspace isolation,
      path confinement, request limits, error mapping, progress reconnection, startup rollback,
      and graceful shutdown.
- [ ] **S2-29** Add race tests for concurrent workspace changes, file access, session invalidation,
      and server shutdown while requests are active.
- [ ] **S2-30** Run `go test ./studio/server/...`, relevant application tests, and
      `scripts/script.sh`. Record platform-specific skips and verify the listener is loopback-only.

### Findings during Phase 2 implementation

Append new findings here as checklist items.

---

## Phase 3 — Frontend Foundation and Project Onboarding

Branch: `feat/studio-frontend-foundation`

Goal: a production-structured React/TypeScript/Vite application is served by the Studio server
and can create, open, discover, and switch between normal Neuron projects in both browser and
desktop presentation modes.

Exit criteria:

- The same Vite application runs in browser and Tauri without duplicated feature code.
- A user can launch Studio from an empty or non-project directory, create a project, open an
  existing project, and reopen a recent project.
- Project creation uses the same scaffold and validation behavior as the CLI.
- Every visible onboarding action has loading, success, error, and cancellation behavior.
- Frontend architecture, API contracts, accessibility baseline, and design tokens are
  established before feature pages proliferate.

### 3.1 Frontend workspace and build

- [ ] **S3-1** Create an independently managed pnpm workspace under `studio/frontend`. Keep
      JavaScript tooling scoped to Studio; do not convert the entire Go repository into a
      JavaScript monorepo. Commit the lockfile and pin supported Node and pnpm versions.
- [ ] **S3-2** Create one Vite React TypeScript application under `apps/studio`. Configure
      strict TypeScript, linting, formatting, production build, and test scripts. Enable
      `noUncheckedIndexedAccess` and other strict options where compatible; do not suppress
      errors globally to accelerate implementation.
- [ ] **S3-3** Define development and production asset behavior. In development Vite serves HMR
      and proxies only the Studio API to Go. In production the Go server serves the built
      assets. Add a test that production asset serving handles SPA routes and does not expose
      source maps or development server endpoints unintentionally.
- [ ] **S3-4** Define environment configuration without embedding runtime credentials or
      machine secrets in Vite variables. Validate required public build-time settings and fail
      the build on missing or unsafe production configuration.
- [ ] **S3-5** Add CI jobs for dependency installation, lint, typecheck, unit tests, and production
      build. Cache dependencies safely and ensure the lockfile is enforced with frozen install.

### 3.2 Frontend architecture and contracts

- [ ] **S3-6** Establish feature-oriented modules for projects, workspace, configuration,
      Assemblies, dependencies, providers, builds, runtime, and executions. Each feature owns
      its API operations, query keys, mutations, feature types, and components. Expose stable
      public entry points and prohibit deep imports into another feature's private files.
- [ ] **S3-7** Implement one typed API client package for base URL, session bootstrap, request
      IDs, cancellation, response parsing, normalized errors, and schema-derived types.
      Components must not construct endpoint URLs or call `fetch` directly.
- [ ] **S3-8** Choose and document the server-state/query strategy. Define cache keys, invalidation,
      retry rules by operation type, stale-time defaults, and mutation error handling. Do not
      put server data into a global client store merely to share it between screens.
- [ ] **S3-9** Limit global client state to true session concerns: active workspace/project,
      selected runtime target, global notifications, and transient navigation state. Feature
      data remains owned by its query/cache layer.
- [ ] **S3-10** Add route-level loading boundaries, error boundaries, not-found handling, and
      navigation guards for unsaved edits or active destructive operations.
- [ ] **S3-11** Establish component and feature testing conventions. Test user-observable
      behavior and accessibility rather than implementation details or internal hook calls.

### 3.3 Design system and application shell

- [ ] **S3-12** Create a shared UI package for reusable primitives and composition patterns.
      Keep project logic, runtime API calls, filesystem operations, and feature-specific
      decisions out of this package. Publish explicit package entry points; avoid deep imports.
- [ ] **S3-13** Define semantic design tokens for backgrounds, surface levels, panels, text,
      borders, focus, actions, status, radii, spacing, shadows, and motion. Components must use
      semantic tokens rather than arbitrary repeated color literals.
- [ ] **S3-14** Implement accessible primitives for buttons, fields, labels, selects, checkboxes,
      dialogs, drawers, menus, tabs, tooltips, notifications, and data display. Each primitive
      must support keyboard interaction, visible focus, disabled/error states, and appropriate
      accessible names.
- [ ] **S3-15** Configure Tailwind source scanning to include shared UI package source in both
      development and production builds. Add a production visual smoke test proving shared
      styles are present.
- [ ] **S3-16** Implement the application shell: project/workspace selector, runtime target
      indicator, primary navigation, content region, contextual panel, notification area, and
      collapsible bottom panel. Layout must remain usable at supported desktop window sizes.
- [ ] **S3-17** Define typography and density rules for technical values, IDs, hashes, timestamps,
      code, and ordinary prose. Use monospace only for technical data; ensure truncation has an
      accessible way to inspect the full value.
- [ ] **S3-18** Add keyboard navigation, focus restoration, reduced-motion support, and
      screen-reader labels to the shell. Document supported keyboard shortcuts and test the
      primary navigation without a mouse.

### 3.4 Project onboarding

- [ ] **S3-19** Implement the welcome screen with Create project, Open existing project, and
      Recent projects. The screen must work when Studio launches from an empty directory or a
      directory that is not a Neuron project.
- [ ] **S3-20** Implement create-project form using the authoritative scaffold service. Collect
      only required values: project name, destination, authoring language, and optional
      supported starter template. Validate names and destination before submission; show the
      exact destination and resulting project root before final creation.
- [ ] **S3-21** Make project creation collision-safe. If the destination exists or contains
      files, require an explicit safe decision; never overwrite or merge an existing directory
      silently. On partial failure, report created files and provide a safe cleanup/retry path.
- [ ] **S3-22** Implement open-project flow using a directory selection mechanism appropriate to
      web and Tauri. Web mode must not pretend it can browse arbitrary local directories without
      a user-granted mechanism. Tauri native selection must return only a path for backend
      validation, not grant the frontend unrestricted filesystem access.
- [ ] **S3-23** On project open, load configuration, declarations, authoring files, build
      records, local runtime discovery, and dependency status through authoritative services.
      Show validation issues without silently rewriting project files or installing dependencies.
- [ ] **S3-24** Implement recent-project metadata with explicit remove-from-recents behavior.
      Do not delete project files when removing a recent entry. Handle moved/deleted paths and
      permission errors without crashing the application.
- [ ] **S3-25** Implement project switching with a clear confirmation when unsaved edits or
      active operations exist. Cancel or detach view subscriptions cleanly and prevent data from
      the previous project appearing in the new project context.
- [ ] **S3-26** Add project overview showing project identity, authoring languages, declared
      Assemblies, configuration validity, dependency status, latest build state, and selected
      runtime target. Every status must come from an authoritative response with a timestamp or
      freshness indicator where relevant.

### 3.5 Validation

- [ ] **S3-27** Add frontend tests for create/open/recent/switch flows, invalid project paths,
      collisions, partial creation failure, stale recent entries, and project-load errors.
- [ ] **S3-28** Add browser end-to-end tests for the onboarding journey using a temporary
      filesystem-backed test project service.
- [ ] **S3-29** Verify the same project created by Studio can be opened, validated, and operated
      through the CLI, and a CLI-created project opens in Studio without migration or rewrite.
- [ ] **S3-30** Run frontend lint, typecheck, unit tests, production build, Go server tests, and
      `scripts/script.sh`. Record exact results.

### Findings during Phase 3 implementation

Append new findings here as checklist items.

---

## Phase 4 — Configuration and Safe Project-File Workspace

Branch: `feat/studio-project-workspace`

Goal: users can understand and safely edit project configuration and managed project files
without Studio becoming a competing configuration authority or overwriting changes made by an
IDE, CLI, or another process.

Exit criteria:

- Effective configuration and provenance match the authoritative Neuron loader.
- Guided and raw editing operate on the same underlying project files.
- Invalid configuration cannot be applied.
- Stale writes and external edits are detected; no edit is silently lost.
- File writes are atomic and constrained to the selected project.
- Secrets are redacted from UI, diagnostics, and logs.

### 4.1 Effective configuration and provenance

- [ ] **S4-1** Expose effective configuration together with field-level provenance from the
      authoritative config loader. For each displayed field provide effective value, source
      (default/global/project/environment/CLI where applicable), editability in the current
      scope, validation state, and whether a change requires rebuild or runtime restart.
- [ ] **S4-2** Generate the UI field catalog from authoritative configuration metadata or schema.
      Do not manually maintain a second list of field names, defaults, descriptions, or enum
      values in React. Add a contract test that detects schema/UI metadata drift.
- [ ] **S4-3** Distinguish project configuration, global user configuration, environment
      overrides, runtime state, and Studio preferences in navigation and editing permissions.
      Never write an effective environment or CLI override into a project file merely because
      it is the current effective value.
- [ ] **S4-4** Show where each value is defined and why it is effective. When a value is
      overridden, show the winning source and the shadowed source(s) without exposing secret
      contents.
- [ ] **S4-5** Define safe display behavior for secrets. Show whether a secret is configured and
      its source, but do not return the underlying value to React unless a narrowly scoped
      reveal operation is explicitly justified and secured. Never include secrets in diff previews
      or copied diagnostic bundles.

### 4.2 Guided and raw configuration editing

- [ ] **S4-6** Implement guided forms for common project settings using schema-derived types,
      descriptions, validation constraints, and defaults. Unknown or newly introduced fields
      must remain visible in the raw view and must not be dropped by a guided save.
- [ ] **S4-7** Implement raw JSON/YAML editing for supported configuration formats. Preserve
      comments, ordering, and formatting where the selected parser/writer supports it. If a
      save necessarily normalizes the document, show the normalized diff before applying.
- [ ] **S4-8** Keep guided and raw views synchronized against one document revision. Switching
      views must not discard pending edits; detect incompatible simultaneous changes and ask the
      user to resolve them.
- [ ] **S4-9** Validate proposed content through the authoritative configuration service before
      apply. Return structured field paths, error codes, and actionable messages. Do not create
      a frontend-only validator that can disagree with Neuron.
- [ ] **S4-10** Implement diff preview, Apply, Revert, and Export. Apply must re-check the
      expected revision immediately before the atomic write. Revert must restore only the
      selected file/document revision, not reset unrelated project changes.
- [ ] **S4-11** Implement external-change detection using file identity/revision or content hash.
      If the file changes while the editor is open, preserve the user's draft, load the external
      version separately, and provide compare/rebase/discard choices.
- [ ] **S4-12** Ensure writes preserve file permissions where appropriate, use a temporary file
      in the same filesystem followed by atomic replacement, and clean up temporary files on
      failure. Test interrupted writes and permission errors.

### 4.3 Project file browser and generated artifacts

- [ ] **S4-13** Implement a project file browser limited to supported project-root content.
      Clearly distinguish source files, configuration, generated artifacts, runtime state,
      Studio metadata, and ignored/hidden content. Do not expose arbitrary host filesystem
      browsing through the project view.
- [ ] **S4-14** Add safe read-only inspection for canonical manifests, build records, lock/freeze
      artifacts, and diagnostics. These are generated outputs; do not allow editing them as if
      they were authoritative source.
- [ ] **S4-15** Define file operation permissions by category. Source/config edits may be
      supported; generated runtime state and build artifacts must be read-only unless an existing
      Neuron operation explicitly owns their mutation.
- [ ] **S4-16** Add external file watcher behavior with debounce, rename/delete handling, watcher
      overflow recovery, and full refresh fallback. Watcher events are hints; verify the current
      file revision before acting.
- [ ] **S4-17** Add export of a project or selected non-secret artifacts with explicit inclusion
      rules. Exclude runtime credentials, local database files, caches, and secret-bearing
      machine configuration by default. Test archive path traversal on both export and import
      paths if import is supported.
- [ ] **S4-18** Implement project-level diagnostics for invalid configuration, missing source
      files, missing dependencies, stale build artifacts, and unavailable runtime targets.
      Each diagnostic must identify its source and a safe next action; do not silently repair.

### 4.4 Validation

- [ ] **S4-19** Add backend tests for config precedence/provenance, schema validation, atomic
      writes, stale revision conflicts, symlink/path restrictions, watcher changes, and secret
      redaction.
- [ ] **S4-20** Add frontend tests for guided/raw parity, unknown-field preservation, invalid
      edits, diff preview, apply/revert, external conflict resolution, and read-only generated
      artifacts.
- [ ] **S4-21** Add an interoperability test: edit configuration in Studio, change it externally
      with an IDE/CLI, return to Studio, and prove neither version is silently overwritten.
- [ ] **S4-22** Run the complete frontend and backend suites plus `scripts/script.sh`. Record
      supported formatting-preservation limits and any file types intentionally read-only.

### Findings during Phase 4 implementation

Append new findings here as checklist items.

---

## Phase 5 — Assembly Authoring, Dependencies, Providers, Build, and Registration

Branch: `feat/studio-assembly-workspace`

Goal: Studio provides a reliable Assembly workspace and guided build workflow using the existing
authoring contracts, canonical compiler, dependency/provider services, and build orchestration.
Visual editing is enabled only for source constructs that can be represented without loss.

Exit criteria:

- Studio can list and create supported Assemblies and open their source.
- Visual editing produces valid existing authoring source and round-trips without semantic loss
  for every construct it claims to support.
- TypeScript source is never silently transformed into an incomplete visual model.
- Compiler validation is authoritative.
- Build and registration use the same application service as the CLI and the explicitly selected
  runtime target.
- Build diagnostics, progress, immutable artifact identity, and build records are inspectable.

### 5.1 Assembly discovery and source workspace

- [ ] **S5-1** Implement Assembly discovery from the authoritative project declarations and
      builder registry. Show declared name/identity, authoring language, source location, latest
      build identity, and validation/build status. Do not infer Assembly identity solely from
      filenames.
- [ ] **S5-2** Implement Assembly creation using supported project declarations and scaffolding
      operations. Validate naming collisions and source paths before creating files. The result
      must be buildable by the ordinary CLI without Studio metadata.
- [ ] **S5-3** Provide source access for each supported authoring language. Use an embedded
      editor only if language support, diagnostics, keyboard behavior, and large-file handling
      meet the project standard; otherwise open the source through the operating system/editor
      integration without claiming an in-app editor exists.
- [ ] **S5-4** Show the canonical manifest as a generated, read-only view with identity, nodes,
      edges, mappings, join semantics, result contract, and content hash. Label it as compiled
      output and never write it back as source.
- [ ] **S5-5** Show compiler diagnostics with source file, line/column where available, diagnostic
      code, severity, and related node/field. Clicking a diagnostic must navigate to the source
      location when the editor supports it.

### 5.2 Visual graph editing contract

- [ ] **S5-6** Before implementing editing, define a supported-source matrix for each authoring
      language and construct. Mark every construct as editable, read-only visualizable, or
      unsupported. Include mappings, fan-out/fan-in, join policy, conditional edges, detached
      scopes, result contracts, and language-specific expressions.
- [ ] **S5-7** Select a visual-editing source format only after verifying its complete schema and
      compiler semantics. If YAML is the initial editable format, test the full graph model and
      all supported constructs. Do not introduce a Studio-only graph document or DSL.
- [ ] **S5-8** Define a lossless parse-edit-serialize contract. For every supported source fixture,
      parse it into the editor model, serialize without edits, and prove semantic equivalence
      through canonical compilation. Unknown fields/comments/expressions must be preserved or
      cause the editor to switch to read-only mode; never drop them silently.
- [ ] **S5-9** Implement graph operations only for constructs in the supported matrix. Each
      operation must produce source changes through the canonical authoring format and run
      authoritative compiler validation before it can be applied.
- [ ] **S5-10** Keep arbitrary TypeScript source source-first. A compiled graph may be displayed
      read-only, but the UI must not claim it can reconstruct arbitrary TypeScript. Disable
      visual mutation when source constructs cannot be round-tripped safely and explain why.
- [ ] **S5-11** Implement graph layout metadata under a Studio-only project metadata directory.
      Version the layout schema; key layouts by stable Assembly/node identity; tolerate missing,
      stale, or corrupt layout data by regenerating layout without modifying source or manifest.
- [ ] **S5-12** Prove layout metadata does not affect source, canonical manifest, Assembly hash,
      build fingerprint, runtime registration, or execution. Add a test that changes only node
      coordinates and compares all semantic artifacts.
- [ ] **S5-13** Implement graph editor correctness states: invalid source, unsupported construct,
      unresolved node, duplicate mapping, missing predecessor, pending validation, and valid
      compiled graph. The editor must never display an invalid draft as a successfully compiled
      Assembly.

### 5.3 Dependencies and providers

- [ ] **S5-14** Implement dependency inventory using the authoritative capability-runtime
      catalog/resolver/store. Show required, installed, available, incompatible, and missing
      versions with provenance and actionable status. Do not implement registry downloads or
      artifact extraction in React.
- [ ] **S5-15** Implement dependency installation/update through the existing application
      installer with structured progress, cancellation where supported, checksum/signature
      validation, and clear partial-failure recovery. Do not mutate project declarations
      implicitly.
- [ ] **S5-16** Implement provider inventory and configuration through existing provider
      services. Show which providers are required, configured, unavailable, or incompatible.
      Expose only fields permitted by the provider contract.
- [ ] **S5-17** Redact provider credentials in every list/detail response and form state. Support
      setting/replacing a secret without returning the stored value. Test that secrets never
      appear in API responses, logs, error messages, or exported diagnostics.
- [ ] **S5-18** Explain which dependency/provider changes invalidate a build and require rebuild
      or re-registration. Derive this from the build/fingerprint contract rather than hardcoding
      assumptions in the frontend.

### 5.4 Build and registration workflow

- [ ] **S5-19** Implement build selection for one Assembly or all declared Assemblies. Display
      the project and selected runtime target before registration; require explicit confirmation
      when the target differs from the previously used target.
- [ ] **S5-20** Implement build validation and registration through the shared application
      build service. Do not invoke `neuron build` as a subprocess. Ensure the selected target is
      passed explicitly through every stage that registers an artifact.
- [ ] **S5-21** Render structured build progress by stage, including validation, dependency
      preparation, authoring compilation, canonical compilation, dependency/provider freezing,
      registration, and build-record persistence where applicable. Progress must survive
      frontend route changes and reconnects by operation ID.
- [ ] **S5-22** Render diagnostics grouped by severity and source. Provide a clear distinction
      between validation failure, build failure, registration failure, cancellation, and
      successful registration. Preserve the complete structured diagnostic payload for copying
      or support export with secrets redacted.
- [ ] **S5-23** Show resulting Assembly identity, version, content hash, build fingerprint,
      dependency/provider lock identity, build timestamp, and runtime target. Values must come
      from the build result and persisted record, not be reconstructed in the UI.
- [ ] **S5-24** Prevent concurrent conflicting builds for the same project/Assembly unless the
      build service explicitly supports them. Display active operation state and provide a
      safe cancel/retry path.
- [ ] **S5-25** Define behavior when registration succeeds but a later client connection or UI
      update fails. The UI must recover the authoritative registration/build result by operation
      ID or build record; it must not encourage duplicate registration as an automatic retry.
- [ ] **S5-26** Implement build history from authoritative build records, including failed and
      cancelled attempts where retained. Link each record to its exact immutable artifact and
      runtime target.

### 5.5 Validation

- [ ] **S5-27** Add backend tests for Assembly discovery/create, build-service delegation,
      target pinning, progress delivery, cancellation, dependency installation, provider
      redaction, and registration outcome recovery.
- [ ] **S5-28** Add graph editor fixture tests covering every construct in the supported-source
      matrix. Require parse/serialize semantic equivalence and canonical compiler validation.
- [ ] **S5-29** Add an end-to-end test proving an Assembly authored or edited in Studio builds
      with the CLI and an Assembly authored outside Studio opens without source rewriting.
- [ ] **S5-30** Run frontend lint/typecheck/tests/build, application integration tests, and
      `scripts/script.sh`. Record every intentionally read-only or unsupported authoring case.

### Findings during Phase 5 implementation

Append new findings here as checklist items.

---

## Phase 6 — Runtime Workspace and Execution Inspector

Branch: `feat/studio-runtime-inspector`

Goal: users can select and manage the correct runtime target, inspect runtime health and
registered Assemblies, execute Assemblies, and inspect authoritative execution state and events.
Studio does not maintain its own execution state machine or event history.

Exit criteria:

- Runtime target selection is explicit, stable, and consistent across build and execution actions.
- Project-local runtime discovery validates identity, liveness, health, and credential source.
- Studio never stops a runtime it does not own.
- Execution lists/details/events come from N.O.R.E. through the typed client.
- Live event reconnect/replay does not lose or duplicate user-visible state.
- The inspector exposes only data supported by the public runtime protocol and labels unavailable
  details accurately.

### 6.1 Runtime target discovery and selection

- [ ] **S6-1** Implement discovery of project-local runtime records using the existing
      `shared/types/projectruntime` contract. Validate the canonical project root, process
      identity/liveness, endpoint, and authenticated health response before presenting a target
      as available.
- [ ] **S6-2** Discover the local daemon through the existing connection abstraction. Present it
      as a distinct target from a project-local runtime; do not merge their Assemblies or
      executions into one apparent runtime.
- [ ] **S6-3** Implement runtime target selection with stable session-scoped identity. Persist
      only a safe target preference; resolve and revalidate the actual target on each Studio
      launch. Never persist runtime credentials in Studio preferences.
- [ ] **S6-4** Display target kind, project association, endpoint type, process state, health,
      runtime version, and ownership. Do not display tokens, private socket paths, or secret
      connection metadata.
- [ ] **S6-5** When no healthy project runtime exists, offer a named Start project runtime
      operation. If a matching runtime is already active, attach to it rather than starting a
      duplicate process. Handle stale discovery records without deleting them until ownership
      and process identity are verified.
- [ ] **S6-6** Make build registration, Assembly listing, execution, cancellation, event
      subscription, and health use the same selected target identity. Add an integration test
      that fails if any operation silently resolves a different default target.

### 6.2 Runtime process lifecycle

- [ ] **S6-7** Implement start through the existing application runtime/daemon service with
      explicit project root, validated configuration, controlled environment, and cancellable
      process context. Do not create a second N.O.R.E. launcher in Studio.
- [ ] **S6-8** Record process ownership for each target attached during the session. A process
      started by this Studio session may be stopped under the defined policy; an attached
      project runtime or global daemon must not be stopped automatically.
- [ ] **S6-9** Implement explicit Stop runtime confirmation with target identity and ownership
      information. Disable stop for externally managed or shared targets unless the existing
      application contract authorizes that operation.
- [ ] **S6-10** Define close behavior: closing the Studio window or browser tab does not imply
      stopping N.O.R.E. The launcher shutdown policy must leave attached/shared runtimes alone
      and must not terminate a Studio-owned runtime unless the user selected that policy.
- [ ] **S6-11** Handle crashed, unresponsive, or replaced runtime processes. Revalidate process
      identity before signaling a PID; never trust a stale PID alone. Report orphaned discovery
      records with a safe recovery action.

### 6.3 Runtime overview and resources

- [ ] **S6-12** Implement runtime overview using health/readiness responses: version, health,
      worker information, storage health, and other fields only when exposed by the public API.
      Show last successful check and distinguish unknown from unhealthy.
- [ ] **S6-13** Implement registered Assembly listing from the selected runtime. Display exact
      identity, version, content hash, and registration metadata returned by N.O.R.E.
- [ ] **S6-14** Implement instance listing and detail where supported by the existing runtime
      contract. Preserve the distinction between instance identity and execution identity.
- [ ] **S6-15** Add polling/revalidation policy for health and resource lists. Avoid aggressive
      polling, pause or reduce refresh when the app is backgrounded, and expose a manual refresh.
      Live updates must use existing event/API contracts where available.

### 6.4 Execution creation and management

- [ ] **S6-16** Implement an Assembly execution form from the registered Assembly's public input
      contract. Validate required fields and types using authoritative schema data; do not infer
      parameter types from labels or example values.
- [ ] **S6-17** Submit execution requests through the typed N.O.R.E. client with request
      correlation metadata. Show the returned execution ID immediately and navigate to its
      inspector without waiting for completion.
- [ ] **S6-18** Implement execution list with filtering by status, Assembly, time range, and
      correlation/parent identity where supported. Use server pagination and stable sorting.
- [ ] **S6-19** Implement execution detail from the public protocol: identity, Assembly artifact
      identity/hash, status, timestamps, duration, inputs/outputs subject to data policy,
      errors, retries/attempts, parent/child relations, and wait state when supported.
- [ ] **S6-20** Implement cancellation with explicit confirmation and authoritative outcome
      refresh. Disable or explain cancellation when the selected execution is terminal or the
      runtime reports it is not cancellable.
- [ ] **S6-21** Handle uncertain request outcomes. If the connection drops after submission,
      query by request/correlation ID where supported before offering a retry. Never assume a
      timeout means the execution was not created.

### 6.5 Events, timeline, and graph inspector

- [ ] **S6-22** Implement initial event history retrieval and live event subscription through the
      existing N.O.R.E. event API. Use the server-provided cursor and reconnect contract; do not
      create a Studio event store.
- [ ] **S6-23** Implement reconnect with cursor-based replay, duplicate detection, sequence
      ordering, backoff, cancellation, and terminal-state handling. Prove that a disconnect
      between replay and live subscription cannot lose an event.
- [ ] **S6-24** Normalize events into a frontend view model without changing event meaning.
      Preserve unknown event types as inspectable raw structured data rather than dropping them.
- [ ] **S6-25** Implement execution timeline from authoritative timestamps and event sequence.
      Handle missing timestamps, clock skew, duplicate delivery, and out-of-order arrival without
      inventing event order or duration.
- [ ] **S6-26** Implement graph visualization using the immutable canonical Assembly artifact
      associated with the execution. Display node state only when supplied by the public
      execution-detail contract. Do not infer node completion from log text or event absence.
- [ ] **S6-27** Show node inputs/outputs only under an explicit data exposure policy. Redact
      secrets and support bounded/expandable large payloads. Never dump arbitrary payloads into
      logs or browser developer diagnostics by default.
- [ ] **S6-28** Display retries, attempt identity, parent/child and detached execution
      relationships, and durable wait/signal/timer information only when those fields are
      available from the runtime protocol. Mark unsupported details as unavailable rather than
      fabricating a simplified state.
- [ ] **S6-29** Provide raw event and structured data views for developers, with copy/export
      actions that apply the same secret-redaction policy as the rest of Studio.

### 6.6 Validation

- [ ] **S6-30** Add backend integration tests for project-runtime discovery, target authentication,
      target consistency, process ownership, start/stop behavior, and runtime failure handling.
- [ ] **S6-31** Add frontend tests for target switching, health states, execution form validation,
      uncertain submission outcomes, cancellation, event replay/reconnect, duplicate events,
      and unknown event types.
- [ ] **S6-32** Add an end-to-end test that builds against a selected target, executes against
      that same target, disconnects/reconnects during event delivery, and verifies the final
      authoritative execution result.
- [ ] **S6-33** Run frontend and backend suites plus `scripts/script.sh`. Record any runtime
      protocol fields that remain intentionally unavailable and ensure the UI does not imply
      support for them.

### Findings during Phase 6 implementation

Append new findings here as checklist items.

---

## Phase 7 — Desktop/Web Packaging, Hardening, Documentation, and Release

Branch: `feat/studio-release`

Goal: Studio is a dependable local product with reproducible web and desktop builds, secure
native integration, predictable installation and upgrade behavior, complete user journeys, and
documentation that accurately describes supported functionality.

Exit criteria:

- `neuron --studio` launches the packaged Tauri desktop application.
- `neuron --studio --web` launches the same frontend in the default browser.
- Both modes use the same Go server and frontend build artifacts.
- Missing desktop assets produce an actionable error; desktop mode never silently changes to
  browser mode.
- Supported operating systems have tested packaging and lifecycle behavior.
- The complete acceptance journey passes from an empty directory through CLI interoperability.
- Documentation, release artifacts, and known limitations match the shipped implementation.

### 7.1 CLI launch integration

- [ ] **S7-1** Add root flags `--studio` and `--web` with explicit validation. `--web` without
      `--studio` must fail with a concise usage error. Preserve bare `neuron` project-runtime
      behavior and all existing subcommand behavior.
- [ ] **S7-2** Adjust root command bootstrap so Studio can start from an empty directory, an
      existing project directory, or a non-project directory without requiring project
      configuration to preload. Do not weaken or bypass normal configuration loading for
      existing project commands.
- [ ] **S7-3** Define precedence and rejection behavior when Studio flags are combined with
      subcommands or incompatible flags. Reject ambiguous combinations rather than silently
      ignoring flags. Add root-command tests for all valid and invalid combinations.
- [ ] **S7-4** Implement one shared launcher path that starts the Go server, resolves the
      presentation mode, launches the selected frontend shell, waits for shutdown, and cleans
      up session-owned resources. Avoid separate server implementations for web and desktop.
- [ ] **S7-5** Implement `--studio --web` browser launch using the actual bound URL. Handle
      browser-open failure by printing the URL and a clear manual-open instruction while keeping
      the server available until explicit shutdown or process termination.
- [ ] **S7-6** Implement `--studio` desktop launch by resolving a packaged Tauri companion
      executable/app bundle. If it is missing or incompatible, return an actionable error that
      points to web mode; do not silently open a browser.
- [ ] **S7-7** Implement shutdown coordination for Ctrl+C, server shutdown request, desktop
      window closure, and launcher termination. Use bounded cleanup and distinguish closing the
      Studio server from stopping a runtime target.

### 7.2 Tauri desktop shell

- [ ] **S7-8** Create a thin Tauri shell that loads the local Studio URL and contains no duplicate
      project, configuration, build, runtime, or execution logic. Web mode must remain fully
      functional without Tauri globals.
- [ ] **S7-9** Configure a restrictive production Content Security Policy and allow only the
      local Studio origin and required bundled assets. Do not load arbitrary remote pages into
      the privileged webview.
- [ ] **S7-10** Minimize Tauri permissions. Add only required window, directory-selection, and
      lifecycle permissions. Do not enable broad filesystem or shell plugins as a shortcut.
- [ ] **S7-11** If native directory selection or external-editor opening is required, expose
      narrow typed commands with input validation and backend authorization. Never expose a
      generic shell command or unrestricted filesystem API to frontend code.
- [ ] **S7-12** Verify deep links, refresh/reload behavior, window close, app quit, crash, and
      server disconnect. The shell must recover gracefully or show an actionable connection
      error without losing project files or pending edits.
- [ ] **S7-13** Verify Tauri development mode loads Vite HMR while production mode loads the
      packaged local server URL. Ensure development permissions and CSP exceptions are absent
      from release builds.

### 7.3 Packaging and distribution

- [ ] **S7-14** Define supported operating systems and architectures for the first release.
      Record the exact Go, Node, pnpm, Rust, and Tauri toolchain versions used to produce
      artifacts.
- [ ] **S7-15** Build reproducible frontend assets and Tauri packages in CI. Pin dependencies,
      use lockfiles, record build metadata, and fail on uncommitted generated changes or
      dependency drift.
- [ ] **S7-16** Define how the CLI locates the matching desktop companion. Validate version
      compatibility between CLI, Studio frontend, and Tauri shell; reject incompatible bundles
      with a useful error.
- [ ] **S7-17** Package the production frontend assets with the Go distribution or a clearly
      versioned companion asset bundle. Verify the executable works from paths containing
      spaces and from installations without a repository checkout.
- [ ] **S7-18** Define installer/update/uninstall behavior for each supported platform. Do not
      delete user projects, global Neuron configuration, runtime data, or databases during
      Studio uninstall.
- [ ] **S7-19** Generate checksums and provenance metadata for release artifacts. Test clean
      installation and upgrade from the previous supported release on each platform.

### 7.4 Reliability, security, and performance

- [ ] **S7-20** Run a security review covering loopback binding, host/origin validation, session
      handling, CSRF, workspace confinement, symlinks, file writes, secret redaction, process
      launch, Tauri permissions, CSP, and dependency supply chain. Record findings and close
      all critical/high findings before release.
- [ ] **S7-21** Add fuzz/property tests for path validation, API decoding, configuration patching,
      event cursor handling, and graph source round-tripping where applicable.
- [ ] **S7-22** Measure startup time, project-load time, Assembly list/build UI responsiveness,
      event-stream memory use, and large execution payload behavior. Set reasonable budgets and
      fix regressions that materially degrade the local workflow.
- [ ] **S7-23** Verify frontend caches are disposable and recover from server restart, runtime
      restart, project file changes, and target switching. No stale cache may be presented as
      authoritative without a freshness marker.
- [ ] **S7-24** Test multiple Studio launches and concurrent sessions. Session credentials,
      workspace selection, runtime ownership, and shutdown must remain isolated. Do not let one
      session stop a runtime owned by another.
- [ ] **S7-25** Verify crash recovery and cleanup for abandoned Studio server processes and
      temporary files. Use process identity and session ownership checks; never kill processes
      based only on a stale PID or port number.

### 7.5 Documentation and onboarding

- [ ] **S7-26** Write `studio/README.md` covering product scope, architecture, package ownership,
      prerequisites, development setup, build commands, web/desktop launch, supported platforms,
      and troubleshooting. Keep it aligned with actual scripts and package paths.
- [ ] **S7-27** Document the user journey: create/open project, configure, author Assembly,
      install dependencies, configure providers, validate/build, select runtime, execute, and
      inspect results. Screenshots and examples must reflect the shipped UI.
- [ ] **S7-28** Document project portability and state ownership. Explain which files are source,
      generated artifacts, runtime state, and Studio-only metadata; explain how to continue using
      the CLI or another IDE without Studio.
- [ ] **S7-29** Document security and privacy behavior: listener scope, session lifecycle,
      filesystem boundary, secret handling, process ownership, telemetry (if any), and data
      retained outside project directories.
- [ ] **S7-30** Publish a feature support matrix for authoring languages and graph constructs.
      Explicitly identify read-only and unsupported visual editing cases. Do not market a
      read-only graph view as a visual editor.
- [ ] **S7-31** Add troubleshooting for missing desktop companion, browser launch failure,
      occupied/blocked local ports, invalid project, stale runtime records, unavailable runtime,
      configuration conflicts, build failure, and event-stream reconnect.

### 7.6 Release acceptance

- [ ] **S7-32** Run the complete acceptance journey from an empty directory:
      launch Studio, create a project, select authoring language, edit configuration, create an
      Assembly, validate, build/register, start or attach to a runtime, execute, inspect result
      and events, close Studio, then open and validate the same project through the CLI.
- [ ] **S7-33** Run the reverse interoperability journey: create and build a project through the
      CLI, open it in Studio, inspect its configuration/Assembly/build/runtime state, and verify
      Studio makes no unsolicited source or configuration changes.
- [ ] **S7-34** Run acceptance tests in both browser and Tauri modes on every supported platform.
      Include empty directory, non-project directory, existing project, moved project, permission
      denial, missing desktop companion, runtime unavailable, and abrupt shutdown cases.
- [ ] **S7-35** Run frontend lint/typecheck/unit/e2e tests, Go unit/integration/race tests,
      Tauri build checks, and `scripts/script.sh`. Record exact commands, results, skipped
      platforms, and known limitations.
- [ ] **S7-36** Verify release artifacts on clean machines without a source checkout or developer
      toolchain. Confirm both launch commands work and that uninstall/upgrade preserve user data.
- [ ] **S7-37** Complete a final architecture review. Remove temporary adapters, duplicate
      contracts, unused UI components, debug routes, development credentials, dead code, and
      misleading feature flags. Confirm the locked architectural decisions remain true.

### Findings during Phase 7 implementation

Append new findings here as checklist items.

---

## Cross-phase acceptance matrix

These scenarios define completion for the Studio roadmap. Each maps to the phase that owns it.

| Scenario | Owner | Required assertion |
| --- | --- | --- |
| Application reuse | S1 | CLI and Studio invoke the same application operations; Studio imports no CLI internals |
| Project portability | S1/S3/S7 | Studio-created projects work through CLI and ordinary IDEs without Studio |
| Runtime target consistency | S1/S6 | Build, register, execute, inspect, and cancel use the same selected target |
| Local server security | S2/S7 | Loopback-only listener, authenticated requests, origin/CSRF protections |
| Filesystem confinement | S2/S4 | Traversal and symlink escapes are rejected; writes are atomic and revision-checked |
| Session isolation | S2/S7 | Concurrent sessions cannot access each other's workspace or credentials |
| Project onboarding | S3 | Empty directory to valid project, with collision and failure handling |
| Configuration parity | S1/S4 | Studio effective values and validation match the authoritative loader |
| External edit conflict | S4 | IDE/CLI edits are detected and never silently overwritten |
| Authoring correctness | S5 | Visual editing round-trips supported source and canonical compilation agrees |
| TypeScript safety | S5 | Arbitrary TypeScript is not silently reconstructed or rewritten |
| Build correctness | S1/S5 | Studio and CLI produce equivalent canonical artifacts and build records |
| Registration target | S5/S6 | Build result identifies the exact runtime target that received registration |
| Runtime ownership | S1/S6 | Studio stops only a process it started and owns |
| Execution truth | S6 | Inspector displays authoritative N.O.R.E. state and does not infer missing details |
| Event continuity | S2/S6 | Replay/reconnect handles cursors, duplicates, ordering, and terminal events |
| Web/desktop parity | S3/S7 | Same frontend and server behavior in browser and Tauri modes |
| Release portability | S7 | Clean installation works without repository checkout or developer toolchain |

---

## Non-goals for this roadmap

- Do not build a second Neuron runtime, execution engine, scheduler, persistence layer, or event
  store.
- Do not add a second compiler, Assembly DSL, graph validator, manifest format, or project
  format.
- Do not make Studio a cloud control plane, hosted service, account system, or remote project
  management platform in the initial release.
- Do not require a Studio database to open, build, run, or maintain a Neuron project.
- Do not replace the CLI or ordinary IDE workflows.
- Do not shell out to CLI commands for application operations that should be reusable services.
- Do not expose arbitrary shell execution or unrestricted filesystem access to the frontend.
- Do not expose runtime or provider credentials to browser code.
- Do not claim arbitrary TypeScript can be edited visually.
- Do not let editor layout metadata affect canonical Assembly semantics or hashes.
- Do not duplicate execution history, events, build records, or runtime state in Studio-owned
  persistence.
- Do not stop an attached runtime or shared daemon when Studio closes.
- Do not implement remote runtime support until local target selection, authentication, and
  lifecycle contracts are stable.

---

## Reporting requirements

When a phase completes, report:

- what changed and which application, server, frontend, protocol, and packaging boundaries it
  touched
- which application services were reused or extracted, and whether CLI behavior remained
  compatible
- what user-visible workflows became functional and which remain intentionally unsupported
- what was validated (exact commands and results), including `scripts/script.sh`
- what security, filesystem, lifecycle, and interoperability tests were added
- which findings were appended and which were fixed
- remaining risks, platform limitations, and follow-up items promoted into a phase
