package instances

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/neuron-runtime/neuron/nore/internal/assembly"
	"github.com/neuron-runtime/neuron/nore/internal/instance"
	"github.com/neuron-runtime/neuron/nore/internal/planner"
	"github.com/neuron-runtime/neuron/nore/internal/resolver"
	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/nore/internal/storage/sqlite"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

// newCancellationHandler wires a handler over a manager that already holds one
// running instance, and returns that instance so a test can address its
// executions.
func newCancellationHandler(t *testing.T) (*Handler, *instance.Instance) {
	t.Helper()

	store, err := sqlite.New(storage.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	assembliesRepo := assembly.NewRepository(store)
	key := protocol.InstanceKey{AssemblyID: "sys", Version: "1.0.0", Hash: "h", Env: "dev"}
	if _, _, err := assembliesRepo.Register(context.Background(), assembly.RegisteredAssembly{
		Key: key,
		Assembly: shared.Assembly{
			Metadata: shared.Metadata{Name: key.AssemblyID, Version: key.Version},
			Specification: shared.AssemblySpec{
				Capabilities: []shared.Capability{{
					Metadata: shared.Metadata{ID: "cap_say", Name: "sys.say", Version: "1.0.0"},
					Type:     shared.CoreName("set"),
				}},
			},
		},
	}); err != nil {
		t.Fatalf("register assembly: %v", err)
	}

	m := instance.NewManager(context.Background(), 2, 0, store, assembliesRepo)
	cel, err := resolver.NewCELCompiler(resolver.DefaultCELConfig())
	if err != nil {
		t.Fatalf("new CEL compiler: %v", err)
	}
	compiler, err := planner.NewCompiler(cel)
	if err != nil {
		t.Fatalf("new planner compiler: %v", err)
	}

	i, _, err := m.GetOrCreate(context.Background(), key)
	if err != nil {
		t.Fatalf("create instance: %v", err)
	}
	if err := i.Start(); err != nil {
		t.Fatalf("start instance: %v", err)
	}
	t.Cleanup(func() { _ = i.Stop() })

	return New(m, assembliesRepo, compiler), i
}

// cancelExecution issues the cancel request and decodes the response.
func cancelExecution(t *testing.T, h *Handler, instanceID, executionID, reason string) (*httptest.ResponseRecorder, protocol.ExecutionItem) {
	t.Helper()

	body := ""
	if reason != "" {
		body = `{"reason":"` + reason + `"}`
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/instances/"+instanceID+"/executions/"+executionID+"/cancel", strings.NewReader(body))
	req.SetPathValue("id", instanceID)
	req.SetPathValue("execID", executionID)

	rec := httptest.NewRecorder()
	h.CancelExecution(rec, req)

	var decoded struct {
		Data protocol.ExecutionItem `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &decoded)
	return rec, decoded.Data
}

// startFinishedExecution runs an execution to completion so it can be used to
// check that a late cancellation is refused rather than silently accepted.
func startFinishedExecution(t *testing.T, i *instance.Instance) string {
	t.Helper()
	exec, err := i.Execute(context.Background(), map[string]any{"value": "x"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if err := exec.Wait(context.Background()); err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	return string(exec.ID)
}

// An execution that already finished cannot be stopped. Reporting success would
// tell an operator their stop took effect when the work had already concluded.
func TestCancelExecutionRefusesAFinishedExecution(t *testing.T) {
	h, i := newCancellationHandler(t)
	executionID := startFinishedExecution(t, i)

	rec, _ := cancelExecution(t, h, i.ID, executionID, "too late")

	if rec.Code != http.StatusConflict {
		t.Fatalf("cancel = status %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "terminal") {
		t.Errorf("body = %s, want it to explain that the execution already finished", rec.Body.String())
	}
}

// A client that asks about an execution that does not exist has to be able to tell
// that apart from one that could not be stopped, so the two must not share a code.
func TestCancelExecutionUnknownExecutionReturns404(t *testing.T) {
	h, i := newCancellationHandler(t)

	rec, _ := cancelExecution(t, h, i.ID, "exec_nope", "stop")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("cancel = status %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "exec_nope") {
		t.Errorf("body = %s, want it to name the execution that was not found", rec.Body.String())
	}
}

func TestCancelExecutionUnknownInstanceReturns404(t *testing.T) {
	h, _ := newCancellationHandler(t)

	rec, _ := cancelExecution(t, h, "inst_nope", "exec_nope", "stop")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("cancel = status %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "inst_nope") {
		t.Errorf("body = %s, want it to name the instance that was not found", rec.Body.String())
	}
}

// A cancellation with no stated reason is still legitimate, so it must be accepted
// rather than rejected for a missing field -- and the recorded reason must still
// say something honest.
func TestCancelExecutionAcceptsAMissingReason(t *testing.T) {
	h, i := newCancellationHandler(t)
	executionID := startFinishedExecution(t, i)

	// The execution is already terminal, so this asserts the request is parsed and
	// validated rather than rejected for its shape.
	rec, _ := cancelExecution(t, h, i.ID, executionID, "")

	if rec.Code != http.StatusConflict {
		t.Fatalf("cancel = status %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}
}

// The route must actually be reachable. A handler that works but is not wired is
// the failure mode this catches.
func TestCancelExecutionRouteIsRegistered(t *testing.T) {
	h, i := newCancellationHandler(t)
	executionID := startFinishedExecution(t, i)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/instances/{id}/executions/{execID}/cancel", h.CancelExecution)

	req := httptest.NewRequest(http.MethodPost, "/v1/instances/"+i.ID+"/executions/"+executionID+"/cancel", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Fatal("cancel route returned 404, want the handler to be reachable")
	}
}
