import { Capability } from "@neuron/sdk";
import type { EnrichCustomerParams, EnrichCustomerResult } from "../types";

export const enrichCustomer = Capability({
  name: "enrich-customer",
  version: "1.0.0",
  description: "Enrich with customer data",
})
  .runtime({ name: "neuron:core:set" })
  .paramsSchema<EnrichCustomerParams>()
  .resultSchema<EnrichCustomerResult>();
