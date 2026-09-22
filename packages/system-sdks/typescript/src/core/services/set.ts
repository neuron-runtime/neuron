import {Service} from "@/service";

export const setService = Service({
    name: 'set',
}).inputSchema()
.outputSchema()
.executor({
    name: 'neuron:core:set',
    version: '1.0.0',
})