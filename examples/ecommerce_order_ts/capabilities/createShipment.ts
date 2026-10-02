import { Capability } from "@neuron/sdk";
import type { CreateShipmentInput, CreateShipmentOutput } from "../types";

export const createShipment = Capability({
  name: "create-shipment",
  version: "1.0.0",
  description: "Create a shipment for the order",
})
  .runtime({ name: "neuron:core:set" })
  .paramsSchema<CreateShipmentInput>()
  .resultSchema<CreateShipmentOutput>();
