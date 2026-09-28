import { Capability, Assembly, connect, string, type Expression } from "./index.js";

interface GitHubReadInput {
    owner: string;
    repository: string;
    path: string;
    branch?: string;
}

interface GitHubReadOutput {
    content: string;
    path: string;
    sha: string;
    metadata: {
        size: number;
    };
}

interface AnalyzeInput {
    content: string;
    path: string;
}

interface AnalyzeOutput {
    summary: string;
}

const githubRead = Capability({ name: "github.read" })
    .paramsSchema<GitHubReadInput>()
    .resultSchema<GitHubReadOutput>();

const analyzeContent = Capability({ name: "analyze.content" })
    .paramsSchema<AnalyzeInput>()
    .resultSchema<AnalyzeOutput>();

githubRead.result.content satisfies Expression<string>;
githubRead.result.path satisfies Expression<string>;
githubRead.result.sha satisfies Expression<string>;
githubRead.result.metadata.size satisfies Expression<number>;

// @ts-expect-error unknown result fields must fail in the IDE.
githubRead.result.fileData;

githubRead.withParams({
    owner: "Muhammad-Jay",
    repository: "neuron",
    path: "README.md",
});

// @ts-expect-error required input path is missing.
githubRead.withParams({
    owner: "Muhammad-Jay",
    repository: "neuron",
});

analyzeContent.withParams({
    content: githubRead.result.content,
    path: githubRead.result.path,
});

analyzeContent.withParams({
    // @ts-expect-error number expressions cannot bind to string inputs.
    content: githubRead.result.metadata.size,
    path: githubRead.result.path,
});

// @ts-expect-error sha is a string but the required path input is missing.
analyzeContent.withParams({
    content: githubRead.result.sha,
});

analyzeContent.connect<GitHubReadOutput>((source) => ({
    content: source.result.content,
    path: source.result.path,
}));

analyzeContent.connect<GitHubReadOutput>((source) => ({
    // @ts-expect-error source result has no fileData field.
    content: source.result.fileData,
    path: source.result.path,
}));

connect<GitHubReadOutput, AnalyzeInput>((source) => ({
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
    .paramsSchema<GitHubReadInput>()
    .withParams((data) =>
        githubRead.withParams({
            owner: data.owner,
            repository: data.repository,
            path: data.path,
        })
    );