#!/usr/bin/env bash
# Builds the example echo capability runtimes into the local registry catalog
# layout consumed by `neuron add` and `neuron build`:
#
#   catalog/example/echo/1.0.0/         process-runtime capability runtime
#   catalog/example/echo-wasm/1.0.0/    wasm-runtime capability runtime
#
# Each version directory also receives its canonical capability runtime package
# archive (<name>-<version>-capability-runtime.neuron.tar.gz): a single
# immutable artifact that registries prefer over per-platform assets because it
# carries runtime.json plus every platform binary referenced by it.
#
# Both are compiled from the same source in ./echo. Generated binaries are
# gitignored; re-run this script after changing the source or to rebuild.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SRC="$ROOT/echo"
CATALOG="$ROOT/catalog"
VERSION="1.0.0"

# The echo example is a standalone stdlib-only module; ignore the workspace so
# it builds offline and independently of the repo workspace.
export GOWORK=off

echo_dir="$CATALOG/example/echo/$VERSION"
wasm_dir="$CATALOG/example/echo-wasm/$VERSION"
mkdir -p "$echo_dir" "$wasm_dir"

GOOS="$(go env GOOS)"
GOARCH="$(go env GOARCH)"

# Native binary for the process runtime.
go build -C "$SRC" -o "$echo_dir/echo" .

# Portability-proven WASI module for the wasm runtime.
GOOS=wasip1 GOARCH=wasm go build -C "$SRC" -o "$wasm_dir/echo.wasm" .

# The echo example speaks the stdin/stdout JSON protocol (one JSON request on
# stdin, one JSON response on stdout). Under the canonical
# neuron/capability-runtime-v1 (gRPC) meaning, stdout is transport not payload,
# so the manifest must declare the JSON transport explicitly.
JSON_PROTOCOL="neuron/capability-runtime-v1-json"

cat > "$echo_dir/runtime.json" <<EOF
{
  "apiVersion": "neuron/v1",
  "kind": "CapabilityRuntime",
  "metadata": {
    "name": "example:echo",
    "version": "$VERSION",
    "description": "Echoes the execution params and captures NEURON_CAPABILITY_RUNTIME_* env vars."
  },
  "runtime": { "type": "process", "entrypoint": "echo", "protocol": "$JSON_PROTOCOL" },
  "capabilities": ["example:echo"],
  "features": [],
  "platforms": { "$GOOS-$GOARCH": { "artifact": "echo" } }
}
EOF

cat > "$wasm_dir/runtime.json" <<EOF
{
  "apiVersion": "neuron/v1",
  "kind": "CapabilityRuntime",
  "metadata": {
    "name": "example:echo-wasm",
    "version": "$VERSION",
    "description": "Echoes the execution params as a WASI module and captures NEURON_CAPABILITY_RUNTIME_* env vars."
  },
  "runtime": { "type": "wasm", "entrypoint": "echo.wasm", "protocol": "$JSON_PROTOCOL" },
  "capabilities": ["example:echo-wasm"],
  "features": [],
  "platforms": { "wasm32-wasi": { "artifact": "echo.wasm" } }
}
EOF

# Canonical capability runtime package archives: runtime.json plus the platform
# binary, with the files at the archive root (no wrapping directory). Registries
# resolve these as the preferred single-asset distribution. The archive is
# staged outside the scanned directory so tar never reads a dir it mutates.
stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT

tar -C "$echo_dir" -czf "$stage/example-echo-1.0.0-capability-runtime.neuron.tar.gz" .
mv "$stage/example-echo-1.0.0-capability-runtime.neuron.tar.gz" "$echo_dir/"
tar -C "$wasm_dir" -czf "$stage/example-echo-wasm-1.0.0-capability-runtime.neuron.tar.gz" .
mv "$stage/example-echo-wasm-1.0.0-capability-runtime.neuron.tar.gz" "$wasm_dir/"

echo "Built example capability runtimes into $CATALOG"