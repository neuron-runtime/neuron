import { describe, it, expect } from "vitest";
import { set, http, command, filesystem } from "../src/core.js";

describe("built-in capability runtimes", () => {
  it("declares every built-in with the core namespace and no version", () => {
    const runtimes = [set("values"), http("call"), command("run"), filesystem("read")];

    for (const capability of runtimes) {
      const runtime = capability.toManifest().capabilityRuntime;
      expect(runtime.name.startsWith("neuron:core:")).toBe(true);
      expect(runtime.version).toBeUndefined();
      expect(runtime.registry).toBeUndefined();
    }
  });

  it("declares the built-ins the runtime provides", () => {
    const runtimes = [set("values"), http("call"), command("run"), filesystem("read")];

    expect(runtimes.map((c) => c.toManifest().capabilityRuntime.name)).toEqual([
      "neuron:core:set",
      "neuron:core:http",
      "neuron:core:command",
      "neuron:core:filesystem",
    ]);
  });

  it("names the capability the author declared", () => {
    expect(set("values").ref).toBe("values");
    expect(http("call").ref).toBe("call");
    expect(command("run").ref).toBe("run");
    expect(filesystem("read").ref).toBe("read");
  });

  it("declares no ports of its own", () => {
    // The contract of a built-in is published by the runtime, not authored per
    // capability, so the manifest carries no ports for it. The runtime
    // validates the input against the contract it publishes.
    for (const capability of [set("values"), http("call"), command("run"), filesystem("read")]) {
      const manifest = capability.toManifest();
      expect(manifest.params).toEqual([]);
      expect(manifest.results).toEqual([]);
    }
  });

  it("binds the http params", () => {
    const node = http("call").withParams({ url: "https://example.com/api" });

    expect(node.bindings).toEqual({
      url: { kind: "literal", value: "https://example.com/api" },
    });
  });

  it("binds the command args", () => {
    const node = command("run").withParams({ command: "git", args: ["status"] });

    expect(node.bindings).toEqual({
      command: { kind: "literal", value: "git" },
      args: { kind: "literal", value: ["status"] },
    });
  });

  it("binds the filesystem operation", () => {
    const node = filesystem("read").withParams({ operation: "read", path: "data.json" });

    expect(node.bindings).toEqual({
      operation: { kind: "literal", value: "read" },
      path: { kind: "literal", value: "data.json" },
    });
  });

  it("binds the http credentials and the retry policy", () => {
    const node = http("call").withParams({
      url: "https://example.com/api",
      auth: { type: "bearer", token: "secret" },
      retry: { attempts: 3, backoff: "200ms", methods: ["GET"] },
    });

    expect(Object.keys(node.bindings)).toEqual(["url", "auth", "retry"]);
  });
});
