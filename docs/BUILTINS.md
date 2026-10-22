# Built-in Capability Runtimes

Neuron ships four built-in capability runtimes. They run **in-process** inside the N.O.R.E. runtime engine and require no resolution, installation, or external dependency. Referencing one in a Capability is a plain module reference — the runtime dispatches it directly to the in-process implementation.

| Built-in | Purpose |
| --- | --- |
| `neuron:core:set` | Materializes a capability's value set: the capability's configuration with its params overlaid |
| `neuron:core:http` | Performs an HTTP request with SSRF protection, host allowlist, and optional retry policy |
| `neuron:core:command` | Runs a local program with working-directory confinement, environment allowlist, and process-group termination |
| `neuron:core:filesystem` | Reads files and lists directories, confined to the project root and any extra roots the project declares |

The `neuron builtins` command reports the contract of every built-in, or the contract of one built-in when named:

```bash
neuron builtins            # every built-in
neuron builtins http       # one built-in
neuron builtins http -v    # with the full descriptions
```

---

## `neuron:core:set`

**Summary:** Materializes a capability's value set.

**Description:**
`set` combines the capability's own configuration with the params bound to the invocation and returns the combination, with params overriding configuration. It is the default runtime for a capability that declares none, which makes it the way an assembly supplies a value set to a downstream capability without writing a capability runtime.

**Parameters:**

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| `format` | string | no | `json` | How the values of the set are represented: `json` or `text`. |

**Results:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `value` | object | yes | The merged value set: the capability's configuration with the params overlaid on it. The result of the capability carries the names of the merged set. |

**Constraints:**

- Reads only the capability's own input; it reaches nothing outside the runtime.
- The format is an instruction to the built-in rather than a value of the set, so it is not returned as one.

---

## `neuron:core:http`

**Summary:** Makes an HTTP request.

**Description:**
`http` performs a single HTTP request and returns the status, the response headers, and the body. It is the general-purpose integration boundary: an author who needs to talk to an HTTP API should not have to write a capability runtime for it.

**Parameters:**

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| `url` | string | yes | — | The absolute URL to request. |
| `method` | string | no | `GET` | The HTTP method. |
| `headers` | object of strings | no | — | Request headers. |
| `query` | object of strings | no | — | Query string parameters, appended to the URL. |
| `body` | any | no | — | The request body, encoded as JSON. |
| `contentType` | string | no | `application/json` | The request content type. Only used when a body is present. |
| `auth` | object | no | — | Credentials for the request: `{type: "bearer" \| "basic" \| "apiKey", ...}`. |
| `timeout` | duration string | no | — | How long the request may take, such as `30s`. |
| `format` | string | no | `json` | How the response body is represented: `json` or `text`. |
| `followRedirects` | boolean | no | `false` | Whether redirects are followed. Each hop is checked against the address policy. |
| `retry` | object | no | — | Retry behavior: `{attempts: n, backoff: "200ms", methods: [...]}`. Off by default. |

**Results:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `status_code` | number | yes | The HTTP response status code. |
| `headers` | object of strings | yes | The response headers. |
| `body` | any or string | yes | The response body: parsed JSON in the json format, a plain string in the text format. |
| `content_type` | string | yes | The response content type. |
| `duration_ms` | number | yes | How long the request took, in milliseconds. |

**Constraints:**

- Refuses loopback, private, link-local, and cloud-metadata addresses, so a request cannot be aimed at the host or an internal service.
- Does not follow redirects unless asked to; when it does, every hop is checked against the same address policy.
- Does not retry unless asked to, and never retries a method that was not opted in.
- Bounds the request body and the response body.

---

## `neuron:core:command`

**Summary:** Runs a local program.

**Description:**
`command` executes a local program with structured arguments and returns its exit code, standard output, and standard error. It is the boundary between an assembly and the programs already installed on the machine.

**Parameters:**

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| `command` | string | yes | — | The program to run. A single executable, never a shell command line. |
| `args` | array of strings | no | — | The program's arguments. |
| `env` | object of strings | no | — | Environment variables added to the program's environment. |
| `cwd` | string | no | — | The working directory, resolved against the allowed root. |
| `stdin` | string | no | — | Text written to the program's standard input. |
| `timeout` | duration string | no | — | How long the program may run, such as `30s`. |
| `format` | string | no | `text` | How standard output is represented: `json` or `text`. |
| `failOnNonZeroExit` | boolean | no | `true` | Whether a non-zero exit fails the capability. Set it to false to inspect the result instead. |
| `maxOutputBytes` | number | no | — | The most output to retain per stream. The runtime's ceiling applies when this is absent. |

**Results:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `exit_code` | number | yes | The program's exit code. |
| `stdout` | any or string | yes | The program's standard output: parsed JSON in the json format, a plain string in the text format. |
| `stderr` | string | yes | The program's standard error. |
| `success` | boolean | yes | Whether the program exited with code zero. |
| `timed_out` | boolean | yes | Whether the program was terminated by its deadline. |
| `duration_ms` | number | yes | How long the program ran, in milliseconds. |
| `stdout_truncated` | boolean | no | Present when the standard output was shortened to the output limit. |
| `stderr_truncated` | boolean | no | Present when the standard error was shortened to the output limit. |

**Constraints:**

- Never runs a shell: the program and its arguments are passed to the operating system separately, so an argument can never become a command.
- Runs only the programs the policy permits, when the policy names any.
- Runs the program with an allowlisted environment, never the runtime's own environment.
- Confines the working directory to an allowed root.
- Bounds the retained output and reports truncation rather than hiding it.
- Terminates the program's whole process group on cancellation or deadline, so no child survives the capability.

---

## `neuron:core:filesystem`

**Summary:** Reads files and directories.

**Description:**
`filesystem` reads a file, lists a directory, and reports what a path is. It is the read-only view of the machine the assembly runs on: the files the assembly was written to work with, and nothing else. Writing, deleting, and moving files are not part of it.

**Parameters:**

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| `operation` | string | yes | — | What to do with the path: `read`, `list`, or `stat`. |
| `path` | string | yes | — | The path to read, list, or describe. A relative path is resolved against the project root. |
| `recursive` | boolean | no | `false` | Whether a listing descends into subdirectories. |
| `format` | string | no | `json` | How the content of a read file is represented: `json` or `text`. |

**Results:**

| Name | Type | Required | Description |
|------|------|----------|-------------|
| `path` | string | yes | The absolute path the operation applied to. |
| `content` | any or string | no | The content of the file, for the read operation: parsed JSON in the json format, a plain string in the text format. |
| `size` | number | no | The size of the file in bytes, for the read and stat operations. |
| `entries` | array of objects | no | The entries of the directory, for the list operation. Each entry has `name`, `path`, `type` (`file`, `directory`, `link`, `other`), and `size`. |
| `exists` | boolean | no | Whether the path exists, for the stat operation. |
| `type` | string | no | What the path is: `file`, `directory`, `link`, or `other`, for the stat operation. |
| `entries_truncated` | boolean | no | Present when the listing was shortened to the entry limit. |

**Constraints:**

- Refuses a path that resolves outside the permitted roots, and a symbolic link cannot be used to leave them.
- Reads only: no operation writes, deletes, or moves anything.
- Bounds the content a single read returns and the entries a single listing returns.

---

## Using built-ins in TypeScript

```ts
import { set, http, command, filesystem } from "@neuronruntime/sdk/core";

const fetchUser = http("users.get");
const listFiles = filesystem("files.list");
const runTool = command("tools.build");
const values = set("config.values");
```

Each export is a factory that takes the capability's name and returns a definition with the built-in's contract already applied, so the params and results are typed.

---

## Removed built-ins

| Built-in | Removed because | Replacement |
| --- | --- | --- |
| `neuron:core:ai` | Mock/placeholder; not a real capability | — |
| `neuron:core:log` | Logging is an observability concern, not a capability | — |
| `neuron:core:delay` | Waiting is a sequencing concern, not a capability | Explicit dependency between the capabilities that must run in order |

A reference to a removed built-in is reported as an actionable error:

```
capability runtime "neuron:core:delay" does not exist: it was removed because it only waited, which is a sequencing concern rather than a capability; use an explicit dependency between the capabilities that must run in order instead. available built-in capability runtimes are: neuron:core:command, neuron:core:filesystem, neuron:core:http, neuron:core:set
```