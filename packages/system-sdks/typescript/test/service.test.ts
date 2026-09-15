import { describe, it, expect } from "vitest";
import { Service } from "../src/service.js";
import { string, number, boolean, list, record } from "../src/schema.js";
import { createExecutionContext, createSourceContext } from "../src/expression.js";

describe("Service", () => {
  it("defaults the executor to the built-in neuron:core:set", () => {
    const svc = Service({ name: "validate-order" });
    expect(svc.ref).toBe("validate-order");
    expect(svc.toManifest()).toEqual({
      name: "validate-order",
      version: undefined,
      description: undefined,
      executor: { name: "neuron:core:set", version: "latest", registry: "local" },
      inputs: [],
      outputs: [],
    });
  });

  it("sets version and description from the config object", () => {
    const svc = Service({
      name: "my-service",
      version: "2.0.0",
      description: "A test service",
    });

    expect(svc.toManifest().name).toBe("my-service");
    expect(svc.toManifest().version).toBe("2.0.0");
    expect(svc.toManifest().description).toBe("A test service");
  });

  it("defaults the executor version to latest", () => {
    const svc = Service({ name: "my-service", version: "1.5.0" });
    expect(svc.toManifest().executor).toEqual({
      name: "neuron:core:set",
      version: "latest",
      registry: "local",
    });
  });

  it("cannot set version/description via chain methods", () => {
    const svc = Service({ name: "my-service" });
    expect(typeof (svc as any).version).toBe("undefined");
    expect(typeof (svc as any).description).toBe("undefined");
  });

  it("produces a node reference", () => {
    const svc = Service({ name: "my-service" });
    expect(svc.node()).toEqual({ kind: "service", service: "my-service" });
  });
});

describe("Service executor", () => {
  it("sets explicit executor name, version, registry", () => {
    const svc = Service({ name: "http-call" }).executor({
      name: "http.get",
      version: "2.0.0",
      registry: "github",
    });

    expect(svc.toManifest().executor).toEqual({
      name: "http.get",
      version: "2.0.0",
      registry: "github",
    });
  });

  it("defaults missing executor version to latest", () => {
    const svc = Service({ name: "http-call" }).executor({ name: "http.get" });
    expect(svc.toManifest().executor).toEqual({
      name: "http.get",
      version: "latest",
      registry: "local",
    });
  });
});

describe("Service schemas", () => {
  it("declares input schema with runtime validation rules", () => {
    const svc = Service({ name: "my-service" }).inputSchema({
      email: string().email().required(),
      age: number().min(18).max(120),
    });

    const manifest = svc.toManifest();
    expect(manifest.inputs).toEqual([
      {
        name: "email",
        type: "string",
        required: true,
        rules: { type: "string", format: "email", required: true },
      },
      {
        name: "age",
        type: "number",
        required: false,
        rules: { type: "number", minimum: 18, maximum: 120 },
      },
    ]);
  });

  it("declares output schema with runtime validation rules", () => {
    const svc = Service({ name: "my-service" }).outputSchema({
      verified: boolean(),
      tier: string(),
      items: list(),
      meta: record(),
    });

    const manifest = svc.toManifest();
    expect(manifest.outputs.map((o) => o.type)).toEqual([
      "boolean",
      "string",
      "array",
      "object",
    ]);
  });

  it("supports the type-only schema overload (no runtime rules)", () => {
    const svc = Service({ name: "my-service" }).inputSchema().outputSchema();
    const manifest = svc.toManifest();
    expect(manifest.inputs).toEqual([]);
    expect(manifest.outputs).toEqual([]);
  });
});

describe("Service.withInput()", () => {
  it("returns an immutable composition node with bindings", () => {
    const svc = Service({ name: "github.read" })
      .inputSchema({
        owner: string().required(),
        repository: string().required(),
      })
      .outputSchema({
        content: string(),
        sha: string(),
      });

    const node = svc.withInput({
      owner: "Muhammad-Jay",
      repository: "neuron",
    });

    expect(node).toBeInstanceOf(Object);
    expect(node.serviceRef).toBe("github.read");
    expect(node.bindings).toEqual({
      owner: "'Muhammad-Jay'",
      repository: "'neuron'",
    });
  });

  it("converts expressions to expression strings", () => {
    const svc = Service({ name: "a" }).inputSchema({
      customerId: string().required(),
      email: string().required(),
    });
    const exec = createExecutionContext<{ id: string }>();
    const source = createSourceContext<{ email: string }>();

    const node = svc.withInput({
      customerId: exec.input.id,
      email: source.output.email,
    });

    expect(node.bindings.customerId).toBe("execution.input.id");
    expect(node.bindings.email).toBe("source.output.email");
  });

  it("does not mutate the original service definition", () => {
    const svc = Service({ name: "a" }).outputSchema({ out: string() });
    const before = JSON.stringify(svc.toManifest());
    svc.withInput({});
    expect(JSON.stringify(svc.toManifest())).toBe(before);
  });

  it("produces typed output expressions", () => {
    const svc = Service({ name: "a" }).outputSchema({ content: string(), sha: string() });
    expect(String(svc.output.content)).toBe("source.output.content");
    expect(String(svc.output.sha)).toBe("source.output.sha");
  });
});

describe("Service.executionConfig()", () => {
  it("returns a composition node with execution config", () => {
    const svc = Service({ name: "a" });
    const node = svc.executionConfig({ timeout: "10s", retries: 2 });
    expect(node.executionConfig).toEqual({ timeout: "10s", retries: 2 });
  });
});

describe("Service.next()", () => {
  it("chains two services into a flat sequence", () => {
    const a = Service({ name: "a" });
    const b = Service({ name: "b" });

    const seq = a.next(b.withInput({}));
    expect(seq._composition.kind).toBe("sequence");
    expect(seq._composition.steps).toHaveLength(2);
  });

  it("chains three services via .next().next()", () => {
    const a = Service({ name: "a" });
    const b = Service({ name: "b" });
    const c = Service({ name: "c" });

    const seq = a.next(b.withInput({})).next(c.withInput({}));
    expect(seq._composition.kind).toBe("sequence");
    expect(seq._composition.steps).toHaveLength(3);
  });

  it("attaches conditions via the second argument", () => {
    const a = Service({ name: "a" }).outputSchema({ valid: boolean() });
    const b = Service({ name: "b" });

    const seq = a.next(b.withInput({}), {
      when: a.output.valid.equals(true),
      message: "failed",
    });

    const target = seq._composition.steps[1] as any;
    expect(target.incomingConditions).toEqual([
      { expression: "source.output.valid == true", message: "failed" },
    ]);
  });
});

describe("installable service package pattern", () => {
  it("exports a reusable typed service definition", () => {
    const githubRead = Service({
      name: "github.read",
      version: "1.0.0",
    })
      .inputSchema({
        owner: string().required(),
        repository: string().required(),
        path: string().required(),
      })
      .outputSchema({
        content: string(),
        sha: string(),
      })
      .executor({ name: "github.read" });

    expect(githubRead.ref).toBe("github.read");
    expect(githubRead.toManifest().inputs).toHaveLength(3);
    expect(githubRead.toManifest().outputs).toHaveLength(2);
    expect(githubRead.toManifest().executor.name).toBe("github.read");
  });
});
