import { Capability } from "@neuron/sdk";
import type { ValidateOrderParams, ValidateOrderResult } from "../types";

export const validateOrder = Capability("validate-order")
  .runtime({ name: "neuron:core:set" })
  .paramsSchema<ValidateOrderParams>()
  .resultSchema<ValidateOrderResult>();
