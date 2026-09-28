import { Capability } from "@neuron/sdk";
import type { AuthorizePaymentInput, AuthorizePaymentOutput } from "../types";

export const authorizePayment = Capability({
  name: "authorize-payment",
  version: "1.0.0",
  description: "Authorize payment for the order",
})
  .capabilityRuntime({ name: "neuron:core:set" })
  .paramsSchema<AuthorizePaymentInput>()
  .resultSchema<AuthorizePaymentOutput>();
