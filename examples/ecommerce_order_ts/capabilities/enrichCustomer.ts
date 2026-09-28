import { Capability } from "@neuron/sdk";
import type { EnrichCustomerInput, EnrichCustomerOutput } from "../types";

export const enrichCustomer = Capability({
  name: "enrich-customer",
  version: "1.0.0",
  description: "Enrich with customer data",
})
  .capabilityRuntime({ name: "neuron:core:set" })
  .paramsSchema<EnrichCustomerInput>()
  .resultSchema<EnrichCustomerOutput>();
