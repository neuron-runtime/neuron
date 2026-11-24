package instance

import (
	"context"
	"fmt"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
	exec "github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/execution/scheduler"
	core "github.com/neuron-runtime/neuron/shared/types/core"
)

// waitDeadlinePolicy maps a resolved execution-policy string onto the domain
// policy persisted with a wait. The operator string has already been normalized
// by core.NormalizeWaitDeadlinePolicy when the policy was resolved, so this is a
// mapping and not a second validator. Empty maps to fail, matching the domain's
// own default; an unrecognized value can only arrive from a hand-built policy
// and is mapped to fail closed rather than persisted verbatim.
func waitDeadlinePolicy(policy string) exec.WaitDeadlinePolicy {
	switch policy {
	case "cancel":
		return exec.WaitDeadlinePolicyCancel
	case "continue":
		return exec.WaitDeadlinePolicyContinue
	case "fail", "":
		return exec.WaitDeadlinePolicyFail
	default:
		return exec.WaitDeadlinePolicyFail
	}
}

// waitLifecycle is the instance-owned adapter between the engine's wait port
// and the scheduler's durable wait transitions. It loads the execution through
// the repository and delegates every state and durability decision to the
// scheduler; it owns no wait state of its own.
type waitLifecycle struct {
	executions     contracts.ExecutionRepository
	scheduler      *scheduler.Scheduler
	deadlinePolicy exec.WaitDeadlinePolicy
}

func (a *waitLifecycle) EnterWait(ctx context.Context, executionID, capabilityID core.ID, spec exec.WaitSpec) error {
	execution, err := a.executions.Get(ctx, executionID)
	if err != nil {
		return fmt.Errorf("load execution %s for wait: %w", executionID, err)
	}
	// Applied here, once, and persisted with the wait. The engine leaves the
	// spec policy empty so an executor can never choose its own deadline rule.
	spec.DeadlinePolicy = a.deadlinePolicy
	return a.scheduler.EnterWait(ctx, scheduler.WaitEntryRequest{
		Execution:    execution,
		CapabilityID: capabilityID,
		Spec:         spec,
	})
}

func (a *waitLifecycle) BeginRetryWait(ctx context.Context, executionID, capabilityID core.ID, nextAttemptAt time.Time) error {
	execution, err := a.executions.Get(ctx, executionID)
	if err != nil {
		return fmt.Errorf("load execution %s for retry wait: %w", executionID, err)
	}
	return a.scheduler.BeginRetryWait(ctx, scheduler.RetryWaitRequest{
		Execution:     execution,
		CapabilityID:  capabilityID,
		NextAttemptAt: nextAttemptAt,
	})
}
