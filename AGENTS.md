# Neuron / N.O.R.E. — Engineering Contract

## Purpose

This repository contains **Neuron**, including its system-definition SDK, compiler, loaders, runtime integration, and **N.O.R.E.**, the execution engine.

This is infrastructure software. It is not a demo application, workflow toy, collection of unrelated utilities, or experimental codebase where architectural correctness can be traded for convenience.

Every change MUST preserve clear boundaries between:

* system definition
* canonical manifests
* loading
* validation
* compilation
* execution planning
* executor discovery
* executor resolution
* executor installation
* executor storage
* executor runtime
* execution
* persistence
* transport
* CLI/application concerns

The primary engineering goals are:

1. Correctness
2. Architectural integrity
3. Isolation
4. Security
5. Performance
6. Scalability
7. Maintainability
8. Clear public APIs
9. Testability
10. Documentation

Do not optimize for the smallest diff. Optimize for the smallest **correct abstraction**.

Role-specific rules live in the global agent files at `~/.config/opencode/agents/<id>.md`. This file is the authoritative project contract and applies to every agent, including the coordinator, specialists, and built-in agents.

---

## Repository map

```text
application/    compiler, loader, language, project, runtime, capabilityruntime,
                provider, client, daemon, config, connection, process, build, cmd, internal
nore/           N.O.R.E. engine: assembly, contracts, planner, execution, invoke, instance,
                event, stream, types, builtins, registry, resolver, plugin, mechanism,
                runtimeconfig, storage, data, analytics, api, cmd
packages/assembly-sdks/   authoring SDKs
packages/executor-sdks/   capability runtime SDKs
shared/         protocol, types, version
docs/           architecture, CLI, API, guides, migration
examples/       runnable assemblies and capability runtimes
scripts/        build, install, release, validation
todos/          product backlog
.agents/        coordination ledger, ownership, reports, decisions
```

Go modules are wired by `go.work`; JavaScript/TypeScript packages by `pnpm-workspace.yaml`.

Do not casually reorganize these areas. Before moving code, establish why the current boundary is insufficient.

---

## Canonical data flow

```text
Source Language
    ↓
Language-Specific Loader
    ↓
Canonical System Manifest
    ↓
Validation
    ↓
Compiler
    ↓
Core System
    ↓
Execution Plan
    ↓
Runtime
```

Source languages MUST converge on the canonical representation before entering source-language-independent compiler logic. Do not put source-language-specific behavior into universal compiler code. Do not make the runtime understand source languages.

---

## Architectural boundaries

### Compiler

The compiler transforms the canonical manifest into runtime/core structures and MUST remain source-language agnostic.

The compiler MUST NOT:

* parse YAML or TypeScript
* resolve GitHub repositories
* download, install, or launch executors
* execute services
* own HTTP clients
* contain registry-specific behavior

Those responsibilities belong to their respective layers.

### Executor model

An Executor is a runtime capability contract. It MUST NOT be equated with one particular binary format or implementation technology. It MAY be a native executable, a managed application, a WASM module, an OCI/container image, a remote service, or anything else implementing the capability runtime protocol.

The model MUST remain language- and runtime-independent and SHOULD distinguish:

```text
Executor Requirement → Executor Package → Executor Artifact
    → Executor Runtime → Executor Instance
```

Do not collapse these concepts merely because one package currently implements several.

### Executor runtime

N.O.R.E. SHOULD host executors through runtime backends:

```text
Executor Runtime
    ├── Process Runtime
    ├── WASM Runtime
    ├── Container Runtime
    └── Remote Runtime
```

The runtime backend owns starting, connecting, health checking, executing, cancellation, deadlines, lifecycle, resource limits, termination, and restart/recovery. The registry, installer, and compiler MUST NOT own these responsibilities.

### Capability runtime protocol

The protocol MUST be language independent. Do not require a particular language or require compilation to WASM. It SHOULD use a stable, versioned RPC boundary supporting handshake, version negotiation, runtime identity, capabilities, initialization, execution, structured input/output/errors, cancellation, deadlines, health, and graceful shutdown. Transport details MUST remain behind the capability runtime abstraction.

### Installation lifecycle

```text
Requirement → Registry → Resolver → Package → Verification
    → Installation → Store → Runtime → Instance
```

The registry answers where a package can be obtained. The resolver answers which version satisfies the requirement. The installer answers how to securely install it. The store answers where the immutable artifact is kept. The runtime answers how to execute it. Do not merge these responsibilities.

### Language and SDK boundaries

Authoring SDKs define systems and capability contracts; they are not runtime implementations. Keep runtime concerns out of authoring SDKs. Authoring SDKs describe services, contracts, composition, connections, mappings, validations, executor requirements, and configuration.

The core owns parsing, validating, compiling canonical representations, and runtime execution. Do not place language-specific or source-format-specific behavior into universal compiler logic or the core runtime. Do not over-split packages; each package SHOULD own a cohesive responsibility with clear internal types. Use interfaces at architectural boundaries, not everywhere.

### Public API stability

Treat public SDK APIs as contracts. Before changing one:

1. identify current consumers
2. inspect type tests
3. inspect generated manifests
4. inspect documentation
5. determine compatibility impact
6. update tests
7. update documentation

Do not casually expose internal types or implementation details.

### CLI discipline

CLI commands MUST correspond to coherent domain operations (`neuron add`, `build`, `run`, `inspect`, `remove`), not internal implementation steps. Internal lifecycle operations remain implementation details unless there is a deliberate operational reason to expose them.

### Isolation, duplication, dead code, naming

* Every package MUST have a reason to exist. Before adding one, ask what responsibility it owns that another should not.
* Do not create abstractions because interfaces look architectural. Use a dependency or interface only when a boundary requires it.
* Never duplicate validation, manifest parsing, executor resolution, version handling, path handling, hashing, error formatting, protocol definitions, configuration loading, environment detection, or runtime lifecycle logic. There MUST be one authoritative implementation.
* Do not leave unused functions, obsolete packages, abandoned abstractions, commented-out code, unused imports, duplicate models, compatibility layers without justification, or debugging utilities. Document any temporary compatibility shim with why it exists, what depends on it, the migration path, and the removal condition.
* Names MUST describe domain responsibility (`executor`, `resolver`, `installer`, `store`, `runtime`, `protocol`, `manifest`, `compiler`, `loader`, `validator`). Avoid `manager`, `helper`, `utils`, `misc`, `common`, `processor`, `thing`, `service2`, `new`. "Manager" is allowed only for genuine lifecycle/state ownership that cannot be named more precisely.
* Files and folders MUST be named after the responsibility they contain, and related logic MUST be grouped together.

### Architectural drift

Before implementing a request, compare it with the current direction. Stop and flag the issue if it would duplicate an abstraction, introduce a competing representation, mix source-language concerns into runtime code, mix registry concerns into execution code, mix CLI concerns into domain code, make the core runtime depend on external providers, introduce unnecessary framework coupling, or make a temporary implementation permanent. Do not silently implement architectural drift.

---

## Quality, security, and validation

### Executor security

Executors are arbitrary code. Treat external executors as untrusted unless explicitly trusted. Never execute downloaded artifacts without verification. Executor packages SHOULD have immutable versions, cryptographic digests, verified artifacts, explicit runtime declarations, platform/architecture declarations, protocol versions, and capabilities. A capability declaration is not proof of permission; actual permissions MUST be enforced by the runtime.

### Runtime isolation

Do not execute arbitrary external code directly inside the N.O.R.E. process. Prefer process isolation for external executors, and support dedicated runtime backends (process, WASM, container, remote) for stronger isolation. Do not load arbitrary third-party executor libraries into the N.O.R.E. address space without a deliberate, documented reason. Dependency collisions, memory corruption, crashes, untrusted code, and incompatible runtimes MUST NOT compromise the core engine unnecessarily.

### Performance

Execution performance is a first-class concern. Do not spawn a fresh runtime per request unless the runtime explicitly requires one-shot execution. Prefer long-lived workers when startup cost is significant:

```text
Executor Pool
    ├── Worker 1
    ├── Worker 2
    └── ...
```

Requests SHOULD be leased to available workers. The runtime SHOULD support worker reuse, bounded concurrency, backpressure, health checks, restart, graceful shutdown, cancellation, and deadlines. Executor instance semantics MUST remain independent of worker-pool implementation.

### Execution-facing model vs internal events

N.O.R.E. may use events internally for scheduling, state transitions, diagnostics, observability, persistence, and recovery. Internal events MUST NOT automatically become the public abstraction. The execution-facing model remains closer to:

```text
input → execution → output / stderr / status
```

Persistent execution history is a storage/retention concern, not a mandatory part of execution semantics.

### Persistence

Execution history SHOULD be configurable with policies conceptually equivalent to `none`, `memory`, and `local`. Execution semantics MUST NOT depend on persistent storage being enabled. The runtime may produce an internal execution record; retention MUST be controlled by storage policy.

### Documentation

Important code MUST contain useful documentation. Document exported types and functions, public SDK APIs, protocol contracts, runtime and lifecycle behavior, security assumptions, configuration semantics, and non-obvious architectural decisions. Explain intent and guarantees, not syntax.

### Testing

Every significant change MUST include appropriate tests at the correct boundary: manifest, compiler, resolver, installer, runtime, protocol, integration, CLI. Do not test implementation details when behavior can be tested through a stable boundary. Important infrastructure changes SHOULD have integration tests. For executor changes, prefer exercising registry → resolution → installation → runtime startup → protocol handshake → execution → result.

### Performance validation

Do not claim an architecture is "fast" without measuring it. For runtime changes, measure cold-start latency, warm execution latency, memory usage, concurrent behavior, throughput, and restart cost. Optimize based on measurements. Do not prematurely introduce complicated distributed infrastructure.

### Security validation

For changes involving external executors, installation, registries, packages, or runtime execution, explicitly consider artifact authenticity, digest verification, path traversal, arbitrary file writes, executable permissions, environment leakage, secret leakage, privilege escalation, network access, filesystem access, process isolation, resource exhaustion, malicious executor behavior, and dependency compromise.

Do not assume GitHub is a security boundary. A GitHub release is a distribution source, not proof of trust.

### Dependency discipline

Prefer standard-library and existing repository dependencies. Before adding one, determine whether the repository already solves the problem, whether it is maintained, its security/reliability implications, whether its abstraction matches the architecture, and whether it creates unnecessary coupling. Do not add a dependency for a trivial helper.

### Research discipline

Prefer official documentation, standards, and primary sources. Distinguish facts from inference, record important architectural assumptions, and do not copy external architecture blindly. Research validates decisions; it does not replace repository understanding.

---

## Change discipline

### Understand before modifying

Before a non-trivial change: read the relevant structure and documentation, identify the boundary, trace data/control flow, identify the public API, tests, and related implementations, determine whether the behavior already exists elsewhere, and determine whether the request conflicts with the architecture. Only then propose an implementation.

### Plan before coding

For significant changes, write a short plan covering problem, current and desired behavior, affected boundaries and packages, data and control flow, compatibility, security, performance, testing, migration, and documentation. Verify that no existing abstraction solves the problem, that the proposal has one responsibility, and that it does not duplicate logic or introduce a competing architecture. Resolve conflicts before coding.

### Git branch and commit discipline

Do not implement a significant feature, architectural change, refactor, or bug fix directly on the primary branch. Use a descriptive, lowercase, intent-grouped branch: `feature/`, `fix/`, `refactor/`, `perf/`, `security/`, `docs/`, `test/`, `chore/`. Avoid meaningless names (`test`, `changes`, `new`, `work`, `stuff`, `update`).

Commits MUST be coherent units of work that build and pass relevant tests. Prefer conventional messages such as `feat(executor): add process runtime`, `fix(loader): reject invalid executor requirements`, `refactor(compiler): isolate executor resolution`. Do not mix unrelated changes. Do not leave debugging code, temporary files, generated artifacts, commented-out experiments, or unrelated formatting. Never commit secrets.

### Validation

Run `scripts/script.sh` at the validation points required by this repository and before declaring a phase complete or committing it. If the script is missing, unavailable, destructive, environment-dependent, or fails, inspect its behavior and report the exact condition. Do not invent a successful run or blindly repeat a potentially destructive command. Run focused checks during development and the full applicable suite before final completion. A phase is not validated until its required checks pass or an explicit, justified limitation is recorded.

### Definition of done

Before work starts, define observable acceptance criteria. A task is complete only when:

* all agreed deliverables exist and are integrated
* behavior matches the acceptance criteria, including failure behavior
* required tests, builds, scripts, lint, and static checks have run successfully
* changes have been reviewed for regressions, security, compatibility, and unintended scope
* documentation and `todos/` reflect the resulting behavior
* no unresolved blocker is hidden or misrepresented

If a required check cannot run, the task is not fully verified. Record the exact check, why it could not run, and the missing evidence. Do not mark it `done`.

### Task states

Use `not_started`, `in_progress`, `blocked`, `needs_review`, and `done` consistently. A task MUST NOT move to `done` because code was written, a PR was opened, a delegate finished, or a command was attempted. Record the evidence. A `blocked` task MUST name the blocker, its impact, and the condition to resume. Partial progress MUST remain visible.

A delegate's completion message is a claim to review, not acceptance.

---

## Multi-Agent Operation

### Default coordinator

The default agent is `coordinator`, defined globally at `~/.config/opencode/agents/coordinator.md`. It is the accountable technical owner: it establishes scope and acceptance criteria, maintains the ledger, assigns ownership and leases, delegates, integrates, validates, and reports. It does not edit source code; it writes only under `.agents/`.

The coordinator remains responsible for the complete outcome. Delegation transfers execution, never responsibility for correctness.

### Roster

| Agent | Mode | Writes | Purpose |
|---|---|---|---|
| `coordinator` | all | `.agents/**` only | Scope, ledger, leases, delegation, integration, validation |
| `architect` | subagent | `.agents/decisions/**` | Boundaries, dependency direction, isolation, scalability, ADRs |
| `coder` | subagent | leased source paths | Bounded implementation with tests |
| `bugfixer` | subagent | leased source paths | Reproduction-first defect repair with regression tests |
| `reviewer` | subagent | none | Correctness, regressions, acceptance, boundaries |
| `security` | subagent | none | Supply chain, secrets, isolation, permissions, resource bounds |
| `documenter` | subagent | docs and markdown | Documentation synchronized with verified behavior |

Role rules are authoritative in each agent's global file. This table is an index, not a replacement.

### Coordination surface

```text
.agents/
    README.md          protocol and schema
    ledger.yaml        authoritative active-work ledger
    ownership.yaml     path-to-owner map and sign-off triggers
    reports/           one report per delegated task, plus handoffs
    decisions/         architecture decision records
```

`todos/` remains the product backlog. `ledger.yaml` tracks active execution only.

### Startup protocol

Before any work:

1. Read this file in full, plus any nested `AGENTS.md` governing the paths in scope.
2. Read `.agents/README.md`, `.agents/ownership.yaml`, and `.agents/ledger.yaml`. Create the scaffolds if missing.
3. Record the branch and working-tree state.
4. Restate the objective, constraints, explicit out-of-scope items, acceptance criteria, and definition of done.
5. Identify affected boundaries, modules, dependencies, risks, and existing tests before dispatching work.

### Ledger

`.agents/ledger.yaml` is the single source of truth. Do not keep a competing plan.

```yaml
version: 1
tasks:
  - id: T-001
    objective: "One sentence, observable"
    scope: ["path/or/module/**"]
    owner: coder
    reviewer: reviewer
    signoff: [architect]
    dependencies: []
    acceptance:
      - "Observable criterion"
    verification:
      - "Exact command or method"
    status: not_started
    lease: null
    evidence: []
    reports: []
    open_risks: []
```

One owner per task. `done` requires every acceptance criterion verified and every required check recorded in `evidence`. `blocked` names the blocker, its impact, and the resume condition.

### Ownership and leases

`.agents/ownership.yaml` maps paths to owners and sign-off triggers. Before dispatching a mutating task, read the map, assign an owner if needed, claim a lease in the ledger, and confirm no overlapping active lease. Release the lease when the task reaches `needs_review` or `done`. Read-only agents never need a lease and may run in parallel.

Never run overlapping mutating tasks concurrently. If ownership is unclear, derive it from the repository layout, write a proposed map, and proceed without reorganizing the repository to fit the map.

### Delegation brief

Every task brief MUST contain:

```text
TASK: <id> — <one-line objective>
OWNER: <agent>
LEASE: <paths you may modify, or "read-only">
OBJECTIVE: <observable outcome>
CONTEXT: <verified facts, file:line references, relevant contracts>
IN SCOPE: <exact paths, modules, symbols>
OUT OF SCOPE: <explicit exclusions>
ACCEPTANCE: <observable criteria>
VERIFICATION: <exact commands or methods>
DELIVERABLE: <diff summary + .agents/reports/<id>.md + short chat summary>
CONSTRAINTS: <boundaries, compatibility, security, performance>
```

Parallelize only independent read-only tasks, or mutating tasks with disjoint leases. Never split tightly coupled work across concurrent mutating agents. Do not delegate a task you have not bounded. Prefer `explore` for broad discovery.

### Required sign-off

| Trigger | Required |
|---|---|
| Any non-trivial change | `reviewer` |
| Module boundary, dependency direction, public contract, folder layout, persistence, concurrency, isolation | `architect` |
| Runtime, registry, resolver, installer, artifact, storage, provider, daemon, auth, secrets, dependencies, CI, scripts, executor protocol | `security` |
| User-facing behavior, CLI, configuration, public API, docs | `documenter` |

The coordinator may not waive a required sign-off without recording the reason and residual risk in the ledger.

### Parallelism and integration

1. Review the actual diff; do not trust a summary.
2. Verify each acceptance criterion against the ledger.
3. Run the project-declared validation (`scripts/script.sh`) and any targeted checks.
4. Require the sign-offs above.
5. Reconcile contradictions yourself; do not average conflicting findings.
6. If a check cannot run, record `blocked` or an explicit verification limitation.

Necessary, directly caused, small, and bounded defects are fixed in scope and recorded. Unrelated, broad, or risky defects are recorded in `todos/` with evidence, impact, and a completion condition. Critical security, data-loss, or correctness risks are escalated before proceeding.

### Handoff and compaction

Before compaction or handoff, write `.agents/reports/handoff-<timestamp>.md` containing the objective and constraints, an explicit instruction to read `AGENTS.md` in full, the branch and tree state, verified facts with `file:line`, decisions and rejected approaches, the ledger snapshot, commands run with exact outcomes, open risks, and the next safe action.

Never mark an in-progress task done during a handoff. Never omit failed checks. Never convert an assumption into a fact. After resuming, read the instructions, verify the branch and tree, re-check the ledger against reality, and correct a stale summary before proceeding.

### Conflict resolution and escalation

Leases and the ledger win over chat claims. Inspect primary evidence before deciding. If a decision is architectural, ask `architect` for a recommendation and then decide. If a delegate returns incomplete or contradictory work, request a correction or handle it with available means. Do not hide coordination failures.

Ask the user only when a material architectural, scope, or acceptance decision cannot be resolved from the repository or this contract. Ask precise questions that identify the decision.

### Subagent file access

Subagents MUST NOT use `cat`, `head`, `tail`, or `sed` to read files from the terminal. Use the Read, Grep, and Glob tools. This keeps access auditable and prevents terminal output from bypassing review.

### Non-negotiables

* Do not hallucinate repository behavior, APIs, types, files, packages, runtime behavior, configuration, protocol semantics, CLI commands, registry behavior, deployment behavior, or undocumented assumptions.
* If the repository does not establish something, say so and ask.
* Do not describe an attempted action as completed.
* Do not conceal a failed, skipped, unavailable, or inconclusive check.
* Do not leave work half-done without making the remaining work, reason, and owner visible.
* Do not add comments that merely restate code.
* Never commit, amend, push, or force-push unless the user explicitly asks.

---

## Final rule

When forced to choose between fast implementation and correct architecture, choose the correct architecture.

When forced to choose between a clever abstraction and a simple explicit boundary, choose the simple explicit boundary.

When forced to choose between guessing and asking for clarification, ask for clarification.

When forced to choose between declaring a task done and finishing it correctly, finish it correctly.

Neuron/N.O.R.E. is infrastructure. Treat every boundary, protocol, package, runtime, and public API as something that must remain understandable and defensible years after the original implementation.
