import { describe, it, expect } from "vitest";
import { Assembly } from "../src/assembly.js";
import { Capability } from "../src/capability.js";
import { number, string } from "../src/schema.js";

describe("T-012 fan-in same target", () => {
  it("supports fan-in: a capability reads from multiple sources (different paths)", () => {
    const a = Capability("a").resultSchema({ x: string() });
    const b = Capability("b").resultSchema({ y: string() });
    const target = Capability("target").paramsSchema({ x: string(), y: string() });

    const sys = Assembly().run([
      a.withParams({}),
      b.withParams({}),
      target.withParams({ x: a.result.x, y: b.result.y }),
    ]);

    expect(() => sys.toManifest()).not.toThrow();
    const manifest = sys.toManifest();
    const fanInBinding = manifest.bindings.find(
      (b) => b.from === "a" || b.from === "b"
    );
    expect(fanInBinding).toBeDefined();
    const aBinding = manifest.bindings.find((b) => b.from === "a");
    const bBinding = manifest.bindings.find((b) => b.from === "b");
    expect(aBinding).toBeDefined();
    expect(bBinding).toBeDefined();
    expect(aBinding?.mappings).toHaveLength(1);
    expect(bBinding?.mappings).toHaveLength(1);
  });

  it("supports fan-in with same target path removal (R3-10 removed)", () => {
    // With R3-10 removed, multiple bindings to the same target capability 
    // should be allowed. The target has two different param names so both
    // sources can contribute.
    const a = Capability("a").resultSchema({ value: number() });
    const b = Capability("b").resultSchema({ value: number() });
    const target = Capability("target").paramsSchema({ 
      countFromA: number(), 
      countFromB: number() 
    });

    const sys = Assembly().run([
      a.withParams({}),
      b.withParams({}),
      target.withParams({ countFromA: a.result.value, countFromB: b.result.value }),
    ]);

    expect(() => sys.toManifest()).not.toThrow();
    const manifest = sys.toManifest();
    // Two bindings from a and b should be generated
    expect(manifest.bindings).toHaveLength(2);
    const aBinding = manifest.bindings.find((b) => b.from === "a");
    const bBinding = manifest.bindings.find((b) => b.from === "b");
    expect(aBinding).toBeDefined();
    expect(bBinding).toBeDefined();
    // Each binding has one mapping to its respective target path
    expect(aBinding?.mappings).toHaveLength(1);
    expect(aBinding?.mappings[0].target).toBe("countFromA");
    expect(bBinding?.mappings).toHaveLength(1);
    expect(bBinding?.mappings[0].target).toBe("countFromB");
  });
});
