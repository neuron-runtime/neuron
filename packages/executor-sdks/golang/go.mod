module github.com/neuron-runtime/neuron/packages/executor-sdks/golang

go 1.26.5

require (
	github.com/neuron-runtime/neuron/shared v0.0.0-00010101000000-000000000000
	google.golang.org/grpc v1.83.2
)

require (
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)

// PUBLISH BLOCKER: this module is not yet consumable outside this repository.
//
// A `replace` directive is honoured only in the main module, so downstream
// `go get github.com/neuron-runtime/neuron/packages/executor-sdks/golang`
// resolves `shared` through the module proxy and fails while this line exists.
// Before the first public release of this SDK both of the following must
// happen together:
//
//  1. tag the contracts module, e.g. `shared/v0.1.0`, and push the tag so the
//     module proxy can serve it, and
//  2. replace the v0.0.0 placeholder require above with that real version and
//     delete this replace directive.
//
// Nothing in this repository should depend on step 1 having happened yet.
replace github.com/neuron-runtime/neuron/shared => ../../../shared
