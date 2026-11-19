package instance

import (
	"context"
	"testing"

	"github.com/neuron-runtime/neuron/nore/internal/assembly"
	"github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/nore/internal/types"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

// persistInterruptedExecution writes an execution that was still running when
// the previous process ended, so reconcile has something unfinished to handle.
func persistInterruptedExecution(t *testing.T, store storage.Store, instanceID shared.ID) *execution.Execution {
	t.Helper()
	exec, err := execution.NewExecution(&types.ExecutionBlueprint{
		Metadata: shared.Metadata{ID: "sys_interrupted"},
		Nodes: map[shared.ID]types.ExecutionNode{
			"cap_slow": {Capability: shared.Capability{Metadata: shared.Metadata{ID: "cap_slow"}}},
		},
		EntryCapabilityIDs: []shared.ID{"cap_slow"},
	}, shared.NewID("request_"), instanceID, "", "", "", "")
	if err != nil {
		t.Fatalf("new execution: %v", err)
	}
	if err := exec.Start(map[string]any{}, 1); err != nil {
		t.Fatalf("start execution: %v", err)
	}
	if err := execution.NewExecutionStore(store).Add(context.Background(), exec); err != nil {
		t.Fatalf("add execution: %v", err)
	}
	return exec
}

func persistInterruptedInstance(t *testing.T, store storage.Store, instanceID string) {
	t.Helper()
	if err := newMetadataStore(store).Save(context.Background(), metadata{
		ID:     instanceID,
		Status: StatusRunning,
	}); err != nil {
		t.Fatalf("save instance metadata: %v", err)
	}
}

func newTestManager(t *testing.T, store storage.Store, durability string) *Manager {
	t.Helper()
	manager, err := NewManager(
		context.Background(),
		1,
		0,
		store,
		assembly.NewRepository(store),
		WithDurability(durability),
	)
	if err != nil {
		t.Fatalf("create manager: %v", err)
	}
	return manager
}

// TestReconcileFailsInterruptedExecutionsInNonDurableModes covers R4-28: with
// durability off, an execution interrupted by a restart must be persisted as
// failed and must never be left reporting itself as running.
func TestReconcileFailsInterruptedExecutionsInNonDurableModes(t *testing.T) {
	cases := []struct {
		name       string
		durability string
	}{
		{name: "default", durability: ""},
		{name: "history", durability: "history"},
		{name: "none", durability: "none"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestStore(t)
			instanceID := shared.ID("inst_non_durable")
			persistInterruptedInstance(t, store, string(instanceID))
			exec := persistInterruptedExecution(t, store, instanceID)

			manager := newTestManager(t, store, tc.durability)

			reloaded, err := execution.NewExecutionStore(store).Get(context.Background(), exec.ID)
			if err != nil {
				t.Fatalf("load execution %s after reconcile: %v", exec.ID, err)
			}
			if reloaded.Status() == execution.StatusRunning {
				t.Fatal("interrupted execution is still reported as running in non-durable mode")
			}
			if !reloaded.IsTerminal() {
				t.Fatalf("interrupted execution status = %s, want a terminal state", reloaded.Status())
			}
			if reloaded.Status() != execution.StatusFailed {
				t.Fatalf("interrupted execution status = %s, want %s", reloaded.Status(), execution.StatusFailed)
			}
			if reloaded.Error() == "" {
				t.Fatal("interrupted execution error is empty, want the reason no runtime could resume it")
			}

			restored, ok := manager.GetByID(string(instanceID))
			if !ok {
				t.Fatalf("instance %s was not restored", instanceID)
			}
			if restored.Status() != StatusFailed {
				t.Fatalf("restored instance status = %s, want %s", restored.Status(), StatusFailed)
			}
		})
	}
}

// TestReconcileKeepsInterruptedExecutionsInDurableMode proves durable mode does
// not sweep unfinished executions to failed during reconcile; recovery needs
// them to remain non-terminal so it can resume them later.
func TestReconcileKeepsInterruptedExecutionsInDurableMode(t *testing.T) {
	store := newTestStore(t)
	instanceID := shared.ID("inst_durable")
	persistInterruptedInstance(t, store, string(instanceID))
	exec := persistInterruptedExecution(t, store, instanceID)

	newTestManager(t, store, "durable")

	reloaded, err := execution.NewExecutionStore(store).Get(context.Background(), exec.ID)
	if err != nil {
		t.Fatalf("load execution %s after reconcile: %v", exec.ID, err)
	}
	if reloaded.IsTerminal() {
		t.Fatalf("durable execution status = %s, want a non-terminal state", reloaded.Status())
	}
	if reloaded.Status() != execution.StatusRunning {
		t.Fatalf("durable execution status = %s, want %s", reloaded.Status(), execution.StatusRunning)
	}
	if reloaded.Error() != "" {
		t.Fatalf("durable execution error = %q, want empty", reloaded.Error())
	}
}
