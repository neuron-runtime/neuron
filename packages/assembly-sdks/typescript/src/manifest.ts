import type { RuntimeConfig } from "./capability.js";

export interface AssemblyManifest {
  apiVersion: "neuron/v1";
  kind: "Assembly";
  metadata: {
    name: string;
    version: string;
    description?: string;
  };
  variables?: Record<string, unknown>;
  capabilities: CapabilityManifest[];
  bindings: BindingManifest[];
}

export interface CapabilityManifest {
  name: string;
  version?: string;
  description?: string;

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

export interface BindingMappingManifest {
  target: string;
  expression: string;
}

export interface BindingValidationManifest {
  expression: string;
  message: string;
}