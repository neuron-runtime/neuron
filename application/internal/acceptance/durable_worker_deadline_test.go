package acceptance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

// workerDeadlineCapability is the capability contract for the local
// workerdeadline fixture. deadline_ms is test data that lets a signal wait carry
// an explicit deadline; the deployment policy applied at expiry is never chosen
// by the capability.
const workerDeadlineCapability = `apiVersion: neuron/v1
kind: Capability

metadata:
  name: workerdeadline
  version: 1.0.0

spec:
  capability runtime:
    type: acctest:workerdeadline

  mappings:
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
      source: deadline_ms
      target: deadline_ms
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

const workerDeadlineAssembly = `apiVersion: neuron/v1
kind: Assembly

capabilities:
  - ref: workerdeadline
    entry: ../../capabilities/workerdeadline.yaml

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
        capability: workerdeadline
        path: [done]
    - name: cause
      source:
        kind: capabilityResult
        capability: workerdeadline
        path: [cause]
    - name: signal
      source:
        kind: capabilityResult
        capability: workerdeadline
        path: [signal]
    - name: delivery_id
      source:
        kind: capabilityResult
        capability: workerdeadline
        path: [delivery_id]
    - name: payload
      source:
        kind: capabilityResult
        capability: workerdeadline
        path: [payload]
`

// installWorkerDeadlineRuntime installs the self-contained stdio JSON fixture
// as a local process capability runtime. It mirrors the suspend fixture's
// installation path but owns a distinct runtime type so the two fixtures cannot
// shadow each other in the shared local store.
func installWorkerDeadlineRuntime(t *testing.T, p project) {
	t.Helper()

	src := filepath.Join("testdata", "workerdeadline")
	dst := filepath.Join(p.root, "neuron", "capabilityRuntimes", "acctest", "workerdeadline", "1.0.0")
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
			"name":    "acctest:workerdeadline",
			"version": "1.0.0",
		},
		"runtime": map[string]any{
			"type":       "process",
			"entrypoint": "workerdeadline",
			"protocol":   capabilityrt.ProtocolJSONV1,
		},
		"capabilities": []string{"acctest:workerdeadline"},
		"build":        map[string]any{"command": "./build.sh"},
		"artifact":     map[string]any{"path": "workerdeadline"},
		"platforms": map[string]any{
			capabilityrt.HostPlatform(): map[string]any{"artifact": "workerdeadline"},
		},
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeWithMode(t, filepath.Join(dst, "runtime.json"), data, 0o644)
}

func setupWorkerDeadlineProject(t *testing.T) project {
	t.Helper()

	p := writeProject(t)
	if res := runNeuron(t, p.root, nil, "provider", "build"); res.code != 0 {
		t.Fatalf("provider build exit = %d\nstdout: %s\nstderr: %s", res.code, res.stdout, res.stderr)
	}
	installWorkerDeadlineRuntime(t, p)
	writeFile(t, filepath.Join(p.root, "capabilities", "workerdeadline.yaml"), workerDeadlineCapability)
	writeFile(t, filepath.Join(p.root, "assemblies", p.assembly, "assembly.yaml"), workerDeadlineAssembly)
	writeFile(t, filepath.Join(p.root, "assemblies", p.failing, "assembly.yaml"), workerDeadlineAssembly)
	buildProject(t, p.root)
	return p
}

func workerDeadlineParams(resumeMarker, correlationKey string, deadlineMS int) map[string]any {
	return map[string]any{
		"resume_marker":   resumeMarker,
		"wait_type":       "signal",
		"expected_signal": "approved",
		"correlation_key": correlationKey,
		"deadline_ms":     deadlineMS,
		"payload_schema":  "",
	}
}

// TestR5_26_WaitingExecutionsDoNotOccupyWorkers proves that entering a wait
// releases the execution's engine worker slot. With one configured worker, two
// signal waits can only both be reported as waiting if neither one holds the
// slot while suspended; both must then resume and complete after delivery.
func TestR5_26_WaitingExecutionsDoNotOccupyWorkers(t *testing.T) {
	t.Setenv("NEURON_RUNTIME_WORKERS_MAX", "1")

	p := setupWorkerDeadlineProject(t)
	session := startDurableRuntime(t, p)

	dir := t.TempDir()
	resumeA := filepath.Join(dir, "resume-a.json")
	resumeB := filepath.Join(dir, "resume-b.json")
	execA := detachSuspendExecution(t, session.record, p.assembly, workerDeadlineParams(resumeA, "approval-26-a", 0))
	execB := detachSuspendExecution(t, session.record, p.assembly, workerDeadlineParams(resumeB, "approval-26-b", 0))
	if execA == execB {
		t.Fatalf("two executions resolved to the same id %s", execA)
	}

	// A single snapshot must show both executions waiting at the same time.
	bothWaiting := waitForExecutions(t, session.record, p.assembly, 30*time.Second, func(items []protocol.ExecutionItem) bool {
		statuses := executionStatuses(items)
		return statuses[execA] == "waiting" && statuses[execB] == "waiting"
	})
	waits := map[string][]protocol.WaitItem{}
	for _, item := range bothWaiting {
		waits[string(item.ID)] = item.Waits
	}
	for executionID, correlationKey := range map[string]string{execA: "approval-26-a", execB: "approval-26-b"} {
		items := waits[executionID]
		if len(items) != 1 || items[0].WaitType != "signal" || items[0].ExpectedSignal != "approved" || items[0].CorrelationKey != correlationKey {
			t.Fatalf("execution %s waits = %#v, want one approved signal wait with correlation %s", executionID, items, correlationKey)
		}
		if items[0].Deadline != nil {
			t.Fatalf("execution %s wait has deadline %d, want an open-ended signal wait", executionID, *items[0].Deadline)
		}
	}

	c := newTestClient(t, session.record)
	deliver := func(executionID, deliveryID, correlationKey, resumeMarker string) {
		t.Helper()
		resp, err := c.SignalExecution(context.Background(), executionID, "approved", protocol.SignalDeliveryRequest{
			DeliveryID:     deliveryID,
			CorrelationKey: correlationKey,
			Payload: map[string]any{
				"approved":      true,
				"resume_marker": resumeMarker,
			},
		})
		if err != nil {
			t.Fatalf("signal %s: %v\nlog: %s", executionID, err, session.log())
		}
		if resp.Status != "accepted" && resp.Status != "replay" {
			t.Fatalf("signal %s response = %#v, want accepted/replay", executionID, resp)
		}
	}
	deliver(execA, "r5-26-delivery-a", "approval-26-a", resumeA)
	deliver(execB, "r5-26-delivery-b", "approval-26-b", resumeB)

	bothCompleted := waitForExecutions(t, session.record, p.assembly, 45*time.Second, func(items []protocol.ExecutionItem) bool {
		statuses := executionStatuses(items)
		return statuses[execA] == "completed" && statuses[execB] == "completed"
	})
	for _, item := range bothCompleted {
		if id := string(item.ID); id == execA || id == execB {
			if item.Error != "" {
				t.Fatalf("execution %s completed with error %q\nlog: %s", id, item.Error, session.log())
			}
			if item.CompletedAt == nil {
				t.Fatalf("execution %s has no completion time: %#v", id, item)
			}
			if len(item.Waits) != 0 {
				t.Fatalf("execution %s still reports waits: %#v", id, item.Waits)
			}
		}
	}
	for _, tc := range []struct {
		executionID string
		deliveryID  string
		marker      string
	}{
		{execA, "r5-26-delivery-a", resumeA},
		{execB, "r5-26-delivery-b", resumeB},
	} {
		resumed := readResumeMarker(t, tc.marker, 30*time.Second)
		if resumed["cause"] != "signal" || resumed["signal"] != "approved" || resumed["delivery_id"] != tc.deliveryID {
			t.Fatalf("execution %s resume marker = %#v, want cause/signal/delivery %q", tc.executionID, resumed, tc.deliveryID)
		}
		payload, _ := resumed["payload"].(map[string]any)
		if payload["approved"] != true {
			t.Fatalf("execution %s resume payload = %#v, want approved=true", tc.executionID, payload)
		}
	}
}

func executionStatuses(items []protocol.ExecutionItem) map[string]string {
	statuses := make(map[string]string, len(items))
	for _, item := range items {
		statuses[string(item.ID)] = item.Status
	}
	return statuses
}

// TestR5_27_DeadlinePoliciesFailCancelContinue proves that the deployment
// deadline policy is what decides an expired signal wait. The same fixture
// wait must fail, cancel, or resume to completion solely by changing
// NEURON_WAIT_DEADLINE_POLICY.
func TestR5_27_DeadlinePoliciesFailCancelContinue(t *testing.T) {
	const deadlineMS = 2500

	cases := []struct {
		name       string
		policy     string
		wantStatus string
		wantError  string
		wantResume bool
	}{
		{name: "fail", policy: "fail", wantStatus: "failed", wantError: "wait deadline exceeded"},
		{name: "cancel", policy: "cancel", wantStatus: "cancelled", wantError: "wait deadline cancelled"},
		{name: "continue", policy: "continue", wantStatus: "completed", wantResume: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NEURON_WAIT_DEADLINE_POLICY", tc.policy)

			p := setupWorkerDeadlineProject(t)
			session := startDurableRuntime(t, p)
			resumeMarker := filepath.Join(t.TempDir(), "resume.json")
			executionID := detachSuspendExecution(t, session.record, p.assembly,
				workerDeadlineParams(resumeMarker, "deadline-"+tc.name, deadlineMS))

			waiting := waitForStatusByID(t, session.record, p.assembly, executionID, "waiting", 20*time.Second)
			if len(waiting.Waits) != 1 || waiting.Waits[0].WaitType != "signal" || waiting.Waits[0].Deadline == nil {
				t.Fatalf("execution %s waits = %#v, want one signal wait with a deadline", executionID, waiting.Waits)
			}

			final := waitForStatusByID(t, session.record, p.assembly, executionID, tc.wantStatus, 45*time.Second)
			if final.CompletedAt == nil {
				t.Fatalf("execution %s has no completion time: %#v", executionID, final)
			}
			if tc.wantError != "" {
				if !strings.Contains(final.Error, tc.wantError) {
					t.Fatalf("execution %s error = %q, want it to contain %q\nlog: %s", executionID, final.Error, tc.wantError, session.log())
				}
			} else if final.Error != "" {
				t.Fatalf("execution %s error = %q, want no error\nlog: %s", executionID, final.Error, session.log())
			}

			if tc.wantResume {
				resumed := readResumeMarker(t, resumeMarker, 30*time.Second)
				if resumed["cause"] != "deadline" || resumed["done"] != true {
					t.Fatalf("execution %s resume marker = %#v, want cause=deadline done=true", executionID, resumed)
				}
				return
			}
			// A terminal fail/cancel must not re-invoke the capability. The marker
			// is the only observable write on that path, so give a wrongly
			// dispatched resume a bounded window to appear.
			time.Sleep(750 * time.Millisecond)
			if _, err := os.Stat(resumeMarker); !os.IsNotExist(err) {
				t.Fatalf("execution %s wrote a resume marker under the %s policy (stat error %v)", executionID, tc.policy, err)
			}
		})
	}
}
