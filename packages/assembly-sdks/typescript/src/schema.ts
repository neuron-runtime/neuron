declare const schemaBrand: unique symbol;

export interface FieldRules {
  type: string;
  required?: boolean;
  minLength?: number;
  maxLength?: number;
  pattern?: string;
  format?: string;
  minimum?: number;
  maximum?: number;
  exclusiveMinimum?: number;
  exclusiveMaximum?: number;
  multipleOf?: number;
  minItems?: number;
  maxItems?: number;
  uniqueItems?: boolean;
  properties?: Record<string, FieldRules>;
  items?: FieldRules;
  [key: string]: unknown;
}

export interface SchemaField<T = unknown, TRequired extends boolean = false> {
  readonly [schemaBrand]: T;
  readonly __type: T;
  readonly __required: TRequired;
  readonly __rules: FieldRules;
}

export type SchemaObject = Record<string, SchemaField<unknown, boolean>>;

type RequiredSchemaKeys<S extends SchemaObject> = {
  [K in keyof S]-?: S[K] extends SchemaField<unknown, true> ? K : never;
}[keyof S];

type OptionalSchemaKeys<S extends SchemaObject> = Exclude<keyof S, RequiredSchemaKeys<S>>;

export type InferSchema<S extends SchemaObject> = {
  [K in RequiredSchemaKeys<S>]: S[K] extends SchemaField<infer T, boolean> ? T : never;
} & {
  [K in OptionalSchemaKeys<S>]?: S[K] extends SchemaField<infer T, boolean> ? T : never;
};

export type Schema<T extends object> = {
  [K in keyof T]-?: SchemaField<Exclude<T[K], undefined>, undefined extends T[K] ? false : true>;
};

export type Infer<S extends SchemaObject> = InferSchema<S>;

function makeField<T, TRequired extends boolean>(rules: FieldRules): SchemaField<T, TRequired> {
  return {
    __type: undefined as T,
    __required: Boolean(rules.required) as TRequired,
    __rules: rules,
  } as SchemaField<T, TRequired>;
}

export interface StringField<TRequired extends boolean = false> extends SchemaField<string, TRequired> {
  minimumLength(length: number): StringField<TRequired>;
  maximumLength(length: number): StringField<TRequired>;
  matches(regex: string | RegExp): StringField<TRequired>;
  email(): StringField<TRequired>;
  uuid(): StringField<TRequired>;
  required(): StringField<true>;
  min(length: number): StringField<TRequired>;
  max(length: number): StringField<TRequired>;
  pattern(regex: string | RegExp): StringField<TRequired>;
}

export function string(): StringField {
  const rules: FieldRules = { type: "string" };
  const field = {
    ...makeField<string, false>(rules),
    minimumLength(length: number) {
      rules.minLength = length;
      return field;
    },
    maximumLength(length: number) {
      rules.maxLength = length;
      return field;
    },
    matches(regex: string | RegExp) {
      rules.pattern = regex instanceof RegExp ? regex.source : regex;
      return field;
    },
    email() {
      rules.format = "email";
      return field;
    },
    uuid() {
      rules.format = "uuid";
      return field;
    },
    required() {
      rules.required = true;
      return field as unknown as StringField<true>;
    },
    min(length: number) {
      return field.minimumLength(length);
    },
    max(length: number) {
      return field.maximumLength(length);
    },
    pattern(regex: string | RegExp) {
      return field.matches(regex);
    },
  } as StringField;
  return field;
}

export interface NumberField<TRequired extends boolean = false> extends SchemaField<number, TRequired> {
  minimum(value: number): NumberField<TRequired>;
  maximum(value: number): NumberField<TRequired>;
  exclusiveMinimum(value: number): NumberField<TRequired>;
  exclusiveMaximum(value: number): NumberField<TRequired>;
  integer(): NumberField<TRequired>;
  required(): NumberField<true>;
  min(value: number): NumberField<TRequired>;
  max(value: number): NumberField<TRequired>;
  exclusiveMin(value: number): NumberField<TRequired>;
  exclusiveMax(value: number): NumberField<TRequired>;
}

export function number(): NumberField {
  const rules: FieldRules = { type: "number" };
  const field = {
    ...makeField<number, false>(rules),
    minimum(value: number) {
      rules.minimum = value;
      return field;
    },
    maximum(value: number) {
      rules.maximum = value;
      return field;
    },
    exclusiveMinimum(value: number) {
      rules.exclusiveMinimum = value;
      return field;
    },
    exclusiveMaximum(value: number) {
      rules.exclusiveMaximum = value;
      return field;
    },
    integer() {
      rules.multipleOf = 1;
      return field;
    },
    required() {
      rules.required = true;
      return field as unknown as NumberField<true>;
    },
    min(value: number) {
      return field.minimum(value);
    },
    max(value: number) {
      return field.maximum(value);
    },
    exclusiveMin(value: number) {
      return field.exclusiveMinimum(value);
    },
    exclusiveMax(value: number) {
      return field.exclusiveMaximum(value);
    },
  } as NumberField;
  return field;
}

export interface BooleanField<TRequired extends boolean = false> extends SchemaField<boolean, TRequired> {
  required(): BooleanField<true>;
}

export function boolean(): BooleanField {
  const rules: FieldRules = { type: "boolean" };
  const field = {
    ...makeField<boolean, false>(rules),
    required() {
      rules.required = true;
      return field as unknown as BooleanField<true>;
    },
  } as BooleanField;
  return field;
}

export interface ListField<T = unknown, TRequired extends boolean = false> extends SchemaField<T[], TRequired> {
  minimumItems(count: number): ListField<T, TRequired>;
  maximumItems(count: number): ListField<T, TRequired>;
  uniqueItems(): ListField<T, TRequired>;
  of<U>(item: SchemaField<U, boolean>): ListField<U, TRequired>;
  required(): ListField<T, true>;
  minItems(count: number): ListField<T, TRequired>;
  maxItems(count: number): ListField<T, TRequired>;
}

export function list<T = unknown>(item?: SchemaField<T, boolean>): ListField<T> {
  const rules: FieldRules = { type: "array" };
  if (item) rules.items = item.__rules;
  const field = {
    ...makeField<T[], false>(rules),
    minimumItems(count: number) {
      rules.minItems = count;
      return field;
    },
    maximumItems(count: number) {
      rules.maxItems = count;
      return field;
    },
    uniqueItems() {
      rules.uniqueItems = true;
      return field;
    },
    of<U>(nextItem: SchemaField<U, boolean>) {
      rules.items = nextItem.__rules;
      return field as unknown as ListField<U>;
    },
    required() {
      rules.required = true;
      return field as unknown as ListField<T, true>;
    },
    minItems(count: number) {
      return field.minimumItems(count);
    },
    maxItems(count: number) {
      return field.maximumItems(count);
    },
  } as ListField<T>;
  return field;
}

export interface RecordField<T extends object = Record<string, unknown>, TRequired extends boolean = false>
  extends SchemaField<T, TRequired> {
  required(): RecordField<T, true>;
}

export function record<T extends object = Record<string, unknown>>(): RecordField<T> {
  const rules: FieldRules = { type: "object" };
  const field = {
    ...makeField<T, false>(rules),
    required() {
      rules.required = true;
      return field as unknown as RecordField<T, true>;
    },
  } as RecordField<T>;
  return field;
}

export function schemaToManifest(schema: SchemaObject): {
  ports: Array<{
    name: string;
    type: "any" | "string" | "number" | "boolean" | "object" | "array";
    required: boolean;
    rules?: FieldRules;
  }>;
  rules: Record<string, FieldRules>;
} {
  const ports: Array<{
    name: string;
    type: "any" | "string" | "number" | "boolean" | "object" | "array";
    required: boolean;
    rules?: FieldRules;
  }> = [];
  const rules: Record<string, FieldRules> = {};

  for (const [name, field] of Object.entries(schema)) {
    const fieldRules = field.__rules;
    const type = normalizeType(fieldRules.type);
    ports.push({
      name,
      type,
      required: Boolean(fieldRules.required),
      rules: fieldRules,
    });
    rules[name] = fieldRules;
  }

  return { ports, rules };
}

function normalizeType(type: string): "any" | "string" | "number" | "boolean" | "object" | "array" {
  if (type === "string" || type === "number" || type === "boolean" || type === "object" || type === "array") {
    return type;
  }
  return "any";
}
