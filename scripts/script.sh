#!/usr/bin/env bash
# Development helper: verifies every Go module and npm package in the
# workspace. Run via `npm run script` from the repository root.
#
# Go modules are tested and built with `go test ./...` / `go build ./...`.
# npm packages are typechecked, tested, and built with pnpm.
set -u

go_modules=(
  "./nore"
  "./shared"
  "./application"
  "./packages/executor-sdks/golang"
)

for mod in "${go_modules[@]}"; do
  if [ ! -f "$mod/go.mod" ]; then
    echo "skip $mod (no go.mod)"
    continue
  fi
  echo "== Go: $mod =="
  (
    cd "$mod" || exit 1
    # Module integrity: tidy each module with GOWORK=off so the workspace's
    # implicit `shared` substitution cannot hide a stale go.mod/go.sum. This
    # mirrors CI's module-integrity job; a non-tidy module fails here instead
    # of only in CI. The tidy result must be committed.
    GOWORK=off go mod tidy || {
      echo "FAILED: $mod go mod tidy" >&2
      exit 1
    }
    if ! git diff --exit-code -- go.mod go.sum >/dev/null; then
      echo "FAILED: $mod go.mod/go.sum not tidy; commit the tidy result" >&2
      exit 1
    fi
    # Build into a scratch directory so `go build ./...` never drops binaries
    # into the source tree. Library-only modules (no package main) have nothing
    # to link, so build them without -o; the scratch dir only applies when there
    # is something to build.
    scratch="$(mktemp -d)"
    go vet ./... \
      && go test ./... \
      && if grep -rq '^package main' --include='*.go' .; then
           go build -o "$scratch" ./...
         else
           go build ./...
         fi || {
        echo "FAILED: $mod" >&2
        exit 1
      }
    rm -rf "$scratch"
  ) || exit 1
done

for pkg in ./packages/assembly-sdks/* ./packages/executor-sdks/* ./examples/*; do
  if [ ! -f "$pkg/package.json" ]; then
    echo "skip $pkg (not a pnpm package)"
    continue
  fi
  echo "== pnpm: $pkg =="
  (
    cd "$pkg" || exit 1
    # --if-present keeps the loop green for packages that do not define every
    # script (for example, example projects that only typecheck).
    pnpm run --if-present typecheck \
      && pnpm run --if-present test \
      && pnpm run --if-present build || {
        echo "FAILED: $pkg" >&2
        exit 1
      }
  ) || exit 1
done

echo "All modules built and tested successfully."