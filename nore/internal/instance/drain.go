package instance

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
	executionmodel "github.com/neuron-runtime/neuron/nore/internal/execution"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

// An execution must never be reported as running without something that could
// still finish it. That invariant has two failure points, and both are handled
// here:
//
//   - When an instance stops, the scheduler's Run loop observes the cancelled
//     instance context and returns, closing its subscriptions. The capability
//     runtime engine keeps running until its in-flight workers finish, but those
//     workers report a capability's outcome as an event on a bus that no longer
//     has a scheduler listening. Nothing else transitions an execution to a
//     terminal state, so it keeps an in-flight count above zero, never closes
//     its done channel, and persists as running.
//
//   - When an instance is restored after a restart it is metadata only: it has
//     no scheduler, no engine, and no bus. An execution that was running when
//     the previous process died could never advance again, yet it would still be
//     listed as running.
//
// Draining explicitly at both points keeps the terminal state a property of the
// execution rather than a side effect of which goroutine happens to win a
// shutdown race. Exactly one transition can take effect, because MarkCompleted
// and MarkFailed both refuse an execution that is already terminal.

// sweepUnresumableExecutions drives every execution of an instance to failed,
// persisting each one.
//
// It is used after a restart, where the instance has no scheduler, no engine and
// no bus. Nothing in this process can advance any of its executions, so reporting
// them as running would be a permanent lie rather than a temporary inaccuracy.
// Detached executions are included here for exactly that reason: without the
// runtime that owned them they cannot resume at all.
func sweepUnresumableExecutions(store contracts.ExecutionRepository, instanceID shared.ID, reason error) {
	sweepExecutions(store, instanceID, reason, nil)
}

// sweepAbandonedExecutions drives the executions an instance owned to failed
// after it stops, leaving detached executions alone.
//
// The distinction is the point. A detached task owns its own execution and is the
// only component that can still advance it: the engine finishes it during the
// drain if it can, and if the drain budget runs out nothing remains that could.
// Failing it here would replace a truthful "still running" with a false "failed"
// while the work it describes may well have completed moments later. It is left
// instead for the restore path, which records the accurate reason -- that the
// process ended with no runtime to resume it -- rather than a misleading one.
func sweepAbandonedExecutions(store contracts.ExecutionRepository, instanceID shared.ID, reason error) {
	sweepExecutions(store, instanceID, reason, func(e *executionmodel.Execution) bool {
		return e.ParentExecutionID != ""
	})
}

// sweepExecutions persists a terminal failure for every unfinished execution of an
// instance, skipping those that skip reports as not the instance's to abandon.
//
// The snapshot is written straight to the repository rather than published as an
// event. Both call sites run after the instance's event bus has stopped serving:
// the event persister has already returned, and it would save through the
// cancelled instance context in any case. There is also nothing left streaming to
// inform, so a publish would be discarded rather than observed.
func sweepExecutions(store contracts.ExecutionRepository, instanceID shared.ID, reason error, skip func(*executionmodel.Execution) bool) {
	if store == nil {
		return
	}
	for _, exec := range store.ListByInstance(instanceID) {
		if exec.IsTerminal() {
			continue
		}
		if skip != nil && skip(exec) {
			continue
		}
		if !exec.MarkFailed(reason) {
			// Another transition won the race, which is a valid outcome: the
			// execution reached a terminal state of its own accord.
			continue
		}
		// The instance context is already cancelled, so persistence cannot be
		// bounded by it; a bounded context would risk abandoning the write and
		// leaving the stranded snapshot on disk.
		if err := store.Save(context.Background(), exec); err != nil {
			slog.Error("persist terminal execution state",
				slog.String("execution_id", string(exec.ID)),
				slog.String("instance_id", string(instanceID)),
				slog.String("error", err.Error()),
			)
		}
	}
}

// stoppedBeforeFinished explains that an execution lost its instance before it
// reached a terminal state. It is a failure rather than a cancellation because
// nothing asked the execution to stop; the instance went away underneath it.
func stoppedBeforeFinished(instanceID string) error {
	return fmt.Errorf("instance %s stopped before the execution finished", instanceID)
}

// restoredWithoutRuntime explains that an execution survived a process that
// ended without reaching a terminal state, and that no runtime exists that could
// finish it now.
func restoredWithoutRuntime(instanceID string) error {
	return fmt.Errorf("execution was interrupted by a restart of instance %s and has no runtime to resume it", instanceID)
}
