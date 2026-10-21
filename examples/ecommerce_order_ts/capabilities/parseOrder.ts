import { Capability } from "@neuronruntime/sdk";
import type { ParseOrderParams, ParseOrderResult } from "../types";

export const parseOrder = Capability("parse-order")
  .runtime({ name: "neuron:core:set" })
  .paramsSchema<ParseOrderParams>()
  .resultSchema<ParseOrderResult>();
