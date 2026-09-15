import { describe, it, expect } from "vitest";
import { string, number, boolean, list, record, type Infer } from "../src/schema.js";
import { schemaToManifest } from "../src/schema.js";
import { Service } from "../src/service.js";

describe("schema → manifest conversion", () => {
  it("converts a schema object into port manifests", () => {
    const schema = {
      name: string().required(),
      age: number().min(18),
      tags: list(),
      profile: record(),
      active: boolean(),
    };

    const { ports } = schemaToManifest(schema);
    expect(ports).toEqual([
      {
        name: "name",
        type: "string",
        required: true,
        rules: { type: "string", required: true },
      },
      {
        name: "age",
        type: "number",
        required: false,
        rules: { type: "number", minimum: 18 },
      },
      {
        name: "tags",
        type: "array",
        required: false,
        rules: { type: "array" },
      },
      {
        name: "profile",
        type: "object",
        required: false,
        rules: { type: "object" },
      },
      {
        name: "active",
        type: "boolean",
        required: false,
        rules: { type: "boolean" },
      },
    ]);
  });
});

describe("Infer type utility", () => {
  it("extracts field types from a schema object", () => {
    const User = {
      name: string(),
      age: number(),
      active: boolean(),
      tags: list(),
      meta: record(),
    };

    type UserType = Infer<typeof User>;
    const user: UserType = {
      name: "Ada",
      age: 36,
      active: true,
      tags: ["a"],
      meta: { x: 1 },
    };
    expect(user.name).toBe("Ada");
    expect(user.age).toBe(36);
  });
});

describe("typed service with schema", () => {
  it("carries the schema in the manifest via toManifest", () => {
    const svc = Service({ name: "user.create" }).inputSchema({
      email: string().email().required(),
    });

    const manifest = svc.toManifest();
    expect(manifest.inputs[0]).toMatchObject({
      name: "email",
      type: "string",
      required: true,
    });
  });
});
