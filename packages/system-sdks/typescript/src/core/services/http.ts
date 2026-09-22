import {Service} from "@/service";

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

export const http = Service({
    name: "http service"
}).inputSchema<HttpInputType>()
.outputSchema<HttpOutputType>()
.executor({
    name: 'neuron:core:http',
    version: '1.0.0',
})