import { Capability, type CapabilityDefinition } from "../../capability.js";

/**
 * The credentials a request carries.
 *
 * The credentials are applied to the request itself, never to the URL, so they
 * do not end up in a log line or in an error that names the URL.
 */
export type HttpAuthType =
    | { type: "bearer"; token: string }
    | { type: "basic"; username: string; password: string }
    | { type: "apiKey"; name: string; value: string; in?: "header" | "query" };

/**
 * How a failed request is retried.
 *
 * Retries are off by default. A method that is not named in `methods` is never
 * retried, because a request that was not designed to be repeated may not be
 * safe to repeat.
 */
export type HttpRetryType = {
    /** How many attempts are made in total. At least 1. */
    attempts?: number;
    /** How long the runtime waits between attempts, such as "200ms". */
    backoff?: string;
    /** The methods that may be retried. Defaults to the idempotent methods. */
    methods?: string[];
};

export type HttpParamsType = {
    /** The absolute URL to request. */
    url: string;
    /** The HTTP method. Defaults to GET. */
    method?: string;
    /** Request headers. */
    headers?: Record<string, string>;
    /** Query string parameters, appended to the URL. */
    query?: Record<string, string>;
    /** The request body, encoded as JSON. */
    body?: unknown;
    /** The request content type. Only used when a body is present. */
    contentType?: string;
    /** Credentials for the request. */
    auth?: HttpAuthType;
    /** How long the request may take, such as "30s". */
    timeout?: string;
    /** How the response body is represented: "json" or "text". */
    format?: "json" | "text";
    /** Whether redirects are followed. Each hop is checked against the address policy. */
    followRedirects?: boolean;
    /** Retry behavior. Off by default. */
    retry?: HttpRetryType;
};

export type HttpResultType<TBody = unknown> = {
    /** The HTTP response status code. */
    status_code: number;
    /** The response headers. */
    headers: Record<string, string>;
    /** The response body: parsed JSON in the json format, a plain string in the text format. */
    body: TBody | string;
    /** The response content type. */
    content_type: string;
    /** How long the request took, in milliseconds. */
    duration_ms: number;
};

/**
 * Declares a capability that performs a single HTTP request.
 *
 * The request is bounded by the runtime's policy: it does not reach a host that
 * is not publicly routable unless the project permits it, it does not follow
 * redirects unless asked to, and it does not retry unless asked to.
 *
 * @param name the capability's logical identity in the assembly.
 */
export function http<TBody = unknown>(
    name: string
): CapabilityDefinition<HttpParamsType, HttpResultType<TBody>> {
    return Capability(name)
        .runtime({ name: "neuron:core:http" })
        .paramsSchema<HttpParamsType>()
        .resultSchema<HttpResultType<TBody>>();
}
