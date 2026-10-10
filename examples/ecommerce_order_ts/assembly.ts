import { Assembly } from "@neuron/sdk";
import { buildPipeline } from "./pipeline.js";
import type { AssemblyParams } from "./types.js";

const manifest = Assembly()
  .paramsSchema<AssemblyParams>()
  .withParams((data) => buildPipeline(data))
  .toManifest();

export default manifest;
