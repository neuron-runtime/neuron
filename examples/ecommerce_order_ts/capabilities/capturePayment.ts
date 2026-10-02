import { Capability } from "@neuron/sdk";
import type { CapturePaymentInput, CapturePaymentOutput } from "../types";

export const capturePayment = Capability({
  name: "capture-payment",
  version: "1.0.0",
  description: "Capture an authorized payment",
})
  .runtime({ name: "neuron:core:set" })
  .paramsSchema<CapturePaymentInput>()
  .resultSchema<CapturePaymentOutput>();
