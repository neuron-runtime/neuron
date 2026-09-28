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

  capabilityRuntime: {
    name: string;
    version: string;
    registry: string;
  };

  params: PortManifest[];
  results: PortManifest[];

  config?: Record<string, unknown>;

  execution?: {
    mode?: string;
    timeout?: string;
    retries?: number;
    concurrency?: number;
    continueOnFail?: boolean;
  };
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