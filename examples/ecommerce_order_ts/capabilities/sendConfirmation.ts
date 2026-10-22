import { Capability, record, string, number } from "@neuronruntime/sdk";
import type { SendConfirmationParams, SendConfirmationResult } from "../types";

// The runtime form of resultSchema declares the result ports the assembly's
// `.result({...})` contract maps from. `neuron:core:set` echoes these params,
// so the ports mirror SendConfirmationResult.
export const sendConfirmation = Capability("send-confirmation")
  .runtime({ name: "neuron:core:set" })
  .paramsSchema<SendConfirmationParams>()
  .resultSchema({
    order: record<SendConfirmationResult["order"]>(),
    email: string(),
    grandTotal: number(),
  });
