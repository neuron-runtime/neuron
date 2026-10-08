import { Capability } from "@neuron/sdk";
import type { CapturePaymentParams, CapturePaymentResult } from "../types";

export const capturePayment = Capability({
  name: "capture-payment",
  version: "1.0.0",
  description: "Capture an authorized payment",
})
  .runtime({ name: "neuron:core:set" })
  .paramsSchema<CapturePaymentParams>()
  .resultSchema<CapturePaymentResult>();
