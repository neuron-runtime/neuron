# Neuron Command Line Reference

The `neuron` CLI is the single user-facing interface to Neuron. It lets you author projects, build and register systems, manage external modules, create instances, execute systems, and stream execution events — while it manages the N.O.R.E. runtime engine (the daemon) for you in the background.

Everything below is generated from the actual command definitions in this module (`application/cmd/neuron` and `application/internal/cli`), so it stays accurate as long as it is kept in sync with the code.

**You never interact with the N.O.R.E. daemon directly.** The CLI starts it, checks its health, talks to it over a local Unix socket, and stops it. From the user's perspective there is a single product: `neuron`.

---



## Table of Contents

- [Install & Verify](#install--verify)
- [How the CLI Talks to N.O.R.E.](#how-the-cli-talks-to-nore)
- [Global Flags](#global-flags)
- [Command Reference](#command-reference)
  - `[neuron version](#neuron-version)`
  - `[neuron init](#neuron-init)`
  - `[neuron register](#neuron-register)`
  - `[neuron run](#neuron-run)`
  - `[neuron add](#neuron-add)`
  - `[neuron remove](#neuron-remove)`
  - `[neuron executor](#neuron-executor)`
  - `[neuron instance](#neuron-instance)`
  - `[neuron daemon](#neuron-daemon)`
  - `[neuron completion](#neuron-completion)`
- [Configuration Reference](#configuration-reference)
- [Module Resolution](#module-resolution)

---



## Install & Verify

Neuron is distributed as a single archive containing the `neuron` CLI and the N.O.R.E. runtime. You only place `neuron` on your `PATH`.

```bash
neuron version
```

See [docs/INSTALLATION.md](../docs/INSTALLATION.md) for the full installation guide.

---



## How the CLI Talks to N.O.R.E.

The CLI communicates with the N.O.R.E. daemon over a local Unix domain socket. When you run a command that needs the runtime, the CLI automatically:

1. Checks whether the daemon is already healthy.
2. If not, starts it from the bundled `nore` binary with the effective configuration (socket path, data directory, worker count).
3. Talks to it over the socket.
4. Stops it again on `neuron daemon stop` or when the process exits.

The socket defaults to `~/.neuron/nore.sock` and can be overridden with the `NEURON_SOCKET` environment variable or the `daemon.socket` configuration value. A remote daemon can be used instead with the `--remote` flag.

Regular requests are JSON over HTTP. Live execution events are streamed over the WebSocket endpoint (`/v1/ws`), with a Server-Sent Events fallback for transports without WebSocket support.

---



## Global Flags

The following flags are available on every `neuron` command.


| Flag                 | Description                                                      |
| -------------------- | ---------------------------------------------------------------- |
| `--config string`    | Project config file (defaults to `./neuron.yaml`)                |
| `--log-level string` | Logging level (default `"info"`)                                 |
| `-v, --verbose`      | Enable verbose output (shows N.O.R.E. daemon logs)               |
| `--remote string`    | Remote N.O.R.E. endpoint (e.g., `https://api.nore.example.com`)  |
| `--nore-path string` | Path to the `nore` daemon binary                                 |
| `--force`            | Force replacement (clear existing state before the command acts) |
| `-h, --help`         | Help for the command                                             |
| `--version`          | Print the CLI version and exit                                   |


---



## Command Reference



### `neuron version`

Print the Neuron CLI version.

```bash
neuron version
neuron --version
```

Both print the same version. The version is injected at build time from the release tag, so a development build reports `dev`.

### `neuron init`

Initialize a new Neuron workspace.

```
Usage:
  neuron init [Target] [flags]
```

`Target` is the directory to create (relative to the current directory). `neuron init` creates the directory and writes a starter `neuron.yaml` into it.

```bash
# Create ./my-system with a starter neuron.yaml
neuron init my-system
cd my-system
```

The scaffolded config selects the YAML authoring language. Add `systems/`, `services/`, and `connectors/` directories and start defining your system — see [docs/GETTING_STARTED.md](../docs/GETTING_STARTED.md).

### `neuron register`

Build and register the current project with N.O.R.E.

```
Usage:
  neuron register [flags]

Flags:
  -l, --lang string   project authoring language (yaml, yml, typescript, ts)
  -r, --root string   project root (defaults to the current directory)
```

`neuron register` runs the full authoring pipeline in one step:

1. **Build** — the project (YAML or TypeScript) is resolved into the canonical `.neuron/manifest.json`.
2. **Compile** — the canonical manifest is compiled into a runtime system representation.
3. **Resolve** — external module requirements declared by services are resolved through the configured registries, installed into the local store, and the exact versions are frozen into the registration payload. Built-in modules are skipped — they run in-process inside N.O.R.E.
4. **Register** — the compiled system and its frozen module set are sent to N.O.R.E., which persists it and returns a system key.

The CLI stores the registration key locally and prints it on success:

```text
order-processing@1.0.0#a1b2c3d4:development
```

```bash
# Register the project in the current directory (language auto-detected)
neuron register

# Force a language and a different project root
neuron register --lang typescript --root ./pipeline

# Replace any previously registered version of this system
neuron register --force
```

After registration, `neuron run` executes the registered system.

### `neuron run`

Run a Neuron System.

```
Usage:
  neuron run [flags]

Flags:
      --detach         Return execution handles immediately without streaming live events
      --input string   JSON input payload for execution (e.g., '{"key":"value"}')
  -v, --verbose        Enable verbose output to display event payloads
```

`neuron run` loads the registration key stored by `neuron register`, asks N.O.R.E. to create an instance and execute the system, and streams live execution events back to the terminal over the WebSocket endpoint, falling back to Server-Sent Events when WebSocket is unavailable.

```bash
# Run the registered system with no input, streaming events
neuron run

# Provide input
neuron run --input '{"order": {"id": "ord_123", "total": 4250, "currency": "USD"}}'

# Show event payloads
neuron run -v

# Start execution and return immediately with execution/instance handles
neuron run --detach
```

In the default (streaming) mode, the command returns when the execution reaches a terminal state (`execution.completed`, `execution.failed`, or `execution.cancelled`). In `--detach` mode it prints the execution ID, instance ID, and status, and returns immediately, so you can follow the run with `neuron instance list`.

### `neuron add`

Resolve and install an external module (executor) into the local store.

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

Installed modules are stored immutably under the executor store directory (`~/.neuron/executors` by default). See [Module Resolution](#module-resolution) and [docs/MODULES.md](../docs/MODULES.md).

### `neuron remove`

Remove an installed external module (executor).

```
Usage:
  neuron remove [name@version] [flags]
```

```bash
neuron remove example:echo@1.0.0
```



### `neuron executor`

Manage external executor (module) packages installed in the local store.

```
Usage:
  neuron executor [flags]
  neuron executor [command]

Available Commands:
  inspect     Inspect an installed executor
  list        List installed executors
```



#### `neuron executor list`

List installed executors.

```
Flags:
  -t, --type string   filter by executor type
```

```bash
# List everything installed
neuron executor list

# Only show executors of one type
neuron executor list --type process
```



#### `neuron executor inspect`

Inspect an installed executor.

```
Usage:
  neuron executor inspect [name@version] [flags]
```

```bash
neuron executor inspect example:echo@1.0.0
```



### `neuron instance`

Create, list, and manage running system instances.

```
Usage:
  neuron instance [flags]
  neuron instance [command]

Available Commands:
  clear       Remove all instances and their executions and events
  list        List instances or executions of a specific instance
  remove      Remove an instance and its executions and events
```

An instance is a living realization of a registered system. `neuron run` creates one implicitly.

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
  neuron instance remove [instance-id|system-key] [flags]

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

The daemon is normally started and stopped automatically. `neuron daemon stop` is useful when you want to release the daemon before the CLI exits.

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

---



## Configuration Reference

The CLI resolves configuration from several layers, later layers overriding earlier ones:

1. Built-in defaults (compiled in).
2. The project's `neuron.yaml` (or the file passed with `--config`).
3. Environment variables.
4. Command-line flags.

The scaffolded `neuron.yaml` produced by `neuron init` looks like:

```yaml
apiVersion: neuron/v1
kind: Project

metadata:
  name: my-system
  version: 0.1.0
  description: A Neuron system

lang: yaml

systems:
  entry: ./systems/my-system/system.yaml

runtime:
  execution:
    mode: wait
    timeout: 30m
  workers:
    min: 1
    max: 8

storage:
  provider: local
  directory: ./.neuron/data

executors:
  registries:
    - name: github
      url: https://api.github.com
    - name: local
      url: local://

inspector:
  enabled: true
  address: 127.0.0.1:7433
```



### Key configuration values


| Key                         | Default               | Meaning                                                                  |
| --------------------------- | --------------------- | ------------------------------------------------------------------------ |
| `lang`                      | auto-detected         | Project authoring language (`yaml`, `yml`, `typescript`, `ts`); detected from the project files when unset |
| `systems.entry`             | —                     | Path to the project's entry system definition                            |
| `runtime.execution.mode`    | `wait`                | Execution mode (`wait` for a blocking result, `detach` for asynchronous) |
| `runtime.execution.timeout` | `30m`                 | Execution timeout                                                        |
| `runtime.workers.min`       | `1`                   | Minimum executor workers                                                 |
| `runtime.workers.max`       | `8`                   | Maximum executor workers                                                 |
| `storage.provider`          | `local`               | Storage provider                                                         |
| `storage.directory`         | `~/.neuron/nore`      | Persistent data directory                                                |
| `daemon.socket`             | `~/.neuron/nore.sock` | Local Unix socket for the daemon                                         |
| `daemon.pidFile`            | platform default      | Where the daemon records its process ID                                  |
| `daemon.norePath`           | (bundled)             | Path to the `nore` daemon binary                                         |
| `executors.storeDir`        | `~/.neuron/executors` | Where resolved modules are installed                                     |
| `executors.registries`      | `github`, `local`     | Registries used to resolve modules                                       |
| `inspector.enabled`         | `true`                | Enable the runtime inspector                                             |
| `inspector.address`         | `127.0.0.1:7433`      | Inspector address                                                        |




### Environment variables


| Variable          | Meaning                                       |
| ----------------- | --------------------------------------------- |
| `NEURON_SOCKET`   | Override the daemon Unix socket path          |
| `NEURON_DATA_DIR` | Override the daemon persistent data directory |


Environment variables can also be used for any configuration value with the `NEURON_` prefix pattern (for example `NEURON_LOG_LEVEL`), and command-line flags always take precedence.

### Project layout

A Neuron project (authoring surface: YAML) is conventionally laid out as:

```text
<project>
├── neuron.yaml            project configuration
├── systems/               system definitions
│   └── <system>/system.yaml
├── services/              service (module) definitions
└── connectors/            connector definitions
```

`neuron register` resolves this layout, builds the canonical manifest into `.neuron/manifest.json`, compiles it, and registers the result. The TypeScript authoring surface produces the same canonical manifest from SDK definitions.

---



## Module Resolution

When a system references an external module (for example `example:echo@1.0.0`), the CLI resolves it as follows:

1. **Requirement** — the system declares the module by logical name (`owner:path`) and optionally a version constraint.
2. **Resolution** — the configured registries are queried for available versions; the best matching version is chosen using semantic versioning (an empty constraint selects the latest version).
3. **Verification** — the selected package archive is verified (canonical archive `<name>-<version>-executor.neuron.tar.gz`, cryptographic digest).
4. **Installation** — the artifact is installed immutably into the executor store.
5. **Freezing** — the exact resolved versions are frozen into the registered system, so the runtime can launch instances without resolving anything itself.

Built-in modules shipped inside N.O.R.E. are skipped during resolution; they run in-process. Everything else travels the resolution + installation + freezing path.

See [docs/MODULES.md](../docs/MODULES.md) for the complete module model and how to author a module.

---



## Building the CLI

From the repository root (a Go workspace):

```bash
go build -o neuron ./application/cmd/neuron
./neuron version
```

The release pipeline passes the release version through `-ldflags` so `neuron version` reports the exact release; see [scripts/release.sh](../scripts/release.sh).

---



## License

This application is part of Neuron, which is released under the MIT License. See [LICENSE](../LICENSE).