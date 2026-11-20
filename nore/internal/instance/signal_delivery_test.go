package instance

import (
	"context"
	"errors"
	"testing"

	"github.com/neuron-runtime/neuron/nore/internal/assembly"
	"github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/execution/scheduler"
	"github.com/neuron-runtime/neuron/nore/internal/storage"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

func signalTestKey() protocol.InstanceKey {
	return protocol.InstanceKey{AssemblyID: "sys_signal", Version: "1.0.0", Hash: "h", Env: "dev"}
}

func newSignalInstance(t *testing.T, store storage.Store, id string, durability string) *Instance {
	t.Helper()
	key := signalTestKey()
	asm := recoveryAssembly(key, "cap")
	inst, err := New(context.Background(), id, key, &asm, 1, store, WithDurability(durability))
	if err != nil {
		t.Fatalf("New instance: %v", err)
	}
	t.Cleanup(func() { _ = inst.Stop() })
	return inst
}

// durableWaitExecution persists an execution parked in a signal wait, owned by
// instanceID, using only execution mutators to create the wait state.
func durableWaitExecution(t *testing.T, store storage.Store, instanceID shared.ID, signalName, schema string) *execution.Execution {
	t.Helper()
	ctx := context.Background()
	exec, err := execution.NewExecution(recoveryBlueprint("cap"), shared.NewID("request_"), instanceID, "sys", "1.0.0", "h", "dev")
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	if err := exec.Start(map[string]any{}, 1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := exec.MarkCapabilityReady("cap", map[string]any{}); err != nil {
		t.Fatalf("MarkCapabilityReady: %v", err)
	}
	if err := exec.MarkCapabilityRunning("cap"); err != nil {
		t.Fatalf("MarkCapabilityRunning: %v", err)
	}
	if err := exec.EnterWait("cap", execution.WaitSpec{Type: execution.WaitTypeSignal, ExpectedSignal: signalName, PayloadSchema: schema}); err != nil {
		t.Fatalf("EnterWait: %v", err)
	}
	if err := execution.NewExecutionStore(store).Add(ctx, exec); err != nil {
		t.Fatalf("persist waiting execution: %v", err)
	}
	return exec
}

func TestInstanceDeliverSignalDurableWiresWaitStore(t *testing.T) {
	store := newTestStore(t)
	inst := newSignalInstance(t, store, "inst_signal", DurabilityDurable)
	exec := durableWaitExecution(t, store, "inst_signal", "approved", "")

	ctx := context.Background()
	result, err := inst.DeliverSignal(ctx, SignalDeliveryRequest{
		ExecutionID: exec.ID,
		DeliveryID:  "del_1",
		SignalName:  "approved",
		Payload:     map[string]any{"status": "approved"},
	})
	if err != nil {
		t.Fatalf("DeliverSignal: %v", err)
	}
	if result.Replay || result.DeliveryID != "del_1" {
		t.Fatalf("result = %+v, want a committed delivery", result)
	}

	loaded, err := inst.store.Get(ctx, exec.ID)
	if err != nil {
		t.Fatalf("load execution: %v", err)
	}
	if state := loaded.CapabilityState("cap"); state.Status != execution.CapabilityReady {
		t.Fatalf("durable capability status = %s, want ready", state.Status)
	}
	execStore, ok := inst.store.(*execution.ExecutionStore)
	if !ok {
		t.Fatal("durable instance did not use an ExecutionStore")
	}
	if _, err := execStore.GetSignalDelivery(ctx, exec.ID, "del_1"); err != nil {
		t.Fatalf("durable delivery missing after successful delivery: %v", err)
	}
}

func TestInstanceDeliverSignalEnforcesDurableOwnership(t *testing.T) {
	store := newTestStore(t)
	inst := newSignalInstance(t, store, "inst_signal", DurabilityDurable)
	foreign := durableWaitExecution(t, store, "inst_other", "approved", "")

	ctx := context.Background()
	_, err := inst.DeliverSignal(ctx, SignalDeliveryRequest{
		ExecutionID: foreign.ID,
		DeliveryID:  "del_cross",
		SignalName:  "approved",
		Payload:     map[string]any{},
	})
	if !errors.Is(err, ErrExecutionNotFound) {
		t.Fatalf("cross-instance delivery error = %v, want %v", err, ErrExecutionNotFound)
	}

	_, err = inst.DeliverSignal(ctx, SignalDeliveryRequest{
		ExecutionID: shared.NewID("request_missing"),
		DeliveryID:  "del_missing",
		SignalName:  "approved",
		Payload:     map[string]any{},
	})
	if !errors.Is(err, ErrExecutionNotFound) {
		t.Fatalf("missing execution error = %v, want %v", err, ErrExecutionNotFound)
	}
}

func TestInstanceDeliverSignalNonDurableUnchanged(t *testing.T) {
	store := newTestStore(t)
	inst := newSignalInstance(t, store, "inst_none", DurabilityNone)
	ctx := context.Background()
	exec, err := execution.NewExecution(recoveryBlueprint("cap"), shared.NewID("request_"), shared.ID(inst.ID), "sys", "1.0.0", "h", "dev")
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	if err := exec.Start(map[string]any{}, 1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := inst.store.Add(ctx, exec); err != nil {
		t.Fatalf("store.Add: %v", err)
	}

	_, err = inst.DeliverSignal(ctx, SignalDeliveryRequest{
		ExecutionID: exec.ID,
		DeliveryID:  "del_1",
		SignalName:  "approved",
		Payload:     map[string]any{},
	})
	if !errors.Is(err, scheduler.ErrWaitStoreUnavailable) {
		t.Fatalf("non-durable delivery error = %v, want %v", err, scheduler.ErrWaitStoreUnavailable)
	}
}

func TestManagerInstanceForExecution(t *testing.T) {
	store := newTestStore(t)
	inst := newSignalInstance(t, store, "inst_signal", DurabilityDurable)
	exec := durableWaitExecution(t, store, "inst_signal", "approved", "")

	manager, err := NewManager(context.Background(), 1, 0, store, assembly.NewRepository(store))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	manager.instancesByID[inst.ID] = inst

	ctx := context.Background()
	got, err := manager.InstanceForExecution(ctx, exec.ID)
	if err != nil {
		t.Fatalf("InstanceForExecution: %v", err)
	}
	if got != inst {
		t.Fatalf("InstanceForExecution returned %v, want the owning instance", got)
	}

	if _, err := manager.InstanceForExecution(ctx, shared.NewID("request_missing")); !errors.Is(err, ErrExecutionNotFound) {
		t.Fatalf("unknown execution error = %v, want %v", err, ErrExecutionNotFound)
	}

	ghost := durableWaitExecution(t, store, "inst_ghost", "approved", "")
	if _, err := manager.InstanceForExecution(ctx, ghost.ID); !errors.Is(err, ErrExecutionNotFound) {
		t.Fatalf("execution with no live instance error = %v, want %v", err, ErrExecutionNotFound)
	}
}
