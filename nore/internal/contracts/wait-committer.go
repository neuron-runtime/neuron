package contracts

import (
	"context"
	"time"

	executionmodel "github.com/neuron-runtime/neuron/nore/internal/execution"
)

// WaitCommitter is the durable-mode commit boundary for waits, signal
// delivery, and durable retry. It is separate from TransitionCommitter so
// adding waits does not change the existing scheduler/engine contract: callers
// opt in to this interface in durable mode and remain unchanged otherwise.
//
// No method publishes events. A caller publishes only after a transition is
// committed; the dispatched intent is the durable source of truth if a publish
// fails.
type WaitCommitter interface {
	// SaveWaitTransition atomically persists the execution snapshot with its
	// timer, delivery, and dispatch intent effects. An idempotent delivery
	// replay reports Replay and writes nothing.
	SaveWaitTransition(ctx context.Context, req executionmodel.WaitTransitionRequest) (*executionmodel.WaitTransitionResult, error)

	// ClaimWaitTimer claims a due timer under owner. It reports false when the
	// timer is missing, not yet due, or held under a live lease.
	ClaimWaitTimer(ctx context.Context, key, owner string, lease time.Duration) (bool, error)

	// RenewWaitTimer extends a live claim. Lease loss is reported as
	// execution.ErrWaitTimerLeaseLost.
	RenewWaitTimer(ctx context.Context, key, owner string, lease time.Duration) error

	// DeleteWaitTimerOwned removes a timer only while owner holds its claim.
	DeleteWaitTimerOwned(ctx context.Context, key, owner string) (bool, error)
}

var _ WaitCommitter = (*executionmodel.ExecutionStore)(nil)
