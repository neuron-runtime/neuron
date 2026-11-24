package instance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/assembly"
	"github.com/neuron-runtime/neuron/nore/internal/runtimeconfig"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

func TestWithCapabilityRuntimesInvalidPayloadReturnsError(t *testing.T) {
	_, err := withCapabilityRuntimes(assembly.RegisteredAssembly{
		Key:                     protocol.InstanceKey{AssemblyID: "sys_test"},
		ExecutionConfigurations: make(chan int),
	})
	if err == nil {
		t.Fatal("withCapabilityRuntimes() error = nil, want decode failure")
	}
}

func TestWithCapabilityRuntimesValidPayloadReturnsOption(t *testing.T) {
	opt, err := withCapabilityRuntimes(assembly.RegisteredAssembly{
		Key: protocol.InstanceKey{AssemblyID: "sys_test"},
		ExecutionConfigurations: map[string]any{
			"resolved_capability_runtimes": []any{},
		},
	})
	if err != nil {
		t.Fatalf("withCapabilityRuntimes() error = %v", err)
	}
	if opt == nil {
		t.Fatal("withCapabilityRuntimes() option = nil, want non-nil Option")
	}
}

func TestRemoveRefusesUnfinishedExecutionsWithoutForce(t *testing.T) {
	store := newTestStore(t)
	inst := startBlockingInstance(t, store)
	startBlockingExecution(t, inst)

	manager, err := NewManager(context.Background(), 1, 0, store, assembly.NewRepository(store))
	if err != nil {
		t.Fatalf("create manager: %v", err)
	}
	manager.instancesByID[inst.ID] = inst
	manager.instancesByKey[inst.Key] = inst

	_, err = manager.Remove(context.Background(), inst.ID, false)
	if !errors.Is(err, ErrActiveExecutions) {
		t.Fatalf("Remove() error = %v, want ErrActiveExecutions", err)
	}
	if inst.Status() == StatusStopped {
		t.Fatal("refused removal still stopped the instance")
	}

	removed, err := manager.Remove(context.Background(), inst.ID, true)
	if err != nil {
		t.Fatalf("forced Remove(): %v", err)
	}
	if !removed {
		t.Fatal("forced Remove() did not remove the instance")
	}
}

// TestManagerPassesExecutionPolicyToCreatedInstance proves the manager carries
// the resolved deployment policy into every instance it constructs.
func TestManagerPassesExecutionPolicyToCreatedInstance(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	key := protocol.InstanceKey{AssemblyID: "sys_policy_manager", Version: "1.0.0", Hash: "policy_manager_hash"}
	registerRecoveryAssembly(t, store, key, "cap")
	policy := runtimeconfig.ExecutionPolicy{
		WaitDeadlinePolicy: "continue",
		MaxLifetime:        2 * time.Hour,
		TimerPollInterval:  25 * time.Millisecond,
		TimerPollBatchSize: 7,
	}

	manager, err := NewManager(ctx, 1, 0, store, assembly.NewRepository(store),
		WithDurability(DurabilityNone), WithExecutionPolicy(policy))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	inst, created, err := manager.GetOrCreate(ctx, key)
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if !created || inst == nil {
		t.Fatalf("GetOrCreate returned created=%v instance=%v", created, inst)
	}
	defer func() { _ = inst.Stop() }()

	if inst.executionPolicy != policy {
		t.Fatalf("instance policy = %+v, want %+v", inst.executionPolicy, policy)
	}
}
