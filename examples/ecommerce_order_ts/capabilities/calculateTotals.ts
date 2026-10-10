import { Capability } from "@neuron/sdk";
import type { CalculateTotalsParams, CalculateTotalsResult } from "../types";

export const calculateTotals = Capability("calculate-totals")
  .runtime({ name: "neuron:core:set" })
  .paramsSchema<CalculateTotalsParams>()
  .resultSchema<CalculateTotalsResult>();
