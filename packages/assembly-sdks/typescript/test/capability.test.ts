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

describe("Capability.runtime()", () => {
  it("sets explicit runtime name, version, registry", () => {
    const svc = Capability({ name: "http-call" }).runtime({
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

  it("defaults missing runtime version to latest and registry to local", () => {
    const svc = Capability({ name: "http-call" }).runtime({ name: "http.get" });
    expect(svc.toManifest().capabilityRuntime).toEqual({
      name: "http.get",
      version: "latest",
      registry: "local",
    });
  });

  it("emits no runtimeConfig key when none is declared", () => {
    // An author who declares nothing must produce no key at all, so N.O.R.E.
    // supplies every default rather than the SDK manufacturing an empty one.
    const svc = Capability({ name: "http-call" }).runtime({ name: "http.get" });
    expect("runtimeConfig" in svc.toManifest().capabilityRuntime).toBe(false);
    expect(Object.keys(svc.toManifest().capabilityRuntime)).toEqual([
      "name",
      "version",
      "registry",
    ]);
  });

  it("carries a grouped runtimeConfig into the manifest runtime declaration", () => {
    const svc = Capability({ name: "http-call" }).runtime({
      name: "http.get",
      runtimeConfig: {
        execution: { mode: "detach", timeout: "5s" },
        retry: { policy: "exponential", maxAttempts: 3, initialBackoff: "100ms" },
      },
    });

    expect(svc.toManifest().capabilityRuntime).toEqual({
      name: "http.get",
      version: "latest",
      registry: "local",
      runtimeConfig: {
        execution: { mode: "detach", timeout: "5s" },
        retry: { policy: "exponential", maxAttempts: 3, initialBackoff: "100ms" },
      },
    });
  });

  it("keeps runtimeConfig out of the capability's params and results", () => {
    // runtimeConfig instructs N.O.R.E.; it is never capability input.
    const svc = Capability({ name: "http-call" })
      .runtime({
        name: "http.get",
        runtimeConfig: { execution: { mode: "detach", timeout: "5s" } },
      })
      .paramsSchema({ url: string() })
      .resultSchema({ status: number() });

    const manifest = svc.toManifest();
    expect(manifest.params.map((p) => p.name)).toEqual(["url"]);
    expect(manifest.results.map((p) => p.name)).toEqual(["status"]);
    expect(manifest.capabilityRuntime.runtimeConfig).toBeDefined();
    expect(JSON.stringify(manifest.params)).not.toContain("runtimeConfig");
    expect(JSON.stringify(manifest.results)).not.toContain("runtimeConfig");
  });

  it("gives two capabilities on the same runtime their own runtimeConfig", () => {
    const shared = { name: "http.get", version: "1.0.0", registry: "github" };
    const wait = Capability({ name: "wait-call" }).runtime({
      ...shared,
      runtimeConfig: { execution: { mode: "wait" } },
    });
    const detach = Capability({ name: "detach-call" }).runtime({
      ...shared,
      runtimeConfig: { execution: { mode: "detach" } },
    });

    expect(wait.toManifest().capabilityRuntime.runtimeConfig).toEqual({
      execution: { mode: "wait" },
    });
    expect(detach.toManifest().capabilityRuntime.runtimeConfig).toEqual({
      execution: { mode: "detach" },
    });
  });

  it("does not let one capability's runtimeConfig leak through a shared object", () => {
    const runtimeConfig = { execution: { mode: "wait" as const, timeout: "5s" } };
    const svc = Capability({ name: "a" }).runtime({ name: "http.get", runtimeConfig });
    runtimeConfig.execution.timeout = "99s";

    expect(svc.toManifest().capabilityRuntime.runtimeConfig).toEqual({
      execution: { mode: "wait", timeout: "5s" },
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
      owner: "neuron-runtime",
      repository: "neuron",
    });

    expect(node).toBeInstanceOf(Object);
    expect(node.capabilityRef).toBe("github.read");
    expect(node.bindings).toEqual({
      owner: "'neuron-runtime'",
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
      .runtime({ name: "github.read" });

    expect(githubRead.ref).toBe("github.read");
    expect(githubRead.toManifest().params).toHaveLength(3);
    expect(githubRead.toManifest().results).toHaveLength(2);
    expect(githubRead.toManifest().capabilityRuntime.name).toBe("github.read");
  });
});