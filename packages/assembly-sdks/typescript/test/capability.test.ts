import { describe, it, expect } from "vitest";
import { Capability } from "../src/capability.js";
import { string, number, boolean, list, record } from "../src/schema.js";
import { createExecutionContext, createSourceContext } from "../src/expression.js";

describe("Capability", () => {
  it("defaults the capability runtime to the built-in neuron:core:set", () => {
    const svc = Capability({ name: "validate-order" });
    expect(svc.ref).toBe("validate-order");
    expect(svc.toManifest()).toEqual({
      name: "validate-order",
      version: undefined,
      description: undefined,
      capabilityRuntime: { name: "neuron:core:set", version: "latest", registry: "local" },
      params: [],
      results: [],
    });
  });

  it("sets version and description from the config object", () => {
    const svc = Capability({
      name: "my-capability",
      version: "2.0.0",
      description: "A test capability",
    });

    expect(svc.toManifest().name).toBe("my-capability");
    expect(svc.toManifest().version).toBe("2.0.0");
    expect(svc.toManifest().description).toBe("A test capability");
  });

  it("defaults the capability runtime version to latest", () => {
    const svc = Capability({ name: "my-capability", version: "1.5.0" });
    expect(svc.toManifest().capabilityRuntime).toEqual({
      name: "neuron:core:set",
      version: "latest",
      registry: "local",
    });
  });

  it("cannot set version/description via chain methods", () => {
    const svc = Capability({ name: "my-capability" });
    expect(typeof (svc as any).version).toBe("undefined");
    expect(typeof (svc as any).description).toBe("undefined");
  });
});

describe("Capability capabilityRuntime", () => {
  it("sets explicit capability runtime name, version, registry", () => {
    const svc = Capability({ name: "http-call" }).capabilityRuntime({
      name: "http.get",
      version: "2.0.0",
      registry: "github",
    });

    expect(svc.toManifest().capabilityRuntime).toEqual({
      name: "http.get",
      version: "2.0.0",
      registry: "github",
    });
  });

  it("defaults missing capability runtime version to latest", () => {
    const svc = Capability({ name: "http-call" }).capabilityRuntime({ name: "http.get" });
    expect(svc.toManifest().capabilityRuntime).toEqual({
      name: "http.get",
      version: "latest",
      registry: "local",
    });
  });
});

describe("Capability schemas", () => {
  it("declares input schema with runtime validation rules", () => {
    const svc = Capability({ name: "my-capability" }).paramsSchema({
      email: string().email().required(),
      age: number().min(18).max(120),
    });

    const manifest = svc.toManifest();
    expect(manifest.params).toEqual([
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
    const svc = Capability({ name: "my-capability" }).resultSchema({
      verified: boolean(),
      tier: string(),
      items: list(),
      meta: record(),
    });

    const manifest = svc.toManifest();
    expect(manifest.results.map((o) => o.type)).toEqual([
      "boolean",
      "string",
      "array",
      "object",
    ]);
  });

  it("supports the type-only schema overload (no runtime rules)", () => {
    const svc = Capability({ name: "my-capability" }).paramsSchema().resultSchema();
    const manifest = svc.toManifest();
    expect(manifest.params).toEqual([]);
    expect(manifest.results).toEqual([]);
  });
});

describe("Capability.withParams()", () => {
  it("returns an immutable composition node with bindings", () => {
    const svc = Capability({ name: "github.read" })
      .paramsSchema({
        owner: string().required(),
        repository: string().required(),
      })
      .resultSchema({
        content: string(),
        sha: string(),
      });

    const node = svc.withParams({
      owner: "Muhammad-Jay",
      repository: "neuron",
    });

    expect(node).toBeInstanceOf(Object);
    expect(node.capabilityRef).toBe("github.read");
    expect(node.bindings).toEqual({
      owner: "'Muhammad-Jay'",
      repository: "'neuron'",
    });
  });

  it("converts expressions to expression strings", () => {
    const svc = Capability({ name: "a" }).paramsSchema({
      customerId: string().required(),
      email: string().required(),
    });
    const exec = createExecutionContext<{ id: string }>();
    const source = createSourceContext<{ email: string }>();

    const node = svc.withParams({
      customerId: exec.params.id,
      email: source.result.email,
    });

    expect(node.bindings.customerId).toBe("execution.params.id");
    expect(node.bindings.email).toBe("source.result.email");
  });

  it("does not mutate the original capability definition", () => {
    const svc = Capability({ name: "a" }).resultSchema({ out: string() });
    const before = JSON.stringify(svc.toManifest());
    svc.withParams({});
    expect(JSON.stringify(svc.toManifest())).toBe(before);
  });

  it("produces typed result expressions", () => {
    const svc = Capability({ name: "a" }).resultSchema({ content: string(), sha: string() });
    expect(String(svc.result.content)).toBe("source.result.content");
    expect(String(svc.result.sha)).toBe("source.result.sha");
  });
});

describe("Capability.runtimeConfig()", () => {
  it("returns a composition node with execution config", () => {
    const svc = Capability({ name: "a" });
    const node = svc.runtimeConfig({ timeout: "10s", retries: 2 });
    expect(node.runtimeConfig).toEqual({ timeout: "10s", retries: 2 });
  });
});

describe("Capability.bind()", () => {
  it("collects capability invocations into a flat composition", () => {
    const a = Capability({ name: "a" });
    const b = Capability({ name: "b" });

    const composition = a.bind(b.withParams({}));
    expect(composition._nodes).toHaveLength(2);
    expect(composition._nodes[0]!.capabilityRef).toBe("a");
    expect(composition._nodes[1]!.capabilityRef).toBe("b");
  });

  it("chains three capabilities via .bind().bind()", () => {
    const a = Capability({ name: "a" });
    const b = Capability({ name: "b" });
    const c = Capability({ name: "c" });

    const composition = a.bind(b.withParams({})).bind(c.withParams({}));
    expect(composition._nodes).toHaveLength(3);
    expect(composition._nodes[0]!.capabilityRef).toBe("a");
    expect(composition._nodes[1]!.anchor).toBe("a");
    expect(composition._nodes[2]!.anchor).toBe("b");
  });

  it("derives the source from a result reference instead of the positional anchor", () => {
    const a = Capability({ name: "a" }).resultSchema({ data: string() });
    const b = Capability({ name: "b" }).paramsSchema({ data: string().required() });

    const composition = a.bind(b.withParams({ data: a.result.data }));
    expect(composition._nodes[1]!.sources).toEqual(["a"]);
    expect(composition._nodes[1]!.anchor).toBeUndefined();
  });

  it("attaches conditions via the second argument", () => {
    const a = Capability({ name: "a" }).resultSchema({ valid: boolean() });
    const b = Capability({ name: "b" });

    const composition = a.bind(b.withParams({}), {
      when: a.result.valid.equals(true),
      message: "failed",
    });

    const target = composition._nodes[1]!;
    expect(target.incomingConditions).toEqual([
      { expression: "source.result.valid == true", message: "failed" },
    ]);
    expect(target.sources).toEqual(["a"]);
  });
});

describe("installable capability package pattern", () => {
  it("exports a reusable typed capability definition", () => {
    const githubRead = Capability({
      name: "github.read",
      version: "1.0.0",
    })
      .paramsSchema({
        owner: string().required(),
        repository: string().required(),
        path: string().required(),
      })
      .resultSchema({
        content: string(),
        sha: string(),
      })
      .capabilityRuntime({ name: "github.read" });

    expect(githubRead.ref).toBe("github.read");
    expect(githubRead.toManifest().params).toHaveLength(3);
    expect(githubRead.toManifest().results).toHaveLength(2);
    expect(githubRead.toManifest().capabilityRuntime.name).toBe("github.read");
  });
});