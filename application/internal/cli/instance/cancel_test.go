package instance

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/neuron-runtime/neuron/application/config"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

func TestCancelCommandSendsReason(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody protocol.CancelExecutionRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		if r.URL.Path == "/health" {
			_ = json.NewEncoder(w).Encode(protocol.Response{Status: http.StatusOK, Message: "ok"})
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(protocol.Response{
			Status:  http.StatusOK,
			Message: "execution cancelled",
			Data: protocol.ExecutionItem{
				ID:     "exec_1",
				Status: "cancelled",
			},
		})
	}))
	t.Cleanup(srv.Close)

	cmd := newCancelCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(config.NewContext(context.Background(), config.Config{Daemon: config.DaemonConfig{Endpoint: srv.URL}}))
	cmd.SetArgs([]string{"inst_1", "exec_1", "--reason", "operator request"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, out.String())
	}

	if gotMethod != http.MethodPost {
		t.Fatalf("method = %s, want POST", gotMethod)
	}
	if gotPath != protocol.CancelExecution("inst_1", "exec_1") {
		t.Fatalf("path = %s, want %s", gotPath, protocol.CancelExecution("inst_1", "exec_1"))
	}
	if gotBody.Reason != "operator request" {
		t.Fatalf("reason = %q", gotBody.Reason)
	}
	if !strings.Contains(out.String(), "cancelled execution exec_1 (status=cancelled)") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestCancelCommandReportsConflictClearly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			_ = json.NewEncoder(w).Encode(protocol.Response{Status: http.StatusOK, Message: "ok"})
			return
		}
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(protocol.Response{Status: http.StatusConflict, Message: "execution is already terminal"})
	}))
	t.Cleanup(srv.Close)

	cmd := newCancelCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(config.NewContext(context.Background(), config.Config{Daemon: config.DaemonConfig{Endpoint: srv.URL}}))
	cmd.SetArgs([]string{"inst_1", "exec_1"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "cannot be cancelled") {
		t.Fatalf("error = %v", err)
	}
}
