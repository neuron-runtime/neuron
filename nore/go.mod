module github.com/neuron-runtime/neuron/nore

go 1.26.5

require (
	github.com/coder/websocket v1.8.15
	github.com/google/cel-go v0.30.0
	github.com/neuron-runtime/neuron/shared v0.0.0-20260905200234-63564fa7a003
	github.com/tetratelabs/wazero v1.12.0
	google.golang.org/grpc v1.83.2
	modernc.org/sqlite v1.56.0
)

require (
	cel.dev/expr v0.25.2 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/exp v0.0.0-20240823005443-9b4947da3948 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/protobuf v1.36.12 // indirect
	modernc.org/libc v1.74.4 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)

// The shared module holds the canonical types and the capability runtime
// protocol contract. It is resolved from the repository tree rather than a
// published version so that a contract change and the runtime that depends on
// it can never drift apart in a release.
//
// This replace is a main-module directive, so it applies to builds of this
// module only, and it is what makes `GOWORK=off go build ./...` a meaningful
// verification step in CI rather than a check masked by the go.work file.
replace github.com/neuron-runtime/neuron/shared => ../shared
