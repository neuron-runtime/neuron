import type {
  BindingManifest,
  AssemblyManifest,
  AssemblyResultFieldManifest,
  ValueRefManifest,
  PortManifest,
} from "./manifest.js";
import { createExecutionContext, toValueRef, type Expressionify } from "./expression.js";
import {
  Composition,
  CompositionNode,
  CapabilityDefinition,
  type CapabilityComposition,
  type ParamValue,
} from "./capability.js";
import { schemaToManifest, type InferSchema, type SchemaObject } from "./schema.js";

// A value that produces one or more flat capability invocations. Edges between
// them are derived at compilation time from the data each node references;
// composition carries no ordering of its own.
export type Runnable<TParams extends object = object> =
  | Composition<TParams>
  | CompositionNode<object, TParams>
  | CapabilityDefinition<object, TParams>
  | CapabilityComposition
  | Runnable<TParams>[];

type RequiredResultKeys<T extends object> = {
  [K in keyof T]-?: undefined extends T[K] ? never : K;
}[keyof T];

type OptionalResultKeys<T extends object> = Exclude<keyof T, RequiredResultKeys<T>>;

/**
 * Result mappings follow the declared result object: required fields must be
 * mapped, optional fields may be omitted, and every value must match its field.
 */
export type AssemblyResultBindings<T extends object> =
  keyof T extends never ? never : {
    [K in RequiredResultKeys<T>]: ParamValue<T[K]>;
  } & {
    [K in OptionalResultKeys<T>]?: ParamValue<Exclude<T[K], undefined>>;
  };

type MissingResultSchema = {
  readonly __resultSchemaRequired:
    "Call .resultSchema<ResultType>() or .resultSchema({...}) before .result({...}).";
};

type ResultArgument<T extends object> =
  [T] extends [never] ? MissingResultSchema : AssemblyResultBindings<T>;

export class AssemblyDefinition<TParams extends object = object, TResult extends object = never> {
  private _nodes: CapabilityComposition[] = [];
  private _paramPorts: PortManifest[] = [];
  private _resultPorts: PortManifest[] = [];
  private _resultFields: AssemblyResultFieldManifest[] = [];
  private _resultSchemaMode: "none" | "typed" | "runtime" = "none";

  paramsSchema<T extends object>(): AssemblyDefinition<T, TResult>;
  paramsSchema<S extends SchemaObject>(schema: S): AssemblyDefinition<InferSchema<S>, TResult>;
  paramsSchema(schema?: SchemaObject): AssemblyDefinition<any, TResult> {
    const next = new AssemblyDefinition<any, TResult>();
    next._nodes = [...this._nodes];
    if (schema) {
      next._paramPorts = schemaToManifest(schema).ports;
    } else {
      next._paramPorts = [...this._paramPorts];
    }
    next._resultPorts = [...this._resultPorts];
    next._resultFields = [...this._resultFields];
    next._resultSchemaMode = this._resultSchemaMode;
    return next;
  }

  /**
   * Declares the Assembly's public result schema. The type-only form carries
   * no runtime rules and is for authoring ergonomics; the schema form records
   * the typed result ports that every `.result({...})` field must be declared
   * against.
   */
  resultSchema<T extends object>(): AssemblyDefinition<TParams, T>;
  resultSchema<S extends SchemaObject>(schema: S): AssemblyDefinition<TParams, InferSchema<S>>;
  resultSchema(schema?: SchemaObject): AssemblyDefinition<TParams, any> {
    if (this._resultSchemaMode !== "none") {
      throw new Error(
        "Assembly result schema is already declared. Declare it once, then provide its mapping with .result({...})."
      );
    }

    const next = new AssemblyDefinition<TParams, any>();
    next._nodes = [...this._nodes];
    next._paramPorts = [...this._paramPorts];
    next._resultPorts = schema ? schemaToManifest(schema).ports : [];
    next._resultFields = [...this._resultFields];
    next._resultSchemaMode = schema ? "runtime" : "typed";
    return next;
  }

  /**
   * Declares the Assembly's public result contract: one structured source per
   * declared field. Field order is the author's contract and is preserved
   * verbatim. Each value is the same structured reference surface as
   * `.withParams()`: a capability `.result`/`.params` field, an assembly
   * `.params` field, or a literal.
   */
  result(fields: ResultArgument<TResult>): this {
    if (this._resultSchemaMode === "none") {
      throw new Error(
        "Cannot declare Assembly result mappings before a result schema. " +
        "Call .resultSchema<ResultType>() or .resultSchema({...}) first."
      );
    }
    if (this._resultFields.length > 0) {
      throw new Error(
        "Assembly result mappings are already defined. Combine all output fields into one .result({...}) call."
      );
    }
    if (fields === null || typeof fields !== "object" || Array.isArray(fields)) {
      throw new Error("Assembly .result(...) expects an object mapping result field names to references or literal values.");
    }

    const entries = Object.entries(fields as Record<string, unknown>);
    if (entries.length === 0) {
      throw new Error("Assembly .result(...) must map at least one result field.");
    }

    if (this._resultSchemaMode === "runtime") {
      const schemaNames = new Set(this._resultPorts.map((port) => port.name));
      const suppliedNames = new Set(entries.map(([name]) => name));
      for (const [name] of entries) {
        if (!schemaNames.has(name)) {
          const declared = this._resultPorts.map((port) => port.name);
          throw new Error(
            `Assembly result field "${name}" is not declared by .resultSchema(...). ` +
            `Declared fields: ${declared.length ? declared.join(", ") : "(none)"}.`
          );
        }
      }
      for (const port of this._resultPorts) {
        if (port.required && !suppliedNames.has(port.name)) {
          throw new Error(
            `Assembly result schema marks "${port.name}" as required, but .result({...}) does not map it. ` +
            `Add "${port.name}" to the result mapping or make the schema field optional.`
          );
        }
      }
    } else {
      // A type-only schema has no runtime reflection. Its field names come
      // from this mapping and use the any type because TypeScript types are erased.
      this._resultPorts = entries.map(([name]) => ({
        name,
        type: "any",
        required: true,
      }));
    }

    for (const [name, value] of entries) {
      if (value === undefined) {
        throw new Error(
          `Assembly result field "${name}" is undefined. Provide a capability reference or a literal value.`
        );
      }
      let source: ValueRefManifest;
      try {
        source = toResultSource(name, value);
      } catch (error) {
        const detail = error instanceof Error ? error.message : String(error);
        throw new Error(`Assembly result field "${name}" could not be mapped: ${detail}`);
      }
      this._resultFields.push({ name, source });
    }
    return this;
  }

  get params(): Expressionify<TParams> {
    return createExecutionContext<TParams>().params;
  }

  withParams(define: (data: Expressionify<TParams>) => Runnable): this {
    return this.run(define(this.params));
  }

  run(node: Runnable): this {
    this._nodes.push(...toNodeList(node));
    return this;
  }

  toManifest(): AssemblyManifest {
    if (this._nodes.length === 0) {
      throw new Error(
        "Assembly has no capability definitions. Add at least one capability with .run(...) or .withParams(...)."
      );
    }
    if (this._resultSchemaMode !== "none" && this._resultFields.length === 0) {
      throw new Error(
        "Assembly result schema was declared but no result mapping was provided. Add .result({...}) after .resultSchema(...)."
      );
    }

    const capabilityDefs = new Map<string, CapabilityDefinition<object, object>>();
    collectCapabilityDefs(this._nodes, capabilityDefs);

    collectMissingRefs(this._nodes, capabilityDefs, this._resultFields);

    const capabilities: AssemblyManifest["capabilities"] = [];
    for (const [ref, def] of capabilityDefs) {
      capabilities.push(def.toManifest());
    }

    const bindings = collectBindings(this._nodes, capabilityDefs);

    const manifest: AssemblyManifest = {
      apiVersion: "neuron/v1",
      kind: "Assembly",
      capabilities,
      bindings,
    };

    if (this._paramPorts.length > 0) {
      manifest.params = this._paramPorts.map((p) => ({ ...p }));
    }

    if (this._resultFields.length > 0) {
      this.validateResultFields(capabilityDefs);
      manifest.result = {
        schema: this._resultPorts.map((p) => ({ ...p })),
        fields: this._resultFields.map((f) => ({
          name: f.name,
          source: cloneSource(f.source),
        })),
      };
    }

    return manifest;
  }

  /**
   * Validates every declared result mapping against the result schema and the
   * referenced capability ports, mirroring the canonical compiler's checks:
   * the field name must be declared by the schema, a capability-scoped source
   * must name an existing capability with a matching declared port, and the
   * source must otherwise be structurally valid. Field names are the public
   * contract and are matched exactly.
   */
  private validateResultFields(capabilityDefs: Map<string, CapabilityDefinition<object, object>>): void {
    const schemaNames = new Set(this._resultPorts.map((p) => p.name));
    for (const field of this._resultFields) {
      const prefix = `assembly result field "${field.name}"`;
      if (!schemaNames.has(field.name)) {
        throw new Error(`${prefix}: the assembly result schema declares no field named "${field.name}"`);
      }
      const source = field.source;
      if (source.kind === "capabilityResult" || source.kind === "capabilityParams") {
        const capability = source.capability;
        if (!capability) {
          throw new Error(`${prefix}: ${source.kind} reference has no capability identity`);
        }
        const def = capabilityDefs.get(capability);
        if (!def) {
          throw new Error(`${prefix}: references unknown capability "${capability}"`);
        }
        if (!source.path || source.path.length === 0) {
          throw new Error(
            `${prefix}: ${source.kind} reference to capability "${capability}" has an empty path; it must name one declared port`
          );
        }
        const ports = source.kind === "capabilityResult" ? def.toManifest().results : def.toManifest().params;
        const root = camelToSnake(source.path[0]!);
        // An empty port list means the capability uses a type-only/untyped
        // contract (for example a built-in whose result shape is owned by N.O.R.E.).
        // There is no runtime port metadata to validate against; TypeScript's
        // declared result type remains the authoring-time check.
        if (ports.length > 0 && !ports.some((p) => camelToSnake(p.name) === root)) {
          throw new Error(
            `${prefix}: capability "${capability}" declares no ${
              source.kind === "capabilityResult" ? "result" : "param"
            } port matching "${source.path[0]}" (snake_case: "${root}")`
          );
        }
      } else if (source.kind !== "assemblyParams" && source.kind !== "literal") {
        throw new Error(`${prefix}: source kind "${source.kind}" is not supported for assembly result fields`);
      }
    }
  }
}

// ---------------------------------------------------------------------------
// Factory
// ---------------------------------------------------------------------------

export function Assembly(): AssemblyDefinition {
  return new AssemblyDefinition();
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
  capabilityDefs: Map<string, CapabilityDefinition<object, object>>,
  resultFields: AssemblyResultFieldManifest[] = []
): void {
  const referenced = new Set<string>();
  for (const node of nodes) {
    for (const source of node.sources) {
      if (source) referenced.add(source);
    }
    if (node.anchor) referenced.add(node.anchor);
    referenced.add(node.capabilityRef);
  }
  for (const field of resultFields) {
    if (field.source.capability) referenced.add(field.source.capability);
  }
  for (const ref of referenced) {
    if (!capabilityDefs.has(ref)) {
      throw new Error(
        `Capability "${ref}" is referenced in the assembly definition but not defined. ` +
        `Ensure the capability was created with Capability("${ref}") and used via .withParams(), .connect(), .bind(), or .result().`
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
// more than one source is execution fan-in: each source emits one binding
    // with its own mappings. The target implicitly becomes an "all-required join":
    // all sources must provide their data before the target can execute. An
    // explicit join declaration is only added when the implicit form is ambiguous
    // (e.g. when some sources are optional or when a join capability is needed).
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

    for (const from of sourceRefs) {
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
  }

  return bindings;
}

function bindingMappings(
  fromRef: string,
  target: CapabilityTarget,
  capabilities: Map<string, CapabilityDefinition<object, object>>
): BindingManifest["mappings"] {
  const explicit = Object.entries(target.bindings).map(([name, source]) => {
    const mapped = qualifySource(source, fromRef, name, target.capabilityRef);
    // For capabilityResult/capabilityParams, only include mappings that reference
    // this binding's from capability. This enables fan-in: each source emits a
    // binding with only its own mappings. Other kinds (e.g. assemblyParams) are
    // always included.
    const isCapRef = mapped.kind === "capabilityResult" || mapped.kind === "capabilityParams";
    if (isCapRef && mapped.capability !== fromRef) return null;
    return { target: name, source: mapped };
  }).filter((m): m is NonNullable<typeof m> => m !== null);
  if (explicit.length > 0) return explicit;

  const from = capabilities.get(fromRef)?.toManifest();
  const to = capabilities.get(target.capabilityRef)?.toManifest();
  if (!from || !to) return [];

  const results = new Map(from.results.map((port) => [port.name, port]));
  return to.params.flatMap((param) => {
    const result = results.get(param.name);
    if (!result || !compatiblePortTypes(result.type, param.type)) return [];
    return [
      {
        target: param.name,
        source: { kind: "capabilityResult", capability: fromRef, path: [param.name] },
      },
    ];
  });
}

/**
 * qualifySource gives a capability-scoped reference its source capability
 * identity. References that originate from a named capability (`.result`,
 * `.params`) already carry it; references from a generic source context do not,
 * and are resolved against the binding's `from` capability — exactly how the
 * canonical compiler qualifies `source.*` references.
 */
function qualifySource(
  source: ValueRefManifest,
  fromRef: string,
  target: string,
  toRef: string
): ValueRefManifest {
  if (source.kind !== "capabilityResult" && source.kind !== "capabilityParams") return source;
  if (source.capability) return source;
  if (!fromRef) {
    throw new Error(
      `Capability "${toRef}" maps target "${target}" from "source.*" but no source capability is known for the binding`
    );
  }
  return { ...source, capability: fromRef };
}

function compatiblePortTypes(from: string, to: string): boolean {
  return from === to || from === "any" || to === "any";
}

interface CapabilityTarget {
  capabilityRef: string;
  bindings: Record<string, ValueRefManifest>;
  incomingConditions: Array<{ expression: string; message?: string }>;
}

/**
 * toResultSource converts a declared result-field value into its structured
 * source. A capability-scoped reference must be written as a named
 * capability `.result`/`.params` reference — unlike bindings there is no
 * source capability to qualify an unqualified `source.*` reference against.
 */
function toResultSource(name: string, value: unknown): ValueRefManifest {
  const source = toValueRef(value);
  if (
    (source.kind === "capabilityResult" || source.kind === "capabilityParams") &&
    !source.capability
  ) {
    throw new Error(
      `assembly result field "${name}" references source.* but no capability is known; ` +
      `reference a capability's .result or .params directly`
    );
  }
  return cloneSource(source);
}

function cloneSource(source: ValueRefManifest): ValueRefManifest {
  return {
    ...source,
    path: source.path ? [...source.path] : undefined,
  };
}

/**
 * camelToSnake ports the canonical snake_case convention (shared/types/core
 * `CamelToSnake`) so the SDK validates result-field sources against the exact
 * matching the canonical compiler applies: port names may be authored in any
 * casing and both sides resolve on the snake_cased form.
 */
function camelToSnake(value: string): string {
  const runes = Array.from(value);
  const out: string[] = [];
  for (let i = 0; i < runes.length; i++) {
    const r = runes[i]!;
    if (r === "_" || isDigit(r)) {
      out.push(r);
      continue;
    }
    if (isUpper(r)) {
      if (i > 0) {
        const prev = runes[i - 1]!;
        const boundary = isLowerOrDigit(prev) ||
          (isUpper(prev) && i + 1 < runes.length && isLower(runes[i + 1]!));
        if (boundary) out.push("_");
      }
      out.push(r.toLowerCase());
      continue;
    }
    out.push(r);
  }
  return out.join("");
}

function isLower(r: string): boolean {
  return r !== r.toUpperCase() && r === r.toLowerCase();
}

function isUpper(r: string): boolean {
  return r === r.toUpperCase() && r !== r.toLowerCase();
}

function isDigit(r: string): boolean {
  return r >= "0" && r <= "9";
}

function isLowerOrDigit(r: string): boolean {
  return isLower(r) || isDigit(r);
}