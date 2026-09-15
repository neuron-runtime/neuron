import type {
  ConnectorManifest,
  SystemManifest,
} from "./manifest.js";
import { createExecutionContext, type Expressionify } from "./expression.js";
import type {
  Composition,
  SequenceComposition,
  ParallelComposition,
  ServiceComposition,
} from "./service.js";
import type { ServiceDefinition } from "./service.js";
import { schemaToManifest, type InferSchema, type SchemaObject } from "./schema.js";

type Runnable = Composition | { node(): Composition } | { _composition: Composition };

export class SystemDefinition<TInput extends object = object> {
  private _root?: Composition;
  private _inputPorts: ReturnType<typeof schemaToManifest>["ports"] = [];

  constructor(
    readonly name: string,
    readonly version?: string,
    readonly description?: string
  ) {}

  inputSchema<T extends object>(): SystemDefinition<T>;
  inputSchema<S extends SchemaObject>(schema: S): SystemDefinition<InferSchema<S>>;
  inputSchema(schema?: SchemaObject): SystemDefinition<object> {
    const next = new SystemDefinition<object>(this.name, this.version, this.description);
    next._root = this._root;
    if (schema) {
      next._inputPorts = schemaToManifest(schema).ports;
    } else {
      next._inputPorts = [...this._inputPorts];
    }
    return next;
  }

  get input(): Expressionify<TInput> {
    return createExecutionContext<TInput>().input;
  }

  withParams(define: (input: Expressionify<TInput>) => Runnable): this {
    return this.run(define(this.input));
  }

  run(node: Runnable): this {
    if (node && typeof node === "object" && "_composition" in node) {
      this._root = node._composition;
    } else if (node && typeof node === "object" && "node" in node) {
      this._root = node.node();
    } else {
      this._root = node as Composition;
    }
    return this;
  }

  toManifest(): SystemManifest {
    if (!this.version) {
      throw new Error(`System "${this.name}" is missing a version`);
    }
    if (!this._root) {
      throw new Error(`System "${this.name}" has no definition`);
    }

    const serviceDefs = new Map<string, ServiceDefinition<object, object>>();
    collectServiceDefs(this._root, serviceDefs);

    collectMissingRefs(this._root, serviceDefs);

    const services: SystemManifest["services"] = [];
    for (const [ref, def] of serviceDefs) {
      services.push(def.toManifest());
    }

    const connectors = collectConnectors(this._root, serviceDefs);

    return {
      apiVersion: "neuron/v1",
      kind: "System",
      metadata: {
        name: this.name,
        version: this.version,
        description: this.description,
      },
      inputs: this._inputPorts,
      services,
      connectors,
      definition: toManifestNode(this._root),
    };
  }
}

// ---------------------------------------------------------------------------
// Factory
// ---------------------------------------------------------------------------

export function System(config: { name: string; version?: string; description?: string }): SystemDefinition {
  return new SystemDefinition(config.name, config.version, config.description);
}

// ---------------------------------------------------------------------------
// Manifest node conversion
// ---------------------------------------------------------------------------

function toManifestNode(comp: Composition): SystemManifest["definition"] {
  switch (comp.kind) {
    case "service":
      return { kind: "service", service: comp.serviceRef };
    case "sequence":
      return { kind: "sequence", steps: comp.steps.map(toManifestNode) };
    case "parallel":
      return { kind: "parallel", branches: comp.branches.map(toManifestNode) };
  }
}

// ---------------------------------------------------------------------------
// Tree traversal — collect ServiceDefinition instances
// ---------------------------------------------------------------------------

function collectServiceDefs(
  node: Composition,
  out: Map<string, ServiceDefinition<object, object>>
): void {
  switch (node.kind) {
    case "service":
      if (node.serviceDef && !out.has(node.serviceRef)) {
        out.set(node.serviceRef, node.serviceDef);
      }
      break;
    case "sequence":
      for (const step of node.steps) {
        collectServiceDefs(step, out);
      }
      break;
    case "parallel":
      for (const branch of node.branches) {
        collectServiceDefs(branch, out);
      }
      break;
  }
}

function collectMissingRefs(
  node: Composition,
  serviceDefs: Map<string, ServiceDefinition<object, object>>
): void {
  switch (node.kind) {
    case "service":
      if (!serviceDefs.has(node.serviceRef)) {
        throw new Error(
          `Service "${node.serviceRef}" is referenced in the system definition but not defined. ` +
          `Ensure the service was created with Service({ name: "${node.serviceRef}", ... }) and used via .withInput(), .connect(), or .next().`
        );
      }
      break;
    case "sequence":
      for (const step of node.steps) {
        collectMissingRefs(step, serviceDefs);
      }
      break;
    case "parallel":
      for (const branch of node.branches) {
        collectMissingRefs(branch, serviceDefs);
      }
      break;
  }
}

// ---------------------------------------------------------------------------
// Connector generation from composition tree
// ---------------------------------------------------------------------------

function collectConnectors(
  node: Composition,
  services: Map<string, ServiceDefinition<object, object>>
): ConnectorManifest[] {
  const connectors: ConnectorManifest[] = [];
  walkConnectors(node, connectors, services);
  return connectors;
}

function walkConnectors(
  node: Composition,
  out: ConnectorManifest[],
  services: Map<string, ServiceDefinition<object, object>>
): void {
  switch (node.kind) {
    case "sequence": {
      const seq = node as SequenceComposition;
      for (let i = 0; i < seq.steps.length - 1; i++) {
        const current = seq.steps[i]!;
        const next = seq.steps[i + 1]!;

        const fromRefs = getServiceRefsFlat(current);
        const nextServiceTargets = getTargetServiceComps(next);

        for (const from of fromRefs) {
          for (const target of nextServiceTargets) {
            const mappings = connectorMappings(from, target, services);
            const validations = target.incomingConditions.map((c) => ({
              expression: c.expression,
              message: c.message ?? "",
            }));
            out.push({
              from,
              to: target.serviceRef,
              mappings,
              validations,
            });
          }
        }
      }

      for (const step of seq.steps) {
        walkNested(step, out, services);
      }
      break;
    }
    case "parallel": {
      const par = node as ParallelComposition;
      for (const branch of par.branches) {
        walkNested(branch, out, services);
      }
      break;
    }
    case "service":
      break;
  }
}

function walkNested(
  node: Composition,
  out: ConnectorManifest[],
  services: Map<string, ServiceDefinition<object, object>>
): void {
  switch (node.kind) {
    case "sequence":
      walkConnectors(node, out, services);
      break;
    case "parallel":
      walkConnectors(node, out, services);
      break;
    default:
      break;
  }
}

function connectorMappings(
  fromRef: string,
  target: ServiceTarget,
  services: Map<string, ServiceDefinition<object, object>>
): ConnectorManifest["mappings"] {
  const explicit = Object.entries(target.bindings).map(([name, expression]) => ({
    target: name,
    expression,
  }));
  if (explicit.length > 0) return explicit;

  const from = services.get(fromRef)?.toManifest();
  const to = services.get(target.serviceRef)?.toManifest();
  if (!from || !to) return [];

  const outputs = new Map(from.outputs.map((port) => [port.name, port]));
  return to.inputs.flatMap((input) => {
    const output = outputs.get(input.name);
    if (!output || !compatiblePortTypes(output.type, input.type)) return [];
    return [{ target: input.name, expression: `source.output.${input.name}` }];
  });
}

function compatiblePortTypes(from: string, to: string): boolean {
  return from === to || from === "any" || to === "any";
}

interface ServiceTarget {
  serviceRef: string;
  bindings: Record<string, string>;
  incomingConditions: Array<{ expression: string; message?: string }>;
}

function getTargetServiceComps(node: Composition): ServiceTarget[] {
  if (node.kind === "service") {
    return [node];
  }
  if (node.kind === "sequence") {
    const steps = node.steps;
    if (steps.length === 0) return [];
    return getTargetServiceComps(steps[0]!);
  }
  if (node.kind === "parallel") {
    const targets: ServiceTarget[] = [];
    for (const branch of node.branches) {
      targets.push(...getTargetServiceComps(branch));
    }
    return targets;
  }
  return [];
}

function getServiceRefsFlat(node: Composition): string[] {
  if (node.kind === "service") return [node.serviceRef];
  if (node.kind === "parallel") {
    const refs: string[] = [];
    for (const branch of node.branches) {
      refs.push(...getServiceRefsFlat(branch));
    }
    return refs;
  }
  if (node.kind === "sequence") {
    if (node.steps.length === 0) return [];
    return getServiceRefsFlat(node.steps[0]!);
  }
  return [];
}
