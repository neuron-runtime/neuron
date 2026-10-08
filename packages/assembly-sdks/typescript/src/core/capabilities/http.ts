import {Capability} from "@/capability";

export type HttpParamsType = {
    url: string;
    method: 'GET' | 'POST' | 'PUT' | 'DELETE';
    headers?: Record<string, any>;
    body?: Record<string, any>;
}

export type HttpResultType<T = unknown> = {
    status_code: number;
    body?: T;
}

export const http = Capability({
    name: "neuron:core:http"
}).paramsSchema<HttpParamsType>()
.resultSchema<HttpResultType>()
.runtime({
    name: 'neuron:core:http',
    version: '1.0.0',
})