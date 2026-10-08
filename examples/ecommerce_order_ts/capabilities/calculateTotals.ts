import { Capability } from "@neuron/sdk";
import type { CalculateTotalsParams, CalculateTotalsResult } from "../types";

export const calculateTotals = Capability({
  name: "calculate-totals",
  version: "1.0.0",
  description: "Calculate order totals with tax and discounts",
})
  .runtime({ name: "neuron:core:set" })
  .paramsSchema<CalculateTotalsParams>()
  .resultSchema<CalculateTotalsResult>();
