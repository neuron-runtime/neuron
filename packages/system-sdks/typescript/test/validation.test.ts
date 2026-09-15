import { describe, it, expect } from "vitest";
import {
  string,
  number,
  boolean,
  list,
  record,
} from "../src/schema.js";

describe("string()", () => {
  it("produces a string field", () => {
    expect(string().__rules).toEqual({ type: "string" });
  });

  it("builds min/max", () => {
    expect(string().min(1).max(100).__rules).toEqual({
      type: "string",
      minLength: 1,
      maxLength: 100,
    });
  });

  it("builds pattern/email/uuid", () => {
    expect(string().pattern("^[a-z]+$").__rules).toEqual({
      type: "string",
      pattern: "^[a-z]+$",
    });
    expect(string().email().__rules).toEqual({ type: "string", format: "email" });
    expect(string().uuid().__rules).toEqual({ type: "string", format: "uuid" });
  });

  it("marks required", () => {
    expect(string().required().__rules).toEqual({
      type: "string",
      required: true,
    });
  });
});

describe("number()", () => {
  it("produces a number field", () => {
    expect(number().__rules).toEqual({ type: "number" });
  });

  it("builds min/max/exclusive", () => {
    expect(number().min(0).max(100).__rules).toEqual({
      type: "number",
      minimum: 0,
      maximum: 100,
    });
    expect(number().exclusiveMin(0).exclusiveMax(100).__rules).toEqual({
      type: "number",
      exclusiveMinimum: 0,
      exclusiveMaximum: 100,
    });
  });

  it("builds integer", () => {
    expect(number().integer().__rules).toEqual({ type: "number", multipleOf: 1 });
  });
});

describe("boolean()", () => {
  it("produces a boolean field", () => {
    expect(boolean().__rules).toEqual({ type: "boolean" });
  });
});

describe("list()", () => {
  it("produces an array field", () => {
    expect(list().__rules).toEqual({ type: "array" });
  });

  it("builds minItems/maxItems/uniqueItems/of", () => {
    expect(list().minItems(1).maxItems(10).__rules).toEqual({
      type: "array",
      minItems: 1,
      maxItems: 10,
    });
    expect(list().uniqueItems().__rules).toEqual({
      type: "array",
      uniqueItems: true,
    });
    expect(list(string()).__rules).toEqual({
      type: "array",
      items: { type: "string" },
    });
  });
});

describe("record()", () => {
  it("produces an object field", () => {
    expect(record().__rules).toEqual({ type: "object" });
  });
});
