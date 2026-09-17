package instances

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Muhammad-Jay/neuron/nore/internal/instance"
	"github.com/Muhammad-Jay/neuron/nore/internal/planner"
	"github.com/Muhammad-Jay/neuron/nore/internal/resolver"
	"github.com/Muhammad-Jay/neuron/nore/internal/storage"
	"github.com/Muhammad-Jay/neuron/nore/internal/storage/sqlite"
	"github.com/Muhammad-Jay/neuron/nore/internal/system"
	shared "github.com/Muhammad-Jay/neuron/shared/types/core"
	"github.com/Muhammad-Jay/neuron/shared/types/protocol"
)

// newTestHandler wires the instances handler against an isolated sqlite store
// with no registered systems or instances.
func newTestHandler(t *testing.T) *Handler {
	t.Helper()

	store, err := sqlite.New(storage.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	systemsRepo := system.NewRepository(store)
	m := instance.NewManager(context.Background(), 2, store, systemsRepo)

	cel, err := resolver.NewCELCompiler(resolver.DefaultCELConfig())
	if err != nil {
		t.Fatalf("new CEL compiler: %v", err)
	}
	compiler, err := planner.NewCompiler(cel)
	if err != nil {
		t.Fatalf("new planner compiler: %v", err)
	}

	return New(m, systemsRepo, compiler)
}

// registerSystem persists an arbitrary RegisteredSystem keyed as given.
func registerSystem(t *testing.T, h *Handler, key protocol.InstanceKey) {
	t.Helper()
	_, _, err := h.systems.Register(context.Background(), system.RegisteredSystem{
		Key:    key,
		System: shared.System{Metadata: shared.Metadata{Name: key.SystemID}},
	})
	if err != nil {
		t.Fatalf("register system %s: %v", key.String(), err)
	}
}

func testListExecutions(t *testing.T, h *Handler, id string) (*httptest.ResponseRecorder, int, []protocol.ExecutionItem) {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/v1/instances/"+id+"/executions", nil)
	req.SetPathValue("id", id)

	rec := httptest.NewRecorder()
	h.ListExecutions(rec, req)

	var body struct {
		Message string                   `json:"message"`
		Status  int                      `json:"status"`
		Data    []protocol.ExecutionItem `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec, body.Status, body.Data
}

// A system that is registered but never instantiated is addressable by a
// partial key: listing its executions yields an empty list, not a 404, so
// `neuron instance list --target=<name>@<version>` works before the first run.
func TestListExecutionsRegisteredButIdleReturnsEmpty(t *testing.T) {
	h := newTestHandler(t)
	registerSystem(t, h, protocol.InstanceKey{
		SystemID: "sys", Version: "2.0.0", Hash: "h", Env: "wait",
	})

	rec, status, items := testListExecutions(t, h, "sys:2.0.0::")
	if status != http.StatusOK {
		t.Fatalf("list executions = status %d, want 200 (body %s)", status, rec.Body.String())
	}
	if len(items) != 0 {
		t.Errorf("executions = %v, want empty", items)
	}
}

// A full key (hash+env) for a registered but idle system behaves the same way.
func TestListExecutionsFullKeyIdleReturnsEmpty(t *testing.T) {
	h := newTestHandler(t)
	registerSystem(t, h, protocol.InstanceKey{
		SystemID: "sys", Version: "2.0.0", Hash: "h", Env: "wait",
	})

	rec, status, items := testListExecutions(t, h, "sys:2.0.0:h:wait")
	if status != http.StatusOK {
		t.Fatalf("list executions = status %d, want 200 (body %s)", status, rec.Body.String())
	}
	if len(items) != 0 {
		t.Errorf("executions = %v, want empty", items)
	}
}

// An unregistered system key must 404.
func TestListExecutionsUnknownSystemReturns404(t *testing.T) {
	h := newTestHandler(t)
	registerSystem(t, h, protocol.InstanceKey{
		SystemID: "sys", Version: "2.0.0", Hash: "h", Env: "wait",
	})

	rec, status, _ := testListExecutions(t, h, "unknown-system:1.0.0")
	if status != http.StatusNotFound {
		t.Fatalf("list executions = status %d, want 404 (body %s)", status, rec.Body.String())
	}
}

// An unknown instance ID always 404s; it never falls back to system lookup.
func TestListExecutionsUnknownInstanceIDReturns404(t *testing.T) {
	h := newTestHandler(t)

	rec, status, _ := testListExecutions(t, h, "inst_deadbeef")
	if status != http.StatusNotFound {
		t.Fatalf("list executions = status %d, want 404 (body %s)", status, rec.Body.String())
	}
}

// A stale instance restored metadata-only after a daemon restart (its runtime
// is gone, status coerced to failed) must be transparently recreated by
// Execute rather than rejected with "instance is not running". This is what
// makes `neuron run` re-run an already-instantiated system.
func TestExecuteRecreatesStaleRestoredInstance(t *testing.T) {
	store, err := sqlite.New(storage.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	systemsRepo := system.NewRepository(store)
	key := protocol.InstanceKey{SystemID: "sys", Version: "1.0.0", Hash: "h", Env: "dev"}
	if _, _, err := systemsRepo.Register(context.Background(), system.RegisteredSystem{
		Key: key,
		System: shared.System{
			Metadata: shared.Metadata{Name: key.SystemID, Version: key.Version},
			Specification: shared.SystemSpec{
				Services: []shared.Service{{
					Metadata: shared.Metadata{ID: shared.NewID("svc_"), Name: "sys.say", Version: "1.0.0"},
					Type:     shared.CoreName("set"),
				}},
			},
		},
	}); err != nil {
		t.Fatalf("register system: %v", err)
	}

	// Seed an instance metadata record as a previous process would have left it.
	rec := map[string]any{
		"id":                 "inst_stale",
		"system_id":          key.SystemID,
		"version":            key.Version,
		"hash":               key.Hash,
		"env":                key.Env,
		"status":             "running",
		"blueprint_metadata": map[string]any{},
		"created_at":         time.Now().UTC(),
		"updated_at":         time.Now().UTC(),
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	if err := store.Put(context.Background(), "instances/inst_stale", data); err != nil {
		t.Fatalf("seed metadata: %v", err)
	}

	// A fresh manager reconciles the record as metadata-only; the runtime is
	// intentionally absent.
	m := instance.NewManager(context.Background(), 2, store, systemsRepo)
	stale, ok := m.GetByID("inst_stale")
	if !ok {
		t.Fatal("stale instance was not restored")
	}
	if stale.Status() == instance.StatusRunning {
		t.Fatal("restored instance should not report running")
	}

	cel, err := resolver.NewCELCompiler(resolver.DefaultCELConfig())
	if err != nil {
		t.Fatalf("new CEL compiler: %v", err)
	}
	compiler, err := planner.NewCompiler(cel)
	if err != nil {
		t.Fatalf("new planner compiler: %v", err)
	}
	h := New(m, systemsRepo, compiler)

	body := `{"mode":"detach"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/instances/inst_stale/executions", strings.NewReader(body))
	req.SetPathValue("id", "inst_stale")
	w := httptest.NewRecorder()
	h.Execute(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("execute stale instance = status %d, want 202 (body %s)", w.Code, w.Body.String())
	}
	recreated, ok := m.GetByID("inst_stale")
	if !ok || recreated.Status() != instance.StatusRunning {
		t.Fatalf("instance was not recreated as running (ok=%v status=%v)", ok, recreated)
	}
}
