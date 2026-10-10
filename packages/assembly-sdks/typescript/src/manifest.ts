import type { RuntimeConfig } from "./capability.js";

export interface AssemblyManifest {
  apiVersion: "neuron/v1";
  kind: "Assembly";
  variables?: Record<string, unknown>;
  capabilities: CapabilityManifest[];
  bindings: BindingManifest[];

  // Params declare the Assembly's public invocation contract. They are the
  // source-language-neutral shape of the TypeScript paramsSchema. Omitted
  // entirely when no schema was declared.
  params?: PortManifest[];

  // Result declares the Assembly's public result contract: the schema a caller
  // reads after a synchronous execution, and the structured mapping that
  // produces each declared field. Omitted entirely when no result was declared.
  result?: AssemblyResultManifest;
}

/**
 * AssemblyResultManifest is the public result contract of an Assembly
 * execution. Field order is the author's declared contract and is preserved
 * verbatim; each field resolves from a structured source at execution time,
 * never from an arbitrary expression string.
 */
export interface AssemblyResultManifest {
  schema: PortManifest[];
  fields: AssemblyResultFieldManifest[];
}

export interface AssemblyResultFieldManifest {
  name: string;
  source: ValueRefManifest;
}

export interface CapabilityManifest {
  name: string;

  capabilityRuntime: CapabilityRuntimeManifest;

  params: PortManifest[];
  results: PortManifest[];

  config?: Record<string, unknown>;
}

/**
 * CapabilityRuntimeManifest is the runtime a capability is executed through,
 * plus the configuration N.O.R.E. uses to drive that single invocation.
 *
 * The runtime identity (name, version, registry) is what resolution and
 * installation deduplicate on, so several capabilities may share one resolved
 * artifact. `runtimeConfig` is not part of that identity: it stays attached to
 * the capability whose invocation it governs.
 */
export interface CapabilityRuntimeManifest {
  name: string;
  version: string;
  registry: string;
  /**
   * How N.O.R.E. executes this capability through this runtime. Absent means
   * the author declared nothing and N.O.R.E. supplies every default. It is
   * never capability input and never reaches the runtime as params.
   */
  runtimeConfig?: RuntimeConfig;
}

// PortManifest describes a single typed slot on the capability boundary:
// a parameter (input) or a result (output). The manifest stays a purely
// source-language-neutral Assembly description; project-level runtime
// configuration is owned by the neuron.config.* project configuration and
// assembled by the CLI (`application/internal/cli/register`).
export interface PortManifest {
  name: string;
  type: "any" | "string" | "number" | "boolean" | "object" | "array";
  required: boolean;
  rules?: Record<string, unknown>;
}

export interface BindingManifest {
  from: string;
  to: string;
  mappings: BindingMappingManifest[];
  validations: BindingValidationManifest[];
}

/**
 * ValueRefManifest is the structured reference a mapping reads from, mirroring
 * the canonical ValueRef contract: `kind` discriminates the source, `capability`
 * names the Capability for capability-scoped references, `path` selects a field
 * inside that source, and `value` carries an inline literal. A capability-scoped
 * reference may be emitted without `capability` when it originates from a
 * generic source context; the assembly qualifies it with the binding's source
 * capability before emitting the manifest.
 */
export type ValueRefKind =
  | "capabilityResult"
  | "capabilityParams"
  | "assemblyParams"
  | "variable"
  | "literal";

export interface ValueRefManifest {
  kind: ValueRefKind;
  capability?: string;
  path?: string[];
  value?: unknown;
}

export interface BindingMappingManifest {
  target: string;
  source: ValueRefManifest;
}

export interface BindingValidationManifest {
  expression: string;
  message: string;
}