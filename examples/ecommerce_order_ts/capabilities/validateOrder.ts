import { Capability } from "@neuron/sdk";
import type { ValidateOrderInput, ValidateOrderOutput } from "../types";

export const validateOrder = Capability({
  name: "validate-order",
  version: "1.0.0",
  description: "Validate incoming order request",
})
  .runtime({ name: "neuron:core:set" })
  .paramsSchema<ValidateOrderInput>()
  .resultSchema<ValidateOrderOutput>();
