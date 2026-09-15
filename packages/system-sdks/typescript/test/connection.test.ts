import { describe, it, expect } from "vitest";
import { connect } from "../src/connection.js";
import { createSourceContext } from "../src/expression.js";
import { string, boolean } from "../src/schema.js";
import { Service } from "../src/service.js";

describe("connect()", () => {
  it("creates bindings from a source-typed expression object", () => {
    const conn = connect<{ id: string; email: string }, { id: string; email: string }>(
      (src) => ({ id: src.output.id, email: src.output.email })
    );

    expect(conn.__bindings).toEqual([
      { target: "id", expression: "source.output.id" },
      { target: "email", expression: "source.output.email" },
    ]);
  });

  it("supports literal string values", () => {
    const conn = connect<{ id: string }, { id: string; region: string }>(
      (src) => ({ id: src.output.id, region: "us-east-1" })
    );

    expect(conn.__bindings).toEqual([
      { target: "id", expression: "source.output.id" },
      { target: "region", expression: "'us-east-1'" },
    ]);
  });

  it("adds conditions via when()", () => {
    const source = createSourceContext<{ verified: boolean }>();
    const conn = connect<{ verified: boolean; id: string }, { id: string }>(
      (src) => ({ id: src.output.id })
    ).when(source.output.verified.equals(true), "Not verified");

    expect(conn.__bindings).toEqual([
      { target: "id", expression: "source.output.id" },
    ]);
    expect(conn.__conditions).toEqual([
      { expression: "source.output.verified == true", message: "Not verified" },
    ]);
  });

  it("is immutable — when() returns a new connection", () => {
    const source = createSourceContext<{ verified: boolean }>();
    const base = connect<{ verified: boolean; id: string }, { id: string }>(
      (src) => ({ id: src.output.id })
    );
    const withCondition = base.when(source.output.verified.equals(true), "msg");

    expect(base.__conditions).toHaveLength(0);
    expect(withCondition.__conditions).toHaveLength(1);
  });

  it("works with a service's .withInput()", () => {
    const verify = Service({ name: "verify" }).outputSchema({ id: string() });
    const save = Service({ name: "save" }).inputSchema({ id: string().required() });

    const conn = connect((src) => ({ id: src.output.id }));
    const node = save.withInput(conn);

    expect(node.bindings).toEqual({ id: "source.output.id" });
  });
});
