import { describe, it, expect } from "vitest";
import { connect } from "../src/connection.js";
import { createSourceContext } from "../src/expression.js";
import { string, boolean } from "../src/schema.js";
import { Capability } from "../src/capability.js";

describe("connect()", () => {
  it("creates bindings from a source-typed expression object", () => {
    const conn = connect<{ id: string; email: string }, { id: string; email: string }>(
      (src) => ({ id: src.result.id, email: src.result.email })
    );

    expect(conn.__bindings).toEqual([
      { target: "id", expression: "source.result.id" },
      { target: "email", expression: "source.result.email" },
    ]);
  });

  it("supports literal string values", () => {
    const conn = connect<{ id: string }, { id: string; region: string }>(
      (src) => ({ id: src.result.id, region: "us-east-1" })
    );

    expect(conn.__bindings).toEqual([
      { target: "id", expression: "source.result.id" },
      { target: "region", expression: "'us-east-1'" },
    ]);
  });

  it("adds conditions via when()", () => {
    const source = createSourceContext<{ verified: boolean }>();
    const conn = connect<{ verified: boolean; id: string }, { id: string }>(
      (src) => ({ id: src.result.id })
    ).when(source.result.verified.equals(true), "Not verified");

    expect(conn.__bindings).toEqual([
      { target: "id", expression: "source.result.id" },
    ]);
    expect(conn.__conditions).toEqual([
      { expression: "source.result.verified == true", message: "Not verified" },
    ]);
  });

  it("is immutable — when() returns a new connection", () => {
    const source = createSourceContext<{ verified: boolean }>();
    const base = connect<{ verified: boolean; id: string }, { id: string }>(
      (src) => ({ id: src.result.id })
    );
    const withCondition = base.when(source.result.verified.equals(true), "msg");

    expect(base.__conditions).toHaveLength(0);
    expect(withCondition.__conditions).toHaveLength(1);
  });

  it("works with a capability's .withParams()", () => {
    const verify = Capability({ name: "verify" }).resultSchema({ id: string() });
    const save = Capability({ name: "save" }).paramsSchema({ id: string().required() });

    const conn = connect((src) => ({ id: src.result.id }));
    const node = save.withParams(conn);

    expect(node.bindings).toEqual({ id: "source.result.id" });
  });
});