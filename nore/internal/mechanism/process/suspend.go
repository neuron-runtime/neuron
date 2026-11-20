package process

import (
	"fmt"
	"time"

	v1 "github.com/neuron-runtime/neuron/shared/protocol/capabilityruntime/v1"
	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
	executionmodel "github.com/neuron-runtime/neuron/nore/internal/execution"
)

// This file maps the untrusted gRPC suspend/resume extension onto the engine's
// typed outcome. The gRPC runtime is arbitrary code: a suspend outcome is data
// to validate, never a permission or a successful result. All validation lives
// in capabilityrt.ValidateSuspend (shared with the WASM transport) so there is
// one authoritative shape/size check at the transport boundary.

// mapSuspendOutcome validates a proto SuspendOutcome and maps it to the
// engine-facing typed suspend. deadline_unix_ms is converted to a *time.Time
// WaitUntil; zero means no deadline and yields nil. Signal waits may carry a
// positive deadline as a bound; timer waits require one.
func mapSuspendOutcome(src *v1.SuspendOutcome) (*contracts.CapabilitySuspend, error) {
	if src == nil {
		return nil, nil
	}

	wire := &capabilityrt.Suspend{
		CorrelationKey: src.CorrelationKey,
		WaitType:       src.WaitType,
		ExpectedSignal: src.ExpectedSignal,
		DeadlineUnixMs: src.DeadlineUnixMs,
		PayloadSchema:  src.PayloadSchema,
	}
	if err := capabilityrt.ValidateSuspend(wire); err != nil {
		return nil, err
	}

	out := &contracts.CapabilitySuspend{
		CorrelationKey: wire.CorrelationKey,
		WaitType:       executionmodel.WaitType(wire.WaitType),
		ExpectedSignal: wire.ExpectedSignal,
		PayloadSchema:  wire.PayloadSchema,
	}
	if wire.DeadlineUnixMs > 0 {
		until := time.UnixMilli(wire.DeadlineUnixMs).UTC()
		out.WaitUntil = &until
	}
	return out, nil
}

// suspendToWire renders the typed suspend into the additive JSON-envelope
// value carried on capabilityrt.Response. Deriving the wire value from the
// typed suspend keeps one authoritative representation of the executor's
// metadata inside this transport.
func suspendToWire(src *contracts.CapabilitySuspend) *capabilityrt.Suspend {
	if src == nil {
		return nil
	}
	out := &capabilityrt.Suspend{
		CorrelationKey: src.CorrelationKey,
		WaitType:       string(src.WaitType),
		ExpectedSignal: src.ExpectedSignal,
		PayloadSchema:  src.PayloadSchema,
	}
	if src.WaitUntil != nil {
		out.DeadlineUnixMs = src.WaitUntil.UnixMilli()
	}
	return out
}

// rejectSuspendWithOutput enforces the mutual-exclusion rule on the gRPC
// ExecuteResponse: a suspend cannot be combined with a non-empty result or a
// controlled error. Such a response is malformed and must fail closed rather
// than silently drop either side.
func rejectSuspendWithOutput(resp *v1.ExecuteResponse) error {
	if resp == nil || resp.Suspend == nil {
		return nil
	}
	if len(resp.Result) != 0 || resp.Error != "" {
		return fmt.Errorf("invalid_suspend_outcome: suspend must not be combined with a non-empty result or error")
	}
	return nil
}

// resumeToProto maps the additive JSON resume input onto the gRPC ResumeInput.
// The shared validator bounds untrusted metadata before it crosses into the
// runtime, and the payload values are converted with the existing Value mapper
// so resume payloads use the same encoding as ordinary params.
func resumeToProto(src *capabilityrt.ResumeInput) (*v1.ResumeInput, error) {
	if src == nil {
		return nil, nil
	}
	if err := capabilityrt.ValidateResumeInput(src); err != nil {
		return nil, err
	}

	out := &v1.ResumeInput{
		Cause:      src.Cause,
		SignalName: src.SignalName,
		DeliveryId: src.DeliveryID,
		Payload:    make(map[string]*v1.Value, len(src.Payload)),
	}
	for k, v := range src.Payload {
		out.Payload[k] = makeProtoValue(v)
	}
	return out, nil
}
