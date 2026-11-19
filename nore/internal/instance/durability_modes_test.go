package instance

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/assembly"
	"github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/storage"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

func TestNormalizeDurability(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{in: "", want: DurabilityHistory},
		{in: "none", want: DurabilityNone},
		{in: "history", want: DurabilityHistory},
		{in: "durable", want: DurabilityDurable},
		{in: " DURABLE ", want: DurabilityDurable},
	}
	for _, tc := range cases {
		got, err := NormalizeDurability(tc.in)
		if err != nil {
			t.Fatalf("NormalizeDurability(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("NormalizeDurability(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeDurabilityRejectsUnknownMode(t *testing.T) {
	_, err := NormalizeDurability("sometimes")
	if err == nil {
		t.Fatal("expected an error for an unknown durability mode")
	}
	for _, want := range []string{"sometimes", DurabilityNone, DurabilityHistory, DurabilityDurable} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// TestManagerPassesDurabilityToLiveInstance proves GetOrCreate does not fall
// back to the default mode when the manager was configured otherwise.
func TestManagerPassesDurabilityToLiveInstance(t *testing.T) {
	store := newTestStore(t)
	repo := assembly.NewRepository(store)
	key := protocol.InstanceKey{AssemblyID: "sys_durability", Version: "1.0.0", Hash: "h", Env: "dev"}
	if _, _, err := repo.Register(context.Background(), assembly.RegisteredAssembly{
		Key: key,
		Assembly: shared.Assembly{
			Metadata: shared.Metadata{Name: key.AssemblyID, Version: key.Version},
			Specification: shared.AssemblySpec{
				Capabilities: []shared.Capability{{
					Metadata: shared.Metadata{ID: "cap_set", Name: "sys.set", Version: "1.0.0"},
					Type:     shared.CoreName("set"),
				}},
			},
		},
	}); err != nil {
		t.Fatalf("register assembly: %v", err)
	}

	manager, err := NewManager(context.Background(), 1, 0, store, repo, WithDurability(DurabilityNone))
	if err != nil {
		t.Fatalf("create manager: %v", err)
	}

	inst, _, err := manager.GetOrCreate(context.Background(), key)
	if err != nil {
		t.Fatalf("get or create instance: %v", err)
	}
	t.Cleanup(func() { _ = inst.Stop() })

	if inst.durability != DurabilityNone {
		t.Errorf("instance durability = %q, want %q from the manager", inst.durability, DurabilityNone)
	}
}

// TestDurabilityNoneKeepsHistoryInMemory covers the "none" contract: executions
// remain observable while the instance lives, but neither executions nor events
// reach the persistent store.
func TestDurabilityNoneKeepsHistoryInMemory(t *testing.T) {
	store := newTestStore(t)
	assembly := blockingAssembly()
	inst, err := New(context.Background(), "inst_none", protocol.InstanceKey{
		AssemblyID: "sys_blocking", Version: "1.0.0",
	}, &assembly, 2, store, WithDurability(DurabilityNone))
	if err != nil {
		t.Fatalf("create instance: %v", err)
	}
	if err := inst.Start(); err != nil {
		t.Fatalf("start instance: %v", err)
	}
	t.Cleanup(func() { _ = inst.Stop() })

	exec := runToCompletion(t, inst)
	waitForEvents(t, inst, exec.ID)

	if keys := listPrefix(t, store, "executions"); len(keys) != 0 {
		t.Fatalf("persistent store holds executions in none mode: %v", keys)
	}
	if keys := listPrefix(t, store, "events"); len(keys) != 0 {
		t.Fatalf("persistent store holds events in none mode: %v", keys)
	}

	if _, err := inst.GetExecution(context.Background(), exec.ID); err != nil {
		t.Fatalf("execution is not observable in memory: %v", err)
	}
}

// TestDurabilityHistoryPersistsHistory covers the "history" contract: the same
// execution and its events are written to the persistent store.
func TestDurabilityHistoryPersistsHistory(t *testing.T) {
	store := newTestStore(t)
	assembly := blockingAssembly()
	inst, err := New(context.Background(), "inst_history", protocol.InstanceKey{
		AssemblyID: "sys_blocking", Version: "1.0.0",
	}, &assembly, 2, store, WithDurability(DurabilityHistory))
	if err != nil {
		t.Fatalf("create instance: %v", err)
	}
	if err := inst.Start(); err != nil {
		t.Fatalf("start instance: %v", err)
	}
	t.Cleanup(func() { _ = inst.Stop() })

	exec := runToCompletion(t, inst)
	waitForPersistedExecution(t, store, exec.ID)
	waitForPersistedEvents(t, store, exec.ID)
}

// TestDurabilityDurablePersistsThroughTransitions covers the "durable" contract:
// the runtime commits each transition itself, so a completed execution reaches
// the store without the event-driven persister, and no dispatch intent is left
// behind once the work it announced has run.
func TestDurabilityDurablePersistsThroughTransitions(t *testing.T) {
	store := newTestStore(t)
	assembly := blockingAssembly()
	inst, err := New(context.Background(), "inst_durable", protocol.InstanceKey{
		AssemblyID: "sys_blocking", Version: "1.0.0",
	}, &assembly, 2, store, WithDurability(DurabilityDurable))
	if err != nil {
		t.Fatalf("create instance: %v", err)
	}
	if err := inst.Start(); err != nil {
		t.Fatalf("start instance: %v", err)
	}
	t.Cleanup(func() { _ = inst.Stop() })

	if inst.execPersister != nil {
		t.Fatal("durable mode must not run the snapshot persister; the scheduler owns commits")
	}

	exec := runToCompletion(t, inst)
	waitForPersistedStatus(t, store, exec.ID, execution.StatusCompleted)
	waitFor(t, 10*time.Second, func() bool {
		return len(listPrefix(t, store, "dispatch_intents")) == 0
	}, "dispatch intents were not cleared after their work ran")
}

func runToCompletion(t *testing.T, inst *Instance) *execution.Execution {
	t.Helper()
	exec, err := inst.Execute(context.Background(), shortParams())
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	waitFor(t, 10*time.Second, exec.IsTerminal, "execution did not reach a terminal state")
	if exec.Status() != execution.StatusCompleted {
		t.Fatalf("execution status = %s, want %s (error %q)", exec.Status(), execution.StatusCompleted, exec.Error())
	}
	return exec
}

func waitFor(t *testing.T, timeout time.Duration, done func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(message)
}

func waitForEvents(t *testing.T, inst *Instance, executionID shared.ID) {
	t.Helper()
	waitFor(t, 10*time.Second, func() bool {
		events, err := inst.ListExecutionEvents(context.Background(), executionID)
		return err == nil && len(events) > 0
	}, "execution events were not observable in memory")
}

func waitForPersistedExecution(t *testing.T, store storage.Store, executionID shared.ID) {
	t.Helper()
	waitFor(t, 10*time.Second, func() bool {
		_, err := store.Get(context.Background(), "executions/"+string(executionID))
		return err == nil
	}, "execution was not persisted in history mode")
}

func waitForPersistedEvents(t *testing.T, store storage.Store, executionID shared.ID) {
	t.Helper()
	prefix, err := storage.Prefix("events", string(executionID))
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, func() bool {
		keys, err := store.List(context.Background(), prefix)
		return err == nil && len(keys) > 0
	}, "execution events were not persisted in history mode")
}

func listPrefix(t *testing.T, store storage.Store, prefix string) []string {
	t.Helper()
	full, err := storage.Prefix(prefix)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := store.List(context.Background(), full)
	if err != nil {
		t.Fatalf("list %s: %v", prefix, err)
	}
	return keys
}
