import { Capability } from "@neuron/sdk";
import type { AuthorizePaymentParams, AuthorizePaymentResult } from "../types";

export const authorizePayment = Capability({
  name: "authorize-payment",
  version: "1.0.0",
  description: "Authorize payment for the order",
})
  .runtime({ name: "neuron:core:set" })
  .paramsSchema<AuthorizePaymentParams>()
  .resultSchema<AuthorizePaymentResult>();
