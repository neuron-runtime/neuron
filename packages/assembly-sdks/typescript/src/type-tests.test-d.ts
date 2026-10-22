import { Capability, Assembly, connect, string, type Expression } from "./index.js";

interface GitHubReadParams {
    owner: string;
    repository: string;
    path: string;
    branch?: string;
}

interface GitHubReadResult {
    content: string;
    path: string;
    sha: string;
    metadata: {
        size: number;
    };
}

interface AnalyzeParams {
    content: string;
    path: string;
}

interface AnalyzeResult {
    summary: string;
}

const githubRead = Capability("github.read")
    .paramsSchema<GitHubReadParams>()
    .resultSchema<GitHubReadResult>();

const analyzeContent = Capability("analyze.content")
    .paramsSchema<AnalyzeParams>()
    .resultSchema<AnalyzeResult>();

githubRead.result.content satisfies Expression<string>;
githubRead.result.path satisfies Expression<string>;
githubRead.result.sha satisfies Expression<string>;
githubRead.result.metadata.size satisfies Expression<number>;

// @ts-expect-error unknown result fields must fail in the IDE.
githubRead.result.fileData;

githubRead.withParams({
    owner: "neuron-runtime",
    repository: "neuron",
    path: "README.md",
});

// @ts-expect-error required params field path is missing.
githubRead.withParams({
    owner: "neuron-runtime",
    repository: "neuron",
});

analyzeContent.withParams({
    content: githubRead.result.content,
    path: githubRead.result.path,
});

analyzeContent.withParams({
    // @ts-expect-error number expressions cannot bind to string params.
    content: githubRead.result.metadata.size,
    path: githubRead.result.path,
});

// @ts-expect-error sha is a string but the required params field path is missing.
analyzeContent.withParams({
    content: githubRead.result.sha,
});

analyzeContent.connect<GitHubReadResult>((source) => ({
    content: source.result.content,
    path: source.result.path,
}));

analyzeContent.connect<GitHubReadResult>((source) => ({
    // @ts-expect-error source result has no fileData field.
    content: source.result.fileData,
    path: source.result.path,
}));

connect<GitHubReadResult, AnalyzeParams>((source) => ({
    content: source.result.content,
    path: source.result.path,
}));

const fromRuntimeSchema = Capability("runtime")
    .paramsSchema({
        requiredName: string().required(),
        optionalName: string(),
    });

fromRuntimeSchema.withParams({ requiredName: "ok" });

// @ts-expect-error runtime schema inference preserves required fields.
fromRuntimeSchema.withParams({ optionalName: "ok" });

Assembly()
    .paramsSchema<GitHubReadParams>()
    .withParams((data) =>
        githubRead.withParams({
            owner: data.owner,
            repository: data.repository,
            path: data.path,
        })
    );

// ---------------------------------------------------------------------------
// Assembly result contract
// ---------------------------------------------------------------------------

interface AssemblyResult {
    summary: string;
    sha: string;
}

Assembly()
    .paramsSchema<GitHubReadParams>()
    .resultSchema<AssemblyResult>()
    .withParams((data) =>
        githubRead.withParams({
            owner: data.owner,
            repository: data.repository,
            path: data.path,
        })
    )
    .result({
        summary: analyzeContent.result.summary,
        sha: githubRead.result.sha,
    });

// The type-only resultSchema form and the schema form both accept `.result()`.
Assembly().resultSchema({ summary: string(), sha: string() });
// ---------------------------------------------------------------------------
// Runtime declaration and runtime configuration
// ---------------------------------------------------------------------------

Capability("minimal").runtime({ name: "neuron:core:set" });

Capability("full").runtime({
    name: "acme:http",
    version: "^1.2.0",
    registry: "github",
    runtimeConfig: {
        execution: { mode: "wait", timeout: "5s" },
        retry: { policy: "exponential", maxAttempts: 3, initialBackoff: "100ms", maxBackoff: "2s" },
    },
});

// Every runtimeConfig group is optional; declaring none is the normal case.
Capability("no-groups").runtime({ name: "acme:http", runtimeConfig: {} });

// @ts-expect-error the runtime name is required.
Capability("no-name").runtime({ version: "1.0.0" });

// @ts-expect-error execution mode is a closed set of wait and detach.
Capability("bad-mode").runtime({ name: "acme:http", runtimeConfig: { execution: { mode: "detatch" } } });

// @ts-expect-error retry policy is a closed set.
Capability("bad-policy").runtime({ name: "acme:http", runtimeConfig: { retry: { policy: "linear" } } });

// @ts-expect-error runtimeConfig is grouped, not a flat config bag.
Capability("flat").runtime({ name: "acme:http", runtimeConfig: { timeout: "5s", retries: 2 } });

// @ts-expect-error the removed capability-level builder is gone.
Capability("legacy").runtimeConfig({ timeout: "5s" });

// @ts-expect-error the runtime declaration builder replaced capabilityRuntime().
Capability("legacy-decl").capabilityRuntime({ name: "acme:http" });

// Two capabilities may declare the same runtime with different configurations.
const sharedRuntime = { name: "acme:http", version: "1.2.0", registry: "github" } as const;

Capability("sync-call").runtime({ ...sharedRuntime, runtimeConfig: { execution: { mode: "wait" } } });
Capability("fire-and-forget").runtime({ ...sharedRuntime, runtimeConfig: { execution: { mode: "detach" } } });

// ---------------------------------------------------------------------------
// Built-in capability runtimes
// ---------------------------------------------------------------------------

import { set, http, command, filesystem } from "./core.js";

// The built-ins are declared with the name the author gives them, and they run
// through the built-in runtime.
const setValues = set("values");
const httpCall = http("call");
const commandRun = command("run");
const filesystemRead = filesystem("read");

setValues.withParams({ orderId: "o-1" });
httpCall.withParams({ url: "https://example.com/api" });
commandRun.withParams({ command: "git", args: ["status"] });
filesystemRead.withParams({ operation: "read", path: "data.json" });

// @ts-expect-error the http capability needs a url.
httpCall.withParams({ method: "GET" });

// @ts-expect-error the command capability needs the program to run.
commandRun.withParams({ args: ["status"] });

// @ts-expect-error the filesystem capability needs an operation and a path.
filesystemRead.withParams({ operation: "read" });

// The results are typed.
httpCall.result.status_code satisfies Expression<number>;
httpCall.result.content_type satisfies Expression<string>;
commandRun.result.exit_code satisfies Expression<number>;
commandRun.result.timed_out satisfies Expression<boolean>;
filesystemRead.result.exists satisfies Expression<boolean | undefined>;

// The body of a response can be narrowed by the author.
const typedCall = http<{ id: string }>("typed-call");
typedCall.result.body satisfies { readonly id: Expression<string> } | Expression<string>;
