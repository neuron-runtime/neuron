export { Capability, CapabilityDefinition, CompositionNode, Composition, type CapabilityComposition } from "./capability.js";
export { Assembly, AssemblyDefinition } from "./assembly.js";
export { connect } from "./connection.js";

export {
  string,
  number,
  boolean,
  list,
  record,
} from "./schema.js";
export type {
  Schema,
  SchemaField,
  Infer,
  InferSchema,
  SchemaObject,
  StringField,
  NumberField,
  BooleanField,
  ListField,
  RecordField,
  FieldRules,
} from "./schema.js";

export type {
  Expression,
  Expressionify,
  ExpressionArray,
  SourceContext,
  ExecutionContext,
} from "./expression.js";

export type {
  Connection,
  ExecutionConfig,
  ParamBindings,
  ParamValue,
  CapabilityReference,
} from "./capability.js";

export type {
  AssemblyManifest,
  CapabilityManifest,
  BindingManifest,
  BindingMappingManifest,
  BindingValidationManifest,
  PortManifest,
} from "./manifest.js";