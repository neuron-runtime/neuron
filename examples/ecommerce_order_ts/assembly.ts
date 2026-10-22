import { Assembly, string, number } from "@neuronruntime/sdk";
import { buildPipeline } from "./pipeline.js";
import { sendConfirmation } from "./capabilities/index.js";
import type { AssemblyParams } from "./types.js";

const assembly = Assembly()
  .paramsSchema<AssemblyParams>()
  .resultSchema({
    orderId: string(),
    status: string(),
    grandTotal: number(),
    email: string(),
  })
  .withParams((data) => buildPipeline(data));

// The result contract demonstrates every supported source kind: the final
// step's payload (echoed back by `neuron:core:set`), the original assembly
// params, and a literal.
const manifest = assembly
  .result({
    orderId: assembly.params.order.id,
    status: "confirmed",
    grandTotal: sendConfirmation.result.grandTotal,
    email: sendConfirmation.result.email,
  })
  .toManifest();

export default manifest;
