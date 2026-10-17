# Neuron Command Line Reference

The `neuron` CLI is the single user-facing interface to Neuron: author projects, build and register assemblies, manage external modules, create instances, execute assemblies, and stream execution events while it manages the N.O.R.E. runtime engine (the daemon) for you in the background.

> [!IMPORTANT]
> You never interact with the N.O.R.E. daemon directly. The CLI starts it, checks its health, talks to it over a local Unix socket, and stops it. From the user's perspective there is a single product: `neuron`.

Everything in the [command reference](#command-reference) is generated from the actual command definitions in this module (`application/cmd/neuron` and `application/internal/cli`), so it stays accurate as long as it is kept in sync with the code.

```mermaid
sequenceDiagram
    participant U as User
    participant C as neuron CLI
    participant D as N.O.R.E. daemon
    participant S as Capability Runtime Store

    U->>C: neuron build
    C->>C: build project → canonical manifest
    C->>C: compile → resolve modules → freeze versions
    C->>D: daemon healthy?
    alt not running
        C->>D: start bundled nore (Unix socket + data dir)
    end
    C->>D: POST /v1/register (compiled assembly + frozen capability runtimes)
    D-->>C: assembly key
    C-->>U: built and registered with key

    U->>C: neuron run --params '{...}'
    C->>D: POST /v1/run?assembly=<key>
    D-->>C: Assembly result
    C-->>U: result on stdout (JSON or text)
```



[Version](https://github.com/neuron-runtime/neuron/releases)
[Go](https://go.dev)
[License](../LICENSE)

---



## Table of Contents

- [Install & Verify](#install--verify)
- [How the CLI Talks to N.O.R.E.](#how-the-cli-talks-to-nore)
- [Global Flags](#global-flags)
- [Command Reference](#command-reference)
- [Configuration Reference](#configuration-reference)
- [Module Resolution](#module-resolution)
- [Building the CLI](#building-the-cli)



## Install & Verify

Neuron is distributed as a single archive containing the `neuron` CLI and the N.O.R.E. runtime. You only place `neuron` on your `PATH`.

```bash
neuron version
```

See [docs/INSTALLATION.md](../docs/INSTALLATION.md) for the full installation guide.

## How the CLI Talks to N.O.R.E.

The CLI communicates with the N.O.R.E. daemon over a local Unix domain socket. When you run a command that needs the runtime, the CLI automatically:

1. Checks whether the daemon is already healthy.
2. If not, starts it from the bundled `nore` binary with the effective configuration (socket path, data directory, worker count).
3. Talks to it over the socket.

The daemon runs as a persistent background process: it survives the CLI process and keeps serving instances until you stop it with `neuron daemon stop`. The socket defaults to `~/.neuron/nore.sock` and can be overridden with the `NEURON_SOCKET` environment variable or the `daemon.socket` value in the machine-level global configuration (`~/.config/neuron/config.yaml`) — `daemon.*` is rejected from project configuration. A remote daemon can be used instead with the `--remote` flag.

The API requires a bearer token on every request except the health probe. The daemon generates one at startup and publishes it beside its socket in `nore.sock.token` (mode `0600`), so a local client authenticates automatically; `neuron` reads it from there, or from `NEURON_API_TOKEN` when the token file is not reachable (for example against `--remote`). The token is removed on graceful shutdown, because a credential outliving the process that issued it is a credential nobody rotated.


| Transport            | Purpose                                                            |
| -------------------- | ------------------------------------------------------------------ |
| JSON over HTTP       | Regular requests — registration, instance and execution management |
| WebSocket (`/v1/ws`) | Live execution event streaming                                     |
| Server-Sent Events   | Streaming fallback for transports without WebSocket support        |




## Global Flags

The following flags are available on every `neuron` command.


| Flag                 | Description                                                      |
| -------------------- | ---------------------------------------------------------------- |
| `--config string`    | Project config file (one of `neuron.config.json` / `.yaml` / `.yml`; auto-discovered) |
| `--log-level string` | Logging level (default `"info"`)                                 |
| `-v, --verbose`      | Enable verbose output (shows N.O.R.E. daemon logs)               |
| `--remote string`    | Remote N.O.R.E. endpoint (e.g., `https://api.nore.example.com`)  |
| `--nore-path string` | Path to the `nore` daemon binary                                 |
| `--force`            | Force replacement (clear existing state before the command acts) |
| `-h, --help`         | Help for the command                                             |
| `--version`          | Print the CLI version and exit                                   |




## Command Reference



### `neuron version`

Print the Neuron CLI version.

```bash
neuron version
neuron --version
```

Both print the same version. The version is injected at build time from the release tag; a development build reports `dev`.

### `neuron init`

Initialize a new Neuron workspace.

```
Usage:
  neuron init [Target] [flags]

Flags:
  -l, --lang string   project authoring language (ts, typescript, yaml, yml) (default "ts")
```

`Target` is the directory to create (relative to the current directory). `neuron init` creates the directory and scaffolds a runnable project: a `neuron.config.json`, and — for the default TypeScript authoring surface — a `package.json` declaring `@neuron/sdk`, a `tsconfig.json`, a starter `assembly.ts`, and the canonical local capability runtime root `neuron/capabilityRuntimes/`.

```bash
# Create ./my-assembly as a TypeScript project (default)
neuron init my-assembly

# Create ./my-assembly as a YAML project
neuron init my-assembly --lang yaml
```

The scaffolded config declares the project identity (`name` / `version`, which must match the authored Assembly metadata), the authoring language and entry file, and the loopback runtime endpoint:

```json
{
  "name": "my-assembly",
  "version": "1.0.0",
  "lang": "typescript",
  "entry": "assembly.ts",
  "runtime": {
    "host": "localhost",
    "port": "auto",
    "execution": { "mode": "wait", "timeout": "30m" }
  },
  "capabilityRuntimes": { "localRoots": ["./neuron/capabilityRuntimes"] },
  "inspector": { "enabled": true, "address": "127.0.0.1:7433" }
}
```

### `neuron build`

Build the current project and register the assembly with N.O.R.E.

```
Usage:
  neuron build [flags]

Flags:
  -l, --lang string   project authoring language (yaml, yml, typescript, ts)
  -r, --root string   project root (defaults to the current directory)
```

`neuron build` runs the full authoring pipeline in one step. The project configuration must declare the Assembly identity (`name` and `version`): the build fails with a clear error when either is missing, or when either disagrees with the authored Assembly metadata.

| Step         | Responsibility                                                                                                                                                                                                                             |
| ------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **Build**    | The project (YAML or TypeScript) is resolved into the canonical `.neuron/manifest.json`                                                                                                                                                    |
| **Compile**  | The canonical manifest is compiled into a runtime assembly representation                                                                                                                                                                   |
| **Resolve**  | External module requirements are resolved through the configured registries, installed into the local store, and the exact versions are frozen into the build record. Built-in modules are skipped — they run in-process inside N.O.R.E. |
| **Register** | The compiled assembly and its frozen module set are sent to N.O.R.E., which persists it and returns an assembly key                                                                                                                          |

The CLI records the registration key in `.neuron/register.json` and prints it on success:

```text
order-processing@1.0.0#a1b2c3d4:development
```

```bash
# Build the project in the current directory (language auto-detected)
neuron build

# Force a language and a different project root
neuron build --lang typescript --root ./pipeline

# Replace any previously registered version of this assembly
neuron build --force
```

After the build, `neuron run` executes the registered assembly.

> `neuron register` is a deprecated alias of `neuron build`, hidden from help, kept for existing workflows. It will be removed.

### `neuron run`

Run a Neuron Assembly.

```
Usage:
  neuron run [instance-id|assembly-key] [flags]

Flags:
      --format string      Output format for the Assembly result (json, text) (default "json")
      --params string      JSON params payload for execution (e.g., '{"key":"value"}')
      --params-file string Path to JSON file with params payload
      --port string        Override local runtime port (default: auto-discover from project)
      --remote string      Remote N.O.R.E. endpoint (e.g., http://host:port)
```

`neuron run` is a client of the project runtime. It discovers the runtime that bare `neuron` started, sends one synchronous execution request to `POST /v1/run`, and prints the Assembly result to stdout. It does not start or stop the runtime, stream events, or return execution handles — starting, observing, streaming, and cancelling executions are advanced inspection concerns exposed through the API, not the normal `run` output.

The Assembly is addressed by the `assembly` selector on the request URL, never inside the params payload, so the same command works whether the runtime hosts one Assembly or many. Without an argument, `neuron run` uses the key of the current project build — the one `neuron build` recorded in `.neuron/current.json`. With a target argument it addresses that Assembly instead:

| Target form             | Example                             | Meaning                                  |
| ----------------------- | ----------------------------------- | ---------------------------------------- |
| Instance ID             | `inst_ab12cd`                       | Run the instance with that exact ID      |
| Key, colon form         | `order-processing:1.0.0`            | Run the assembly at that version         |
| Key, at form            | `order-processing@1.0.0`            | Same as the colon form                   |
| Bare name               | `order-processing`                  | Run the key's latest registered version  |

Remaining key segments (`:hash`, `:env`) are preserved as given. Instance IDs (`inst_*`) pass through unchanged; anything else is parsed as an assembly key and normalized to its colon-encoded wire form. When no argument is given and the project is not built, the command stops with a message pointing you at `neuron build`.

```bash
# Run the current project build with no params
neuron run

# Provide params inline
neuron run --params '{"order": {"id": "ord_123", "total": 4250, "currency": "USD"}}'

# Load params from a file
neuron run --params-file ./params.json

# Run the latest registered version of a named assembly
neuron run order-processing

# Emit the Assembly result as text instead of JSON
neuron run --format text
```

stdout contains only the Assembly result in the selected `--format`; logs, diagnostics, and execution errors go to stderr, and a failed or cancelled execution exits non-zero. The result comes from one synchronous `POST /v1/run` request — the command does not stream events, expose execution handles, or list execution history.

Starting, observing, streaming, and cancelling executions remain available as advanced API operations: `POST /v1/instances/{id}/executions` starts an execution (optionally detached), its event stream is served over `WS /v1/ws` or `GET .../events/stream`, and `POST /v1/instances/{id}/executions/{execID}/cancel` stops one — answering `404` if there is no such instance or execution, `409` if it already reached a terminal state, and `200` if this call stopped it.

### `neuron add`

Resolve and install an external module (capability runtime) into the local store.

```
Usage:
  neuron add [name@version] [flags]
```

`name@version` selects an exact or constrained version (for example `example:echo` or `example:echo@^1.0.0`). Without a version, the best matching version is installed.

```bash
# Install the latest version
neuron add example:echo

# Install a specific version
neuron add example:echo@1.0.0

# Install a version that satisfies a constraint
neuron add example:echo@^1.0.0
```

Installed modules are stored immutably under the capability runtime store directory (`~/.neuron/capabilityRuntimes` by default). See [Module Resolution](#module-resolution) and [docs/MODULES.md](../docs/MODULES.md).

### `neuron remove`

Remove an installed external module (capability runtime).

```
Usage:
  neuron remove [name@version] [flags]
```

```bash
neuron remove example:echo@1.0.0
```



### `neuron capability-runtime`

Manage external capability runtime (module) packages installed in the local store.

```
Usage:
  neuron capability-runtime [flags]
  neuron capability-runtime [command]

Available Commands:
  inspect     Inspect an installed capability runtime
  list        List installed capability runtimes
```



#### `neuron capability-runtime list`

List installed capability runtimes.

```
Flags:
  -t, --type string   filter by capability runtime type
```

```bash
# List everything installed
neuron capability-runtime list

# Only show capability runtimes of one type
neuron capability-runtime list --type process
```



#### `neuron capability-runtime inspect`

Inspect an installed capability runtime.

```
Usage:
  neuron capability-runtime inspect [name@version] [flags]
```

```bash
neuron capability-runtime inspect example:echo@1.0.0
```



### `neuron install`

Resolve, verify, and install a provider artifact into the machine-global provider store.

```
Usage:
  neuron install [flags]

Flags:
      --provider string    provider reference: name, name@version, or name@constraint
      --registry strings   registries allowed to satisfy the requirement (default: configured providers.defaultRegistries)
```

`--provider` selects an exact version (`--provider process@1.0.0`) or a constraint (`--provider process@^1.0.0`); without a version, the best available version is chosen. Providers are resolved through the configured registries (`github`, `local`), verified by content digest, and installed immutably into the machine-global provider store (`~/.neuron/providers` by default). The store layout is `<name>/<version>/<digest>/`.

```bash
# Install the latest version
neuron install --provider process

# Install a specific version
neuron install --provider process@1.0.0

# Restrict resolution to one registry
neuron install --provider process@^1.0.0 --registry local
```

```text
installed process@1.0.0 (a1b2c3d4e5f6a7b8, from local)
```

> [!NOTE]
> `neuron install` installs a **provider** — the host that supervises Capability Runtime artifacts — not a capability runtime. Capability runtimes are installed with `neuron add`. Omitting `--provider` fails with a message pointing you at `neuron add`.

### `neuron provider`

Manage first-party provider artifacts for local development.

```
Usage:
  neuron provider [flags]
  neuron provider [command]

Available Commands:
  build       Bootstrap descriptor-only first-party providers into the store
```

Providers obtained from a registry are installed with `neuron install --provider`. There is no `list`, `inspect`, or `remove` subcommand yet.

#### `neuron provider build`

Bootstrap descriptor-only first-party provider artifacts into the store.

```
Usage:
  neuron provider build [flags]

Flags:
  -o, --out string                override the target provider store root (defaults to providers.storeDir)
      --provider-version string   version assigned to the bootstrapped providers (default "0.1.0")
```

`neuron provider build` builds descriptor-only artifacts for the first-party providers `process` and `wasm` and installs them through the real installer. It performs **no** registry resolution and **no** download, and the artifacts carry no executable payload: each is a valid `provider.runtime.json` identity, so project builds and invocations resolve it exactly like a registry-installed provider. The install record marks every artifact with `source: bootstrap`, so a bootstrap install is never mistaken for a registry install.

```bash
# Install the first-party providers into the default provider store
neuron provider build

# Write them under a specific store root
neuron provider build --out /tmp/neuron-providers

# Assign a specific version
neuron provider build --provider-version 0.1.0
```

```text
bootstrap: process@0.1.0 (a1b2c3d4e5f6a7b8)
bootstrap: wasm@0.1.0 (b8a7f6e5d4c3b2a1)
bootstrap: providers available in /home/user/.neuron/providers
```

Rebuilding over an identical artifact reports `already present` instead of failing or duplicating.

### `neuron instance`

Create, list, and manage running assembly instances.

```
Usage:
  neuron instance [flags]
  neuron instance [command]

Available Commands:
  clear       Remove all instances and their executions and events
  list        List instances or executions of a specific instance
  remove      Remove an instance and its executions and events
```

An instance is a living realization of a registered assembly. `neuron run` creates one implicitly.

#### `neuron instance list`

List instances, or executions of a specific instance.

```
Usage:
  neuron instance list [instance-id] [flags]

Flags:
  -a, --all             List all instances including inactive ones
  -s, --status string   Filter instances by a specific status (e.g., running, stopped)
  -t, --target string   List executions of the instance with the given ID
```

```bash
# List all current instances
neuron instance list

# Show everything, including inactive instances
neuron instance list --all

# Filter by status
neuron instance list --status running

# List executions of one instance
neuron instance list --target <instance-id>
```



#### `neuron instance remove`

Remove an instance and its executions and events.

```
Usage:
  neuron instance remove [instance-id|assembly-key] [flags]

Aliases:
  remove, rm
```

```bash
neuron instance remove <instance-id>
neuron instance rm <instance-id>
```



#### `neuron instance clear`

Remove **all** instances and their executions and events.

```
Usage:
  neuron instance clear [flags]
```

```bash
neuron instance clear
```



### `neuron daemon`

Manage the local N.O.R.E. daemon.

```
Usage:
  neuron daemon [flags]
  neuron daemon [command]

Available Commands:
  stop        Stop the background N.O.R.E. daemon
```

The daemon is normally started automatically on first use and keeps running in the background until you stop it. `neuron daemon stop` shuts it down gracefully; it is not tied to any single CLI process.

```bash
# Stop the background daemon
neuron daemon stop
```



### `neuron completion`

Generate the autocompletion script for your shell (bash, zsh, fish, or powershell).

```bash
# Example: install bash completion
source <(neuron completion bash)
```



## Configuration Reference

The effective configuration is assembled from five layers, later layers overriding earlier ones:

1. Built-in defaults (compiled in).
2. The machine-level global configuration — `~/.config/neuron/config.yaml` (runtime defaults for this machine; during the transition this is also the only file that may carry `daemon.*`).
3. The project's `neuron.config.json` / `neuron.config.yaml` / `neuron.config.yml` (or the file passed with `--config`).
4. Environment variables (`NEURON_*`).
5. Command-line flags.

Project configuration is strict: unknown keys are rejected with an error, so a typo fails loudly instead of being silently ignored.

**The scaffolded** `neuron.config.json` **produced by** `neuron init` (default TypeScript authoring):

```json
{
  "name": "my-assembly",
  "version": "1.0.0",
  "lang": "typescript",
  "assemblies": [
    { "name": "my-assembly", "entry": "assembly.ts", "default": true }
  ],
  "runtime": {
    "host": "localhost",
    "port": "auto",
    "execution": {
      "mode": "wait",
      "timeout": "30m"
    }
  },
  "capabilityRuntimes": {
    "localRoots": ["./neuron/capabilityRuntimes"]
  },
  "inspector": {
    "enabled": true,
    "address": "127.0.0.1:7433"
  }
}
```



### Key configuration values


| Key                         | Default               | Meaning                                                                                                    |
| --------------------------- | --------------------- | ---------------------------------------------------------------------------------------------------------- |
| `name`                      | —                     | Assembly identity for this project; required for build, and must match the authored Assembly metadata        |
| `version`                   | —                     | Assembly semantic version; required for build, participates in Assembly identity and build hashing           |
| `lang`                      | `typescript`          | Project authoring language (`yaml`, `yml`, `typescript`, `ts`)                                             |
| `assemblies`                | —                     | Ordered list of Assembly declarations (`name`, `entry`, optional `default: true`). A project may define several; the one marked default (or the sole declaration) is what `neuron run` uses with no argument |
| `assemblies[].name`         | —                     | Assembly name; must be unique within the project                                                           |
| `assemblies[].entry`        | `index.ts` / `assembly.yaml` | Project-relative path to the Assembly definition (defaults: `index.ts` for TypeScript, `assembly.yaml` for YAML) |
| `assemblies[].default`      | `false`               | Marks the Assembly `neuron run` uses when no name is given; at most one may be `true`                       |
| `entry`                     | —                     | Legacy single-Assembly form, migrated to one `assemblies` declaration named after the project; rejected alongside `assemblies` |
| `variables`                 | —                     | Free-form variables carried into the compiled assembly manifest (e.g. environment-specific settings)        |
| `runtime.host`              | `localhost`           | Interface the project runtime binds to (loopback-first by default)                                         |
| `runtime.port`              | `auto`                | Project runtime port: `"auto"` (OS-assigned) or an explicit integer port                                   |
| `runtime.execution.mode`    | `wait`                | Execution mode (`wait` for a blocking result, `detach` for asynchronous)                                   |
| `runtime.execution.timeout` | `30m`                 | Execution timeout                                                                                          |
| `runtime.workers.min`       | `1`                   | Minimum capability runtime workers                                                                          |
| `runtime.workers.max`       | `8`                   | Maximum capability runtime workers                                                                          |
| `runtime.detachedDrainTimeout` | `30s`              | How long detached capability work may keep running after shutdown begins                                    |
| `capabilityRuntimes.localRoots` | `./neuron/capabilityRuntimes` | Project-scoped directories treated as implicit `local` registries for resolution                      |
| `capabilityRuntimes.registries` | none              | Registries used to resolve external modules; with no block, only built-in capability runtimes are available |
| `capabilityRuntimes.defaultRegistries` | `["local"]` | Registries a requirement falls back to when it declares none of its own                                |
| `providers.registries`      | none                  | Registries used to resolve provider artifacts; with no block, only the machine-global provider store and configured `local` roots are available |
| `providers.localRoots`      | none                  | Machine-global directories served by the `local` provider registry (no implicit project root)          |
| `providers.defaultRegistries` | `["local"]`         | Registries a provider requirement falls back to when it declares none of its own                        |
| `inspector.enabled`         | `true`                | Enable the runtime inspector                                                                               |
| `inspector.address`         | `127.0.0.1:7433`      | Inspector address                                                                                          |
| `dev.maxWorkers`            | `1`                   | How many capability runtime builds may run in parallel during `neuron build`                                |

> [!NOTE]
> Project configuration (`neuron.config.*`) rejects engine plumbing outright: `daemon.*` (daemon endpoint, socket, PID file, N.O.R.E. binary path), `storage.provider` / `storage.directory`, `capabilityRuntimes.storeDir`, and `providers.storeDir` are managed by Neuron, and unknown keys fail validation with an error. Address the engine endpoint with `--remote` / `--nore-path` (or `NEURON_*` variables) instead; `daemon.*` remains settable only in the machine-level global `~/.config/neuron/config.yaml` during the transition. The daemon data directory is controlled with `NEURON_DATA_DIR`.




### Environment variables


| Variable          | Meaning                                       |
| ----------------- | --------------------------------------------- |
| `NEURON_SOCKET`         | Override the daemon Unix socket path          |
| `NEURON_DATA_DIR`       | Override the daemon persistent data directory |
| `NEURON_API_TOKEN`      | API token used to authenticate requests      |
| `NEURON_API_TOKEN_FILE` | Override the token file path                 |


Environment variables can also be used for any configuration value with the `NEURON_` prefix pattern (for example `NEURON_LOG_LEVEL`), and command-line flags always take precedence.

### Project layout

`neuron init <project>` scaffolds a **TypeScript** project by default: `neuron.config.json` (`name`, `version`, `lang: typescript`, `entry: assembly.ts`, `runtime.host` / `runtime.port`), `package.json`, `tsconfig.json`, `assembly.ts`, and `neuron/capabilityRuntimes/`. `neuron init <project> --lang yaml` scaffolds the canonical YAML layout instead (as shipped in `examples/ecommerce_order`):

```text
<project>
├── neuron.config.json   project configuration (name, version, lang, entry, runtime)
└── assembly.yaml        assembly definition (the entry file)
```

The assembly file lists its capabilities either by `entry:` reference or inline, and bindings are declared **inline** in the assembly file (mappings and validations) — there is no separate `bindings/` directory.

`neuron build` resolves this layout, produces the canonical manifest into `.neuron/manifest.json`, compiles it, and registers the result. The TypeScript authoring surface produces the same canonical manifest from SDK definitions.

## Module Resolution

When an assembly references an external module (for example `example:echo@1.0.0`), the CLI resolves it as follows:

```mermaid
flowchart LR
    A[Requirement<br/>logical name + version constraint] --> B[Resolution<br/>registries queried · semver match]
    B --> C[Verification<br/>canonical archive + digest]
    C --> D[Installation<br/>immutable capability runtime store]
    D --> E[Freezing<br/>exact versions pinned in the assembly]
```



1. **Requirement** — the assembly declares the module by logical name (`owner:path`) and optionally a version constraint.
2. **Resolution** — the configured registries are queried for available versions; the best matching version is chosen using semantic versioning (an empty constraint selects the latest version).
3. **Verification** — the selected package archive is verified (canonical archive `<name>-<version>-capability-runtime.neuron.tar.gz`, cryptographic digest).
4. **Installation** — the artifact is installed immutably into the capability runtime store.
5. **Freezing** — the exact resolved versions are frozen into the registered assembly, so the runtime can launch instances without resolving anything itself.

Built-in modules shipped inside N.O.R.E. are skipped during resolution; they run in-process. Everything else travels the resolution + installation + freezing path.

See [docs/MODULES.md](../docs/MODULES.md) for the complete module model and how to author a module.

## Building the CLI

From the repository root (a Go workspace):

```bash
go build -o neuron ./application/cmd/neuron
./neuron version
```

The release pipeline passes the release version through `-ldflags` so `neuron version` reports the exact release; see [scripts/release.sh](../scripts/release.sh).

## Related documentation


|                         |                                                                                             |
| ----------------------- | ------------------------------------------------------------------------------------------- |
| **Getting started**     | Run your first assembly — [docs/GETTING_STARTED.md](../docs/GETTING_STARTED.md)             |
| **Installation**        | Official release and from-source installs — [docs/INSTALLATION.md](../docs/INSTALLATION.md) |
| **Modules & capability runtimes** | The unified module model — [docs/MODULES.md](../docs/MODULES.md)                    |
| **Architecture**        | Canonical pipeline, boundaries, and the runtime engine in depth — [docs/ARCHITECTURE.md](../docs/ARCHITECTURE.md) |




## License

This application is part of Neuron, which is released under the MIT License. See [LICENSE](../LICENSE).
