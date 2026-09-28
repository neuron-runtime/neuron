import type { PortManifest, CapabilityManifest } from "./manifest.js";
import {
  createExpressionProxy,
  createSourceContext,
  expressionRef,
  expressionToString,
  type Expression,
  type Expressionify,
  type SourceContext,
} from "./expression.js";
import { schemaToManifest, type FieldRules, type InferSchema, type SchemaObject } from "./schema.js";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface ExecutionConfig {
  mode?: "wait" | "detach";
  timeout?: string;
  retries?: number;
  concurrency?: number;
  continueOnFail?: boolean;
}

export type Condition = {
  when: Expression<boolean>;
  message?: string;
};

export type ParamValue<T> = T | Expression<T> | Expressionify<Exclude<T, undefined>>;

type RequiredKeys<T extends object> = {
  [K in keyof T]-?: undefined extends T[K] ? never : K;
}[keyof T];

type OptionalKeys<T extends object> = Exclude<keyof T, RequiredKeys<T>>;

export type ParamBindings<T extends object> = {
  [K in RequiredKeys<T>]: ParamValue<T[K]>;
} & {
  [K in OptionalKeys<T>]?: ParamValue<Exclude<T[K], undefined>>;
};

export interface Connection<TSource extends object = object, TTarget extends object = object> {
  readonly __kind: "connection";
  readonly __serviceRef?: string;
  readonly __bindings: Array<{ target: string; expression: string }>;
  readonly __conditions: Array<{ expression: string; message: string }>;
  when(condition: Expression<boolean>, message: string): Connection<TSource, TTarget>;
}

export interface CapabilityReference<TInput extends object, TOutput extends object> {
  readonly ref: string;
  readonly result: Expressionify<TOutput>;
  withParams(bindings: ParamBindings<TInput>): CompositionNode<TInput, TOutput>;
  withParams<TSource extends object>(connection: Connection<TSource, TInput>): CompositionNode<TInput, TOutput>;
  runtimeConfig(config: ExecutionConfig): CompositionNode<TInput, TOutput>;
  connect<TSource extends object>(
    define: (source: SourceContext<TSource>) => ParamBindings<TInput>
  ): Connection<TSource, TInput>;
}

// ---------------------------------------------------------------------------
// Internal composition types
// ---------------------------------------------------------------------------

export interface CapabilityComposition {
  kind: "capability";
  capabilityRef: string;
  capabilityDef?: CapabilityDefinition<object, object>;
  bindings: Record<string, string>;
  execution?: ExecutionConfig;
  incomingConditions: Array<{ expression: string; message?: string }>;
  /**
   * Capability refs this node's bindings/conditions reference. Each distinct
   * entry becomes an incoming binding's `from` (the reference supplies the
   * source identity the runtime evaluates `source.result` through). More than
   * one entry is execution fan-in and is rejected.
   */
  sources: string[];
  /**
   * Positional fallback: when a node references no capability directly, the
   * `.bind()` receiver it was chained after provides its source identity so
   * chains written without result references still produce an ordering.
   */
  anchor?: string;
}

// ---------------------------------------------------------------------------
// Capability runtime config
// ---------------------------------------------------------------------------

interface CapabilityRuntimeConfig {
  name: string;
  version: string;
  registry: string;
}

// ---------------------------------------------------------------------------
// Capability state
// ---------------------------------------------------------------------------

type CapabilityState = {
  capabilityRuntime: CapabilityRuntimeConfig;
  version?: string;
  description?: string;
  inputPorts: PortManifest[];
  outputPorts: PortManifest[];
  inputRules: Record<string, FieldRules>;
};

// ---------------------------------------------------------------------------
// CompositionNode — a single capability invocation with bindings
// ---------------------------------------------------------------------------

export class CompositionNode<TInput extends object = object, TOutput extends object = object> {
  readonly __inputMarker?: TInput;
  readonly __outputMarker?: TOutput;
  readonly _composition: CapabilityComposition;

  constructor(capabilityRef: string, compositionOverride?: Partial<CapabilityComposition>) {
    this._composition = {
      kind: "capability",
      capabilityRef,
      capabilityDef: compositionOverride?.capabilityDef,
      bindings: compositionOverride?.bindings ?? {},
      execution: compositionOverride?.execution,
      incomingConditions: compositionOverride?.incomingConditions ?? [],
      sources: compositionOverride?.sources ?? [],
      anchor: compositionOverride?.anchor,
    };
  }

  get capabilityRef(): string {
    return this._composition.capabilityRef;
  }

  get bindings(): Record<string, string> {
    return this._composition.bindings;
  }

  get runtimeConfig(): ExecutionConfig | undefined {
    return this._composition.execution;
  }

  get result(): Expressionify<TOutput> {
    return createExpressionProxy<TOutput>("source.result", this._composition.capabilityRef);
  }

  bind<TNextInput extends object, TNextOutput extends object>(
    target: BindTarget<TOutput, TNextInput, TNextOutput>,
    condition?: Condition
  ): Composition<TNextOutput> {
    const targets = toNodes(target, condition).map((node) =>
      node.sources.length === 0 && !node.anchor
        ? { ...node, anchor: this._composition.capabilityRef }
        : node
    );
    return new Composition<TNextOutput>([this._composition, ...targets]);
  }
}

// ---------------------------------------------------------------------------
// Composition — a flat set of capability invocations
//
// Composition carries no ordering of its own. Bindings are derived from the
// data each node references: nodes referencing the same source run in
// parallel, chains are implied by reference order, and the `.bind()` receiver
// supplies a positional anchor only when a target references no capability
// directly.
// ---------------------------------------------------------------------------

export class Composition<TOutput extends object = object> {
  readonly __outputMarker?: TOutput;
  readonly _nodes: CapabilityComposition[];

  constructor(nodes: CapabilityComposition[]) {
    this._nodes = nodes;
  }

  get nodes(): CapabilityComposition[] {
    return this._nodes;
  }

  get result(): Expressionify<TOutput> {
    const last = this._nodes[this._nodes.length - 1];
    return createExpressionProxy<TOutput>("source.result", last?.capabilityRef);
  }

  bind<TNextInput extends object, TNextOutput extends object>(
    target: BindTarget<TOutput, TNextInput, TNextOutput>,
    condition?: Condition
  ): Composition<TNextOutput> {
    const last = this._nodes[this._nodes.length - 1];
    const anchor = last?.capabilityRef;
    const targets = toNodes(target, condition).map((node) =>
      node.sources.length === 0 && !node.anchor && anchor
        ? { ...node, anchor }
        : node
    );
    return new Composition<TNextOutput>([...this._nodes, ...targets]);
  }
}

// ---------------------------------------------------------------------------
// CapabilityDefinition — immutable capability blueprint
// ---------------------------------------------------------------------------

export class CapabilityDefinition<TInput extends object = object, TOutput extends object = object>
  implements CapabilityReference<TInput, TOutput> {
  readonly ref: string;
  private readonly _state: CapabilityState;
  private readonly _executionConfig?: ExecutionConfig;

  constructor(ref: string, state?: Partial<CapabilityState>, executionConfig?: ExecutionConfig) {
    this.ref = ref;
    this._state = {
      capabilityRuntime: state?.capabilityRuntime ?? { name: "neuron:core:set", version: "latest", registry: "local" },
      version: state?.version,
      description: state?.description,
      inputPorts: state?.inputPorts ?? [],
      outputPorts: state?.outputPorts ?? [],
      inputRules: state?.inputRules ?? {},
    };
    this._executionConfig = executionConfig;
  }

  capabilityRuntime(config: { name: string; version?: string; registry?: string }): this {
    return this.clone({
      capabilityRuntime: {
        name: config.name,
        version: config.version ?? "latest",
        registry: config.registry ?? "local",
      },
    }) as this;
  }

  paramsSchema<T extends object>(): CapabilityDefinition<T, TOutput>;
  paramsSchema<S extends SchemaObject>(schema: S): CapabilityDefinition<InferSchema<S>, TOutput>;
  paramsSchema(schema?: SchemaObject): CapabilityDefinition<object, TOutput> {
    if (!schema) return this.clone();
    const { ports, rules } = schemaToManifest(schema);
    return this.clone({ inputPorts: ports, inputRules: rules });
  }

  resultSchema<T extends object>(): CapabilityDefinition<TInput, T>;
  resultSchema<S extends SchemaObject>(schema: S): CapabilityDefinition<TInput, InferSchema<S>>;
  resultSchema(schema?: SchemaObject): CapabilityDefinition<TInput, object> {
    if (!schema) return this.clone();
    const { ports } = schemaToManifest(schema);
    return this.clone({ outputPorts: ports });
  }

  get result(): Expressionify<TOutput> {
    return createExpressionProxy<TOutput>("source.result", this.ref);
  }

  get params(): Expressionify<TInput> {
    return createExpressionProxy<TInput>("source.params", this.ref);
  }

  withParams(bindings: ParamBindings<TInput>): CompositionNode<TInput, TOutput>;
  withParams<TSource extends object>(connection: Connection<TSource, TInput>): CompositionNode<TInput, TOutput>;
  withParams(bindingsOrConnection: ParamBindings<TInput> | Connection<object, TInput>): CompositionNode<TInput, TOutput> {
    if (isConnection(bindingsOrConnection)) {
      return new CompositionNode<TInput, TOutput>(this.ref, {
        capabilityDef: this as unknown as CapabilityDefinition<object, object>,
        bindings: bindingsFromConnection(bindingsOrConnection),
        incomingConditions: bindingsOrConnection.__conditions.map((c) => ({
          expression: c.expression,
          message: c.message,
        })),
      });
    }
    const { bindings, sources } = bindingsFromObject(bindingsOrConnection);
    return new CompositionNode<TInput, TOutput>(this.ref, {
      capabilityDef: this as unknown as CapabilityDefinition<object, object>,
      bindings,
      sources,
    });
  }

  runtimeConfig(config: ExecutionConfig): CompositionNode<TInput, TOutput> {
    return new CompositionNode<TInput, TOutput>(this.ref, {
      capabilityDef: this as unknown as CapabilityDefinition<object, object>,
      execution: config,
    });
  }

  connect<TSource extends object>(
    define: (source: SourceContext<TSource>) => ParamBindings<TInput>
  ): Connection<TSource, TInput> {
    return makeConnection(this.ref, define(createSourceContext<TSource>()));
  }

  bind<TNextInput extends object, TNextOutput extends object>(
    target: BindTarget<TOutput, TNextInput, TNextOutput>,
    condition?: Condition
  ): Composition<TNextOutput> {
    const sourceComp: CapabilityComposition = {
      kind: "capability",
      capabilityRef: this.ref,
      capabilityDef: this as unknown as CapabilityDefinition<object, object>,
      bindings: {},
      incomingConditions: [],
      sources: [],
    };
    const targets = toNodes(target, condition).map((node) =>
      node.sources.length === 0 && !node.anchor ? { ...node, anchor: this.ref } : node
    );
    return new Composition<TNextOutput>([sourceComp, ...targets]);
  }

  toManifest(): CapabilityManifest {
    return {
      name: this.ref,
      version: this._state.version,
      description: this._state.description,
      capabilityRuntime: {
        name: this._state.capabilityRuntime.name,
        version: this._state.capabilityRuntime.version,
        registry: this._state.capabilityRuntime.registry,
      },
      params: this._state.inputPorts,
      results: this._state.outputPorts,
      execution: this._executionConfig,
    };
  }

  asReference(): CapabilityReference<TInput, TOutput> {
    return this;
  }

  private clone<TNextInput extends object = TInput, TNextOutput extends object = TOutput>(
    patch: Partial<CapabilityState> = {}
  ): CapabilityDefinition<TNextInput, TNextOutput> {
    return new CapabilityDefinition<TNextInput, TNextOutput>(this.ref, {
      ...this._state,
      capabilityRuntime: { ...this._state.capabilityRuntime },
      inputPorts: [...this._state.inputPorts],
      outputPorts: [...this._state.outputPorts],
      inputRules: { ...this._state.inputRules },
      ...patch,
    });
  }
}

// ---------------------------------------------------------------------------
// Factory
// ---------------------------------------------------------------------------

export function Capability<TInput extends object = object, TOutput extends object = object>(
  config: { name: string; version?: string; description?: string }
): CapabilityDefinition<TInput, TOutput> {
  return new CapabilityDefinition<TInput, TOutput>(config.name, {
    version: config.version,
    description: config.description,
    capabilityRuntime: { name: "neuron:core:set", version: "latest", registry: "local" },
  });
}

// ---------------------------------------------------------------------------
// Connection
// ---------------------------------------------------------------------------

export function makeConnection<TSource extends object, TTarget extends object>(
  serviceRef: string | undefined,
  bindings: ParamBindings<TTarget>,
  conditions: Array<{ expression: string; message: string }> = []
): Connection<TSource, TTarget> {
  const entries = Object.entries(bindings).flatMap(([target, value]) =>
    value === undefined ? [] : [{ target, expression: expressionToString(value) }]
  );
  return {
    __kind: "connection",
    __serviceRef: serviceRef,
    __bindings: entries,
    __conditions: conditions,
    when(condition: Expression<boolean>, message: string) {
      return makeConnection<TSource, TTarget>(serviceRef, bindings, [
        ...conditions,
        { expression: String(condition), message },
      ]);
    },
  };
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

type BindTarget<TSource extends object, TInput extends object, TOutput extends object> =
  | CapabilityDefinition<TInput, TOutput>
  | CapabilityReference<TInput, TOutput>
  | CompositionNode<TInput, TOutput>
  | Composition<TOutput>
  | Connection<TSource, TInput>;

function toNodes<TInput extends object, TOutput extends object>(
  value: BindTarget<object, TInput, TOutput>,
  condition?: Condition
): CapabilityComposition[] {
  if (value instanceof CompositionNode) {
    return [applyCondition(value._composition, condition)];
  }
  if (value instanceof Composition) {
    const nodes = value._nodes.map(cloneComposition);
    if (condition) {
      return [applyCondition(nodes[0]!, condition), ...nodes.slice(1)];
    }
    return nodes;
  }
  if (isConnection(value)) {
    const comp: CapabilityComposition = {
      kind: "capability",
      capabilityRef: value.__serviceRef ?? "",
      bindings: bindingsFromConnection(value),
      incomingConditions: value.__conditions.map((c) => ({
        expression: c.expression,
        message: c.message,
      })),
      sources: [],
    };
    return [applyCondition(comp, condition)];
  }
  if (isCapabilityReference(value)) {
    const comp: CapabilityComposition = {
      kind: "capability",
      capabilityRef: value.ref,
      capabilityDef:
        value instanceof CapabilityDefinition ? (value as unknown as CapabilityDefinition<object, object>) : undefined,
      bindings: {},
      incomingConditions: [],
      sources: [],
    };
    return [applyCondition(comp, condition)];
  }
  throw new Error("Invalid node provided to a `.bind()` chain");
}

function applyCondition(comp: CapabilityComposition, condition?: Condition): CapabilityComposition {
  if (!condition) return comp;
  const ref = expressionRef(condition.when);
  return {
    ...comp,
    incomingConditions: [
      ...comp.incomingConditions,
      { expression: String(condition.when), message: condition.message },
    ],
    sources: ref && !comp.sources.includes(ref) ? [...comp.sources, ref] : comp.sources,
  };
}

function bindingsFromObject<T extends object>(bindings: ParamBindings<T>): {
  bindings: Record<string, string>;
  sources: string[];
} {
  const map: Record<string, string> = {};
  const sources = new Set<string>();
  for (const [name, value] of Object.entries(bindings)) {
    if (value === undefined) continue;
    const ref = expressionRef(value);
    if (ref) sources.add(ref);
    map[name] = expressionToString(value);
  }
  return { bindings: map, sources: [...sources] };
}

function bindingsFromConnection(connection: Connection<object, object>): Record<string, string> {
  return Object.fromEntries(connection.__bindings.map((b) => [b.target, b.expression]));
}

function cloneComposition(node: CapabilityComposition): CapabilityComposition {
  return {
    kind: "capability",
    capabilityRef: node.capabilityRef,
    capabilityDef: node.capabilityDef,
    bindings: { ...node.bindings },
    execution: node.execution,
    incomingConditions: node.incomingConditions.map((c) => ({ ...c })),
    sources: [...node.sources],
    anchor: node.anchor,
  };
}

function isConnection(value: unknown): value is Connection<object, object> {
  return Boolean(value && typeof value === "object" && "__kind" in value && value.__kind === "connection");
}

function isCapabilityReference(value: unknown): value is CapabilityReference<object, object> {
  return Boolean(value && typeof value === "object" && "ref" in value && "result" in value);
}