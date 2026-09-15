import { describe, it, expect } from "vitest";
import { Parallel } from "../src/composition.js";
import { Service } from "../src/service.js";
import { System } from "../src/system.js";
import { string } from "../src/schema.js";

describe("Parallel", () => {
  it("creates a parallel node from composition nodes", () => {
    const a = Service({ name: "a" });
    const b = Service({ name: "b" });

    const node = Parallel(a.withInput({}), b.withInput({}));
    expect(node._composition.kind).toBe("parallel");
    expect(node._composition.branches).toHaveLength(2);
  });

  it("produces a flat manifest tree", () => {
    const a = Service({ name: "a" });
    const b = Service({ name: "b" });
    const c = Service({ name: "c" });

    const sys = System({ name: "test", version: "1.0.0" }).run(
      a.next(Parallel(b.withInput({}), c.withInput({})))
    );

    const manifest = sys.toManifest();
    expect(manifest.definition).toEqual({
      kind: "sequence",
      steps: [
        { kind: "service", service: "a" },
        {
          kind: "parallel",
          branches: [
            { kind: "service", service: "b" },
            { kind: "service", service: "c" },
          ],
        },
      ],
    });
  });

  it("works with services carrying bindings in parallel branches", () => {
    const verify = Service({ name: "verify" }).outputSchema({ email: string() });
    const send = Service({ name: "send" }).inputSchema({ email: string().required() });
    const log = Service({ name: "log" }).inputSchema({ email: string().required() });

    const sys = System({ name: "test", version: "1.0.0" }).run(
      verify.next(Parallel(
        send.withInput({ email: verify.output.email }),
        log.withInput({ email: verify.output.email })
      ))
    );

    const manifest = sys.toManifest();
    expect(manifest.connectors).toHaveLength(2);
    expect(manifest.connectors[0].mappings).toEqual([
      { target: "email", expression: "source.output.email" },
    ]);
  });
});
