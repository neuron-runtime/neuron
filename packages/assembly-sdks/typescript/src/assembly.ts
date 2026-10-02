import type {
  BindingManifest,
  AssemblyManifest,
} from "./manifest.js";
import { createExecutionContext, type Expressionify } from "./expression.js";
import {
  Composition,
  CompositionNode,
  CapabilityDefinition,
  type CapabilityComposition,
} from "./capability.js";
import { schemaToManifest, type InferSchema, type SchemaObject } from "./schema.js";

// A value that produces one or more flat capability invocations. Edges between
// them are derived at compilation time from the data each node references;
// composition carries no ordering of its own.
export type Runnable<TInput extends object = object> =
  | Composition<TInput>
  | CompositionNode<object, TInput>
  | CapabilityDefinition<object, TInput>
  | CapabilityComposition
  | Runnable<TInput>[];

export class AssemblyDefinition<TInput extends object = object> {
  private _nodes: CapabilityComposition[] = [];
  private _inputPorts: ReturnType<typeof schemaToManifest>["ports"] = [];

  constructor(
    readonly name: string,
    readonly version?: string,
    readonly description?: string
  ) {}

  paramsSchema<T extends object>(): AssemblyDefinition<T>;
  paramsSchema<S extends SchemaObject>(schema: S): AssemblyDefinition<InferSchema<S>>;
  paramsSchema(schema?: SchemaObject): AssemblyDefinition<object> {
    const next = new AssemblyDefinition<object>(this.name, this.version, this.description);
    next._nodes = [...this._nodes];
    if (schema) {
      next._inputPorts = schemaToManifest(schema).ports;
    } else {
      next._inputPorts = [...this._inputPorts];
    }
    return next;
  }

  get params(): Expressionify<TInput> {
    return createExecutionContext<TInput>().params;
  }

  withParams(define: (data: Expressionify<TInput>) => Runnable): this {
    return this.run(define(this.params));
  }

  run(node: Runnable): this {
    this._nodes.push(...toNodeList(node));
    return this;
  }

  toManifest(): AssemblyManifest {
    if (!this.version) {
      throw new Error(`Assembly "${this.name}" is missing a version`);
    }
    if (this._nodes.length === 0) {
      throw new Error(`Assembly "${this.name}" has no definition`);
    }

    const capabilityDefs = new Map<string, CapabilityDefinition<object, object>>();
    collectCapabilityDefs(this._nodes, capabilityDefs);

    collectMissingRefs(this._nodes, capabilityDefs);

    const capabilities: AssemblyManifest["capabilities"] = [];
    for (const [ref, def] of capabilityDefs) {
      capabilities.push(def.toManifest());
    }

    const bindings = collectBindings(this._nodes, capabilityDefs);

    return {
      apiVersion: "neuron/v1",
      kind: "Assembly",
      metadata: {
        name: this.name,
        version: this.version,
        description: this.description,
      },
      capabilities,
      bindings,
    };
  }
}

// ---------------------------------------------------------------------------
// Factory
// ---------------------------------------------------------------------------

export function Assembly(config: { name: string; version?: string; description?: string }): AssemblyDefinition {
  return new AssemblyDefinition(config.name, config.version, config.description);
}

// ---------------------------------------------------------------------------
// Input normalization
// ---------------------------------------------------------------------------

function toNodeList(value: Runnable): CapabilityComposition[] {
  if (Array.isArray(value)) {
    const out: CapabilityComposition[] = [];
    for (const item of value) {
      out.push(...toNodeList(item));
    }
    return out;
  }
  if (value instanceof Composition) {
    return value._nodes.map(normalizeNode);
  }
  if (value instanceof CompositionNode) {
    return [normalizeNode(value._composition)];
  }
  if (value instanceof CapabilityDefinition) {
    return [normalizeNode({
      kind: "capability",
      capabilityRef: value.ref,
      capabilityDef: value as unknown as CapabilityDefinition<object, object>,
      bindings: {},
      incomingConditions: [],
      sources: [],
    })];
  }
  if (isCapabilityComposition(value)) {
    return [normalizeNode(value)];
  }
  throw new Error("Invalid node provided to Assembly.run(...)");
}

function normalizeNode(node: CapabilityComposition): CapabilityComposition {
  return {
    kind: "capability",
    capabilityRef: node.capabilityRef,
    capabilityDef: node.capabilityDef,
    bindings: node.bindings ?? {},
    incomingConditions: node.incomingConditions ?? [],
    sources: node.sources ?? [],
    anchor: node.anchor,
  };
}

function isCapabilityComposition(value: unknown): value is CapabilityComposition {
  return Boolean(
    value && typeof value === "object" &&
    "kind" in value && (value as { kind?: unknown }).kind === "capability" &&
    "capabilityRef" in value
  );
}

// ---------------------------------------------------------------------------
// Flat traversal — collect CapabilityDefinition instances
// ---------------------------------------------------------------------------

function collectCapabilityDefs(
  nodes: CapabilityComposition[],
  out: Map<string, CapabilityDefinition<object, object>>
): void {
  for (const node of nodes) {
    if (node.capabilityDef && !out.has(node.capabilityRef)) {
      out.set(node.capabilityRef, node.capabilityDef);
    }
  }
}

function collectMissingRefs(
  nodes: CapabilityComposition[],
  capabilityDefs: Map<string, CapabilityDefinition<object, object>>
): void {
  const referenced = new Set<string>();
  for (const node of nodes) {
    for (const source of node.sources) {
      if (source) referenced.add(source);
    }
    if (node.anchor) referenced.add(node.anchor);
    referenced.add(node.capabilityRef);
  }
  for (const ref of referenced) {
    if (!capabilityDefs.has(ref)) {
      throw new Error(
        `Capability "${ref}" is referenced in the assembly definition but not defined. ` +
        `Ensure the capability was created with Capability({ name: "${ref}", ... }) and used via .withParams(), .connect(), or .bind().`
      );
    }
  }
}

// ---------------------------------------------------------------------------
// Binding generation from data references
//
// Each node's incoming edge is derived from its `sources`: the distinct
// capability refs its bindings and conditions reference. A single source
// produces one incoming binding; zero sources fall back to the positional
// anchor from `.bind()` when present (otherwise the node is an entry point and
// runs from the assembly's execution params, in parallel with other entries);
// more than one source is execution fan-in and is rejected — it requires an
// aggregation capability.
// ---------------------------------------------------------------------------

function collectBindings(
  nodes: CapabilityComposition[],
  capabilities: Map<string, CapabilityDefinition<object, object>>
): BindingManifest[] {
  const bindings: BindingManifest[] = [];
  const seen = new Set<string>();

  for (const node of nodes) {
    const uniqueSources = Array.from(new Set(node.sources.filter(Boolean)));
    const sourceRefs = uniqueSources.length > 0 ? uniqueSources : node.anchor ? [node.anchor] : [];
    if (sourceRefs.length === 0) continue;

    if (sourceRefs.length > 1) {
      throw new Error(
        `Capability "${node.capabilityRef}" reads inputs from multiple sources (${sourceRefs.join(", ")}). ` +
        `Execution does not support fan-in; route the inputs through an aggregation capability instead.`
      );
    }

    const from = sourceRefs[0]!;
    const key = `${from}->${node.capabilityRef}`;
    if (seen.has(key)) continue;
    seen.add(key);

    const mappings = bindingMappings(from, node, capabilities);
    const validations = node.incomingConditions.map((c) => ({
      expression: c.expression,
      message: c.message ?? "",
    }));
    bindings.push({ from, to: node.capabilityRef, mappings, validations });
  }

  return bindings;
}

function bindingMappings(
  fromRef: string,
  target: CapabilityTarget,
  capabilities: Map<string, CapabilityDefinition<object, object>>
): BindingManifest["mappings"] {
  const explicit = Object.entries(target.bindings).map(([name, expression]) => ({
    target: name,
    expression,
  }));
  if (explicit.length > 0) return explicit;

  const from = capabilities.get(fromRef)?.toManifest();
  const to = capabilities.get(target.capabilityRef)?.toManifest();
  if (!from || !to) return [];

  const results = new Map(from.results.map((port) => [port.name, port]));
  return to.params.flatMap((param) => {
    const result = results.get(param.name);
    if (!result || !compatiblePortTypes(result.type, param.type)) return [];
    return [{ target: param.name, expression: `source.result.${param.name}` }];
  });
}

function compatiblePortTypes(from: string, to: string): boolean {
  return from === to || from === "any" || to === "any";
}

interface CapabilityTarget {
  capabilityRef: string;
  bindings: Record<string, string>;
  incomingConditions: Array<{ expression: string; message?: string }>;
}