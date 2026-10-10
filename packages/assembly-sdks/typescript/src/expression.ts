declare const expressionBrand: unique symbol;

import type { ValueRefManifest } from "./manifest.js";

export interface Expression<T = unknown> {
  readonly [expressionBrand]: T;
  equals(value: T): Expression<boolean>;
  notEquals(value: T): Expression<boolean>;
  greaterThan(value: T): Expression<boolean>;
  greaterThanOrEqualTo(value: T): Expression<boolean>;
  lessThan(value: T): Expression<boolean>;
  lessThanOrEqualTo(value: T): Expression<boolean>;
  and(other: Expression<boolean>): Expression<boolean>;
  or(other: Expression<boolean>): Expression<boolean>;
  toString(): string;
  valueOf(): string;
}

type Primitive = string | number | boolean | bigint | null | undefined;

export type Expressionify<T> =
  T extends Primitive
    ? Expression<T>
    : T extends readonly (infer U)[]
      ? ExpressionArray<U>
      : T extends object
        ? { readonly [K in keyof T]-?: Expressionify<T[K]> }
        : Expression<T>;

export type ExpressionArray<T> = {
  readonly length: Expression<number>;
  readonly [index: number]: Expressionify<T>;
};

export interface SourceContext<TResult extends object> {
  readonly result: Expressionify<TResult>;
}

export interface ExecutionContext<TParams extends object> {
  readonly params: Expressionify<TParams>;
}

interface ExpressionRecord {
  ref?: string;
  segments: string[];
}

const expressionRecords = new WeakMap<object, ExpressionRecord>();

/**
 * createExpressionProxy builds a lazily-evaluated expression proxy.
 *
 * `path` is the CEL expression the proxy compiles to. `ref`, when provided,
 * is the identity of the capability whose result/params the expression
 * references; it lets the compiler derive execution edges from data
 * references instead of an explicit composition tree. Proxies created from a
 * generic source/execution context carry no ref. `segments` records the
 * semantic segments the path is made of (for example ["source", "result"] as
 * the root) so that mappings can be emitted structurally.
 */
export function createExpressionProxy<T>(path: string, ref?: string, segments: string[] = []): Expressionify<T> {
  const handler: ProxyHandler<(...args: unknown[]) => string> = {
    get(_target, prop) {
      if (prop === Symbol.toPrimitive || prop === "toString" || prop === "valueOf") {
        return () => path;
      }
      if (prop === "then") return undefined;
      if (typeof prop === "string" && METHOD_OPERATORS.has(prop)) {
        return (value: unknown) => comparison(`${path} ${METHOD_OPERATORS.get(prop)} ${serialize(value)}`, ref);
      }
      if (prop === "and") {
        return (other: Expression<boolean>) => comparison(`(${path}) && (${String(other)})`, ref);
      }
      if (prop === "or") {
        return (other: Expression<boolean>) => comparison(`(${path}) || (${String(other)})`, ref);
      }
      return createExpressionProxy<unknown>(`${path}.${String(prop)}`, ref, [
        ...segments,
        String(prop),
      ]);
    },
    apply() {
      return path;
    },
  };

  const proxy = new Proxy(() => path, handler) as unknown as Expressionify<T>;
  expressionRecords.set(proxy as unknown as object, { ref, segments });
  return proxy;
}

/**
 * expressionRef returns the capability identity a bound expression references,
 * or undefined when the expression is execution-scoped or literal.
 */
export function expressionRef(value: unknown): string | undefined {
  if (typeof value !== "function") return undefined;
  return expressionRecords.get(value as unknown as object)?.ref;
}

/**
 * toValueRef converts a mapping value into the canonical structured value
 * reference the canonical manifest expects. Expression proxies resolve to
 * capabilityResult / capabilityParams / assemblyParams references; every other
 * value becomes a literal. Expression operator results and references nested
 * inside literals are rejected because the mapping contract only accepts plain
 * references and literals.
 *
 * capabilityResult and capabilityParams references may or may not carry a
 * capability identity. When it is absent the caller must qualify the reference
 * with the binding's source capability before emitting the mapping.
 */
export function toValueRef(value: unknown): ValueRefManifest {
  if (typeof value === "function") {
    const record = expressionRecords.get(value as unknown as object);
    if (!record || record.segments.length < 2) {
      throw new Error(
        `mapping value "${String(value)}" is an expression operator result; mapping values must be plain references or literals`
      );
    }
    const [root, scope] = record.segments;
    const path = record.segments.slice(2);
    if (root === "source" && scope === "result") {
      return { kind: "capabilityResult", capability: record.ref, path };
    }
    if (root === "source" && scope === "params") {
      return { kind: "capabilityParams", capability: record.ref, path };
    }
    if (root === "execution" && scope === "params") {
      return { kind: "assemblyParams", path };
    }
    throw new Error(
      `mapping value "${String(value)}" is not a supported reference; use a capability result/params field or an assembly param`
    );
  }
  return { kind: "literal", value: cloneValue(value) };
}

export function createSourceContext<TResult extends object>(): SourceContext<TResult> {
  return {
    get result() {
      return createExpressionProxy<TResult>("source.result", undefined, ["source", "result"]);
    },
  };
}

export function createExecutionContext<TParams extends object>(): ExecutionContext<TParams> {
  return {
    get params() {
      return createExpressionProxy<TParams>("execution.params", undefined, ["execution", "params"]);
    },
  };
}

function comparison(path: string, ref?: string): Expression<boolean> {
  return createExpressionProxy<boolean>(path, ref) as Expression<boolean>;
}

function cloneValue(value: unknown): unknown {
  if (typeof value === "function") {
    throw new Error("expression references cannot be nested inside literal mapping values");
  }
  if (Array.isArray(value)) {
    return value.map(cloneValue);
  }
  if (value !== null && typeof value === "object") {
    const out: Record<string, unknown> = {};
    for (const [key, entry] of Object.entries(value as Record<string, unknown>)) {
      out[key] = cloneValue(entry);
    }
    return out;
  }
  return value;
}

function serialize(value: unknown): string {
  if (isExpression(value)) return String(value);
  if (typeof value === "string") return `'${value.replaceAll("'", "\\'")}'`;
  if (typeof value === "boolean" || typeof value === "number" || typeof value === "bigint") {
    return String(value);
  }
  if (value === null || value === undefined) return "null";
  return JSON.stringify(value);
}

function isExpression(value: unknown): value is Expression<unknown> {
  return typeof value === "function";
}

const METHOD_OPERATORS = new Map<string, string>([
  ["equals", "=="],
  ["notEquals", "!="],
  ["greaterThan", ">"],
  ["greaterThanOrEqualTo", ">="],
  ["lessThan", "<"],
  ["lessThanOrEqualTo", "<="],
]);
