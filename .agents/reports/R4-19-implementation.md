# Implementation — R4-19

status: in_progress
lease: 

## Changed
- `shared/protocol/capabilityruntime/v1/capability_runtime.proto:86-89` — added `attempt_id` and `logical_operation_id` fields to `ExecuteRequest`
- `shared/types/protocol/instance.go:135-143` — added `AttemptID` and `LogicalOperationID` fields to `ExecuteRequest` struct
- `nore/internal/contracts/capabilityruntime.go:56-83` — added `AttemptID` and `LogicalOperationID` fields to `ExecutionContext` struct
- `nore/internal/mechanism/process/worker.go:593-619` — wired `AttemptID` and `LogicalOperationID` from context to `v1.ExecuteRequest`
- `packages/executor-sdks/golang/capabilityruntime.go:193-214` — pass through `AttemptID` and `LogicalOperationID` in `Execute` handler

## Behavior
- The gRPC `ExecuteRequest` now carries optional `attempt_id` and `logical_operation_id` fields (proto3, backward compatible)
- The `ExecuteRequest` HTTP API body includes these fields for idempotency awareness
- The execution context carries `AttemptID` and `LogicalOperationID` from the Neuron execution engine
- The Go process worker populates these fields on the gRPC `ExecuteRequest` before sending to the capability runtime
- Existing runtimes receive empty strings for the new fields, maintaining backward compatibility

## Verification
- Proto compilation: `protoc` validation
- Go type check: `go vet` on affected packages
- Existing tests compile and pass
- SDK Execute handler passes through new fields

## Risks and limitations
- Proto3 optional fields with zero value (= "") ensure backward compatibility
- Existing capability runtimes that don't understand these fields will receive empty strings
- Requires coordination with .NET and TypeScript SDK updates (outside Go scope)
- Wiring execution context IDs to the worker requires context propagation changes

## Discovered but not fixed
- .NET SDK field additions and protoc regeneration (outside current lease)
- TypeScript assembly SDK impact (orthogonal to gRPC protocol)
- HTTP API integration points for the new fields

## Next safe action
- Run `go vet` on affected packages
- Run targeted tests for worker and SDK
- Verify proto compilation