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

const githubRead = Capability({ name: "github.read" })
    .paramsSchema<GitHubReadParams>()
    .resultSchema<GitHubReadResult>();

const analyzeContent = Capability({ name: "analyze.content" })
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

const fromRuntimeSchema = Capability({ name: "runtime" })
    .paramsSchema({
        requiredName: string().required(),
        optionalName: string(),
    });

fromRuntimeSchema.withParams({ requiredName: "ok" });

// @ts-expect-error runtime schema inference preserves required fields.
fromRuntimeSchema.withParams({ optionalName: "ok" });

Assembly({ name: "repository-analysis" })
    .paramsSchema<GitHubReadParams>()
    .withParams((data) =>
        githubRead.withParams({
            owner: data.owner,
            repository: data.repository,
            path: data.path,
        })
    );
// ---------------------------------------------------------------------------
// Runtime declaration and runtime configuration
// ---------------------------------------------------------------------------

Capability({ name: "minimal" }).runtime({ name: "neuron:core:set" });

Capability({ name: "full" }).runtime({
    name: "acme:http",
    version: "^1.2.0",
    registry: "github",
    runtimeConfig: {
        execution: { mode: "wait", timeout: "5s" },
        retry: { policy: "exponential", maxAttempts: 3, initialBackoff: "100ms", maxBackoff: "2s" },
    },
});

// Every runtimeConfig group is optional; declaring none is the normal case.
Capability({ name: "no-groups" }).runtime({ name: "acme:http", runtimeConfig: {} });

// @ts-expect-error the runtime name is required.
Capability({ name: "no-name" }).runtime({ version: "1.0.0" });

// @ts-expect-error execution mode is a closed set of wait and detach.
Capability({ name: "bad-mode" }).runtime({ name: "acme:http", runtimeConfig: { execution: { mode: "detatch" } } });

// @ts-expect-error retry policy is a closed set.
Capability({ name: "bad-policy" }).runtime({ name: "acme:http", runtimeConfig: { retry: { policy: "linear" } } });

// @ts-expect-error runtimeConfig is grouped, not a flat config bag.
Capability({ name: "flat" }).runtime({ name: "acme:http", runtimeConfig: { timeout: "5s", retries: 2 } });

// @ts-expect-error the removed capability-level builder is gone.
Capability({ name: "legacy" }).runtimeConfig({ timeout: "5s" });

// @ts-expect-error the runtime declaration builder replaced capabilityRuntime().
Capability({ name: "legacy-decl" }).capabilityRuntime({ name: "acme:http" });

// Two capabilities may declare the same runtime with different configurations.
const sharedRuntime = { name: "acme:http", version: "1.2.0", registry: "github" } as const;

Capability({ name: "sync-call" }).runtime({ ...sharedRuntime, runtimeConfig: { execution: { mode: "wait" } } });
Capability({ name: "fire-and-forget" }).runtime({ ...sharedRuntime, runtimeConfig: { execution: { mode: "detach" } } });
