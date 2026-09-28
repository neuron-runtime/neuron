import { describe, it, expect } from "vitest";
import { Capability } from "../src/capability.js";
import { createExecutionContext, createExpressionProxy, createSourceContext, expressionRef } from "../src/expression.js";
import { string, boolean, number, list, record } from "../src/schema.js";

describe("typed expression proxies", () => {
  it("builds an expression via property access", () => {
    const expr = createExpressionProxy<{ valid: boolean }>("source.result");
    expect(String(expr.valid)).toBe("source.result.valid");
  });

  it("builds nested expressions", () => {
    const expr = createExpressionProxy<{
      validationData: { order: { customerId: string } };
    }>("source.result");
    expect(String(expr.validationData.order.customerId)).toBe(
      "source.result.validationData.order.customerId"
    );
  });

  it("capability.result produces source.result.field", () => {
    const svc = Capability({ name: "github.read" }).resultSchema({
      content: string(),
      sha: string(),
    });
    expect(String(svc.result.content)).toBe("source.result.content");
    expect(String(svc.result.sha)).toBe("source.result.sha");
  });

  it("coerces to string in template literal", () => {
    const expr = createExpressionProxy<{ valid: boolean }>("source.result");
    expect(`${expr.valid}`).toBe("source.result.valid");
  });
});

describe("comparison methods", () => {
  it("equals with boolean", () => {
    const e = createExpressionProxy<{ valid: boolean }>("source.result");
    expect(String(e.valid.equals(true))).toBe("source.result.valid == true");
  });

  it("equals with string value", () => {
    const e = createExpressionProxy<{ status: string }>("source.result");
    expect(String(e.status.equals("active"))).toBe("source.result.status == 'active'");
  });

  it("greaterThan with number", () => {
    const e = createExpressionProxy<{ amount: number }>("source.result");
    expect(String(e.amount.greaterThan(100))).toBe("source.result.amount > 100");
  });

  it("ordered comparisons", () => {
    const e = createExpressionProxy<{ amount: number }>("source.result");
    expect(String(e.amount.greaterThanOrEqualTo(10))).toBe("source.result.amount >= 10");
    expect(String(e.amount.lessThan(100))).toBe("source.result.amount < 100");
    expect(String(e.amount.lessThanOrEqualTo(100))).toBe("source.result.amount <= 100");
  });

  it("notEquals", () => {
    const e = createExpressionProxy<{ status: string }>("source.result");
    expect(String(e.status.notEquals("failed"))).toBe("source.result.status != 'failed'");
  });

  it("and / or", () => {
    const e = createExpressionProxy<{ valid: boolean }>("source.result");
    const left = e.valid.equals(true);
    const right = e.valid.equals(false);
    expect(String(left.and(right))).toBe(
      "(source.result.valid == true) && (source.result.valid == false)"
    );
    expect(String(left.or(right))).toBe(
      "(source.result.valid == true) || (source.result.valid == false)"
    );
  });
});

describe("capability reference identity", () => {
  it("carries capability identity on result references", () => {
    const svc = Capability({ name: "github.read" }).resultSchema({
      content: string(),
      valid: boolean(),
    });
    expect(expressionRef(svc.result.content)).toBe("github.read");
    expect(expressionRef(svc.result.valid.equals(true))).toBe("github.read");
    expect(expressionRef(svc.result.valid.and(svc.result.valid.equals(false)))).toBe("github.read");
  });

  it("carries no identity on generic context references", () => {
    const source = createSourceContext<{ email: string }>();
    const exec = createExecutionContext<{ order: object }>();
    expect(expressionRef(source.result.email)).toBeUndefined();
    expect(expressionRef(exec.params.order)).toBeUndefined();
  });
});

describe("source context", () => {
  it("accesses source.result", () => {
    const source = createSourceContext<{ email: string }>();
    expect(String(source.result.email)).toBe("source.result.email");
  });
});

describe("execution context", () => {
  it("accesses execution.params", () => {
    const exec = createExecutionContext<{ order: { items: string[] } }>();
    expect(String(exec.params.order)).toBe("execution.params.order");
    expect(String(exec.params.order.items)).toBe("execution.params.order.items");
  });
});