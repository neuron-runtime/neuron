package instance

import (
	"context"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/event"
	"github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/nore/internal/storage/sqlite"
	"github.com/neuron-runtime/neuron/nore/internal/types"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

func newTestStore(t *testing.T) *sqlite.Store {
	t.Helper()
	store, err := sqlite.New(storage.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// delayAssembly is the assembly behind startDelayInstance: one capability whose
// implementation blocks until its context dies.
func delayAssembly() shared.Assembly {
	return shared.Assembly{
		Metadata: shared.Metadata{ID: "sys_delay", Name: "sys_delay", Version: "1.0.0"},
		Specification: shared.AssemblySpec{
			Capabilities: []shared.Capability{{
				Metadata: shared.Metadata{ID: "cap_wait", Name: "cap_wait", Version: "1.0.0"},
				Type:     shared.CoreName("delay"),
				Params:   []shared.Port{{Name: "duration", Type: shared.ValueString, Required: true}},
				Results:  []shared.Port{{Name: "delayed_for", Type: shared.ValueString}},
			}},
		},
	}
}

// startDelayInstance brings up an instance whose single capability blocks on the
// in-process delay runtime. A capability that blocks until its context dies is
// what makes an execution genuinely in flight, so stopping the instance
// exercises the window in which the scheduler can no longer report its outcome.
func startDelayInstance(t *testing.T, store *sqlite.Store) *Instance {
	t.Helper()
	assembly := delayAssembly()
	i, err := New(context.Background(), "inst_delay", protocol.InstanceKey{
		AssemblyID: "sys_delay", Version: "1.0.0",
	}, &assembly, 2, store)
	if err != nil {
		t.Fatalf("create instance: %v", err)
	}
	if err := i.Start(); err != nil {
		t.Fatalf("start instance: %v", err)
	}
	t.Cleanup(func() { _ = i.Stop() })
	return i
}

func startBlockingExecution(t *testing.T, i *Instance) *execution.Execution {
	t.Helper()
	exec, err := i.Execute(context.Background(), map[string]any{"duration": "1m"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if exec.Status() == execution.StatusRunning && exec.InFlight() > 0 {
			return exec
		}
		if exec.IsTerminal() {
			t.Fatalf("execution reached %s before it was observed running (error %q)", exec.Status(), exec.Error())
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("execution never reported in flight (status %s, in flight %d)", exec.Status(), exec.InFlight())
	return nil
}

// An execution must not outlive its instance as "running". Once the instance
// context is cancelled the scheduler has already exited, so the drain in Stop is
// the only thing that can give a still-running execution a terminal state.
func TestStopDrivesInFlightExecutionsToTerminal(t *testing.T) {
	store := newTestStore(t)
	i := startDelayInstance(t, store)
	exec := startBlockingExecution(t, i)

	if err := i.Stop(); err != nil {
		t.Fatalf("stop instance: %v", err)
	}

	if got := exec.Status(); got != execution.StatusFailed {
		t.Fatalf("execution status = %s, want %s", got, execution.StatusFailed)
	}
	if got := exec.InFlight(); got != 0 {
		t.Fatalf("execution in flight = %d, want 0 on a terminal execution", got)
	}
	if exec.Error() == "" {
		t.Fatal("execution error is empty, want the reason it was abandoned")
	}

	// Wait observes the same terminal signal a consumer would, so it must not
	// block once the execution is terminal.
	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := exec.Wait(waitCtx); err == nil {
		t.Fatal("Wait() = nil, want the terminal failure of an abandoned execution")
	}
}

// Repeated stops must not reopen or disturb an execution that is already
// terminal.
func TestStopIsIdempotentForExecutions(t *testing.T) {
	store := newTestStore(t)
	i := startDelayInstance(t, store)
	exec := startBlockingExecution(t, i)

	for attempt := 0; attempt < 3; attempt++ {
		if err := i.Stop(); err != nil {
			t.Fatalf("stop instance (attempt %d): %v", attempt, err)
		}
	}
	if got := exec.Status(); got != execution.StatusFailed {
		t.Fatalf("execution status = %s, want %s", got, execution.StatusFailed)
	}
}

// The terminal state has to reach durable storage. A drained execution that is
// only terminal in memory is reported as running again after a restart, which is
// the user-visible half of the same bug.
func TestStopPersistsTerminalStateToStorage(t *testing.T) {
	store := newTestStore(t)
	i := startDelayInstance(t, store)
	exec := startBlockingExecution(t, i)

	if err := i.Stop(); err != nil {
		t.Fatalf("stop instance: %v", err)
	}

	// Read through a fresh repository so the in-memory copy cannot answer.
	reloaded, ok := execution.NewExecutionStore(store).Get(exec.ID)
	if !ok {
		t.Fatalf("execution %s not found in storage after the instance stopped", exec.ID)
	}
	if got := reloaded.Status(); got != execution.StatusFailed {
		t.Fatalf("persisted status = %s, want %s", got, execution.StatusFailed)
	}
	if got := reloaded.InFlight(); got != 0 {
		t.Fatalf("persisted in flight = %d, want 0", got)
	}
}

// An instance restored after a restart has no scheduler, engine or bus, so an
// execution persisted as running could never advance again. Restoring must not
// leave it looking live.
func TestRestoreInstanceDrainsExecutionsLeftRunning(t *testing.T) {
	store := newTestStore(t)
	instanceID := shared.ID("inst_interrupted")

	// Persist an execution that was mid-flight when the process died.
	exec, err := execution.NewExecution(&types.ExecutionBlueprint{
		Metadata: shared.Metadata{ID: "sys_interrupted"},
		Nodes: map[shared.ID]types.ExecutionNode{
			"cap_slow": {Capability: shared.Capability{Metadata: shared.Metadata{ID: "cap_slow"}}},
		},
		EntryCapabilityIDs: []shared.ID{"cap_slow"},
	}, shared.NewID("request_"), instanceID)
	if err != nil {
		t.Fatalf("new execution: %v", err)
	}
	if err := exec.Start(map[string]any{}, 1); err != nil {
		t.Fatalf("start execution: %v", err)
	}
	if err := execution.NewExecutionStore(store).Add(exec); err != nil {
		t.Fatalf("add execution: %v", err)
	}

	restored := restoreInstance(
		context.Background(),
		metadata{ID: string(instanceID), Status: StatusRunning},
		execution.NewExecutionStore(store),
		event.NewStore(store),
	)
	if restored.Status() != StatusFailed {
		t.Fatalf("restored instance status = %s, want %s", restored.Status(), StatusFailed)
	}

	reloaded, ok := execution.NewExecutionStore(store).Get(exec.ID)
	if !ok {
		t.Fatalf("execution %s not found after restore", exec.ID)
	}
	if got := reloaded.Status(); got != execution.StatusFailed {
		t.Fatalf("restored execution status = %s, want %s", got, execution.StatusFailed)
	}
	if reloaded.Error() == "" {
		t.Fatal("restored execution error is empty, want the reason no runtime could resume it")
	}
}

// TestStopSweepsOnlyAfterRuntimeGoroutinesFinish pins the ordering the drain
// depends on.
//
// Stop used to sweep before awaiting the runtime goroutines, because once the
// instance context is cancelled the scheduler has exited and can no longer report
// an outcome. That reasoning is sound for the scheduler and wrong for the engine,
// which is still draining detached work and can still record a real completion
// for it. The sweep therefore claimed a failure for work that was in fact
// finishing successfully, and nothing could later correct the record.
//
// The instance's own wait group stands in for that drain, which makes the ordering
// observable without reproducing the whole detach path. The stand-in goroutine
// reports what the execution looked like *while it still held the wait group*: a
// correct Stop has not swept yet, so the execution is still unfinished.
func TestStopSweepsOnlyAfterRuntimeGoroutinesFinish(t *testing.T) {
	store := newTestStore(t)
	i := startDelayInstance(t, store)
	exec := startBlockingExecution(t, i)

	sweptDuringDrain := make(chan bool, 1)
	drained := make(chan struct{})
	i.wg.Add(1)
	go func() {
		defer i.wg.Done()
		defer close(drained)
		// Released by the cancel inside Stop, so from here on Stop is inside the
		// window it is supposed to be draining.
		<-i.ctx.Done()
		time.Sleep(50 * time.Millisecond)
		sweptDuringDrain <- exec.IsTerminal()
		exec.MarkCompleted()
	}()

	if err := i.Stop(); err != nil {
		t.Fatalf("stop instance: %v", err)
	}

	select {
	case <-drained:
	default:
		t.Fatal("Stop returned before the drain finished")
	}

	if alreadyTerminal := <-sweptDuringDrain; alreadyTerminal {
		t.Errorf("the execution was already %s while the drain was still running: the sweep ran before the runtime goroutines finished, so a drain that was about to succeed was recorded as failed", exec.Status())
	}
	if got := exec.Status(); got != execution.StatusCompleted {
		t.Fatalf("execution status = %s, want %s: the drain's own outcome must be the one that survives", got, execution.StatusCompleted)
	}
}

// TestSweepLeavesDetachedExecutionsForRestore covers the other half of the same
// contract. A detached task owns its own execution and is the only thing that can
// advance it, so the sweep must not report a failure on its behalf. If the drain
// budget runs out, the accurate statement is that the process ended with no
// runtime to resume the work -- which is what the restore path reports.
func TestSweepLeavesDetachedExecutionsForRestore(t *testing.T) {
	store := newTestStore(t)
	executions := execution.NewExecutionStore(store)
	instanceID := shared.ID("inst_detached")

	blueprint := &types.ExecutionBlueprint{
		Metadata: shared.Metadata{ID: "sys_detached"},
		Nodes: map[shared.ID]types.ExecutionNode{
			"cap_slow": {Capability: shared.Capability{Metadata: shared.Metadata{ID: "cap_slow"}}},
		},
		EntryCapabilityIDs: []shared.ID{"cap_slow"},
	}

	// A detached task is its own execution, linked back to the parent that handed
	// the work off, and it is still running when the drain budget runs out.
	detached, err := execution.NewDetachedTask(blueprint, shared.NewID("request_"), instanceID, shared.NewID("exec_parent"))
	if err != nil {
		t.Fatalf("NewDetachedTask: %v", err)
	}
	if err := detached.Start(map[string]any{}, 1); err != nil {
		t.Fatalf("start detached task: %v", err)
	}
	if err := executions.Add(detached); err != nil {
		t.Fatalf("add detached task: %v", err)
	}

	ordinary, err := execution.NewExecution(blueprint, shared.NewID("request_"), instanceID)
	if err != nil {
		t.Fatalf("new execution: %v", err)
	}
	if err := ordinary.Start(map[string]any{}, 1); err != nil {
		t.Fatalf("start execution: %v", err)
	}
	if err := executions.Add(ordinary); err != nil {
		t.Fatalf("add execution: %v", err)
	}

	sweepAbandonedExecutions(executions, instanceID, stoppedBeforeFinished(string(instanceID)))

	if !ordinary.IsTerminal() || ordinary.Status() != execution.StatusFailed {
		t.Errorf("ordinary execution status = %s, want %s: work the instance owns must still be given a terminal state", ordinary.Status(), execution.StatusFailed)
	}
	if detached.IsTerminal() {
		t.Errorf("the detached task was driven to %s; the sweep must leave it for the restore path, which can report accurately", detached.Status())
	}

	reloaded, ok := executions.Get(detached.ID)
	if !ok {
		t.Fatalf("detached task %s not found", detached.ID)
	}
	if reloaded.IsTerminal() {
		t.Error("the detached task was persisted as terminal, so its truthful unfinished state would be lost on restart")
	}

	// A restart is what finally gives it a terminal state, and the reason it
	// records is the accurate one rather than "the instance stopped".
	restored := restoreInstance(
		context.Background(),
		metadata{ID: string(instanceID), Status: StatusRunning},
		execution.NewExecutionStore(store),
		event.NewStore(store),
	)
	t.Cleanup(func() { _ = restored.Stop() })

	recovered, ok := execution.NewExecutionStore(store).Get(detached.ID)
	if !ok {
		t.Fatalf("detached task %s not found after restore", detached.ID)
	}
	if !recovered.IsTerminal() {
		t.Fatalf("restored detached task status = %s, want a terminal state: nothing can resume it", recovered.Status())
	}
	if recovered.Error() == "" {
		t.Fatal("restored detached task error is empty, want the reason no runtime could resume it")
	}
}
