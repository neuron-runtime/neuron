package capabilityruntime

import (
	v1 "github.com/neuron-runtime/neuron/shared/protocol/capabilityruntime/v1"
)

// WaitType values for SuspendOutcome.WaitType.
const (
	// WaitTypeSignal waits for a named signal.
	WaitTypeSignal = "signal"

	// WaitTypeTimer waits for a timer deadline.
	WaitTypeTimer = "timer"
)

// Resume cause values for ResumeInput.Cause.
const (
	// ResumeCauseSignal means a signal resumed the wait.
	ResumeCauseSignal = "signal"

	// ResumeCauseTimer means a timer deadline resumed the wait.
	ResumeCauseTimer = "timer"

	// ResumeCauseDeadline means the wait deadline policy resumed the wait.
	ResumeCauseDeadline = "deadline"
)

// SuspendOutcome asks N.O.R.E. to suspend the capability until a signal or
// timer fires.
//
// Suspend is not a stackful continuation. The invocation ends, and a later
// fresh Execute carries a ResumeInput with the same logical operation ID and a
// new attempt ID. A capability that suspends must therefore reconstruct its
// state from the params and the resume input, and must be idempotent.
//
// A handler may only return a SuspendOutcome when its Initialize result
// advertises v1.SuspendFeature ("suspend"). The SDK rejects an unnegotiated
// suspend explicitly instead of returning an empty success.
type SuspendOutcome struct {
	// CorrelationKey identifies this wait. It is required when more than one
	// concurrent wait on the same signal name can exist in one execution.
	CorrelationKey string

	// WaitType is WaitTypeSignal or WaitTypeTimer.
	WaitType string

	// ExpectedSignal names the signal that resumes a signal wait. It is empty
	// for a timer wait.
	ExpectedSignal string

	// DeadlineUnixMs is the wait deadline in Unix milliseconds. Zero means the
	// wait has no deadline. A timer wait requires a positive value.
	DeadlineUnixMs int64

	// PayloadSchema optionally names the schema the resume payload must match.
	PayloadSchema string
}

// ResumeInput carries the reason a suspended capability is being resumed.
type ResumeInput struct {
	// Cause is ResumeCauseSignal, ResumeCauseTimer, or ResumeCauseDeadline.
	Cause string

	// SignalName names the signal that resumed a signal wait.
	SignalName string

	// DeliveryID is the client-supplied idempotency key for the delivery. The
	// same delivery can be presented more than once; capabilities must dedupe
	// on it.
	DeliveryID string

	// Payload carries the signal payload. It is empty for timer and deadline
	// resumes.
	Payload map[string]any
}

// ExecutionRequest is the full request handed to an outcome-aware handler. The
// legacy Handler.Execute entry point receives only Params.
type ExecutionRequest struct {
	// ExecutionID is a unique identifier for this execution.
	ExecutionID string

	// Params carries the resolved execution params for the capability.
	Params map[string]any

	// TimeoutMs is the maximum time the capability should spend, in
	// milliseconds. Zero means no timeout.
	TimeoutMs int64

	// CorrelationID links this execution to a parent execution or request.
	CorrelationID string

	// AttemptID is the per-invocation attempt identifier. A resumed invocation
	// receives a new attempt ID.
	AttemptID string

	// LogicalOperationID is stable across a suspend and its resume.
	LogicalOperationID string

	// Resume is nil for a first invocation and populated when resuming a
	// suspended capability.
	Resume *ResumeInput
}

// ExecutionResult is the outcome of an outcome-aware handler.
type ExecutionResult struct {
	// Result carries the results of the execution. Keys map to the capability's
	// declared result fields.
	Result map[string]any

	// Error, when non-empty, records a controlled failure. It retains the
	// legacy semantics: the transport succeeds and the runtime treats the
	// error as an execution failure.
	Error string

	// Suspend, when non-nil, asks N.O.R.E. to suspend the capability instead
	// of completing it. Result and Error must be empty when Suspend is set.
	Suspend *SuspendOutcome
}

// suspendOutcomeToProto maps an SDK suspend outcome onto the wire contract.
func suspendOutcomeToProto(outcome *SuspendOutcome) *v1.SuspendOutcome {
	if outcome == nil {
		return nil
	}
	return &v1.SuspendOutcome{
		CorrelationKey: outcome.CorrelationKey,
		WaitType:       outcome.WaitType,
		ExpectedSignal: outcome.ExpectedSignal,
		DeadlineUnixMs: outcome.DeadlineUnixMs,
		PayloadSchema:  outcome.PayloadSchema,
	}
}

// resumeInputFromProto maps the wire resume input onto the SDK type.
func resumeInputFromProto(resume *v1.ResumeInput) *ResumeInput {
	if resume == nil {
		return nil
	}
	payload := make(map[string]any, len(resume.Payload))
	for k, v := range resume.Payload {
		payload[k] = fromProtoValue(v)
	}
	return &ResumeInput{
		Cause:      resume.Cause,
		SignalName: resume.SignalName,
		DeliveryID: resume.DeliveryId,
		Payload:    payload,
	}
}
