package instances

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/assembly"
	"github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/instance"
	"github.com/neuron-runtime/neuron/nore/internal/planner"
	"github.com/neuron-runtime/neuron/nore/internal/resolver"
	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/nore/internal/storage/sqlite"
	"github.com/neuron-runtime/neuron/nore/internal/types"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

// signalHarness wires the handler to a real durable manager and sqlite store.
// The handlers are exercised at their HTTP boundary against the authoritative
// instance/scheduler path, so these tests cover the whole delivery contract
// without reaching into execution internals.
type signalHarness struct {
	h     *Handler
	mgr   *instance.Manager
	store storage.Store
	inst  *instance.Instance
	cel   resolver.Compiler
}

type signalResponse struct {
	Message string                          `json:"message"`
	Status  int                             `json:"status"`
	Code    string                          `json:"code"`
	Data    protocol.SignalDeliveryResponse `json:"data"`
}

type seededWait struct {
	cap         shared.ID
	signalName  string
	schema      string
	correlation string
	waitUntil   *time.Time
	waitType    execution.WaitType
	reason      string
}

func newSignalHarness(t *testing.T) *signalHarness {
	t.Helper()
	ctx := context.Background()
	store, err := sqlite.New(ctx, storage.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	repo := assembly.NewRepository(store)
	mgr, err := instance.NewManager(ctx, 1, 0, store, repo, instance.WithDurability(instance.DurabilityDurable))
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	cel, err := resolver.NewCELCompiler(resolver.DefaultCELConfig())
	if err != nil {
		t.Fatalf("new CEL compiler: %v", err)
	}
	compiler, err := planner.NewCompiler(cel)
	if err != nil {
		t.Fatalf("new compiler: %v", err)
	}
	h := New(mgr, repo, compiler)

	inst := createSignalInstance(t, h, "signal_api")
	return &signalHarness{h: h, mgr: mgr, store: store, inst: inst, cel: cel}
}

// createSignalInstance registers a minimal assembly and brings a durable live
// instance into the manager so both routes can resolve it.
func createSignalInstance(t *testing.T, h *Handler, name string) *instance.Instance {
	t.Helper()
	ctx := context.Background()
	key := protocol.InstanceKey{AssemblyID: name, Version: "1.0.0", Hash: "h", Env: "dev"}
	asm := shared.Assembly{
		Metadata: shared.Metadata{Name: name, Version: key.Version},
		Specification: shared.AssemblySpec{
			Capabilities: []shared.Capability{{
				Metadata: shared.Metadata{ID: shared.ID("cap"), Name: "cap", Version: "1.0.0"},
				Type:     shared.CoreName("set"),
			}},
		},
	}
	if _, _, err := h.assemblies.Register(ctx, assembly.RegisteredAssembly{Key: key, Assembly: asm}); err != nil {
		t.Fatalf("register assembly %s: %v", name, err)
	}
	inst, _, err := h.instances.GetOrCreate(ctx, key)
	if err != nil {
		t.Fatalf("create instance %s: %v", name, err)
	}
	return inst
}

// seed persists an execution with the requested waits, using only execution
// mutators. Waits are read from persisted capability state, never a blueprint,
// so this mirrors a restored execution's authoritative shape.
func (s *signalHarness) seed(t *testing.T, terminal bool, waits ...seededWait) *execution.Execution {
	t.Helper()
	ctx := context.Background()
	blueprint := &types.ExecutionBlueprint{
		Metadata:           shared.Metadata{ID: "signal_test"},
		Nodes:              make(map[shared.ID]types.ExecutionNode),
		EntryCapabilityIDs: make([]shared.ID, 0, len(waits)),
	}
	configs, err := s.cel.CompileCapabilityConfigurations(map[string]any{})
	if err != nil {
		t.Fatalf("compile empty capability configurations: %v", err)
	}
	for _, w := range waits {
		blueprint.Nodes[w.cap] = types.ExecutionNode{
			Capability:     shared.Capability{Metadata: shared.Metadata{ID: w.cap, Name: string(w.cap)}, Type: shared.CoreName("set")},
			Configurations: configs,
			Predecessors:   []types.ExecutionPredecessor{},
			Next:           []types.ExecutionTransition{},
		}
		blueprint.EntryCapabilityIDs = append(blueprint.EntryCapabilityIDs, w.cap)
	}

	exec, err := execution.NewExecution(blueprint, shared.NewID("request_"), shared.ID(s.inst.ID), "signal_api", "1.0.0", "h", "dev")
	if err != nil {
		t.Fatalf("new execution: %v", err)
	}
	if err := exec.Start(map[string]any{}, len(waits)); err != nil {
		t.Fatalf("start execution: %v", err)
	}
	for _, w := range waits {
		if err := exec.MarkCapabilityReady(w.cap, map[string]any{}); err != nil {
			t.Fatalf("mark %s ready: %v", w.cap, err)
		}
		if err := exec.MarkCapabilityRunning(w.cap); err != nil {
			t.Fatalf("mark %s running: %v", w.cap, err)
		}
		waitType := w.waitType
		if waitType == "" {
			waitType = execution.WaitTypeSignal
		}
		if err := exec.EnterWait(w.cap, execution.WaitSpec{
			Type:           waitType,
			Reason:         w.reason,
			CorrelationKey: w.correlation,
			ExpectedSignal: w.signalName,
			WaitUntil:      w.waitUntil,
			PayloadSchema:  w.schema,
		}); err != nil {
			t.Fatalf("enter wait for %s: %v", w.cap, err)
		}
	}
	if terminal && !exec.MarkCancelled(errors.New("cancelled by operator")) {
		t.Fatal("MarkCancelled returned false for a live execution")
	}
	// Seed through the instance's own execution store so the live in-memory
	// record (including its blueprint) is visible to the running engine and
	// the scheduler; a separate wrapper would only see the durable snapshot.
	if err := s.inst.Store().Add(ctx, exec); err != nil {
		t.Fatalf("persist execution: %v", err)
	}
	return exec
}

// postSignal drives one of the two routes through its handler and decodes the
// standardized response envelope.
func (s *signalHarness) postSignal(t *testing.T, byInstance bool, instanceID, execID, signalName, body string) (*httptest.ResponseRecorder, signalResponse) {
	t.Helper()
	var req *http.Request
	if byInstance {
		req = httptest.NewRequest(http.MethodPost, protocol.SignalExecution(instanceID, execID, signalName), strings.NewReader(body))
		req.SetPathValue("id", instanceID)
		req.SetPathValue("execID", execID)
		req.SetPathValue("signalName", signalName)
	} else {
		req = httptest.NewRequest(http.MethodPost, protocol.ExecutionSignal(execID, signalName), strings.NewReader(body))
		req.SetPathValue("id", execID)
		req.SetPathValue("name", signalName)
	}

	rec := httptest.NewRecorder()
	if byInstance {
		s.h.SignalExecution(rec, req)
	} else {
		s.h.SignalExecutionByExecution(rec, req)
	}
	var decoded signalResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &decoded)
	return rec, decoded
}

func assertCode(t *testing.T, rec *httptest.ResponseRecorder, decoded signalResponse, status int, code string) {
	t.Helper()
	if rec.Code != status || decoded.Status != status {
		t.Fatalf("status = %d/%d, want %d (body %s)", rec.Code, decoded.Status, status, rec.Body.String())
	}
	if decoded.Code != code {
		t.Fatalf("code = %q, want %q (body %s)", decoded.Code, code, rec.Body.String())
	}
}

const approveBody = `{"deliveryId":"del_1","payload":{"status":"approved"},"correlationKey":"cap-key"}`

// Both routes accept a delivery and acknowledge only after the domain has
// committed it: the durable delivery record exists when the 200 is written.
func TestSignalExecutionBothRoutesCommitBeforeAck(t *testing.T) {
	for _, tc := range []struct {
		name       string
		byInstance bool
	}{
		{"execution-scoped primary route", false},
		{"instance-scoped compatibility route", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSignalHarness(t)
			exec := s.seed(t, false, seededWait{
				cap:         shared.ID("cap"),
				signalName:  "approved",
				correlation: "cap-key",
				reason:      "awaiting approval",
			})
			rec, decoded := s.postSignal(t, tc.byInstance, s.inst.ID, string(exec.ID), "approved", approveBody)

			assertCode(t, rec, decoded, http.StatusOK, "")
			if decoded.Data.DeliveryID != "del_1" || decoded.Data.SignalName != "approved" {
				t.Fatalf("delivery data = %+v, want del_1/approved", decoded.Data)
			}
			if decoded.Data.Replay || decoded.Data.Status != "accepted" {
				t.Fatalf("delivery data = %+v, want a fresh accepted delivery", decoded.Data)
			}

			record, err := execution.NewExecutionStore(s.store).GetSignalDelivery(context.Background(), exec.ID, "del_1")
			if err != nil {
				t.Fatalf("delivery record missing after 200 (acknowledged before commit): %v", err)
			}
			if record.SignalName != "approved" || record.CorrelationKey != "cap-key" {
				t.Fatalf("delivery record = %+v, want approved/cap-key", record)
			}
		})
	}
}

// A repeated delivery with the same payload is an idempotent 200 replay and
// does not create a second durable delivery.
func TestSignalExecutionIdempotentReplay(t *testing.T) {
	s := newSignalHarness(t)
	exec := s.seed(t, false, seededWait{cap: "cap", signalName: "approved"})

	firstRec, first := s.postSignal(t, false, "", string(exec.ID), "approved", `{"deliveryId":"del_2","payload":{"status":"ok"}}`)
	assertCode(t, firstRec, first, http.StatusOK, "")
	if first.Data.Replay {
		t.Fatal("first delivery was reported as a replay")
	}

	secondRec, second := s.postSignal(t, false, "", string(exec.ID), "approved", `{"payload":{"status":"ok"},"deliveryId":"del_2"}`)
	assertCode(t, secondRec, second, http.StatusOK, "")
	if !second.Data.Replay || second.Data.Status != "replay" {
		t.Fatalf("replay data = %+v, want replay=true", second.Data)
	}
	if second.Data.DeliveryID != first.Data.DeliveryID {
		t.Fatalf("replay delivery id = %q, want %q", second.Data.DeliveryID, first.Data.DeliveryID)
	}

	if _, err := execution.NewExecutionStore(s.store).GetSignalDelivery(context.Background(), exec.ID, "del_2"); err != nil {
		t.Fatalf("original delivery record missing after replay: %v", err)
	}
}

// The same delivery ID with a different payload is a conflict, not a replay.
func TestSignalExecutionPayloadConflict(t *testing.T) {
	s := newSignalHarness(t)
	exec := s.seed(t, false, seededWait{cap: "cap", signalName: "approved"})

	firstRec, first := s.postSignal(t, false, "", string(exec.ID), "approved", `{"deliveryId":"del_3","payload":{"status":"ok"}}`)
	assertCode(t, firstRec, first, http.StatusOK, "")

	conflictRec, conflict := s.postSignal(t, false, "", string(exec.ID), "approved", `{"deliveryId":"del_3","payload":{"status":"denied"}}`)
	assertCode(t, conflictRec, conflict, http.StatusConflict, protocol.SignalCodeConflict)
}

// The request body is validated at the boundary: deliveryId is required and
// safe to use as one storage segment, and the body must be one JSON object.
func TestSignalExecutionBodyValidation(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"empty body", ""},
		{"malformed json", `{`},
		{"missing delivery id", `{"payload":{}}`},
		{"blank delivery id", `{"deliveryId":"   "}`},
		{"traversal delivery id", `{"deliveryId":"a/b"}`},
		{"non-object payload", `{"deliveryId":"del_1","payload":"bad"}`},
		{"trailing json", `{"deliveryId":"del_1"} {}`},
		{"oversized delivery id", `{"deliveryId":"` + strings.Repeat("d", maxSignalFieldBytes+1) + `"}`},
		{"oversized correlation key", `{"deliveryId":"del_1","correlationKey":"` + strings.Repeat("k", maxSignalFieldBytes+1) + `"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newSignalHarness(t)
			exec := s.seed(t, false, seededWait{cap: "cap", signalName: "approved"})
			rec, decoded := s.postSignal(t, false, "", string(exec.ID), "approved", tc.body)
			assertCode(t, rec, decoded, http.StatusBadRequest, protocol.SignalCodeInvalidRequest)
		})
	}
}

func TestSignalExecutionRejectsOversizedBody(t *testing.T) {
	s := newSignalHarness(t)
	exec := s.seed(t, false, seededWait{cap: "cap", signalName: "approved"})
	body := `{"deliveryId":"del_big","payload":{"data":"` + strings.Repeat("x", int(maxSignalBodyBytes)+1) + `"}}`

	rec, decoded := s.postSignal(t, false, "", string(exec.ID), "approved", body)
	assertCode(t, rec, decoded, http.StatusBadRequest, protocol.SignalCodeInvalidRequest)
}

// Fields at the documented bounds and a normal-sized payload remain accepted.
func TestSignalExecutionAcceptsFieldsAtBounds(t *testing.T) {
	s := newSignalHarness(t)
	deliveryID := strings.Repeat("d", maxSignalFieldBytes)
	correlation := strings.Repeat("k", maxSignalFieldBytes)
	exec := s.seed(t, false, seededWait{cap: "cap", signalName: "approved", correlation: correlation})
	body := `{"deliveryId":"` + deliveryID + `","payload":{"data":"` + strings.Repeat("x", 8<<10) + `"},"correlationKey":"` + correlation + `"}`

	rec, decoded := s.postSignal(t, false, "", string(exec.ID), "approved", body)
	assertCode(t, rec, decoded, http.StatusOK, "")
	if decoded.Data.DeliveryID != deliveryID {
		t.Fatalf("delivery id length = %d, want %d", len(decoded.Data.DeliveryID), maxSignalFieldBytes)
	}
}

// Several matching waits without a correlation key are ambiguous; a key that
// selects one of them delivers successfully.
func TestSignalExecutionAmbiguousWait(t *testing.T) {
	s := newSignalHarness(t)
	exec := s.seed(t, false,
		seededWait{cap: "cap_a", signalName: "approved", correlation: "key_a"},
		seededWait{cap: "cap_b", signalName: "approved", correlation: "key_b"},
	)

	rec, decoded := s.postSignal(t, false, "", string(exec.ID), "approved", `{"deliveryId":"del_4","payload":{}}`)
	assertCode(t, rec, decoded, http.StatusConflict, protocol.SignalCodeAmbiguousWait)

	rec, decoded = s.postSignal(t, false, "", string(exec.ID), "approved", `{"deliveryId":"del_5","payload":{},"correlationKey":"key_b"}`)
	assertCode(t, rec, decoded, http.StatusOK, "")
	if decoded.Data.DeliveryID != "del_5" {
		t.Fatalf("delivery id = %q, want del_5", decoded.Data.DeliveryID)
	}
}

func TestSignalExecutionNoMatchingWait(t *testing.T) {
	s := newSignalHarness(t)
	exec := s.seed(t, false, seededWait{cap: "cap", signalName: "approved"})
	rec, decoded := s.postSignal(t, false, "", string(exec.ID), "unrelated", `{"deliveryId":"del_6","payload":{}}`)
	assertCode(t, rec, decoded, http.StatusNotFound, protocol.SignalCodeNoMatchingWait)
}

func TestSignalExecutionTerminalAndExpired(t *testing.T) {
	t.Run("terminal", func(t *testing.T) {
		s := newSignalHarness(t)
		exec := s.seed(t, true, seededWait{cap: "cap", signalName: "approved"})
		rec, decoded := s.postSignal(t, false, "", string(exec.ID), "approved", `{"deliveryId":"del_7","payload":{}}`)
		assertCode(t, rec, decoded, http.StatusGone, protocol.SignalCodeExpired)
	})

	t.Run("past deadline", func(t *testing.T) {
		s := newSignalHarness(t)
		past := time.Now().Add(-time.Minute).UTC()
		exec := s.seed(t, false, seededWait{cap: "cap", signalName: "approved", waitUntil: &past})
		rec, decoded := s.postSignal(t, false, "", string(exec.ID), "approved", `{"deliveryId":"del_8","payload":{}}`)
		assertCode(t, rec, decoded, http.StatusGone, protocol.SignalCodeExpired)
	})
}

// A wrong instance and an unknown execution are both 404 for the primary and
// compatibility routes. No existence is leaked across the instance boundary.
func TestSignalExecutionAuthorizationAndUnknownExecution(t *testing.T) {
	t.Run("cross instance compatibility route", func(t *testing.T) {
		s := newSignalHarness(t)
		owned := createSignalInstance(t, s.h, "signal_other")
		exec := s.seed(t, false, seededWait{cap: "cap", signalName: "approved"})

		rec, decoded := s.postSignal(t, true, owned.ID, string(exec.ID), "approved", `{"deliveryId":"del_9","payload":{}}`)
		assertCode(t, rec, decoded, http.StatusNotFound, protocol.SignalCodeNotFound)
	})

	t.Run("unknown execution compatibility route", func(t *testing.T) {
		s := newSignalHarness(t)
		rec, decoded := s.postSignal(t, true, s.inst.ID, "request_missing", "approved", `{"deliveryId":"del_10","payload":{}}`)
		assertCode(t, rec, decoded, http.StatusNotFound, protocol.SignalCodeNotFound)
	})

	t.Run("unknown execution primary route", func(t *testing.T) {
		s := newSignalHarness(t)
		rec, decoded := s.postSignal(t, false, "", "request_missing", "approved", `{"deliveryId":"del_11","payload":{}}`)
		assertCode(t, rec, decoded, http.StatusNotFound, protocol.SignalCodeNotFound)
	})
}

// Payload validation is delegated to the authoritative domain helper via the
// wait's schema; the handler has no validation logic of its own.
func TestSignalExecutionUsesAuthoritativePayloadValidation(t *testing.T) {
	schema := `{"type":"object","required":["status"],"properties":{"status":{"type":"string"}},"additionalProperties":false}`
	s := newSignalHarness(t)
	exec := s.seed(t, false, seededWait{cap: "cap", signalName: "approved", schema: schema})

	rec, decoded := s.postSignal(t, false, "", string(exec.ID), "approved", `{"deliveryId":"bad_payload","payload":{"unexpected":true}}`)
	assertCode(t, rec, decoded, http.StatusBadRequest, protocol.SignalCodeInvalidPayload)

	rec, decoded = s.postSignal(t, false, "", string(exec.ID), "approved", `{"deliveryId":"good_payload","payload":{"status":"ok"}}`)
	assertCode(t, rec, decoded, http.StatusOK, "")
}

type listResponse struct {
	Message string                   `json:"message"`
	Status  int                      `json:"status"`
	Data    []protocol.ExecutionItem `json:"data"`
}

func (s *signalHarness) listExecutions(t *testing.T, query string) listResponse {
	t.Helper()
	url := "/v1/instances/" + s.inst.ID + "/executions"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.SetPathValue("id", s.inst.ID)
	rec := httptest.NewRecorder()
	s.h.ListExecutions(rec, req)
	var decoded listResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &decoded)
	if rec.Code != http.StatusOK {
		t.Fatalf("list executions = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	return decoded
}

// The list/filter surface exposes each suspended capability's wait reason,
// expected signal or timer, correlation key, and deadline, and supports
// narrowing to suspended executions by status.
func TestListExecutionsExposesWaitDetailsAndFilters(t *testing.T) {
	s := newSignalHarness(t)
	deadline := time.Now().Add(time.Hour).UTC()
	signalExec := s.seed(t, false, seededWait{
		cap:         "cap",
		signalName:  "approved",
		correlation: "approval-key",
		reason:      "awaiting approval",
		waitUntil:   &deadline,
	})
	timerExec := s.seed(t, false, seededWait{
		cap:         "cap",
		waitType:    execution.WaitTypeTimer,
		reason:      "cooling down",
		waitUntil:   &deadline,
		correlation: "timer-key",
	})

	all := s.listExecutions(t, "")
	if len(all.Data) != 2 {
		t.Fatalf("list = %d executions, want 2", len(all.Data))
	}
	byID := map[shared.ID]protocol.ExecutionItem{}
	for _, item := range all.Data {
		byID[item.ID] = item
	}

	signalItem, ok := byID[signalExec.ID]
	if !ok {
		t.Fatalf("signal execution %s missing from list", signalExec.ID)
	}
	if len(signalItem.Waits) != 1 {
		t.Fatalf("signal execution waits = %+v, want one", signalItem.Waits)
	}
	wait := signalItem.Waits[0]
	if wait.WaitType != string(execution.WaitTypeSignal) || wait.ExpectedSignal != "approved" ||
		wait.WaitReason != "awaiting approval" || wait.CorrelationKey != "approval-key" || wait.Deadline == nil {
		t.Fatalf("signal wait = %+v, want signal/approved/awaiting approval/approval-key/deadline", wait)
	}
	if *wait.Deadline != deadline.UnixNano() {
		t.Fatalf("deadline = %d, want %d", *wait.Deadline, deadline.UnixNano())
	}

	timerItem, ok := byID[timerExec.ID]
	if !ok {
		t.Fatalf("timer execution %s missing from list", timerExec.ID)
	}
	if len(timerItem.Waits) != 1 || timerItem.Waits[0].WaitType != string(execution.WaitTypeTimer) ||
		timerItem.Waits[0].WaitReason != "cooling down" || timerItem.Waits[0].Deadline == nil {
		t.Fatalf("timer execution waits = %+v, want one timer wait with reason and deadline", timerItem.Waits)
	}

	filtered := s.listExecutions(t, "status=waiting")
	if len(filtered.Data) != 2 {
		t.Fatalf("waiting filter = %d executions, want 2", len(filtered.Data))
	}
	filtered = s.listExecutions(t, "status=completed")
	if len(filtered.Data) != 0 {
		t.Fatalf("completed filter = %d executions, want 0", len(filtered.Data))
	}
}

// A single-execution read exposes the same wait details as the list.
func TestGetExecutionStateExposesWaitDetails(t *testing.T) {
	s := newSignalHarness(t)
	exec := s.seed(t, false, seededWait{
		cap:         "cap",
		signalName:  "approved",
		correlation: "approval-key",
		reason:      "awaiting approval",
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/instances/"+s.inst.ID+"/executions/"+string(exec.ID), nil)
	req.SetPathValue("id", s.inst.ID)
	req.SetPathValue("execID", string(exec.ID))
	rec := httptest.NewRecorder()
	s.h.GetExecutionState(rec, req)

	var decoded struct {
		Data protocol.ExecutionItem `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &decoded)
	if rec.Code != http.StatusOK || len(decoded.Data.Waits) != 1 {
		t.Fatalf("get execution state = %d, waits=%+v (body %s)", rec.Code, decoded.Data.Waits, rec.Body.String())
	}
	wait := decoded.Data.Waits[0]
	if wait.ExpectedSignal != "approved" || wait.WaitReason != "awaiting approval" || wait.CorrelationKey != "approval-key" {
		t.Fatalf("wait = %+v, want approved/awaiting approval/approval-key", wait)
	}
}
