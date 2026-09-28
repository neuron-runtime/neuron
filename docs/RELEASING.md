# Releasing

This document describes how Neuron's distributable artifacts are released. Each
artifact has its own version line and its own tag prefix, so the layers can
move independently without overwriting each other's releases.

Everything is published through **GitHub Actions with OIDC federation**. No
long-lived registry tokens or API keys are stored in this repository. The only
one-time setup is registering a *trusted publisher* on the registry side.

---

## Release lines at a glance

| Artifact | Registry | Tag | Workflow | Credentials |
| --- | --- | --- | --- | --- |
| `neuron` CLI + N.O.R.E. binaries | GitHub Releases | `v*` | `.github/workflows/release.yml` | `GITHUB_TOKEN` (built in) |
| `@neuron/sdk` | npm | `sdk-v*` | `.github/workflows/publish-sdk.yml` | npm trusted publishing (OIDC) |
| `Neuron.Executor` | NuGet | `dotnet-v*` | `.github/workflows/publish-executor-dotnet.yml` | NuGet trusted publishing (OIDC) |
| `shared` Go module | Go module proxy | `shared/v*` | `.github/workflows/publish-go-sdk.yml` | none (tags only) |
| `packages/executor-sdks/golang` | Go module proxy | `packages/executor-sdks/golang/v*` | `.github/workflows/publish-go-sdk.yml` | none (tags only) |

The binary release (`v*`) is handled by the existing release workflow and is not
covered further here. CI (`ci.yml`) validates Go, the TypeScript SDK, and the
release build matrix on every push and pull request.

---

## What you must provide

### npm — `@neuron/sdk`

npm authenticates the workflow with a short-lived OIDC token, but it must know
which workflow is allowed to publish the package.

One-time setup on [npmjs.com](https://www.npmjs.com):

1. Open the `@neuron/sdk` package → **Settings** → **Trusted Publisher** → **GitHub Actions**.
2. Fill in:
   - **Organization:** `Muhammad-Jay`
   - **Repository:** `neuron`
   - **Workflow name:** `publish-sdk.yml` (must match the file name exactly)
   - **Environment:** leave empty unless the workflow uses a protected environment
3. Save.

No `NPM_TOKEN` secret is needed. `publish-sdk.yml` declares
`permissions: id-token: write`, upgrades npm to a version that supports trusted
publishing (`>= 11.5.1`), and runs `npm publish --provenance`.

**Release:**

```bash
git tag sdk-v0.1.0
git push origin sdk-v0.1.0
```

The tag version replaces the version in `package.json` before publishing; the
tag is the source of truth.

### NuGet — `Neuron.Executor`

One-time setup:

1. On [nuget.org](https://www.nuget.org), create a **Trusted Publishing** policy:
   **Account → Trusted Publishing → Add policy**.
   - **Publisher:** GitHub Actions
   - **Repository owner:** `Muhammad-Jay`
   - **Repository:** `neuron`
   - **Workflow file:** `publish-executor-dotnet.yml` (must match exactly)
   - **Environment:** leave empty unless the workflow uses a protected environment
2. Add a repository **variable** named `NUGET_USER` with your nuget.org
   username. This is an identifier, not a credential — set it under
   **Settings → Secrets and variables → Actions → Variables**, not as a secret.

`publish-executor-dotnet.yml` exchanges the GitHub OIDC token for a short-lived
NuGet API key via `NuGet/login@v1`; nothing long-lived is stored.

**Release:**

```bash
git tag dotnet-v0.1.0
git push origin dotnet-v0.1.0
```

The tag version is passed to `dotnet pack`/`dotnet build` as `-p:Version`.

### Go modules — `shared` and `packages/executor-sdks/golang`

Go modules have no upload step. A module version is published by pushing a git
tag whose prefix matches the module path. No credentials are required.

```bash
git tag shared/v0.2.0
git push origin shared/v0.2.0

git tag packages/executor-sdks/golang/v0.2.0
git push origin packages/executor-sdks/golang/v0.2.0
```

`publish-go-sdk.yml` validates the tagged module (gofmt, `go vet`, `go test`,
`go build`) and verifies the tag prefix matches the module path, so a mistyped
tag cannot silently declare a version. Once the workflow is green the Go module
proxy serves the tag.

---

## Version synchronization

Each artifact carries its own version and is versioned independently:

- `@neuron/sdk` — `version` in `packages/assembly-sdks/typescript/package.json`
- `Neuron.Executor` — `<Version>` in `packages/executor-sdks/dotnet/Directory.Build.props`
- Go modules — the git tag itself

The tag always overrides the checked-in version at release time. Update the
checked-in version in the same change that prepares a release if you want the
repository to reflect the published version outside of CI.

---

## Verifying a release

- **npm:** `npm view @neuron/sdk version` and confirm the published tarball shows
  provenance (`npm view @neuron/sdk dist.attestations`).
- **NuGet:** `dotnet nuget search Neuron.Executor` or the package page on nuget.org.
- **Go:** `go list -m github.com/Muhammad-Jay/neuron/shared@v0.2.0` through the
  public proxy.
