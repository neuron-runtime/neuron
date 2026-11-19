package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
	exec "github.com/neuron-runtime/neuron/nore/internal/execution"
	core "github.com/neuron-runtime/neuron/shared/types/core"
)

// WaitLifecycle is the engine's consumer-local port for entering durable waits.
//
// It is deliberately not a host, transport, scheduler, or store contract: those
// layers decide how a wait is persisted and how it resumes. The engine only
// needs to hand a typed wait to the consumer that owns the execution and then
// stop treating the capability as work it is running.
//
// Neither method publishes events. Event publication stays in the engine, so a
// retry is announced once before any lifecycle commit and a suspend introduces
// no new event contract.
type WaitLifecycle interface {
	// EnterWait records a capability's transition into the wait described by
	// spec. The implementation must validate spec through the execution domain,
	// which remains the authoritative validator of wait metadata.
	EnterWait(ctx context.Context, executionID, capabilityID core.ID, spec exec.WaitSpec) error

	// BeginRetryWait records the earliest time a failed capability may be
	// attempted again. The implementation must release the capability's
	// in-flight slot, so a deferred retry does not keep a worker occupied.
	BeginRetryWait(ctx context.Context, executionID, capabilityID core.ID, nextAttemptAt time.Time) error
}

// ErrSuspendLifecycleUnavailable is the fail-closed failure for a capability
// that suspends before the engine has a wait lifecycle to enter it. In
// non-durable mode there is no honest execution state for a suspend, so it is
// reported as a failure rather than as an empty success.
var ErrSuspendLifecycleUnavailable = errors.New("engine: suspend outcome received but no wait lifecycle is configured")

// errRetryDeferred is the internal control signal from the retry callback to
// executeCapability: the retry has been recorded durably, so the worker must
// neither sleep nor run another attempt nor emit a terminal event. It is
// package-private because it is not an outcome any consumer should observe.
var errRetryDeferred = errors.New("engine: retry deferred to a durable wait")

// WithWaitLifecycle supplies the consumer that owns durable waits. Without it
// the engine keeps its non-durable behavior: a suspend fails closed and a retry
// sleeps inline.
func WithWaitLifecycle(l WaitLifecycle) Option {
	return func(e *CapabilityRuntimeEngine) {
		e.waitLifecycle = l
	}
}

// suspendWaitReason is engine-supplied. A wait reason describes N.O.R.E.'s own
// state; it is not free-form text copied out of an untrusted executor outcome.
const suspendWaitReason = "capability suspended"

// waitSpecFromSuspend maps a typed suspend outcome onto the execution domain's
// WaitSpec. Wait types an executor must never choose are rejected here, before
// the lifecycle is consulted; all other validation remains with EnterWait,
// which is the authoritative domain validator.
func waitSpecFromSuspend(suspend *contracts.CapabilitySuspend) (exec.WaitSpec, error) {
	if suspend == nil {
		return exec.WaitSpec{}, errors.New("engine: suspend outcome has no wait metadata")
	}
	if suspend.WaitType == exec.WaitTypeDeadline {
		return exec.WaitSpec{}, errors.New("engine: executor cannot request a deadline wait; deadline waits are engine-internal")
	}
	if suspend.WaitType != exec.WaitTypeSignal && suspend.WaitType != exec.WaitTypeTimer {
		return exec.WaitSpec{}, fmt.Errorf("engine: invalid suspend wait type %q", suspend.WaitType)
	}
	return exec.WaitSpec{
		Type:           suspend.WaitType,
		Reason:         suspendWaitReason,
		CorrelationKey: suspend.CorrelationKey,
		ExpectedSignal: suspend.ExpectedSignal,
		WaitUntil:      suspend.WaitUntil,
		PayloadSchema:  suspend.PayloadSchema,
		// DeadlinePolicy is deliberately empty: deadline handling belongs to the
		// engine, never to an executor-supplied suspend.
	}, nil
}
