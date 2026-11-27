package acceptance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/application/client"
	"github.com/neuron-runtime/neuron/application/connection"
	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
	"github.com/neuron-runtime/neuron/shared/types/projectruntime"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

const suspendCapability = `apiVersion: neuron/v1
kind: Capability

metadata:
  name: suspend
  version: 1.0.0

spec:
  capability runtime:
    type: acctest:suspend

  mappings:
    - direction: input
      source: marker
      target: marker
    - direction: input
      source: resume_marker
      target: resume_marker
    - direction: input
      source: wait_type
      target: wait_type
    - direction: input
      source: expected_signal
      target: expected_signal
    - direction: input
      source: correlation_key
      target: correlation_key
    - direction: input
      source: timer_delay_ms
      target: timer_delay_ms
    - direction: input
      source: payload_schema
      target: payload_schema
    - direction: output
      source: done
      target: done
    - direction: output
      source: cause
      target: cause
    - direction: output
      source: signal
      target: signal
    - direction: output
      source: delivery_id
      target: delivery_id
    - direction: output
      source: payload
      target: payload
`

const suspendAssembly = `apiVersion: neuron/v1
kind: Assembly

capabilities:
  - ref: suspend
    entry: ../../capabilities/suspend.yaml

result:
  schema:
    - name: done
      type: boolean
    - name: cause
      type: string
    - name: signal
      type: string
    - name: delivery_id
      type: string
    - name: payload
      type: object
  fields:
    - name: done
      source:
        kind: capabilityResult
        capability: suspend
        path: [done]
    - name: cause
      source:
        kind: capabilityResult
        capability: suspend
        path: [cause]
    - name: signal
      source:
        kind: capabilityResult
        capability: suspend
        path: [signal]
    - name: delivery_id
      source:
        kind: capabilityResult
        capability: suspend
        path: [delivery_id]
    - name: payload
      source:
        kind: capabilityResult
        capability: suspend
        path: [payload]
`

func installSuspendRuntime(t *testing.T, p project) {
	t.Helper()

	src := filepath.Join("testdata", "suspendruntime")
	dst := filepath.Join(p.root, "neuron", "capabilityRuntimes", "acctest", "suspend", "1.0.0")
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
			"name":    "acctest:suspend",
			"version": "1.0.0",
		},
		"runtime": map[string]any{
			"type":       "process",
			"entrypoint": "suspendruntime",
			"protocol":   capabilityrt.ProtocolJSONV1,
		},
		"capabilities": []string{"acctest:suspend"},
		"build":        map[string]any{"command": "./build.sh"},
		"artifact":     map[string]any{"path": "suspendruntime"},
		"platforms": map[string]any{
			capabilityrt.HostPlatform(): map[string]any{"artifact": "suspendruntime"},
		},
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeWithMode(t, filepath.Join(dst, "runtime.json"), data, 0o644)
}

func setupSuspendProject(t *testing.T) project {
	t.Helper()
	p := writeProject(t)
	if res := runNeuron(t, p.root, nil, "provider", "build"); res.code != 0 {
		t.Fatalf("provider build exit = %d\nstdout: %s\nstderr: %s", res.code, res.stdout, res.stderr)
	}
	installSuspendRuntime(t, p)
	writeFile(t, filepath.Join(p.root, "capabilities", "suspend.yaml"), suspendCapability)
	writeFile(t, filepath.Join(p.root, "assemblies", p.assembly, "assembly.yaml"), suspendAssembly)
	writeFile(t, filepath.Join(p.root, "assemblies", p.failing, "assembly.yaml"), suspendAssembly)
	buildProject(t, p.root)
	return p
}

func startDurableRuntime(t *testing.T, p project) *runtimeSession {
	t.Helper()
	t.Setenv("NEURON_EXECUTION_DURABILITY", "durable")
	t.Setenv("NEURON_TIMER_POLL_INTERVAL", "100ms")
	t.Setenv("NEURON_TIMER_POLL_BATCH_SIZE", "32")
	return startRuntime(t, p.root)
}

func detachSuspendExecution(t *testing.T, record projectruntime.Record, assembly string, params map[string]any) string {
	t.Helper()
	c := newTestClient(t, record)
	res, err := c.ExecuteByKeyOrTarget(context.Background(), protocol.InstanceKey{}, assembly, params, "detach")
	if err != nil {
		t.Fatalf("detach execute %s: %v", assembly, err)
	}
	if res.ExecutionID == "" {
		t.Fatalf("detach execute %s returned empty execution ID: %#v", assembly, res)
	}
	return string(res.ExecutionID)
}

func waitForStatusByID(t *testing.T, record projectruntime.Record, selector, executionID, status string, timeout time.Duration) protocol.ExecutionItem {
	t.Helper()
	items := waitForExecutions(t, record, selector, timeout, func(items []protocol.ExecutionItem) bool {
		for _, item := range items {
			if string(item.ID) == executionID && item.Status == status {
				return true
			}
		}
		return false
	})
	for _, item := range items {
		if string(item.ID) == executionID {
			return item
		}
	}
	t.Fatalf("execution %s not %s in %#v\nselector=%s", executionID, status, items, selector)
	return protocol.ExecutionItem{}
}

func readResumeMarker(t *testing.T, path string, timeout time.Duration) map[string]any {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			var got map[string]any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("decode resume marker %s: %v\ncontent: %s", path, err, data)
			}
			return got
		}
		if !os.IsNotExist(err) {
			t.Fatalf("read resume marker %s: %v", path, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("resume marker %s was not written within %s", path, timeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func newTestClient(t *testing.T, record projectruntime.Record) *client.Client {
	t.Helper()
	conn := connection.NewRemote(record.Endpoint())
	if transport, ok := conn.Transport().(*connection.HTTPTransport); ok {
		transport.WithToken(record.Token)
	} else {
		t.Fatalf("unexpected transport type %T", conn.Transport())
	}
	c := client.New(conn)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestR5_22_SuspendRestartDeliverSignalResumesWithPayload(t *testing.T) {
	p := setupSuspendProject(t)
	session := startDurableRuntime(t, p)

	marker := filepath.Join(t.TempDir(), "started.marker")
	resumeMarker := filepath.Join(t.TempDir(), "resume.json")
	executionID := detachSuspendExecution(t, session.record, p.assembly, map[string]any{
		"marker":          marker,
		"resume_marker":   resumeMarker,
		"wait_type":       "signal",
		"expected_signal": "approved",
		"correlation_key": "approval-1",
		"timer_delay_ms":  0,
		"payload_schema":  "",
	})

	waiting := waitForStatusByID(t, session.record, p.assembly, executionID, "waiting", 30*time.Second)
	if len(waiting.Waits) != 1 {
		t.Fatalf("execution %s waits = %#v, want 1 wait", executionID, waiting.Waits)
	}
	wait := waiting.Waits[0]
	if wait.WaitType != "signal" || wait.ExpectedSignal != "approved" {
		t.Fatalf("wait = %#v, want signal/approved", wait)
	}
	if wait.CorrelationKey != "approval-1" {
		t.Fatalf("wait.CorrelationKey = %q, want approval-1", wait.CorrelationKey)
	}

	session.kill()
	restarted := startDurableRuntime(t, p)

	// R5-22 requires restart, then delivery, then resume. The execution must
	// still be waiting after restart before the signal is delivered.
	waitForStatusByID(t, restarted.record, p.assembly, executionID, "waiting", 30*time.Second)

	// R5-22 requires restart then delivery then resume. Verify the durable
	// execution is visible in the restarted runtime before signaling through
	// the real CLI path. The API assertion above already proves the full
	// execution ID survived the restart; the CLI table truncates wide cells,
	// so assert only the stable status and wait details it can display.
	items := listExecutions(t, restarted.record, p.assembly)
	allAfterRestart := runNeuron(t, p.root, nil, "instance", "list", "--target", p.assembly)
	waitingAfterRestart := runNeuron(t, p.root, nil, "instance", "list", "--target", p.assembly, "--status", "waiting")
	if waitingAfterRestart.code != 0 {
		t.Fatalf("restarted instance list exit = %d\nitems=%#v\nall=%s\nwaiting=%s\nstderr=%s\nlog=%s", waitingAfterRestart.code, items, allAfterRestart.stdout, waitingAfterRestart.stdout, waitingAfterRestart.stderr, restarted.log())
	}
	visibleCorrelation := wait.CorrelationKey
	if len(visibleCorrelation) > 8 {
		visibleCorrelation = visibleCorrelation[:8]
	}
	hasWaitingStatus := false
	hasWaitRow := false
	for _, line := range strings.Split(waitingAfterRestart.stdout, "\n") {
		if strings.Contains(line, "waiting") {
			hasWaitingStatus = true
		}
		if strings.Contains(line, wait.WaitType) && strings.Contains(line, wait.ExpectedSignal) && strings.Contains(line, visibleCorrelation) {
			hasWaitRow = true
		}
	}
	if !strings.Contains(waitingAfterRestart.stdout, "Waits:") || !hasWaitingStatus || !hasWaitRow {
		t.Fatalf("restarted CLI did not display waiting execution %s with wait type %q, signal %q, correlation %q\nitems=%#v\nall=%s\nwaiting=%s\nstderr=%s\nlog=%s", executionID, wait.WaitType, wait.ExpectedSignal, visibleCorrelation, items, allAfterRestart.stdout, waitingAfterRestart.stdout, waitingAfterRestart.stderr, restarted.log())
	}
	before := waitingAfterRestart

	c := newTestClient(t, restarted.record)
	// A resolved signal replaces the capability's params with the delivered
	// payload, so the fixture's output path travels with the delivery rather
	// than remaining from the initial invocation.
	sigResp, sigErr := c.SignalExecution(context.Background(), executionID, "approved", protocol.SignalDeliveryRequest{
		DeliveryID:     "r5-22-delivery-1",
		Payload:        map[string]any{"approved": true, "by": "ada", "resume_marker": resumeMarker},
		CorrelationKey: "approval-1",
	})
	if sigErr != nil {
		t.Fatalf("client SignalExecution: %v\nwaiting list: %s\nlog: %s", sigErr, before.stdout, restarted.log())
	}
	if sigResp.Status != "accepted" && sigResp.Status != "replay" && !sigResp.Replay {
		t.Fatalf("signal response = %#v, want accepted/replay", sigResp)
	}

	completed := waitForStatusByID(t, restarted.record, p.assembly, executionID, "completed", 45*time.Second)
	if completed.Error != "" {
		t.Fatalf("completed execution %s has error: %s\nlog: %s", executionID, completed.Error, restarted.log())
	}

	resumed := readResumeMarker(t, resumeMarker, 30*time.Second)
	if resumed["cause"] != "signal" || resumed["signal"] != "approved" || resumed["delivery_id"] != "r5-22-delivery-1" {
		t.Fatalf("resume marker = %#v, want cause/signal/delivery_id from signal delivery", resumed)
	}
	payload, _ := resumed["payload"].(map[string]any)
	if payload["approved"] != true || payload["by"] != "ada" {
		t.Fatalf("resume payload = %#v, want approved=true by=ada", payload)
	}

	completedItems, err := c.ListInstanceExecutions(context.Background(), p.assembly, "status=completed")
	if err != nil {
		t.Fatalf("ListInstanceExecutions: %v", err)
	}
	found := false
	for _, item := range completedItems {
		if string(item.ID) == executionID {
			found = true
			if len(item.Waits) != 0 {
				t.Fatalf("completed execution %s still reports waits: %#v", executionID, item.Waits)
			}
		}
	}
	if !found {
		t.Fatalf("completed execution %s not returned by status=completed list: %#v", executionID, completedItems)
	}
}

func TestR5_23_R5_13_DurableTimerFiresAfterRestartIncludingMissedDue(t *testing.T) {
	p := setupSuspendProject(t)
	session := startDurableRuntime(t, p)

	marker := filepath.Join(t.TempDir(), "timer-started.marker")
	resumeMarker := filepath.Join(t.TempDir(), "timer-resume.json")
	executionID := detachSuspendExecution(t, session.record, p.assembly, map[string]any{
		"marker":          marker,
		"resume_marker":   resumeMarker,
		"wait_type":       "timer",
		"expected_signal": "",
		"correlation_key": "",
		"timer_delay_ms":  200,
		"payload_schema":  "",
	})

	waiting := waitForStatusByID(t, session.record, p.assembly, executionID, "waiting", 30*time.Second)
	if len(waiting.Waits) != 1 || waiting.Waits[0].WaitType != "timer" {
		t.Fatalf("execution %s waits = %#v, want one timer wait", executionID, waiting.Waits)
	}

	// Kill before the timer can fire, then wait past the due time so restart
	// must observe a missed timer (R5-13), not a fresh schedule.
	session.kill()
	time.Sleep(400 * time.Millisecond)

	restarted := startDurableRuntime(t, p)

	completed := waitForStatusByID(t, restarted.record, p.assembly, executionID, "completed", 45*time.Second)
	if completed.Error != "" {
		t.Fatalf("completed timer execution %s has error: %s\nlog: %s", executionID, completed.Error, restarted.log())
	}

	resumed := readResumeMarker(t, resumeMarker, 30*time.Second)
	if resumed["cause"] != "timer" {
		t.Fatalf("resume marker cause = %#v, want timer; marker=%#v", resumed["cause"], resumed)
	}
	if resumed["done"] != true {
		t.Fatalf("resume marker done = %#v, want true; marker=%#v", resumed["done"], resumed)
	}
}
