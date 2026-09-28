import { Capability } from "@neuron/sdk";
import type { ParseOrderInput, ParseOrderOutput } from "../types";

export const parseOrder = Capability({
  name: "parse-order",
  version: "1.0.0",
  description: "Parse and normalize order data",
})
  .capabilityRuntime({ name: "neuron:core:set" })
  .paramsSchema<ParseOrderInput>()
  .resultSchema<ParseOrderOutput>();
