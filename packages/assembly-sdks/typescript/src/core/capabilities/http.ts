import {Capability} from "@/capability";

export type HttpInputType = {
    url: string;
    method: 'GET' | 'POST' | 'PUT' | 'DELETE';
    headers?: Record<string, any>;
    body?: Record<string, any>;
}

export type HttpOutputType<T = unknown> = {
    status_code: number;
    body?: T;
}

export const http = Capability({
    name: "neuron:core:http"
}).paramsSchema<HttpInputType>()
.resultSchema<HttpOutputType>()
.runtime({
    name: 'neuron:core:http',
    version: '1.0.0',
})