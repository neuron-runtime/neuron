package instance

import (
	"context"
	"errors"
	"testing"

	"github.com/neuron-runtime/neuron/nore/internal/assembly"
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
