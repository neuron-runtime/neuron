import { Capability } from "@neuron/sdk";
import type { SendConfirmationParams, SendConfirmationResult } from "../types";

export const sendConfirmation = Capability("send-confirmation")
  .runtime({ name: "neuron:core:set" })
  .paramsSchema<SendConfirmationParams>()
  .resultSchema<SendConfirmationResult>();
