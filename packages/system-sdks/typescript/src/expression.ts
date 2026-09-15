declare const expressionBrand: unique symbol;

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

export interface SourceContext<TOutput extends object> {
  readonly output: Expressionify<TOutput>;
}

export interface ExecutionContext<TInput extends object> {
  readonly input: Expressionify<TInput>;
}

export function createExpressionProxy<T>(path: string): Expressionify<T> {
  const handler: ProxyHandler<(...args: unknown[]) => string> = {
    get(_target, prop) {
      if (prop === Symbol.toPrimitive || prop === "toString" || prop === "valueOf") {
        return () => path;
      }
      if (prop === "then") return undefined;
      if (typeof prop === "string" && METHOD_OPERATORS.has(prop)) {
        return (value: unknown) => comparison(`${path} ${METHOD_OPERATORS.get(prop)} ${serialize(value)}`);
      }
      if (prop === "and") {
        return (other: Expression<boolean>) => comparison(`(${path}) && (${String(other)})`);
      }
      if (prop === "or") {
        return (other: Expression<boolean>) => comparison(`(${path}) || (${String(other)})`);
      }
      return createExpressionProxy<unknown>(`${path}.${String(prop)}`);
    },
    apply() {
      return path;
    },
  };

  return new Proxy(() => path, handler) as unknown as Expressionify<T>;
}

export function createSourceContext<TOutput extends object>(): SourceContext<TOutput> {
  return {
    get output() {
      return createExpressionProxy<TOutput>("source.output");
    },
  };
}

export function createExecutionContext<TInput extends object>(): ExecutionContext<TInput> {
  return {
    get input() {
      return createExpressionProxy<TInput>("execution.input");
    },
  };
}

function comparison(path: string): Expression<boolean> {
  return createExpressionProxy<boolean>(path) as Expression<boolean>;
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

export function expressionToString(value: unknown): string {
  if (isExpression(value)) return String(value);
  return serialize(value);
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
