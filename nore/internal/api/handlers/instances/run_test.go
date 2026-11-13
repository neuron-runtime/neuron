package instances

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/neuron-runtime/neuron/nore/internal/assembly"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

// registerRunnableAssembly persists a minimal Assembly with one in-process
// capability so a run can start and reach a terminal state.
func registerRunnableAssembly(t *testing.T, h *Handler, key protocol.InstanceKey) {
	t.Helper()
	_, _, err := h.assemblies.Register(context.Background(), assembly.RegisteredAssembly{
		Key: key,
		Assembly: shared.Assembly{
			Metadata: shared.Metadata{Name: key.AssemblyID, Version: key.Version},
			Specification: shared.AssemblySpec{
				Capabilities: []shared.Capability{{
					Metadata: shared.Metadata{ID: shared.NewID("svc_"), Name: "sys.say", Version: "1.0.0"},
					Type:     shared.CoreName("set"),
				}},
			},
		},
	})
	if err != nil {
		t.Fatalf("register runnable assembly %s: %v", key.String(), err)
	}
}

func runRequest(t *testing.T, h *Handler, rawQuery, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/run"+rawQuery, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.Run(rec, req)
	return rec
}

// The Assembly selector is the one thing the facade cannot infer, so omitting it
// must fail loudly with a client error naming the parameter.
func TestRunMissingSelectorReturns400(t *testing.T) {
	h := newTestHandler(t)
	rec := runRequest(t, h, "", `{"params":{}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("run without selector = status %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
}

// A selector naming no registered Assembly is a client error, not a server
// fault, and must not create an instance.
func TestRunUnknownAssemblyReturns404(t *testing.T) {
	h := newTestHandler(t)
	rec := runRequest(t, h, "?assembly=missing:1.0.0", `{"params":{}}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("run unknown assembly = status %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
}

// A registered Assembly runs synchronously and its terminal status is reported.
func TestRunExecutesAndReturnsResult(t *testing.T) {
	h := newTestHandler(t)
	key := protocol.InstanceKey{AssemblyID: "sys", Version: "1.0.0", Hash: "h", Env: "dev"}
	registerRunnableAssembly(t, h, key)

	rec := runRequest(t, h, "?assembly="+key.ColonString(), `{"params":{}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("run = status %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	var body struct {
		Data protocol.ExecutionResult `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Data.Status != "completed" {
		t.Errorf("status = %q, want completed (body %s)", body.Data.Status, rec.Body.String())
	}
	if body.Data.ExecutionID == "" {
		t.Errorf("execution_id is empty; the result must identify the execution it came from")
	}
	if body.Data.Error != "" {
		t.Errorf("error = %q, want empty", body.Data.Error)
	}
}

// The response body is the whole contract the facade publishes. Requiring it to
// stay within the result fields is what keeps execution history from leaking in
// through a later refactor.
func TestRunResponseExposesNoExecutionHistory(t *testing.T) {
	h := newTestHandler(t)
	key := protocol.InstanceKey{AssemblyID: "sys", Version: "1.0.0", Hash: "h", Env: "dev"}
	registerRunnableAssembly(t, h, key)

	rec := runRequest(t, h, "?assembly="+key.ColonString(), `{"params":{}}`)

	var body struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	allowed := map[string]bool{
		"execution_id": true,
		"instance_id":  true,
		"status":       true,
		"error":        true,
		"result":       true,
	}
	for field := range body.Data {
		if !allowed[field] {
			t.Errorf("/v1/run exposes unexpected field %q; the facade must not leak execution history", field)
		}
	}
}
