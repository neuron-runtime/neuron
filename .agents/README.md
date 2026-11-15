# Multi-Agent Workspace

This directory is the durable, project-local coordination surface for the global specialist agents defined in `~/.config/opencode/agents/`.

It is intentionally versioned so that a fresh session can resume work without relying on chat history.

## Files

| Path | Purpose | Writer |
|---|---|---|
| `ledger.yaml` | Authoritative task ledger: scope, owner, dependencies, acceptance, verification, status, lease, evidence | coordinator |
| `ownership.yaml` | Path-to-owner map and sign-off triggers | coordinator |
| `reports/<task-id>.md` | One report per delegated task, including checks and exact outcomes | task owner |
| `decisions/NNNN-<slug>.md` | Architecture decision records | architect |
| `reports/handoff-<timestamp>.md` | Compaction or session handoff record | coordinator |

The project's `todos/` directory remains the product backlog. `ledger.yaml` tracks active execution; it does not replace `todos/`.

## Agent roster

| Agent | Mode | Writes | Purpose |
|---|---|---|---|
| `coordinator` | all | `.agents/**` only | Owns scope, ledger, leases, delegation, integration, validation |
| `architect` | subagent | `.agents/decisions/**` | Boundaries, dependency direction, isolation, scalability, ADRs |
| `coder` | subagent | leased source paths | Bounded implementation with tests |
| `bugfixer` | subagent | leased source paths | Reproduction-first defect repair with regression tests |
| `reviewer` | subagent | none | Correctness, regressions, acceptance, boundaries |
| `security` | subagent | none | Supply chain, secrets, isolation, permissions, resource bounds |
| `documenter` | subagent | docs and markdown | Documentation synchronized with verified behavior |

## Ledger schema

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

Valid states: `not_started`, `in_progress`, `blocked`, `needs_review`, `done`.

A task may move to `done` only when every acceptance criterion is verified and every required check is recorded in `evidence`. A `blocked` task must name the blocker, its impact, and the condition to resume.

## Lease protocol

1. Read `ownership.yaml` and assign a mutating owner.
2. Claim a lease in the ledger: `lease: { owner: coder, paths: [...], task: T-001 }`.
3. Do not run overlapping mutating tasks. Queue instead.
4. Release the lease when the task reaches `needs_review` or `done`.
5. Read-only agents never need a lease and may run in parallel.

## Required sign-off

| Trigger | Required |
|---|---|
| Any non-trivial change | `reviewer` |
| Module boundary, dependency direction, public contract, folder layout, persistence, concurrency, isolation | `architect` |
| Runtime, registry, resolver, installer, artifact, storage, provider, daemon, auth, secrets, dependencies, CI, scripts, executor protocol | `security` |
| User-facing behavior, CLI, configuration, public API, docs | `documenter` |

The coordinator may not waive a required sign-off without recording the reason and the residual risk in the ledger.

## Handoff

Before compaction or handoff, write `reports/handoff-<timestamp>.md` with the objective, branch and tree state, verified facts (`file:line`), decisions and rejected approaches, ledger snapshot, commands run with exact outcomes, open risks, and the next safe action. Instruct the next agent to read `AGENTS.md` in full before acting.
