import { Capability } from "@neuron/sdk";
import type { SendConfirmationParams, SendConfirmationResult } from "../types";

export const sendConfirmation = Capability({
  name: "send-confirmation",
  version: "1.0.0",
  description: "Send order confirmation email",
})
  .runtime({ name: "neuron:core:set" })
  .paramsSchema<SendConfirmationParams>()
  .resultSchema<SendConfirmationResult>();
