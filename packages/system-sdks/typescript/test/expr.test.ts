import { describe, it, expect } from "vitest";
import { Service } from "../src/service.js";
import { createExecutionContext, createExpressionProxy, createSourceContext } from "../src/expression.js";
import { string, boolean, number, list, record } from "../src/schema.js";

describe("typed expression proxies", () => {
  it("builds an expression via property access", () => {
    const expr = createExpressionProxy<{ valid: boolean }>("source.output");
    expect(String(expr.valid)).toBe("source.output.valid");
  });

  it("builds nested expressions", () => {
    const expr = createExpressionProxy<{
      validationData: { order: { customerId: string } };
    }>("source.output");
    expect(String(expr.validationData.order.customerId)).toBe(
      "source.output.validationData.order.customerId"
    );
  });

  it("service.output produces source.output.field", () => {
    const svc = Service({ name: "github.read" }).outputSchema({
      content: string(),
      sha: string(),
    });
    expect(String(svc.output.content)).toBe("source.output.content");
    expect(String(svc.output.sha)).toBe("source.output.sha");
  });

  it("coerces to string in template literal", () => {
    const expr = createExpressionProxy<{ valid: boolean }>("source.output");
    expect(`${expr.valid}`).toBe("source.output.valid");
  });
});

describe("comparison methods", () => {
  it("equals with boolean", () => {
    const e = createExpressionProxy<{ valid: boolean }>("source.output");
    expect(String(e.valid.equals(true))).toBe("source.output.valid == true");
  });

  it("equals with string value", () => {
    const e = createExpressionProxy<{ status: string }>("source.output");
    expect(String(e.status.equals("active"))).toBe("source.output.status == 'active'");
  });

  it("greaterThan with number", () => {
    const e = createExpressionProxy<{ amount: number }>("source.output");
    expect(String(e.amount.greaterThan(100))).toBe("source.output.amount > 100");
  });

  it("ordered comparisons", () => {
    const e = createExpressionProxy<{ amount: number }>("source.output");
    expect(String(e.amount.greaterThanOrEqualTo(10))).toBe("source.output.amount >= 10");
    expect(String(e.amount.lessThan(100))).toBe("source.output.amount < 100");
    expect(String(e.amount.lessThanOrEqualTo(100))).toBe("source.output.amount <= 100");
  });

  it("notEquals", () => {
    const e = createExpressionProxy<{ status: string }>("source.output");
    expect(String(e.status.notEquals("failed"))).toBe("source.output.status != 'failed'");
  });

  it("and / or", () => {
    const e = createExpressionProxy<{ valid: boolean }>("source.output");
    const left = e.valid.equals(true);
    const right = e.valid.equals(false);
    expect(String(left.and(right))).toBe(
      "(source.output.valid == true) && (source.output.valid == false)"
    );
    expect(String(left.or(right))).toBe(
      "(source.output.valid == true) || (source.output.valid == false)"
    );
  });
});

describe("source context", () => {
  it("accesses source.output", () => {
    const source = createSourceContext<{ email: string }>();
    expect(String(source.output.email)).toBe("source.output.email");
  });
});

describe("execution context", () => {
  it("accesses execution.input", () => {
    const exec = createExecutionContext<{ order: { items: string[] } }>();
    expect(String(exec.input.order)).toBe("execution.input.order");
    expect(String(exec.input.order.items)).toBe("execution.input.order.items");
  });
});
