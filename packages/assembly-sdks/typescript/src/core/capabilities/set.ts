import {Capability} from "@/capability";

export const setCapability = Capability('neuron:core:set')
    .paramsSchema()
    .resultSchema<{value: any}>()
    .runtime({
        name: 'neuron:core:set',
        version: '1.0.0',
    })