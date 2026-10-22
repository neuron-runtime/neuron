# Migrating a Neuron project

This guide describes the changes a project needs to move from the pre-Phase-16
layout to the current one. Most projects only need to run `neuron build` and
`neuron run` again; the sections below name the cases that require an edit.

## Project configuration

Project configuration lives in `neuron.config.json` or `neuron.config.yaml`.
Configuration is strict: unknown keys fail the build, so a removed key is
reported instead of being ignored.

### `entry` to `assemblies`

The singular `entry` is the legacy single-Assembly form. It is still accepted
and is migrated automatically:

- A project with `entry` and no `assemblies` becomes one Assembly named after
  the project (`name`), marked as the default. Its registered identity is
  therefore unchanged.
- A project with neither becomes one Assembly named after the project whose
  entry is the language default.
- A project with `assemblies` uses the list verbatim, in declaration order.
- Declaring both `entry` and `assemblies` is rejected: migrate the entry into an
  `assemblies` declaration or remove it.

A single declared Assembly is the default even without `default: true`. When
several Assemblies are declared, mark exactly one with `default: true`; with
none marked there is no default, and `neuron run` without an Assembly name fails
with the list of built Assemblies and asks for an explicit name. Only one
Assembly may be marked default.

```json
{
  "name": "order-platform",
  "version": "0.1.0",
  "lang": "typescript",
  "assemblies": [
    { "name": "orders", "entry": "src/assemblies/orders.ts", "default": true },
    { "name": "fraud", "entry": "src/assemblies/fraud.ts" }
  ]
}
```

`neuron build` builds every declared Assembly. `neuron build --assembly <name>`
rebuilds one; the other Assembly entries are carried forward from the previous
build record.

### Configuration schema changes

- `version` is the Assembly's semantic version. The retired integer schema
  version (`version: 1`) is rejected; write a version string such as
  `"0.1.0"`.
- `daemon.*` keys are engine configuration and are rejected from project files.
  Set the project runtime endpoint with `runtime.host` and `runtime.port`, or
  override the port with `neuron --port <n>` / `neuron run --port <n>`.
- Internal storage keys (including `capabilityRuntimes.storeDir`) and
  `providers.storeDir` are rejected from project configuration; use the
  documented environment overrides where a non-default location is required.
- The old configuration file names `neuron.yaml` and `neuron.yml` are no longer
  read. Rename the file to `neuron.config.yaml` (or `neuron.config.json`).
- The global configuration file may not declare Assembly identity or entries
  (`name`, `version`, `entry`, `assemblies`); those belong to the project file.

## TypeScript SDK

### `Assembly({ name, version, description })` to `Assembly()`

`Assembly()` takes no arguments. Project configuration is authoritative for the
Assembly name and version, so pass them in `neuron.config.*` and remove the
object argument:

```ts
// before
export default Assembly({ name: "hello", version: "1.0.0" }).run(/* ... */);

// after
export default Assembly().run(/* ... */);
```

An authored Assembly name or version that conflicts with the project
configuration is rejected rather than silently overridden.

### `Capability({ name, version, description })` to `Capability(name)`

A capability's public identity is its name. Version and registry belong to the
Capability Runtime the capability executes through, not to the capability
itself:

```ts
// before
Capability({ name: "greet", version: "1.0.0" }).paramsSchema(/* ... */);

// after
Capability("greet")
  .runtime({ name: "neuron:core:set", version: "latest", registry: "local" })
  .paramsSchema(/* ... */);
```

Capabilities without `.runtime(...)` default to the in-process
`neuron:core:set` runtime.

### Removed capability metadata fields

Generated canonical manifests no longer carry per-capability `version` or
`description` fields. They were not part of the runtime identity: resolution
and installation deduplicate on the Capability Runtime identity
(`name`/`version`/`registry`). Rebuild the manifest; any remaining hand-written
metadata field that is no longer in the canonical model is ignored by the loader
or rejected by strict validation, so remove it.

### Assemblies without params or results

Assembly `params` and `result` are optional and are omitted from the canonical
manifest when the author declares none; the runtime then exposes no public
params or result fields. Add `paramsSchema(...)` / `resultSchema(...)` plus
`.result({...})` where callers need a contract. A result field must use a
structured reference (`capability.result.x`, `assembly.params.x`, or a literal);
arbitrary expression strings are not evaluated for result fields.

## Expressions and structured references

The canonical manifest stores references structurally:

```json
{ "kind": "capabilityResult", "capability": "greet", "path": ["greeting"] }
```

YAML project bindings may continue to use the documented expression form
(`source.result.greeting`, `execution.params.order.items`); the loader parses
those strings into the same structured representation. Nothing needs to change
in existing YAML assemblies. Code that emits the canonical manifest directly
must emit structured `source` objects instead of expression strings.
`validations` remain CEL expressions and are unchanged.

## Build and registration records

Builds are immutable. Each build writes
`.neuron/builds/<fingerprint>/build.json` and atomically points
`.neuron/current.json` at it. Older build directories are never overwritten.

- A pre-multi-Assembly build record (a single `key` plus
  `capabilityRuntimes`) is migrated on read into one Assembly entry named after
  the recorded Assembly id, so existing projects keep working.
- A record already written in the current shape is used as-is.
- The Assembly `description` field was removed and Assembly params/results were
  added to the hash input. A manifest built before that change hashes
  differently; rebuild to produce and register the current artifact.
- Registration is keyed by `(assemblyID, version)`. Registering the same version
  replaces its previous artifact and prunes the old content hash. A build that
  pins an older hash must be rebuilt, otherwise `neuron run` cannot resolve it.
- Multiple immutable builds of one project can coexist; the current pointer
  selects the active one.

No build-record edit is required by hand. Run `neuron build` after upgrading.

## Capability Runtime installation

Capability Runtime artifacts are installed by content digest under
`~/.neuron/capabilityRuntimes/<type-path>/<version>/<digest>/`.

- `RootDir` in an install record is local metadata, not identity. Frozen
  records carry identity and digest, never a host path, and the installed
  directory is derived from the configured store root plus that identity.
  Moving a store does not change a frozen build; reinstalling into a fresh store
  recreates the layout.
- An installed identity is immutable. Reinstalling identical content is a no-op;
  different content under the same identity is rejected. Install a new version
  instead.
- A local package has no digest until it is staged. Builds of local capability
  runtimes now stage first and compare the staged digest, so re-registering an
  unchanged local runtime no longer reports a false conflict.

## Providers

A provider hosts a Capability Runtime; it is not itself a Capability Runtime
and the two artifacts are never interchangeable.

- Capability Runtime manifests keep declaring `runtime.type: process` or
  `runtime.type: wasm`. Existing manifests and examples need no edit: the
  runtime kind selects a provider from the common provider registry instead of a
  special backend path.
- Providers are installed in a machine-global store
  (`~/.neuron/providers/<name>/<version>/<digest>/`). Install one with
  `neuron install --provider <name>@<version>`.
- For local development, `neuron provider build` installs descriptor-only
  first-party `process` and `wasm` providers through the same installer into the
  same store. They carry `source: bootstrap` provenance; the resolver and the
  build-time freezer read them from the store exactly like a registry install.
- `neuron build` freezes the installed provider identity into the build record.
  Freezing never downloads or installs. If no installed provider hosts a
  runtime kind the build fails and names both `neuron provider build` and
  `neuron install --provider`.
- Provider failures at execution are reported as structured provider errors:
  a kind with no registered provider, an unsupported runtime kind, protocol,
  platform, or an incompatible provider contract version.
- The frozen provider identity is descriptive at execution time: N.O.R.E. hosts
  the artifact through its built-in provider for the declared runtime kind.
  Changing the provider store after a build does not change how that build
  executes; rebuild to refresh the frozen identity.

## Deprecated surfaces

These paths remain temporarily and will be removed after the replacement is
covered by end-to-end tests:

- The `neuron register` command is a hidden alias of `neuron build`. Use
  `neuron build`.
- The `neuron instance list/remove/clear` group is an advanced inspection
  surface. Normal authoring and execution use `neuron build`, bare `neuron`, and
  `neuron run`.
- The capability-level legacy `execution` block is deprecated in favour of
  `capability runtime: runtimeConfig:`. `concurrency` and `continueOnFail` have
  no equivalent and are rejected rather than ignored.
- The legacy bare core runtime name (for example `set`) is still accepted and
  canonicalizes to `neuron:core:set`.
- Process Capability Runtimes that declare no protocol, or the legacy
  `neuron/capability-runtime-v1-json` protocol, keep working. New runtimes
  should prefer `neuron/capability-runtime-v1` (gRPC over a Unix socket).
- Pre-multi-Assembly build records and legacy flat store layouts are migrated on
  read. Removal condition: once the 0.1.x migration window closes and no such
  records remain in supported installations.
