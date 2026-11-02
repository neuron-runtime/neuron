//go:build !windows

package command

import (
	"context"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
)

// The program runs in its own process group, and the whole group is stopped
// when the run ends. A child that outlived its parent would otherwise keep
// running inside the runtime.
func TestCommandStopsTheWholeProcessGroup(t *testing.T) {
	result, err := Command{}.Execute(context.Background(), contracts.ExecutionContext{
		Params: helperParamsWith(t, "spawn", map[string]any{
			"timeout":           "300ms",
			"failOnNonZeroExit": false,
		}),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result["timed_out"] != true {
		t.Errorf("timed_out = %v, want true", result["timed_out"])
	}

	// The helper reports the pid of the child it started before it went on
	// to wait, so the test can check that the child is gone.
	pid, err := strconv.Atoi(strings.TrimSpace(result["stdout"].(string)))
	if err != nil {
		t.Fatalf("read the child's pid: %v", err)
	}

	// The group is stopped with the program, so the child is gone with it.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Errorf("the child process %d survived the run", pid)
}
