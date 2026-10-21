// Package acceptance holds process-level acceptance tests for the documented
// Neuron workflows.
//
// The tests in this package exec freshly built `neuron` and `nore` binaries
// against temporary projects and a temporary HOME, so they exercise the real
// CLI, configuration loading, build pipeline, project-runtime discovery, and
// HTTP execution boundary rather than calling internals in-process. They are
// deliberately black-box: each test drives the product the way a user does and
// asserts on stdout, stderr, exit codes, and the documented on-disk contracts.
//
// This file exists so the package builds even though every test lives in
// _test.go files; `go build ./...` and `go vet ./...` must stay green.
package acceptance
