package instance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/event"
	"github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/storage/sqlite"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

// Cancel is only honest if the work actually stops. The delay runtime blocks until
// its context dies, so an execution observed in flight is genuinely running code --
// which makes this the boundary at which "cancelled" can be proven end to end
// rather than inferred from a status string.
func TestCancelExecutionStopsTheRunningWork(t *testing.T) {
	store := newTestStore(t)
	i := startDelayInstance(t, store)
	exec := startBlockingExecution(t, i)

	if err := i.CancelExecution(context.Background(), exec.ID, errors.New("stopped by operator")); err != nil {
		t.Fatalf("CancelExecution: %v", err)
	}

	if got := exec.Status(); got != execution.StatusCancelled {
		t.Errorf("execution status = %s, want %s", got, execution.StatusCancelled)
	}
	if exec.Error() != "stopped by operator" {
		t.Errorf("execution error = %q, want the cancellation reason", exec.Error())
	}
	if got := exec.InFlight(); got != 0 {
		t.Errorf("in flight = %d, want 0 on a cancelled execution", got)
	}

	// Wait is how a caller learns the outcome, so it must not block once the
	// execution is terminal.
	waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := exec.Wait(waitCtx); err == nil {
		t.Error("Wait() = nil, want the cancellation reason")
	}
}

// The capability that was aborted must not be reported as broken. It was stopped
// deliberately, and blaming it would misdirect whoever reads the execution.
func TestCancelExecutionDoesNotReportTheAbortedCapabilityAsFailed(t *testing.T) {
	store := newTestStore(t)
	i := startDelayInstance(t, store)
	exec := startBlockingExecution(t, i)

	if err := i.CancelExecution(context.Background(), exec.ID, errors.New("stopped by operator")); err != nil {
		t.Fatalf("CancelExecution: %v", err)
	}

	// The engine unwinds asynchronously after the cancellation is recorded.
	time.Sleep(250 * time.Millisecond)

	if got := exec.CapabilityState("cap_wait").Status; got != execution.CapabilityCancelled {
		t.Errorf("capability status = %s, want %s", got, execution.CapabilityCancelled)
	}
}

// A caller must be able to tell "I stopped it" from "it was already done".
// Reporting success for a finished execution would tell an operator their stop
// took effect when the work had already concluded on its own.
func TestCancelExecutionRefusesACompletedExecution(t *testing.T) {
	store := newTestStore(t)
	i := startDelayInstance(t, store)
	exec, err := i.Execute(context.Background(), map[string]any{"duration": "1ms"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := exec.Wait(waitCtx); err != nil {
		t.Fatalf("short execution failed: %v", err)
	}
	if !exec.IsTerminal() {
		t.Fatalf("execution status = %s, want terminal before cancelling", exec.Status())
	}
	completed := exec.Status()

	err = i.CancelExecution(context.Background(), exec.ID, errors.New("too late"))
	if !errors.Is(err, ErrExecutionNotCancellable) {
		t.Fatalf("CancelExecution error = %v, want ErrExecutionNotCancellable", err)
	}
	if got := exec.Status(); got != completed {
		t.Errorf("execution status = %s, want %s (a refused cancellation must not rewrite the outcome)", got, completed)
	}
	if exec.Error() != "" {
		t.Errorf("execution error = %q, want empty", exec.Error())
	}
}

// An execution that does not exist cannot be cancelled, and the caller has to be
// able to tell that apart from a refusal.
func TestCancelExecutionReportsAnUnknownExecution(t *testing.T) {
	store := newTestStore(t)
	i := startDelayInstance(t, store)

	err := i.CancelExecution(context.Background(), "exec_does_not_exist", errors.New("stop"))
	if !errors.Is(err, ErrExecutionNotFound) {
		t.Fatalf("CancelExecution error = %v, want ErrExecutionNotFound", err)
	}
	if errors.Is(err, ErrExecutionNotCancellable) {
		t.Error("unknown execution reported as already-terminal")
	}
}

// The cancellation must survive the process. An execution cancelled and then read
// back after a restart has to still say cancelled, or a restart would resurrect
// work an operator stopped.
//
// The snapshot is written by the execution persister when it observes the
// cancellation event, so this waits for that flush rather than assuming it has
// already happened -- stopping the instance first would race it and prove nothing.
func TestCancellationIsPersisted(t *testing.T) {
	store := newTestStore(t)
	i := startDelayInstance(t, store)
	exec := startBlockingExecution(t, i)

	if err := i.CancelExecution(context.Background(), exec.ID, errors.New("stopped by operator")); err != nil {
		t.Fatalf("CancelExecution: %v", err)
	}

	waitForPersistedStatus(t, store, exec.ID, execution.StatusCancelled)

	data, err := store.Get(context.Background(), "executions/"+string(exec.ID))
	if err != nil {
		t.Fatalf("read persisted execution: %v", err)
	}
	restored, err := execution.UnmarshalExecution(data)
	if err != nil {
		t.Fatalf("UnmarshalExecution: %v", err)
	}
	if got := restored.Status(); got != execution.StatusCancelled {
		t.Errorf("restored status = %s, want %s", got, execution.StatusCancelled)
	}
	if restored.Error() != "stopped by operator" {
		t.Errorf("restored error = %q, want the cancellation reason", restored.Error())
	}
}

// waitForPersistedStatus blocks until the durable snapshot of an execution
// reports the expected status.
func waitForPersistedStatus(t *testing.T, store *sqlite.Store, executionID shared.ID, want execution.Status) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last execution.Status
	for time.Now().Before(deadline) {
		data, err := store.Get(context.Background(), "executions/"+string(executionID))
		if err == nil {
			if restored, unmarshalErr := execution.UnmarshalExecution(data); unmarshalErr == nil {
				last = restored.Status()
				if last == want {
					return
				}
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("persisted execution status settled at %s, want %s", last, want)
}

// A restored instance has no scheduler, because the runtime it belonged to is gone.
// It must still be able to answer a cancellation rather than failing the request --
// and the honest answer there is that there is nothing left to stop.
func TestCancelOnARestoredInstanceReportsTheExecutionAsFinished(t *testing.T) {
	store := newTestStore(t)
	i := startDelayInstance(t, store)
	exec := startBlockingExecution(t, i)

	if err := i.Stop(); err != nil {
		t.Fatalf("stop instance: %v", err)
	}

	assembly := delayAssembly()
	restored, err := New(context.Background(), i.ID, protocol.InstanceKey{
		AssemblyID: "sys_delay", Version: "1.0.0",
	}, &assembly, 2, store)
	if err != nil {
		t.Fatalf("restore instance: %v", err)
	}

	// Stop drove the execution to a terminal state, so there is nothing left to
	// stop and the request must say so instead of claiming success.
	if err := restored.CancelExecution(context.Background(), exec.ID, errors.New("too late")); !errors.Is(err, ErrExecutionNotCancellable) {
		t.Fatalf("CancelExecution error = %v, want ErrExecutionNotCancellable", err)
	}
}

// The cancellation must be observable by the event stream, which is how `neuron
// run` learns the outcome. Without the event a client watches a stopped execution
// until its own timeout.
func TestCancelExecutionPublishesTheCancellation(t *testing.T) {
	store := newTestStore(t)
	i := startDelayInstance(t, store)
	exec := startBlockingExecution(t, i)

	subscription, err := i.Bus().SubscribeExecution(exec.ID, 16)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer subscription.Close()

	if err := i.CancelExecution(context.Background(), exec.ID, errors.New("stopped by operator")); err != nil {
		t.Fatalf("CancelExecution: %v", err)
	}

	deadline := time.After(5 * time.Second)
	for {
		select {
		case evt, open := <-subscription.Events():
			if !open {
				t.Fatal("subscription closed before the cancellation event arrived")
			}
			if evt.Type != event.ExecutionCancelled {
				continue
			}
			payload, ok := evt.Payload.(event.ExecutionCancelledPayload)
			if !ok {
				t.Fatalf("payload type = %T, want ExecutionCancelledPayload", evt.Payload)
			}
			if payload.Message != "stopped by operator" {
				t.Errorf("payload message = %q, want the cancellation reason", payload.Message)
			}
			return
		case <-deadline:
			t.Fatal("no ExecutionCancelled event was published")
		}
	}
}
