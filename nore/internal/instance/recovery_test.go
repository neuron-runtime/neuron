package instance

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/assembly"
	"github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/nore/internal/types"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

func recoveryAssembly(key protocol.InstanceKey, capabilityID shared.ID) shared.Assembly {
	return shared.Assembly{
		Metadata: shared.Metadata{ID: shared.ID(key.AssemblyID), Name: key.AssemblyID, Version: key.Version},
		Specification: shared.AssemblySpec{
			Capabilities: []shared.Capability{{
				Metadata: shared.Metadata{ID: capabilityID, Name: string(capabilityID), Version: "1.0.0"},
				Type:     shared.CoreName("set"),
			}},
		},
	}
}

func registerRecoveryAssembly(t *testing.T, store storage.Store, key protocol.InstanceKey, capabilityID shared.ID) {
	t.Helper()
	if _, _, err := assembly.NewRepository(store).Register(context.Background(), assembly.RegisteredAssembly{
		Key:      key,
		Assembly: recoveryAssembly(key, capabilityID),
	}); err != nil {
		t.Fatalf("register recovery assembly: %v", err)
	}
}

func persistRecoveryMetadata(t *testing.T, store storage.Store, instanceID string, key protocol.InstanceKey) {
	t.Helper()
	if err := newMetadataStore(store).Save(context.Background(), metadata{
		ID:         instanceID,
		AssemblyID: key.AssemblyID,
		Version:    key.Version,
		Hash:       key.Hash,
		Env:        key.Env,
		Status:     StatusRunning,
		CreatedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save instance metadata: %v", err)
	}
}

func recoveryBlueprint(capabilityID shared.ID) *types.ExecutionBlueprint {
	return &types.ExecutionBlueprint{
		Metadata: shared.Metadata{ID: "sys_recovery"},
		Nodes: map[shared.ID]types.ExecutionNode{
			capabilityID: {
				Capability: shared.Capability{
					Metadata: shared.Metadata{ID: capabilityID},
					Type:     shared.CoreName("set"),
				},
			},
		},
		EntryCapabilityIDs: []shared.ID{capabilityID},
	}
}

func persistRecoveryExecution(t *testing.T, store storage.Store, instanceID string, key protocol.InstanceKey, capabilityID shared.ID, running bool, params map[string]any) *execution.Execution {
	t.Helper()
	exec, err := execution.NewExecution(recoveryBlueprint(capabilityID), shared.NewID("request_"), shared.ID(instanceID), key.AssemblyID, key.Version, key.Hash, key.Env)
	if err != nil {
		t.Fatalf("new execution: %v", err)
	}
	if running {
		if err := exec.Start(params, 1); err != nil {
			t.Fatalf("start execution: %v", err)
		}
	} else {
		exec.SeedInitialParams(params)
	}
	if err := execution.NewExecutionStore(store).Add(context.Background(), exec); err != nil {
		t.Fatalf("add execution: %v", err)
	}
	return exec
}

// TestRecoveryResumesRunningExecution covers the durable contract for a root
// execution that was running when the process died: the interrupted capability
// is dispatched again and the execution reaches a terminal state.
func TestRecoveryResumesRunningExecution(t *testing.T) {
	store := newTestStore(t)
	key := protocol.InstanceKey{AssemblyID: "sys_recovery", Version: "1.0.0", Hash: "hash_recovery"}
	capabilityID := shared.ID("cap_recover")
	instanceID := shared.ID("inst_recover_running")
	registerRecoveryAssembly(t, store, key, capabilityID)
	persistRecoveryMetadata(t, store, string(instanceID), key)
	exec := persistRecoveryExecution(t, store, string(instanceID), key, capabilityID, true, map[string]any{"value": "recovered"})

	manager := newTestManager(t, store, "durable")
	t.Cleanup(func() { _ = manager.Stop(context.Background(), string(instanceID)) })

	waitForPersistedStatus(t, store, exec.ID, execution.StatusCompleted)
	waitFor(t, 10*time.Second, func() bool {
		return len(listPrefix(t, store, "dispatch_intents")) == 0
	}, "recovery left dispatch intents behind")

	inst, ok := manager.GetByID(string(instanceID))
	if !ok {
		t.Fatalf("instance %s was not recovered", instanceID)
	}
	if inst.Status() != StatusRunning {
		t.Fatalf("recovered instance status = %s, want %s", inst.Status(), StatusRunning)
	}
	if _, err := inst.GetExecution(context.Background(), exec.ID); err != nil {
		t.Fatalf("recovered execution is not live on the instance: %v", err)
	}
}

// TestRecoveryResumesPendingExecution covers an execution persisted before its
// start event was processed. Recovery must replay the recorded input rather than
// drop the execution because it never reached the scheduler.
func TestRecoveryResumesPendingExecution(t *testing.T) {
	store := newTestStore(t)
	key := protocol.InstanceKey{AssemblyID: "sys_recovery", Version: "1.0.0", Hash: "hash_recovery"}
	capabilityID := shared.ID("cap_recover")
	instanceID := shared.ID("inst_recover_pending")
	registerRecoveryAssembly(t, store, key, capabilityID)
	persistRecoveryMetadata(t, store, string(instanceID), key)
	exec := persistRecoveryExecution(t, store, string(instanceID), key, capabilityID, false, map[string]any{"value": "pending"})

	manager := newTestManager(t, store, "durable")
	t.Cleanup(func() { _ = manager.Stop(context.Background(), string(instanceID)) })

	waitForPersistedStatus(t, store, exec.ID, execution.StatusCompleted)
}

// TestRecoveryResumesDetachedTask covers the handoff boundary: a pending task
// does not carry its input, so recovery must take it from the parent's recorded
// params and attach the sub-blueprint compiled for the capability it took over.
func TestRecoveryResumesDetachedTask(t *testing.T) {
	store := newTestStore(t)
	key := protocol.InstanceKey{AssemblyID: "sys_detached_recovery", Version: "1.0.0", Hash: "hash_detached"}
	instanceID := shared.ID("inst_recover_detached")
	manifest := shared.Assembly{
		Metadata: shared.Metadata{ID: shared.ID(key.AssemblyID), Name: key.AssemblyID, Version: key.Version},
		Specification: shared.AssemblySpec{
			Capabilities: []shared.Capability{
				{
					Metadata: shared.Metadata{ID: "cap_first", Name: "cap_first", Version: "1.0.0"},
					Type:     shared.CoreName("set"),
				},
				{
					Metadata: shared.Metadata{ID: "cap_task", Name: "cap_task", Version: "1.0.0"},
					Type:     shared.CoreName("set"),
					RuntimeConfig: &shared.RuntimeConfig{
						Execution: &shared.RuntimeExecution{Mode: shared.RuntimeExecutionModeDetach},
					},
				},
			},
			Bindings: []shared.Binding{{
				Metadata: shared.Metadata{ID: "first-task"},
				From:     shared.Endpoint{CapabilityID: "cap_first"},
				To:       shared.Endpoint{CapabilityID: "cap_task"},
			}},
		},
	}
	if _, _, err := assembly.NewRepository(store).Register(context.Background(), assembly.RegisteredAssembly{
		Key:      key,
		Assembly: manifest,
	}); err != nil {
		t.Fatalf("register detached recovery assembly: %v", err)
	}
	persistRecoveryMetadata(t, store, string(instanceID), key)

	parentBlueprint := &types.ExecutionBlueprint{
		Metadata: shared.Metadata{ID: shared.ID(key.AssemblyID)},
		Nodes: map[shared.ID]types.ExecutionNode{
			"cap_first": {
				Capability: shared.Capability{Metadata: shared.Metadata{ID: "cap_first"}, Type: shared.CoreName("set")},
			},
			"cap_task": {
				Capability: shared.Capability{Metadata: shared.Metadata{ID: "cap_task"}, Type: shared.CoreName("set")},
				Predecessors: []types.ExecutionPredecessor{{
					BindingID: "first-task", CapabilityID: "cap_first",
				}},
			},
		},
		EntryCapabilityIDs: []shared.ID{"cap_first"},
	}
	parent, err := execution.NewExecution(parentBlueprint, shared.NewID("request_"), instanceID, key.AssemblyID, key.Version, key.Hash, key.Env)
	if err != nil {
		t.Fatalf("new parent execution: %v", err)
	}
	if err := parent.Start(map[string]any{}, 1); err != nil {
		t.Fatalf("start parent execution: %v", err)
	}
	if err := parent.MarkCapabilityReady("cap_first", map[string]any{}); err != nil {
		t.Fatalf("mark first ready: %v", err)
	}
	if err := parent.MarkCapabilityRunning("cap_first"); err != nil {
		t.Fatalf("mark first running: %v", err)
	}
	if err := parent.MarkCapabilityCompleted("cap_first", map[string]any{}); err != nil {
		t.Fatalf("mark first completed: %v", err)
	}
	if err := parent.MarkCapabilityReady("cap_task", map[string]any{"value": "detached"}); err != nil {
		t.Fatalf("mark task ready: %v", err)
	}
	if err := parent.MarkCapabilityDetached("cap_task"); err != nil {
		t.Fatalf("mark task detached: %v", err)
	}
	if err := execution.NewExecutionStore(store).Add(context.Background(), parent); err != nil {
		t.Fatalf("add parent execution: %v", err)
	}

	task, err := execution.NewDetachedTask(recoveryBlueprint("cap_task"), parent.CorrelationID, instanceID, parent.ID)
	if err != nil {
		t.Fatalf("new detached task: %v", err)
	}
	if task.DetachedCapabilityID != "cap_task" {
		t.Fatalf("detached capability id = %q, want cap_task", task.DetachedCapabilityID)
	}
	if err := execution.NewExecutionStore(store).Add(context.Background(), task); err != nil {
		t.Fatalf("add detached task: %v", err)
	}

	manager := newTestManager(t, store, "durable")
	t.Cleanup(func() { _ = manager.Stop(context.Background(), string(instanceID)) })

	waitForPersistedStatus(t, store, parent.ID, execution.StatusCompleted)
	waitForPersistedStatus(t, store, task.ID, execution.StatusCompleted)
}

// TestRecoveryFailsExecutionWhenArtifactIsMissing covers R4-26: an execution
// whose immutable artifact was pruned cannot be resumed, so recovery must fail
// it explicitly instead of leaving it running forever or dropping it silently.
func TestRecoveryFailsExecutionWhenArtifactIsMissing(t *testing.T) {
	store := newTestStore(t)
	key := protocol.InstanceKey{AssemblyID: "sys_missing", Version: "1.0.0", Hash: "hash_missing"}
	capabilityID := shared.ID("cap_missing")
	instanceID := shared.ID("inst_recover_missing")
	persistRecoveryMetadata(t, store, string(instanceID), key)
	exec := persistRecoveryExecution(t, store, string(instanceID), key, capabilityID, true, map[string]any{"value": "lost"})

	manager := newTestManager(t, store, "durable")
	t.Cleanup(func() { _ = manager.Stop(context.Background(), string(instanceID)) })

	reloaded, err := execution.NewExecutionStore(store).Get(context.Background(), exec.ID)
	if err != nil {
		t.Fatalf("load execution after recovery: %v", err)
	}
	if reloaded.Status() != execution.StatusFailed {
		t.Fatalf("unrecoverable execution status = %s, want %s", reloaded.Status(), execution.StatusFailed)
	}
	if !strings.Contains(reloaded.Error(), "immutable assembly artifact") {
		t.Fatalf("unrecoverable execution error = %q, want the missing artifact named", reloaded.Error())
	}
}
