# Neuron CLI

The `neuron` command-line interface is your gateway to the Neuron workflow
engine. From a single terminal you can scaffold a project, register its system
with the N.O.R.E. runtime, run executions, keep an eye on instances, manage
executor packages, and control the local daemon.

## Usage

```
neuron [command] [flags]
```

The CLI exposes a small set of focused commands, each with its own help. Run
`neuron --help` for the full command list, or `neuron <command> --help` for
details on a specific command.

## Typical flow

The shortest path from an empty directory to a running execution looks like
this:

```sh
# Scaffold a workspace and generate a neuron.yaml config
neuron init my-project

# Build the project and register its system with N.O.R.E.
cd my-project
neuron register

# Run the registered system and watch live events stream by
neuron run
```



## Global flags

These flags are available on every command.


| Flag                  | Description                                                            |
| --------------------- | ---------------------------------------------------------------------- |
| `--config <file>`     | Path to the project config file (default: `./neuron.yaml`)             |
| `--log-level <level>` | System logging level (default: `info`)                                 |
| `-v, --verbose`       | Enable verbose output, including N.O.R.E. daemon logs                  |
| `--remote <endpoint>` | Target a remote N.O.R.E. endpoint, e.g. `https://api.nore.example.com` |
| `--nore-path <path>`  | Path to the `nore` daemon binary                                       |
| `-h, --help`          | Show help for the command                                              |




## Commands



### `init`

Scaffold a new Neuron workspace. `init` creates the target directory the
current directory if none is given and writes a starter `neuron.yaml` file.

```
neuron init [Target]
```


| Argument | Description                      |
| -------- | -------------------------------- |
| `Target` | Optional directory to initialize |


```sh
# Initialize the current directory
neuron init

# Initialize a new project folder
neuron init my-project
```



### `register`

Build the current project and register its system with N.O.R.E. in one step.
Registration stores the system durably, so it is ready to be run on demand;
instances are created lazily on first execution.

On success, `register` writes the registration key to `.neuron/register.json`,
which `neuron run` reads to know what to execute.

```
neuron register [flags]
```


| Flag                | Description                                                      |
| ------------------- | ---------------------------------------------------------------- |
| `-l, --lang <lang>` | Project authoring language: `yaml`, `yml`, `typescript`, or `ts` |
| `-r, --root <dir>`  | Project root directory (default: current directory)              |
| `--force`           | Clear the registered system (and its instances) before registering |


```sh
# Register the project in the current directory (language from config)
neuron register

# Register with the language specified explicitly
neuron register -l typescript

# Register a project rooted elsewhere
neuron register -r ../shared-system

# Replace an existing registration (name:version) and its instances
neuron register --force
```

Use `--remote` to register against a remote N.O.R.E. endpoint and `--config` to
point at a specific config file.

### `run`

Run a registered system. The N.O.R.E. runtime does the heavy lifting; `run`
simply drops the system into execution.

`run` only executes it does not build or register anything. The project must
be registered first with `neuron register`; running without a prior
registration stops with a message pointing you to `neuron register`.

By default, `run` streams live execution events as they happen. Live events
arrive over the WebSocket endpoint, with a Server-Sent Events fallback when
WebSocket is unavailable. Pass `--detach` to skip the stream and print the
execution handles immediately.

```
neuron run [flags]
```


| Flag             | Description                                                    |
| ---------------- | -------------------------------------------------------------- |
| `-v, --verbose`  | Verbose output, including event payloads in the stream         |
| `--input <json>` | JSON input payload for the execution, e.g. `'{"key":"value"}'` |
| `--detach`       | Print execution handles immediately, without streaming events  |


```sh
# Run and watch events stream by
neuron run

# Run with an input payload
neuron run --input '{"prompt":"hello"}'

# Kick off an execution and get the handles
neuron run --detach
```



### `instance`

Manage N.O.R.E. system instances.

```
neuron instance [command]
```

Running `neuron instance` without a subcommand is not useful on its own; use
the `list` subcommand below.

#### `instance list`

List instances, or list the executions of a specific instance.

With no argument, this lists the currently running instances. Provide an
instance target either as a positional argument or via `--target` to list that
instance's executions instead. A target is an instance ID (`inst_...`), a key
in colon form (`name:version`), or a key with `@` and optional version
(`name@version`); IDs and keys resolving to the same instance are
interchangeable.

```
neuron instance list [instance-target] [flags]
```

```sh
# List running instances
neuron instance list

# List all instances, including inactive ones
neuron instance list --all

# List instances filtered by status
neuron instance list --status stopped

# List executions of a specific instance (positional, by ID or key)
neuron instance list <inst_...>
neuron instance list order-processing:1.0.0
neuron instance list order-processing@1.0.0

# List executions of a specific instance (flag)
neuron instance list --target <instance-id>
neuron instance list -t <instance-id>
```


| Flag                   | Description                                            |
| ---------------------- | ------------------------------------------------------ |
| `-a, --all`            | List all instances, including inactive ones            |
| `-s, --status <state>` | Filter instances by status (e.g. `running`, `stopped`) |
| `-t, --target <id>`    | List executions of the instance with the given ID      |


> `--all`, `--status`, and `--target` are mutually exclusive — only one may be
> used at a time. Passing an instance ID both as a positional argument and via
> `--target` is an error.



#### `instance remove`

Stop and permanently remove an instance, its executions, and its recorded
events. The instance's system remains registered and can create a fresh
instance on its next execution.

```
neuron instance remove <instance-target>
```

```sh
neuron instance remove inst_abc123
neuron instance remove order-processing:1.0.0
neuron instance remove order-processing@1.0.0
```



#### `instance clear`

Stop and remove every instance managed by N.O.R.E. (metadata, executions, and
events). Registered systems are kept.

```
neuron instance clear
```

```sh
neuron instance clear
```



### `daemon`

Manage the local N.O.R.E. daemon.

```
neuron daemon [command]
```

Running `neuron daemon` without a subcommand prints the command help.

#### `daemon stop`

Stop the background N.O.R.E. daemon.

```
neuron daemon stop
```



### `executor`

Inspect and list executor packages in the local store. Installing and removing
executors are top-level commands (`neuron add` and `neuron remove`).

Executors are referenced by logical name, optionally versioned with `@` for
example `github:read` or `github:read@^1.0.0`. Without a version constraint,
the best matching version is selected automatically. Installed executors live
under `~/.neuron/executors`.

```
neuron executor [command]
```



### `add`

Resolve and install an executor package into the local store.

```
neuron add [name@version]
```

```sh
# Install the best matching version
neuron add github:read

# Install a specific or constrained version
neuron add github:read@1.2.0
neuron add github:read@^1.2.0
```



#### `executor list`

List the executors installed in the local store.

```
neuron executor list [flags]
```


| Flag                | Description             |
| ------------------- | ----------------------- |
| `-t, --type <name>` | Filter by executor type |


```sh
# List everything installed
neuron executor list

# List executors of a particular type
neuron executor list --type github:read
```



#### `executor inspect`

Show the details of an installed executor version, registry, digest,
runtime, and the services and capabilities it exposes.

```
neuron executor inspect [name@version]
```

```sh
# Inspect the newest installed version
neuron executor inspect github:read

# Inspect a specific version
neuron executor inspect github:read@1.2.0
```



#### `executor remove`

Remove an installed executor from the local store. A concrete version is
required.

```
neuron remove [name@version]
```

```sh
neuron remove github:read@1.2.0
```



### `completion`

Generate an autocompletion script for your shell, so `neuron` commands and
flags autocomplete as you type.

```
neuron completion [bash|zsh|fish|powershell]
```

```sh
# Example: enable completion in zsh
neuron completion zsh > ~/.zsh/_neuron
```



### `execution`

Listing execution instances is on the roadmap. The `execution` command is
reserved for this and will be fully functional in an upcoming release.
```