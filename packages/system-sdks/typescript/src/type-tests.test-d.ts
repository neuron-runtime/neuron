import { Service, System, connect, string, type Expression } from "./index.js";

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

const githubRead = Service({ name: "github.read" })
    .inputSchema<GitHubReadInput>()
    .outputSchema<GitHubReadOutput>();

const analyzeContent = Service({ name: "analyze.content" })
    .inputSchema<AnalyzeInput>()
    .outputSchema<AnalyzeOutput>();

githubRead.output.content satisfies Expression<string>;
githubRead.output.path satisfies Expression<string>;
githubRead.output.sha satisfies Expression<string>;
githubRead.output.metadata.size satisfies Expression<number>;

// @ts-expect-error unknown output fields must fail in the IDE.
githubRead.output.fileData;

githubRead.withInput({
    owner: "Muhammad-Jay",
    repository: "neuron",
    path: "README.md",
});

// @ts-expect-error required input path is missing.
githubRead.withInput({
    owner: "Muhammad-Jay",
    repository: "neuron",
});

analyzeContent.withInput({
    content: githubRead.output.content,
    path: githubRead.output.path,
});

analyzeContent.withInput({
    // @ts-expect-error number expressions cannot bind to string inputs.
    content: githubRead.output.metadata.size,
    path: githubRead.output.path,
});

// @ts-expect-error sha is a string but the required path input is missing.
analyzeContent.withInput({
    content: githubRead.output.sha,
});

analyzeContent.connect<GitHubReadOutput>((source) => ({
    content: source.output.content,
    path: source.output.path,
}));

analyzeContent.connect<GitHubReadOutput>((source) => ({
    // @ts-expect-error source output has no fileData field.
    content: source.output.fileData,
    path: source.output.path,
}));

connect<GitHubReadOutput, AnalyzeInput>((source) => ({
    content: source.output.content,
    path: source.output.path,
}));

const fromRuntimeSchema = Service({ name: "runtime" })
    .inputSchema({
        requiredName: string().required(),
        optionalName: string(),
    });

fromRuntimeSchema.withInput({ requiredName: "ok" });

// @ts-expect-error runtime schema inference preserves required fields.
fromRuntimeSchema.withInput({ optionalName: "ok" });

System({ name: "repository-analysis" })
    .inputSchema<GitHubReadInput>()
    .withParams((input) =>
        githubRead.withInput({
            owner: input.owner,
            repository: input.repository,
            path: input.path,
        })
    );
