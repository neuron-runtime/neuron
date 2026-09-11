package bridge

import (
	"strings"
	"testing"

	"github.com/Muhammad-Jay/neuron/shared/types/core"
)

func TestExecutionRoomRoundTrip(t *testing.T) {
	instanceID := "inst_123"
	executionID := core.NewID("exec_")

	room := ExecutionRoom(instanceID, executionID)

	gotInstance, gotExecution, err := splitExecutionRoom(room)
	if err != nil {
		t.Fatalf("splitExecutionRoom(%q): %v", room, err)
	}
	if gotInstance != instanceID {
		t.Fatalf("instance = %q, want %q", gotInstance, instanceID)
	}
	if gotExecution != executionID {
		t.Fatalf("execution = %q, want %q", gotExecution, executionID)
	}
}

func TestSplitExecutionRoomInvalid(t *testing.T) {
	cases := []string{
		"",
		"exec",
		"exec:",
		"inst:inst_1:exec_1",
		"exec:inst_1",
		"exec:inst_1:exec_1:extra",
		"exec:inst_1:",
	}
	for _, room := range cases {
		if _, _, err := splitExecutionRoom(room); err == nil {
			t.Errorf("splitExecutionRoom(%q) = nil, want error", room)
		}
	}
}

// TestStreamUninitialized verifies the bridge reports an initialization error
// when no instance manager is attached rather than panicking.
func TestStreamUninitialized(t *testing.T) {
	b := New(nil)
	_, err := b.Stream(t.Context(), "exec:inst_missing:exec_missing")
	if err == nil {
		t.Fatal("expected error for uninitialized bridge")
	}
	if !strings.Contains(err.Error(), "not initialized") {
		t.Fatalf("unexpected error: %v", err)
	}
}
