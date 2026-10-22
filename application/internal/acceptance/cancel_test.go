package acceptance

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
)

const blockingCapability = `apiVersion: neuron/v1
kind: Capability

metadata:
  name: blocking
  version: 1.0.0

spec:
  capability runtime:
    type: acctest:blocking

  mappings:
    - direction: input
      source: sleep_ms
      target: sleep_ms
    - direction: input
      source: marker
      target: marker
    - direction: output
      source: done
      target: done
`

const blockingAssembly = `apiVersion: neuron/v1
kind: Assembly

capabilities:
  - ref: blocking
    entry: ../../capabilities/blocking.yaml

result:
  schema:
    - name: done
      type: boolean
  fields:
    - name: done
      source:
        kind: capabilityResult
        capability: blocking
        path: [done]
`

// installBlockingRuntime lays the stdio JSON fixture into the project's local
// capability runtime root, the same layout `neuron new` generates.
func installBlockingRuntime(t *testing.T, p project) {
	t.Helper()

	src := filepath.Join("testdata", "blockingruntime")
	dst := filepath.Join(p.root, "neuron", "capabilityRuntimes", "acctest", "blocking", "1.0.0")
	for _, name := range []string{"main.go", "go.mod", "build.sh"} {
		data, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if name == "build.sh" {
			mode = 0o755
		}
		writeWithMode(t, filepath.Join(dst, name), data, mode)
	}

	manifest := map[string]any{
		"apiVersion": "neuron/v1",
		"kind":       "CapabilityRuntime",
		"metadata": map[string]any{
			"name":    "acctest:blocking",
			"version": "1.0.0",
		},
		"runtime": map[string]any{
			"type":       "process",
			"entrypoint": "blocking",
			"protocol":   capabilityrt.ProtocolJSONV1,
		},
		"capabilities": []string{"acctest:blocking"},
		"build":        map[string]any{"command": "./build.sh"},
		"artifact":     map[string]any{"path": "blocking"},
		"platforms": map[string]any{
			capabilityrt.HostPlatform(): map[string]any{"artifact": "blocking"},
		},
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeWithMode(t, filepath.Join(dst, "runtime.json"), data, 0o644)
}

func writeWithMode(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}

// Cancelling an in-flight execution terminates the run command with a non-zero
// status, and the project runtime survives to serve later executions.
func TestCancelledExecutionExitsNonZero(t *testing.T) {
	p := writeProject(t)
	if res := runNeuron(t, p.root, nil, "provider", "build"); res.code != 0 {
		t.Fatalf("provider build exit = %d\nstdout: %s\nstderr: %s", res.code, res.stdout, res.stderr)
	}
	installBlockingRuntime(t, p)
	writeFile(t, filepath.Join(p.root, "capabilities", "blocking.yaml"), blockingCapability)
	writeFile(t, filepath.Join(p.root, "assemblies", p.assembly, "assembly.yaml"), blockingAssembly)
	writeFile(t, filepath.Join(p.root, "assemblies", p.failing, "assembly.yaml"), blockingAssembly)
	buildProject(t, p.root)

	session := startRuntime(t, p.root)

	marker := filepath.Join(t.TempDir(), "started")
	params, err := json.Marshal(map[string]any{"sleep_ms": 30_000, "marker": marker})
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

	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("cancelled run exited successfully\nstdout: %s\nstderr: %s", stdout.String(), stderr.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatal("cancelled run did not exit")
	}

	if err := health(session.record); err != nil {
		t.Fatalf("runtime did not survive cancellation: %v", err)
	}

	quick, err := json.Marshal(map[string]any{"sleep_ms": 0, "marker": ""})
	if err != nil {
		t.Fatal(err)
	}
	res := runNeuron(t, p.root, nil, "run", "--params", string(quick))
	if res.code != 0 {
		t.Fatalf("execution after cancellation exit = %d\nstdout: %s\nstderr: %s", res.code, res.stdout, res.stderr)
	}
	if got := decodeSingleJSON(t, res.stdout)["done"]; got != true {
		t.Errorf("done = %v, want true", got)
	}
}
