import { Capability } from "@neuron/sdk";
import type { SendConfirmationInput, SendConfirmationOutput } from "../types";

export const sendConfirmation = Capability({
  name: "send-confirmation",
  version: "1.0.0",
  description: "Send order confirmation email",
})
  .capabilityRuntime({ name: "neuron:core:set" })
  .paramsSchema<SendConfirmationInput>()
  .resultSchema<SendConfirmationOutput>();
