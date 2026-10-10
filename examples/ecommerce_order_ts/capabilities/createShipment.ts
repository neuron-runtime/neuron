import { Capability } from "@neuron/sdk";
import type { CreateShipmentParams, CreateShipmentResult } from "../types";

export const createShipment = Capability("create-shipment")
  .runtime({ name: "neuron:core:set" })
  .paramsSchema<CreateShipmentParams>()
  .resultSchema<CreateShipmentResult>();
