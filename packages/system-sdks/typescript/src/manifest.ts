export interface SystemManifest {
  apiVersion: "neuron/v1";
  kind: "System";
  metadata: {
    name: string;
    version: string;
    description?: string;
  };
  variables?: Record<string, unknown>;
  services: ServiceManifest[];
  inputs?: PortManifest[];
  connectors: ConnectorManifest[];
  definition: SystemNodeManifest;
}

export interface ServiceManifest {
  name: string;
  version?: string;
  description?: string;

  executor: {
    name: string;
    version: string;
    registry: string;
  };

  inputs: PortManifest[];
  outputs: PortManifest[];

  config?: Record<string, unknown>;

  execution?: {
    mode?: string;
    timeout?: string;
    retries?: number;
    concurrency?: number;
    continueOnFail?: boolean;
  };
}

// ProjectConfig carried project-level runtime configuration (registries,
// runtime defaults, storage, inspector) into the manifest. Runtime
// configuration is now owned by the neuron.config.* project configuration
// and assembled by the CLI (`application/internal/cli/register`), so the
// manifest stays a purely source-language-neutral System description.
export interface PortManifest {
  name: string;
  type: "any" | "string" | "number" | "boolean" | "object" | "array";
  required: boolean;
  rules?: Record<string, unknown>;
}

export interface ConnectorManifest {
  from: string;
  to: string;
  mappings: ConnectorMappingManifest[];
  validations: ConnectorValidationManifest[];
}

export interface ConnectorMappingManifest {
  target: string;
  expression: string;
}

export interface ConnectorValidationManifest {
  expression: string;
  message: string;
}

export type SystemNodeManifest =
  | {
      kind: "service";
      service: string;
    }
  | {
      kind: "sequence";
      steps: SystemNodeManifest[];
    }
  | {
      kind: "parallel";
      branches: SystemNodeManifest[];
    };
