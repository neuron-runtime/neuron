import { Capability } from "@neuronruntime/sdk";
import type { AuthorizePaymentParams, AuthorizePaymentResult } from "../types";

export const authorizePayment = Capability("authorize-payment")
  .runtime({ name: "neuron:core:set" })
  .paramsSchema<AuthorizePaymentParams>()
  .resultSchema<AuthorizePaymentResult>();
