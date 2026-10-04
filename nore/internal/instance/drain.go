package instance

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
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

// failUnfinishedExecutions drives every execution of an instance that has not
// reached a terminal state to failed, persisting each one.
//
// The snapshot is written straight to the repository rather than published as an
// event. Both call sites run after the instance's event bus has stopped serving:
// the event persister has already returned, and it would save through the
// cancelled instance context in any case. There is also nothing left streaming
// to inform, so a publish would be discarded rather than observed.
func failUnfinishedExecutions(store contracts.ExecutionRepository, instanceID shared.ID, reason error) {
	if store == nil {
		return
	}
	for _, exec := range store.ListByInstance(instanceID) {
		if exec.IsTerminal() {
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
