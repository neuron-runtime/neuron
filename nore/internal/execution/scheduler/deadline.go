package scheduler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/storage"
	core "github.com/neuron-runtime/neuron/shared/types/core"
)

// executionDeadlineSource is the bounded store read that deadline expiry needs.
// It is local to the scheduler because due-deadline discovery is a scheduler
// use of the execution store, not part of the shared execution repository port.
type executionDeadlineSource interface {
	DueExecutionDeadlines(ctx context.Context, instanceID core.ID, now time.Time, limit int) ([]core.ID, error)
}

// ExpireExecutionDeadlines terminates non-terminal executions whose maximum-
// lifetime deadline has elapsed. Discovery comes from the store's dedicated,
// time-ordered deadline index for one instance: the scheduler never lists
// executions and never spends a poll's budget on another instance's work. The
// batch size is applied at the store call so discovery itself stays bounded.
//
// Each candidate is re-read before acting, so a stale index entry from a
// crashed writer cannot terminalize an execution whose deadline was cleared or
// whose execution already finished. Termination reuses CancelExecution, the
// scheduler's single RunTransition-guarded terminal path: it cancels the scope,
// commits the terminal snapshot together with wait-timer cleanup, and publishes
// ExecutionCancelled only after the commit. A nil, future, or already-terminal
// deadline is ignored, so concurrent ticks cannot terminalize an execution twice.
func (s *Scheduler) ExpireExecutionDeadlines(ctx context.Context, instanceID core.ID) (int, error) {
	if instanceID == "" {
		return 0, fmt.Errorf("instance ID is required for deadline expiry")
	}
	source, ok := s.executions.(executionDeadlineSource)
	if !ok {
		return 0, fmt.Errorf("execution repository does not provide bounded deadline discovery")
	}

	now := time.Now().UTC()
	due, err := source.DueExecutionDeadlines(ctx, instanceID, now, s.pollConfig.BatchSize)
	if err != nil {
		return 0, fmt.Errorf("discover due execution deadlines for instance %s: %w", instanceID, err)
	}
	expired := 0
	var errs []error
	for _, executionID := range due {
		if expired >= s.pollConfig.BatchSize || ctx.Err() != nil {
			break
		}
		execution, err := s.executions.Get(ctx, executionID)
		if errors.Is(err, storage.ErrNotFound) {
			// A stale index pointer to a deleted execution is harmless.
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("load execution %s for deadline expiry: %w", executionID, err))
			continue
		}
		if execution == nil || execution.IsTerminal() {
			continue
		}
		deadline := execution.ExecutionDeadline()
		if deadline == nil || deadline.After(now) {
			continue
		}
		reason := fmt.Errorf("execution %s maximum lifetime exceeded at %s", execution.ID, deadline.Format(time.RFC3339))
		cancelled, err := s.CancelExecution(ctx, execution.ID, reason)
		if err != nil {
			errs = append(errs, fmt.Errorf("expire execution %s: %w", execution.ID, err))
			continue
		}
		if cancelled {
			expired++
		}
	}
	return expired, errors.Join(errs...)
}
