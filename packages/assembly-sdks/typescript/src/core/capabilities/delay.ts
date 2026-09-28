import {Capability} from "@/capability";
import {string} from "@/schema";

export const delay = Capability({
    name: "neuron:core:delay",
}).paramsSchema({
    duration: string(),
}).resultSchema({
    delayed_for: string()
}).capabilityRuntime({
    name: "neuron:core:delay",
    version: "1.0.0"
})