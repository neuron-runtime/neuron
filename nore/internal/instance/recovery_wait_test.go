package instance

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/event"
	"github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/storage"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

// recoveredWaitFixture holds a durable StatusWaiting snapshot loaded from the
// store and a live instance whose RecoverExecution is the boundary under test.
// The instance is deliberately not started: recovery publishes and commits
// synchronously, so the resulting bus events and dispatch intents remain
// observable without an engine consuming them.
type recoveredWaitFixture struct {
	inst         *Instance
	store        storage.Store
	execution    *execution.Execution
	capabilityID shared.ID
	ready        event.Subscription
	failed       event.Subscription
}

// newRecoveredWaitFixture drives a capability into a durable wait, snapshots
// and reloads it without a blueprint (as recovery does), persists the snapshot,
// and loads it back from the store. drive must leave the execution fully
// waiting; the wait itself is created only through execution mutators, never by
// fabricating wait state in this test.
func newRecoveredWaitFixture(t *testing.T, capabilityID shared.ID, drive func(*execution.Execution)) *recoveredWaitFixture {
	t.Helper()
	ctx := context.Background()
	store := newTestStore(t)
	key := protocol.InstanceKey{AssemblyID: "sys_wait_recovery", Version: "1.0.0", Hash: "hash_wait_recovery"}
	instanceID := shared.ID("inst_wait_recovery")

	exec, err := execution.NewExecution(recoveryBlueprint(capabilityID), shared.NewID("request_"), instanceID, key.AssemblyID, key.Version, key.Hash, key.Env)
	if err != nil {
		t.Fatalf("new execution: %v", err)
	}
	if err := exec.Start(map[string]any{}, 1); err != nil {
		t.Fatalf("start execution: %v", err)
	}
	if err := exec.MarkCapabilityReady(capabilityID, map[string]any{}); err != nil {
		t.Fatalf("mark capability ready: %v", err)
	}
	if err := exec.MarkCapabilityRunning(capabilityID); err != nil {
		t.Fatalf("mark capability running: %v", err)
	}
	drive(exec)
	if exec.Status() != execution.StatusWaiting {
		t.Fatalf("test setup: status = %s, want %s before snapshot", exec.Status(), execution.StatusWaiting)
	}

	data, err := execution.MarshalExecution(exec)
	if err != nil {
		t.Fatalf("marshal execution: %v", err)
	}
	snapshot, err := execution.UnmarshalExecution(data)
	if err != nil {
		t.Fatalf("unmarshal execution: %v", err)
	}
	if snapshot.Status() != execution.StatusWaiting {
		t.Fatalf("test setup: snapshot status = %s, want durable %s", snapshot.Status(), execution.StatusWaiting)
	}

	execStore := execution.NewExecutionStore(store)
	if err := execStore.Add(ctx, snapshot); err != nil {
		t.Fatalf("persist waiting snapshot: %v", err)
	}
	loaded, err := execStore.Get(ctx, snapshot.ID)
	if err != nil {
		t.Fatalf("load waiting snapshot: %v", err)
	}
	if loaded.Status() != execution.StatusWaiting {
		t.Fatalf("test setup: loaded status = %s, want durable %s", loaded.Status(), execution.StatusWaiting)
	}
	if loaded.Blueprint != nil {
		t.Fatal("test setup: loaded execution already carries a blueprint; recovery must attach one")
	}

	assembly := recoveryAssembly(key, capabilityID)
	inst, err := New(ctx, string(instanceID), key, &assembly, 1, store, WithDurability(DurabilityDurable))
	if err != nil {
		t.Fatalf("create durable instance: %v", err)
	}
	t.Cleanup(func() { _ = inst.Stop() })

	ready, err := inst.Bus().Subscribe(event.CapabilityReady, 16)
	if err != nil {
		t.Fatalf("subscribe CapabilityReady: %v", err)
	}
	t.Cleanup(func() { _ = ready.Close() })
	failed, err := inst.Bus().Subscribe(event.ExecutionFailed, 16)
	if err != nil {
		t.Fatalf("subscribe ExecutionFailed: %v", err)
	}
	t.Cleanup(func() { _ = failed.Close() })

	return &recoveredWaitFixture{
		inst: inst, store: store, execution: loaded,
		capabilityID: capabilityID, ready: ready, failed: failed,
	}
}

func drainRecoveredWaitEvents(t *testing.T, sub event.Subscription) []event.Event {
	t.Helper()
	got := make([]event.Event, 0)
	for {
		select {
		case evt, open := <-sub.Events():
			if !open {
				return got
			}
			got = append(got, evt)
		default:
			return got
		}
	}
}

func recoveredWaitDispatchIntents(t *testing.T, store storage.Store) []*execution.DispatchIntent {
	t.Helper()
	keys := listPrefix(t, store, "dispatch_intents")
	intents := make([]*execution.DispatchIntent, 0, len(keys))
	for _, key := range keys {
		data, err := store.Get(context.Background(), key)
		if err != nil {
			t.Fatalf("read dispatch intent %s: %v", key, err)
		}
		var intent execution.DispatchIntent
		if err := json.Unmarshal(data, &intent); err != nil {
			t.Fatalf("decode dispatch intent %s: %v", key, err)
		}
		intents = append(intents, &intent)
	}
	return intents
}

// TestInstanceRecoverExecutionKeepsNonDueSignalWaitWaiting proves the instance
// boundary preserves a healthy wait: a non-due signal wait is neither failed nor
// dispatched, occupies no worker, and stays waiting in the durable snapshot.
func TestInstanceRecoverExecutionKeepsNonDueSignalWaitWaiting(t *testing.T) {
	f := newRecoveredWaitFixture(t, "cap", func(exec *execution.Execution) {
		if err := exec.EnterWait("cap", execution.WaitSpec{Type: execution.WaitTypeSignal, ExpectedSignal: "approval"}); err != nil {
			t.Fatalf("enter signal wait: %v", err)
		}
	})

	if err := f.inst.RecoverExecution(context.Background(), f.execution, f.inst.Blueprint, nil); err != nil {
		t.Fatalf("RecoverExecution: %v", err)
	}

	if f.execution.Blueprint != f.inst.Blueprint {
		t.Fatal("instance did not attach the recompiled blueprint to the recovered execution")
	}
	if got := f.execution.Status(); got != execution.StatusWaiting {
		t.Errorf("status = %s, want %s", got, execution.StatusWaiting)
	}
	if state := f.execution.CapabilityState("cap"); state.Status != execution.CapabilityWaiting {
		t.Errorf("capability status = %s, want %s", state.Status, execution.CapabilityWaiting)
	}
	if got := f.execution.InFlight(); got != 0 {
		t.Errorf("in-flight = %d, want 0", got)
	}
	if got := drainRecoveredWaitEvents(t, f.ready); len(got) != 0 {
		t.Errorf("published %d CapabilityReady events for a non-due wait, want 0: %v", len(got), got)
	}
	if got := drainRecoveredWaitEvents(t, f.failed); len(got) != 0 {
		t.Errorf("published %d ExecutionFailed events for a healthy wait, want 0: %v", len(got), got)
	}
	if got := recoveredWaitDispatchIntents(t, f.store); len(got) != 0 {
		t.Errorf("committed %d dispatch intents for a non-due wait, want 0: %v", len(got), got)
	}

	reloaded, err := execution.NewExecutionStore(f.store).Get(context.Background(), f.execution.ID)
	if err != nil {
		t.Fatalf("reload recovered execution: %v", err)
	}
	if got := reloaded.Status(); got != execution.StatusWaiting {
		t.Errorf("persisted status = %s, want %s", got, execution.StatusWaiting)
	}
	if got := reloaded.CapabilityState("cap").Status; got != execution.CapabilityWaiting {
		t.Errorf("persisted capability status = %s, want %s", got, execution.CapabilityWaiting)
	}
}

// TestInstanceRecoverExecutionDispatchesElapsedWaitsExactlyOnce covers the
// elapsed half of the recovery contract at the instance boundary. Each wait is
// created through the real execution mutators and then aged past its due time,
// as a wait whose timer fired while the process was down. Recovery must clear
// the wait, commit exactly one ready intent, and publish exactly one readiness.
func TestInstanceRecoverExecutionDispatchesElapsedWaitsExactlyOnce(t *testing.T) {
	past := time.Now().UTC().Add(-time.Hour)
	cases := []struct {
		name  string
		drive func(*execution.Execution)
	}{
		{
			name: "timer",
			drive: func(exec *execution.Execution) {
				until := past
				if err := exec.EnterWait("cap", execution.WaitSpec{Type: execution.WaitTypeTimer, WaitUntil: &until}); err != nil {
					t.Fatalf("enter timer wait: %v", err)
				}
			},
		},
		{
			name: "retry",
			drive: func(exec *execution.Execution) {
				if err := exec.BeginRetryWait("cap", past); err != nil {
					t.Fatalf("begin retry wait: %v", err)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRecoveredWaitFixture(t, "cap", tc.drive)

			if err := f.inst.RecoverExecution(context.Background(), f.execution, f.inst.Blueprint, nil); err != nil {
				t.Fatalf("RecoverExecution: %v", err)
			}
			if f.execution.Blueprint != f.inst.Blueprint {
				t.Fatal("instance did not attach the recompiled blueprint to the recovered execution")
			}

			ready := drainRecoveredWaitEvents(t, f.ready)
			if len(ready) != 1 {
				t.Fatalf("published %d CapabilityReady events, want 1: %v", len(ready), ready)
			}
			if got := ready[0].Metadata.CapabilityID; got != "cap" {
				t.Errorf("readiness for %q, want cap", got)
			}
			if got := drainRecoveredWaitEvents(t, f.failed); len(got) != 0 {
				t.Errorf("published %d ExecutionFailed events, want 0: %v", len(got), got)
			}

			intents := recoveredWaitDispatchIntents(t, f.store)
			if len(intents) != 1 {
				t.Fatalf("committed %d dispatch intents, want 1: %v", len(intents), intents)
			}
			if got := intents[0].CapabilityID; got != "cap" {
				t.Errorf("intent capability = %q, want cap", got)
			}
			if !intents[0].ReadyToRun {
				t.Error("committed intent is not ready to run")
			}

			if got := f.execution.Status(); got != execution.StatusRunning {
				t.Errorf("status = %s, want %s", got, execution.StatusRunning)
			}
			state := f.execution.CapabilityState("cap")
			if state.Status != execution.CapabilityReady {
				t.Errorf("capability status = %s, want %s", state.Status, execution.CapabilityReady)
			}
			if state.WaitType != "" || state.WaitUntil != nil || state.NextAttemptAt != nil {
				t.Errorf("wait metadata not cleared on resume: %+v", state)
			}
			if got := f.execution.InFlight(); got != 1 {
				t.Errorf("in-flight = %d, want 1", got)
			}

			reloaded, err := execution.NewExecutionStore(f.store).Get(context.Background(), f.execution.ID)
			if err != nil {
				t.Fatalf("reload recovered execution: %v", err)
			}
			if got := reloaded.Status(); got != execution.StatusRunning {
				t.Errorf("persisted status = %s, want %s", got, execution.StatusRunning)
			}
		})
	}
}
