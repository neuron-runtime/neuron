package acceptance

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

// TestR4-24SigKillAfterCompletion verifies R4-24: after SIGKILL and restart, a
// durable execution that already completed is still observable and is not run
// again. The completedAt timestamp is the observable proof: a rerun would either
// produce a new execution or move the completion time.
func TestR4_24SigKillAfterCompletion(t *testing.T) {
	t.Setenv("NEURON_EXECUTION_DURABILITY", "durable")

	p := writeProject(t)
	buildProject(t, p.root)

	session := startRuntime(t, p.root)

	result := runNeuron(t, p.root, nil, "run", p.assembly, "--params", `{"name":"World"}`)
	if result.code != 0 {
		t.Fatalf("neuron run exit = %d\nstdout: %s\nstderr: %s", result.code, result.stdout, result.stderr)
	}
	if got := decodeSingleJSON(t, result.stdout)["caller"]; got != "World" {
		t.Errorf("caller = %v, want World", got)
	}

	// Durable mode commits each transition itself, so the execution is recorded
	// and completed before the synchronous run returns.
	before := waitForExecutions(t, session.record, p.assembly, 10*time.Second, func(items []protocol.ExecutionItem) bool {
		return len(items) == 1 && items[0].Status == "completed"
	})
	completed := before[0]
	if completed.CompletedAt == nil {
		t.Fatalf("completed execution %s has no completedAt", completed.ID)
	}

	// Crash the runtime rather than shutting it down gracefully, then restart it
	// from the same project state.
	session.kill()
	restarted := startRuntime(t, p.root)

	after := waitForExecutions(t, restarted.record, p.assembly, 30*time.Second, func(items []protocol.ExecutionItem) bool {
		return len(items) == 1 && items[0].ID == completed.ID && items[0].Status == "completed"
	})
	recovered := after[0]
	if recovered.Status != "completed" {
		t.Fatalf("recovered execution %s status = %s, want completed (error %q)", recovered.ID, recovered.Status, recovered.Error)
	}
	if recovered.Error != "" {
		t.Fatalf("recovered execution %s has an error: %s", recovered.ID, recovered.Error)
	}
	if recovered.CompletedAt == nil || *recovered.CompletedAt != *completed.CompletedAt {
		t.Fatalf("completed execution was rerun after restart: completedAt = %v, want %v", recovered.CompletedAt, *completed.CompletedAt)
	}
}

// TestR4-24SigKillRecoveryVerifiesExecutionState verifies R4-24 for work that was
// still in flight: after the runtime is killed mid-execution and restarted,
// durable recovery resumes that same execution to completion instead of
// abandoning it or starting a fresh one.
func TestR4_24SigKillRecoveryVerifiesExecutionState(t *testing.T) {
	t.Setenv("NEURON_EXECUTION_DURABILITY", "durable")

	p := writeProject(t)
	if res := runNeuron(t, p.root, nil, "provider", "build"); res.code != 0 {
		t.Fatalf("provider build exit = %d\nstdout: %s\nstderr: %s", res.code, res.stdout, res.stderr)
	}
	installBlockingRuntime(t, p)
	writeFile(t, filepath.Join(p.root, "capabilities", "blocking.yaml"), blockingCapability)
	writeFile(t, filepath.Join(p.root, "assemblies", p.assembly, "assembly.yaml"), blockingAssembly)
	buildProject(t, p.root)

	session := startRuntime(t, p.root)

	// The capability leaves a marker once it has started, then blocks long
	// enough that the runtime can be killed while the execution is in flight.
	marker := filepath.Join(t.TempDir(), "started")
	params, err := json.Marshal(map[string]any{"sleep_ms": 5000, "marker": marker})
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(harness.neuronBin, "--nore-path", harness.noreBin, "run", "--params", string(params))
	cmd.Dir = p.root
	cmd.Env = os.Environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})

	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("run exited before the execution started: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("execution did not start\nstdout: %s\nstderr: %s", stdout.String(), stderr.String())
		}
		time.Sleep(50 * time.Millisecond)
	}

	// The capability is running, so the execution must be recorded as running and
	// carried the identity that recovery will use after the process dies.
	before := waitForExecutions(t, session.record, p.assembly, 10*time.Second, func(items []protocol.ExecutionItem) bool {
		for _, item := range items {
			if item.Status == "running" {
				return true
			}
		}
		return false
	})
	var inFlight protocol.ExecutionItem
	for _, item := range before {
		if item.Status == "running" {
			inFlight = item
			break
		}
	}

	// Kill the runtime (and the blocked capability runtime with it) mid-flight.
	session.kill()
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}

	restarted := startRuntime(t, p.root)

	// Recovery must resume the same execution and drive it to a terminal state.
	after := waitForExecutions(t, restarted.record, p.assembly, 30*time.Second, func(items []protocol.ExecutionItem) bool {
		for _, item := range items {
			if item.ID == inFlight.ID && item.Status == "completed" {
				return true
			}
		}
		return false
	})
	if len(after) != 1 {
		t.Fatalf("executions after recovery = %d, want the single interrupted execution %s (got %#v)", len(after), inFlight.ID, after)
	}
	recovered := after[0]
	if recovered.ID != inFlight.ID {
		t.Fatalf("recovered execution id = %s, want the interrupted execution %s", recovered.ID, inFlight.ID)
	}
	if recovered.Status != "completed" {
		t.Fatalf("recovered execution %s status = %s, want completed (error %q)", recovered.ID, recovered.Status, recovered.Error)
	}
	if recovered.Error != "" {
		t.Fatalf("recovered execution %s has an error: %s", recovered.ID, recovered.Error)
	}
}
