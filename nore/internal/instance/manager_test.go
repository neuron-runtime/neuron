package instance

import (
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
