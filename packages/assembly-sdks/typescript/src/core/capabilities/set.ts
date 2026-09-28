import {Capability} from "@/capability";

export const setCapability = Capability({
    name: 'neuron:core:set',
}).paramsSchema()
.resultSchema()
.capabilityRuntime({
    name: 'neuron:core:set',
    version: '1.0.0',
})