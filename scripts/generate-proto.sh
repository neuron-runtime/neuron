#!/usr/bin/env bash
# Regenerates the canonical capability-runtime gRPC contract from its .proto
# source into the Go module and every language SDK that references it.
#
# Requirements (see AGENTS.md "Do not hand edit generated code"):
#   - protoc (matching protoc-gen-go / protoc-gen-go-grpc)
#   - protoc-gen-go  (go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12)
#   - protoc-gen-go-grpc (go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2)
#
# The Go files are generated on top of the .proto. The .NET SDK references the
# same .proto through Grpc.Tools at build time (see Neuron.Executor.csproj), so
# it needs no regeneration here.
set -eu

root="$(cd "$(dirname "$0")/.." && pwd)"
proto_rel="shared/protocol/capabilityruntime/v1/capability_runtime.proto"

if ! command -v protoc >/dev/null 2>&1; then
  echo "error: protoc not found on PATH" >&2
  exit 1
fi

cd "$root"
protoc \
  --go_out=. --go_opt=paths=source_relative \
  --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  "$proto_rel"

echo "regenerated: $proto_rel (shared/protocol/capabilityruntime/v1)"