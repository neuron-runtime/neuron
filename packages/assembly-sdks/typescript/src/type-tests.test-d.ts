import { Capability, Assembly, connect, number, string, type Expression } from "./index.js";

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

const booleanCapability = Capability("boolean-result").resultSchema<{ active: boolean }>();
  booleanCapability.result.active.and(booleanCapability.result.active);
  // @ts-expect-error boolean expressions cannot be ordered.
  booleanCapability.result.active.greaterThan(true);
  // @ts-expect-error string expressions do not support ordered comparison.
  githubRead.result.content.greaterThan("abc");
  githubRead.result.metadata.size.greaterThan(10);

// ---------------------------------------------------------------------------
// Fan-in: multiple sources binding to the same target capability field
// ---------------------------------------------------------------------------

// Fan-in with different target paths (each source maps to a distinct field).
const targetDiff = Capability("target-diff").paramsSchema({ x: string(), y: string() });
const aDiff = Capability("a-diff").resultSchema({ x: string() });
const bDiff = Capability("b-diff").resultSchema({ y: string() });
// Each source maps to a different target path — this has always been supported.
githubRead.result.content satisfies Expression<string>;
githubRead.result.path satisfies Expression<string>;

// Fan-in with same target path: multiple sources contributing to one field.
// This is now allowed — R3-10 duplicate-target rejection removed.
// The withParams calls below should type-check successfully now that R3-10 is removed.
const targetSame = Capability("target-same").paramsSchema({ count: number() });
const aSame = Capability("a-same").resultSchema({ value: number().required() });
const bSame = Capability("b-same").resultSchema({ value: number().required() });
// Both a and b contribute to the same target field `count` — now permitted.
targetSame.withParams({ count: aSame.result.value });
targetSame.withParams({ count: bSame.result.value });

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

const typedAssemblyResult = Assembly().resultSchema<AssemblyResult>();
typedAssemblyResult.result({ summary: "ok", sha: "abc" });

// @ts-expect-error required assembly result fields cannot be omitted.
typedAssemblyResult.result({ summary: "ok" });

// @ts-expect-error assembly result fields must use the declared value types.
typedAssemblyResult.result({ summary: 42, sha: "abc" });

// @ts-expect-error undeclared assembly result fields are rejected.
typedAssemblyResult.result({ summary: "ok", sha: "abc", extra: true });

type UndefinedValueResult = { requiredValue: string | undefined; optionalValue?: string };
const resultWithUndefined = Assembly().resultSchema<UndefinedValueResult>();
resultWithUndefined.result({ requiredValue: "present" });

// @ts-expect-error a required property remains required even if its value includes undefined.
resultWithUndefined.result({ optionalValue: "optional" });

// @ts-expect-error undefined is not a valid mapped result value.
resultWithUndefined.result({ requiredValue: undefined });

const paramsWithUndefined = Capability("params-with-undefined").paramsSchema<{
    requiredValue: string | undefined;
}>();

// @ts-expect-error a required capability parameter cannot be omitted.
paramsWithUndefined.withParams({});
paramsWithUndefined.withParams({ requiredValue: "present" });

// @ts-expect-error undefined is not a valid capability parameter binding.
paramsWithUndefined.withParams({ requiredValue: undefined });

const runtimeAssemblyResult = Assembly().resultSchema({
    name: string().required(),
    count: number().required(),
});
runtimeAssemblyResult.result({ name: "sample", count: 1 });

// @ts-expect-error runtime schema field values are type checked.
runtimeAssemblyResult.result({ name: "sample", count: "one" });

// @ts-expect-error an Assembly must declare resultSchema before result.
Assembly().result({ summary: "ok" });

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
