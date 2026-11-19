package contracts

import (
	"time"

	executionmodel "github.com/neuron-runtime/neuron/nore/internal/execution"
)

// CapabilityOutcome is the single result of one capability execution.
//
// Result and Suspend are mutually exclusive: a runtime that returns Suspend
// must leave Result empty. Error remains the transport and infrastructure
// channel; suspend is a legitimate outcome, not a failure. Consumers that
// cannot represent a suspend must fail closed rather than treat it as an
// empty success.
type CapabilityOutcome struct {
	Result  map[string]any
	Suspend *CapabilitySuspend
}

// CapabilitySuspend is the typed, transport-neutral suspend metadata returned
// by a capability runtime.
//
// It reuses the execution domain's WaitType so there is exactly one wait
// vocabulary. The transport boundary is responsible for validating untrusted
// executor metadata before it is mapped into this type; the execution domain's
// EnterWait remains the authoritative validator of wait metadata.
type CapabilitySuspend struct {
	CorrelationKey string
	WaitType       executionmodel.WaitType // signal or timer; deadline is engine-internal
	ExpectedSignal string
	WaitUntil      *time.Time // derived from the wire deadline; nil means no deadline
	PayloadSchema  string
}
