import { Capability } from "@neuron/sdk";
import type { EnrichCustomerParams, EnrichCustomerResult } from "../types";

export const enrichCustomer = Capability("enrich-customer")
  .runtime({ name: "neuron:core:set" })
  .paramsSchema<EnrichCustomerParams>()
  .resultSchema<EnrichCustomerResult>();
