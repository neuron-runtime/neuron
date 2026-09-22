import {Service, string } from "@neuron/sdk";

export const delay = Service({
    name: "delay",
}).inputSchema({
    duration: string(),
}).outputSchema({
    delayed_for: string()
}).executor({
    name: "neuron:core:delay",
    version: "1.0.0"
})