# First External Executor — Case Study & UX Audit

> **Status:** internal working document
> **Scope:** how the first external executor (`content-extract`, a .NET executor) was
> authored, registered, and run end-to-end — and every error, constraint, bug, and UX
> friction encountered along the way.
> **Purpose:** an honest engineering post-mortem, not marketing. The goal is to make
> authoring, installing, and running external executors as frictionless as running a
> built-in module.

This document records:

1. the full path a system takes from a Service requirement to a running external
   executor;
2. every issue found while building and shipping the first real executor
   (`content-extract`) through the local registry;
3. a prioritized fix plan;
4. pointers to concrete TODO items in [`/TODO.md`](../TODO.md).

Every claim below points at the source location that establishes it. Where the current
behavior is "dead config" or "silently ignored", that is stated explicitly rather than
assumed to work.

---

## 1. How an external executor actually runs

The happy path, end to end, with the exact artifacts involved:

```
Service (TypeScript or YAML)
    │  .executor({ name: "Muhammad-Jay:content:extract", version: "^1.0.0", registry: "local" })
    ▼
Executor Requirement                      (application/executor/requirement.go:52 ParseType)
    │
    ▼
Local Registry (directory-backed catalog, offline)
    │  reads <catalog>/<owner>/<capability>/<version>/executor.json
    ▼
Resolver (semver selection)
    │  picks the best version satisfying ^1.0.0
    ▼
Installer (immutable install)
    │  downloads/links the canonical archive
    │  verifies SHA-256 digest
    │  extracts, materializes the entrypoint
    │  writes install.json
    ▼
Executor Store                       (~/.neuron/executors/Muhammad-Jay/content/extract/1.0.0/)
    │  (immutable installed artifact — never re-resolved at run time)
    ▼
Freeze                              exact version frozen into the registered System
    ▼
N.O.R.E. Registration               content-metadata-extraction@1.0.0#<sha>:development
    ▼
Runtime (process backend, neuron/executor-v1 gRPC)
    │  launches the installed binary out-of-process
    │  handshake → execution → result
    ▼
Instance + Execution events         service.ready → service.started → service.completed → execution.completed
```

### 1.1 The real end-to-end run

The reference executor lives in a sibling repository (`Desktop/content-extract`) and
reuses the .NET SDK at `packages/executor-dotnet` (built first, `134c44c`). It was
registered and executed against the local registry:

```bash
go build -o /tmp/neuron ./application/cmd/neuron
go build -o /tmp/nore-daemon ./nore/cmd/nore

/tmp/neuron register --lang ts --root . --nore-path /tmp/nore-daemon -v
# → content-metadata-extraction@1.0.0#<sha>:development (registered)

/tmp/neuron run "content-metadata-extraction@1.0.0" \
  --nore-path /tmp/nore-daemon \
  --input '{"document":{"name":"report.md","content":"# Q3 Revenue\n\nRevenue increased by 18%.\n\n- not prose\n\nHeadings tell the story."}}' \
  -v
# → service.ready / service.started / service.completed / execution.completed
# → metadata: title "Q3 Revenue", headings ["Q3 Revenue"], wordCount 8, paragraphCount 2
```

The installed artifact at `~/.neuron/executors/Muhammad-Jay/content/extract/1.0.0/` holds
`.artifact` (the canonical archive), the single-file `content-extract` binary, plus
`executor.json` and `install.json` (registry `local`, platform `linux-amd64`, protocol
`neuron/executor-v1`, digest `sha256` of the archive).

### 1.2 The pieces that had to exist

| Piece | Where | Responsibility |
| --- | --- | --- |
| .NET executor SDK | `packages/executor-dotnet` | `neuron/executor-v1` gRPC server wrapper, `ExecutorHandler`, health/shutdown |
| Executor repo | `Desktop/content-extract` | the actual capability (`content-extract` binary), TS Service package, release script, CI/CD |
| `executor.json` | repo root manifest | identity `Muhammad-Jay:content:extract`, runtime `process/content-extract/neuron/executor-v1`, services, capabilities, platforms |
| TS Service package | `packages/typescript` | `Service("content.extract").executor({name, version, registry:"local"})` |
| Example system | `example/` | `System("content-metadata-extraction")` with `withParams` binding |
| `release.sh` | `scripts/release.sh` | publish → inject real `platforms` into `executor.json` → archive `Muhammad-Jay-content-extract-<ver>-executor.neuron.tar.gz` |
| CI/CD | `.github/workflows/` | `ci.yml` (build + test, TS build) and `release.yml` (tag → archive → GitHub release) |

---

## 2. Issues encountered (the honest inventory)

Issues are grouped by theme, then severity. Each entry states the observed symptom, the
reason, the source location, and the recommended fix.

### A. Config duplication and confusion

**A1 — Two config files are required for a TS project that uses an external executor.**
A TS project *authoring* surface is `neuron.config.ts`, but *executor resolution* reads
`neuron.yaml`. A user who only writes `neuron.config.ts` (and never touches
`neuron.yaml`) finds external executors silently unresolved. There is no single
authoritative config surface.

- `neuron.config.ts` (`config.executorRegistries`) is compiled into the manifest's
  `config` field (`application/compiler/config.go:44-50`), which flows nowhere at
  resolution time.
- Resolution reads the loaded application config from `neuron.yaml` via
  `BuildCatalog` (`application/internal/executorctl/executorctl.go:67-88`).
- The shipped example had to declare registries in *both* places
  (`packages/typescript`/`example/neuron.yaml`) — only `neuron.yaml` took effect.

**A2 — The default `local` registry URL is a dead placeholder.**
`Defaults()` ships `{Name: "local", URL: "local://"}`
(`application/config/defaults.go:41-45`). `BuildCatalog` silently skips any local
registry whose URL is empty or exactly `local://`
(`application/internal/executorctl/executorctl.go:78`). A project that has never declared
its own local registry is therefore already configured to fail against external
executors from `local`.

**A3 — `defaultRegistries` exists but is never populated.**
The field is declared (`application/config/config.go:94`) and used as the fallback when a
requirement declares no registries (`application/internal/executorctl/executorctl.go:106-118`),
but `Defaults()` never sets it. Requirements without an explicit `registry` get no
fallback, and the resulting error does not explain which registry names are valid.

**A4 — Unknown registry names are silently dropped.**
`BuildCatalog` handles exactly two names — `github` and `local`; anything else is ignored
without a warning. The YAML example ships `name: official`
(`examples/ecommerce_order/neuron.yaml:32`), which resolves to nothing, and the failure
only surfaces later as "executor registry not configured".

### B. Naming confusion

**B1 — Service name and executor name default to the same value.**
If a Service omits `.executor()`, the SDK assigns the executor name from the service
name (`packages/sdk/src/service.ts`). Service names are dotted (`content.extract`);
executor names must be `owner:capability[:sub]`. A service named `content.extract` then
fails at `ParseType` with "must contain at least one ':' separator"
(`application/executor/requirement.go:52-55`), and the message never explains the
`owner:capability:sub` convention.

**B2 — The same concept has different names at every layer.**
The executor identity is `type` in YAML, `name` in the manifest/compiler, `Type` in the
resolver, and `core.ServiceType` in the runtime. The registry is `source` in YAML and
`registry` in the manifest/compiler. Users read different vocabulary for the same thing
as data crosses layers.

**B3 — `services[]` in `executor.json` is inert.**
It is schema-required but never matched against a System's service names. Both shipped
executors merely repeat the executor name inside it, reinforcing the false equivalence
between service and executor identity.

**B4 — Local-registry identity reconciliation is skipped.**
The installer only verifies that an artifact's declared name/version matches the
requirement when `pkg.Manifest == nil` (`application/executor/installer.go:78-85`). The
local registry always populates `pkg.Manifest`, so a mismatched `metadata.name` is
silently installed under an unrelated type path.

### C. TypeScript build friction

**C1 — `pnpm install` alone is not enough; the SDK must be built.**
The Go CLI launches `neuron-sdk` from `node_modules/.bin/`. If the workspace is
installed but the SDK target JS was not built (`pnpm build:sdk`), the symlink exists but
the JS does not, and the failure is a cryptic Node ENOENT surfaced as "typescript build
failed".

**C2 — `neuron.config.ts` `script.build` is dead.**
The field exists in the config type (`packages/sdk/src/cli/config.ts:6-8`) and the
shipped example even sets it (`examples/ecommerce_order_ts/neuron.config.ts:5-7`); it is
never executed.

**C3 — `entryFile` is dead code in the SDK.**
`project.ts` hardcodes `"index.ts"` (`packages/sdk/src/cli/project.ts:33`) while
`build.ts` independently recomputes the entry (`packages/sdk/src/cli/build.ts:15`). The
returned `entryFile` is never consumed.

**C4 — Config-file discovery does not short-circuit.**
If multiple `neuron.config.*` candidates exist, the last found wins silently.

**C5 — No manifest validation at build time.**
`build.ts` only checks that a default export exists (`packages/sdk/src/cli/build.ts:21`);
a malformed manifest passes the build stage and fails later inside the Go compiler.

### D. Daemon / runtime UX

**D1 — No explicit `neuron daemon start` command.**
The daemon is always started implicitly by `register`/`run` via `bootstrap.SetupClient`.
Users cannot pre-start it, and there is no status surface short of full lifecycle
commands.

**D2 — Daemon startup failure output is swallowed without `-v`.**
A failing daemon start surfaces as "context deadline exceeded" (`ErrStartTimeout`,
`application/daemon/manager.go:77`) with no daemon stderr attached by default.

**D3 — Stale socket file left on shutdown.**
The daemon closes its listener but does not unlink the socket path; the next start
self-heals, but the presence of a live-looking socket between runs is confusing.

**D4 — One global daemon shared across projects.**
`Ensure` reuses any healthy daemon. If project A started the daemon with
`storage.directory: ./.neuron/data`, project B silently registers into A's data
directory.

**D5 — Restored instances show `failed` instead of `stopped`.**
A clean stop does not persist the terminal status; on restore, `starting`/`running`
states are coerced to `failed`.

**D6 — `neuron daemon stop` timeout can SIGKILL a daemon that is still shutting down.**
The failure path already uses a 5s context (`application/daemon/manager.go:40`); the
worker shutdown grace is larger, so a busy daemon can be force-killed mid-shutdown.

**D7 — Orphaned executor processes on daemon SIGKILL.**
Spawned executors are not placed in a kill-on-parent-death process group; a gRPC
executor with no connection-loss watcher blocks in `Serve` forever after the daemon dies.

**D8 — `--nore-path` is required but undocumented until failure.**
The daemon binary is found via override → sibling → PATH → source-tree fallbacks, but a
plain source-tree build gives "nore runtime not found: set daemon.norePath (or
--nore-path)" — the first the user learns of the flag.

### E. Documentation gaps

**E1 — Shipped examples use stale event names.**
`docs/GETTING_STARTED.md` shows `service.evaluating` (lines 71, 238), but the real
events are `service.started`/`service.completed`/`execution.completed`
(`nore/internal/event/event_types.go:33-35`).

**E2 — Example config carries dead/placeholder values.**
`examples/ecommerce_order_ts/neuron.config.ts` sets a dead `script.build`,
`storage.directory: "./home"`, and `storage.provider: "postgres"` (the runtime
persistence is file-based today).

**E3 — Root help text calls Neuron a "workflow engine".**
`application/internal/cli/cli.go:25` — `"Neuron workflow engine CLI"`. Neuron explicitly
is *not* a workflow engine (see README).

**E4 — `application/README.md` misdescribes the daemon lifecycle.**
It claims daemon stop on process exit; the daemon persists.

**E5 — TODO.md references a nonexistent design-notes dir.**
`docs/executors/2026-09-05-*.md` does not exist; the Documentation item dangles.

**E6 — `neuron instance` with no subcommand exits silently.**
No help, no error — nothing to teach the user what it does.

### F. Structural constraints

**F1 — `neuron.yaml` is overloaded.**
It is at once the project definition (YAML authoring), the Go CLI config, and the
executor-registry config. TS authors do not need it for authoring but *do* need it for
executor resolution — which is exactly the least documented coupling.

**F2 — Path resolution is asymmetric.**
`storage.directory` and `executors.storeDir` expand against the project root, while the
`local` registry URL is resolved against the CLI's working directory, all within the
same config block (`executors.registries`).

**F3 — Viper replaces slices on merge.**
Declaring `executors.registries` in `neuron.yaml` replaces the default list instead of
merging; the default `github` registry silently disappears, so users must re-declare it
to keep both `github` and `local`.

**F4 — The `neuron init` scaffold never mentions executors.**
The generated `neuron.yaml` carries no registry config at all, so a fresh project's
first encounter with external modules is a resolution failure.

---

## 3. Recommended improvements (prioritized)

### P0 — must fix before a public release

1. **Make the config surface single and honest.** Either merge executor-registry config
   into `neuron.config.ts` (and consume the manifest's `executorRegistries` at
   resolution), or make `neuron.yaml` the documented single source and remove the
   misleading `config.executorRegistries` path.
2. **Fix the `local://` dead default.** Remove the default `local` registry, or make the
   package-local/catalog discovery functional. Do not ship a value that is filtering
   itself out of the catalog.
3. **Populate `defaultRegistries`.** Ship defaults (`local`, `github`) so requirements
   without an explicit registry have a defined fallback.
4. **Validate the manifest shape in the SDK** before writing `.neuron/manifest.json`, and
   make the "no executor registries" error state the exact fix.
5. **Explode loudly on unknown registry names** in `BuildCatalog` instead of ignoring
   them.

### P1 — significant UX improvement

1. **Rename `core.ServiceType` → `core.ExecutorType`** to describe what it actually
   holds (the executor identity).
2. **Add `neuron init --lang ts`** scaffolding that produces a runnable project
   including a `neuron.yaml` executor-registry block.
3. **Add `neuron daemon status`** (running/pid/data-dir/socket/uptime).
4. **Surface daemon stderr** in CLI startup errors by default.
5. **Remove dead config:** `script.build`, `Project.entryFile`, `storage.provider`.
6. **Validate executor identity vs requirement** for all install paths
   (`installer.go:78`), not just archive-based ones.

### P2 — polish

1. Fix all stale docs (E1–E6).
2. Short-circuit config-file discovery with a warning.
3. Document the `owner:capability:sub` convention in SDK README + MODULES.md.
4. Consistent path resolution for `executors.registries[].url`.
5. Merge (not replace) slice config in Viper.
6. Clean up the socket file on daemon shutdown.
7. `neuron instance` (no subcommand) prints help.

---

## 4. Relationship to TODO.md

Every actionable item above is recorded as a checkbox in
[`/TODO.md`](../TODO.md) under **Fixes**, **Improvements**, or **Documentation**.
This document is the narrative; the TODO list is the tracker. As items are fixed, tick
them in TODO.md and update the corresponding claim here only if the *behavior*
changed — this document is a living audit, not a changelog.