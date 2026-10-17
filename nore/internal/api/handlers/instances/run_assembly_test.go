package instances

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

func asmRequest(t *testing.T, h *Handler, method, path string, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	switch {
	case method == http.MethodGet && path == protocol.AssembliesPath:
		h.ListAssemblies(rec, req)
	case method == http.MethodGet:
		req.SetPathValue("name", req.URL.Path[len("/v1/assemblies/"):])
		h.GetAssembly(rec, req)
	case path == protocol.AssemblyDefaultRunPath:
		h.RunDefault(rec, req)
	default:
		req.SetPathValue("name", nameFromRunPath(req.URL.Path))
		h.RunNamed(rec, req)
	}
	return rec
}

func nameFromRunPath(path string) string {
	const prefix = "/v1/assemblies/"
	const suffix = "/run"
	return path[len(prefix) : len(path)-len(suffix)]
}

func TestRunNamedAssemblyExecutes(t *testing.T) {
	h := newTestHandler(t)
	key := protocol.InstanceKey{AssemblyID: "orders", Version: "1.0.0", Hash: "h", Env: "dev"}
	registerRunnableAssembly(t, h, key)

	rec := asmRequest(t, h, http.MethodPost, "/v1/assemblies/orders/run", `{"params":{}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("named run = status %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
}

func TestRunNamedUnknownAssemblyReturns404(t *testing.T) {
	h := newTestHandler(t)
	rec := asmRequest(t, h, http.MethodPost, "/v1/assemblies/missing/run", `{"params":{}}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("named run unknown = status %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
}

func TestRunDefaultAssemblyRequiresExactlyOne(t *testing.T) {
	h := newTestHandler(t)

	// No assemblies registered: no default can be inferred.
	rec := asmRequest(t, h, http.MethodPost, protocol.AssemblyDefaultRunPath, `{"params":{}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("default run with none registered = status %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}

	// A single assembly is implicitly the default.
	registerRunnableAssembly(t, h, protocol.InstanceKey{AssemblyID: "solo", Version: "1.0.0", Hash: "h", Env: "dev"})
	rec = asmRequest(t, h, http.MethodPost, protocol.AssemblyDefaultRunPath, `{"params":{}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("default run with one assembly = status %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
}

func TestDefaultRunAmbiguousWithMultipleAssemblies(t *testing.T) {
	h := newTestHandler(t)
	registerRunnableAssembly(t, h, protocol.InstanceKey{AssemblyID: "a", Version: "1.0.0", Hash: "h", Env: "dev"})
	registerRunnableAssembly(t, h, protocol.InstanceKey{AssemblyID: "b", Version: "1.0.0", Hash: "h", Env: "dev"})

	rec := asmRequest(t, h, http.MethodPost, protocol.AssemblyDefaultRunPath, `{"params":{}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("ambiguous default run = status %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
}

func TestListAssembliesReportsRegistered(t *testing.T) {
	h := newTestHandler(t)
	registerRunnableAssembly(t, h, protocol.InstanceKey{AssemblyID: "orders", Version: "1.0.0", Hash: "h", Env: "dev"})

	rec := asmRequest(t, h, http.MethodGet, protocol.AssembliesPath, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list = status %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Data []protocol.AssemblyResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	found := false
	for _, a := range body.Data {
		if a.AssemblyID == "orders" {
			found = true
		}
	}
	if !found {
		t.Fatalf("registered assembly missing from list: %s", rec.Body.String())
	}
}

func TestGetAssemblyMetadata(t *testing.T) {
	h := newTestHandler(t)
	registerRunnableAssembly(t, h, protocol.InstanceKey{AssemblyID: "orders", Version: "1.0.0", Hash: "h", Env: "dev"})

	rec := asmRequest(t, h, http.MethodGet, "/v1/assemblies/orders", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get assembly = status %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Data protocol.AssemblyResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode assembly: %v", err)
	}
	if body.Data.AssemblyID != "orders" {
		t.Errorf("assembly_id = %q, want orders", body.Data.AssemblyID)
	}
}

func TestGetAssemblyUnknownReturns404(t *testing.T) {
	h := newTestHandler(t)
	rec := asmRequest(t, h, http.MethodGet, "/v1/assemblies/missing", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get unknown assembly = status %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
}
