# Examples

These examples show how Neuron assemblies and capability runtimes are defined and run.

## Authoring is language-neutral

An assembly can be authored in any programming language that produces the **Neuron Assembly manifest** — the canonical representation that every authoring surface converges on. Neuron ships two first-class authoring surfaces today:

- **TypeScript** using the [Assembly SDK](../packages/assembly-sdks/typescript/README.md).
- **YAML**, a zero-tooling surface.

The two examples below express the same composition in each surface. Adding another language is a matter of emitting the canonical manifest; the compiler and runtime do not change.

## Examples

- `hello` — the minimal TypeScript assembly. One capability, no bindings. Use this to verify an installation end to end.
- `ecommerce_order_ts` — an order-processing assembly authored in TypeScript.
- `ecommerce_order` — the same order-processing assembly authored in YAML.
- `capability-runtimes` — a reference capability runtime implemented in Go, together with a local registry catalog.

The order examples are intentionally more involved than a starting point needs to be; they exist to exercise bindings, conditions, and assembly result mappings across a realistic multi-step composition.

## Running an example

From an example directory:

```bash
neuron build              # compile the manifest and freeze dependencies
neuron                    # start the project runtime (foreground)
neuron run --params '{}'  # run the assembly in another terminal
```

See [Getting started](../docs/GETTING_STARTED.md) for a full walkthrough.
