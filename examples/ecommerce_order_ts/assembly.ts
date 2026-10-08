import { Assembly } from "@neuron/sdk";
import { buildPipeline } from "./pipeline.js";
import type { AssemblyParams } from "./types.js";

const manifest = Assembly({
  name: "order-processing-ts",
  version: "2.0.0",
  description: "Order processing pipeline",
})
  .paramsSchema<AssemblyParams>()
  .withParams((data) => buildPipeline(data))
  .toManifest();

export default manifest;
