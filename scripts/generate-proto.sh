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
#
# Never hand-edit the generated .pb.go files: a plain-text change inside the
# embedded serialized file descriptor (for example, a module-path rename)
# corrupts the descriptor and panics every binary at init. Always regenerate
# from the .proto source instead.
set -eu

root="$(cd "$(dirname "$0")/.." && pwd)"
proto_dir="shared/protocol/capabilityruntime/v1"
proto_rel="$proto_dir/capability_runtime.proto"

# Preflight every tool before touching the output directory so a missing
# binary never leaves the checked-in generated files deleted.
for tool in protoc protoc-gen-go protoc-gen-go-grpc; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "error: $tool not found on PATH" >&2
    echo "  see $0 -- header comments for install commands" >&2
    exit 1
  fi
done

cd "$root"

# Remove stale generated files so messages/services removed from the .proto can
# never linger in the build.
rm -f "$proto_dir"/*.pb.go

protoc \
  --go_out=. --go_opt=paths=source_relative \
  --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  "$proto_rel"

# Normalize formatting so the CI gofmt check stays green on the regenerated
# files (generated code uses tabs, but -s also simplifies any struct literals).
gofmt -w -s "$proto_dir"

echo "regenerated: $proto_rel (shared/protocol/capabilityruntime/v1)"