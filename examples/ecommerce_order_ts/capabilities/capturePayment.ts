import { Capability } from "@neuron/sdk";
import type { CapturePaymentParams, CapturePaymentResult } from "../types";

export const capturePayment = Capability("capture-payment")
  .runtime({ name: "neuron:core:set" })
  .paramsSchema<CapturePaymentParams>()
  .resultSchema<CapturePaymentResult>();
