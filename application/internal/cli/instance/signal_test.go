package instance

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neuron-runtime/neuron/application/config"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

func TestSignalCommandDeliversPayload(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody protocol.SignalDeliveryRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			_ = json.NewEncoder(w).Encode(protocol.Response{Status: http.StatusOK, Message: "ok"})
			return
		}
		gotPath = r.URL.Path
		gotMethod = r.Method
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(protocol.Response{
			Status:  http.StatusOK,
			Message: "execution signaled",
			Data: protocol.SignalDeliveryResponse{
				DeliveryID: "del_1",
				SignalName: "approved",
				Status:     "accepted",
			},
		})
	}))
	t.Cleanup(srv.Close)

	cmd := newSignalCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(config.NewContext(context.Background(), config.Config{Daemon: config.DaemonConfig{Endpoint: srv.URL}}))
	cmd.SetArgs([]string{"exec_1", "approved", "--delivery-id", "del_1", "--payload", `{"status":"approved"}`, "--correlation-key", "cap-key"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, out.String())
	}

	if gotMethod != http.MethodPost {
		t.Fatalf("method = %s, want POST", gotMethod)
	}
	if gotPath != protocol.ExecutionSignal("exec_1", "approved") {
		t.Fatalf("path = %s, want %s", gotPath, protocol.ExecutionSignal("exec_1", "approved"))
	}
	if gotBody.DeliveryID != "del_1" || gotBody.CorrelationKey != "cap-key" {
		t.Fatalf("body = %+v", gotBody)
	}
	if gotBody.Payload["status"] != "approved" {
		t.Fatalf("payload = %+v", gotBody.Payload)
	}
	if !strings.Contains(out.String(), `accepted signal "approved" for execution exec_1`) {
		t.Fatalf("output = %q", out.String())
	}
}

func TestSignalCommandReadsPayloadFile(t *testing.T) {
	var gotBody protocol.SignalDeliveryRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			_ = json.NewEncoder(w).Encode(protocol.Response{Status: http.StatusOK, Message: "ok"})
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(protocol.Response{
			Status:  http.StatusOK,
			Message: "execution signaled",
			Data:    protocol.SignalDeliveryResponse{DeliveryID: "del_2", SignalName: "approved", Status: "replay", Replay: true},
		})
	}))
	t.Cleanup(srv.Close)

	payloadPath := filepath.Join(t.TempDir(), "payload.json")
	if err := os.WriteFile(payloadPath, []byte(`{"approved":true}`), 0o644); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	cmd := newSignalCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(config.NewContext(context.Background(), config.Config{Daemon: config.DaemonConfig{Endpoint: srv.URL}}))
	cmd.SetArgs([]string{"exec_1", "approved", "--delivery-id", "del_2", "--payload-file", payloadPath})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, out.String())
	}
	if gotBody.Payload["approved"] != true {
		t.Fatalf("payload = %+v", gotBody.Payload)
	}
	if !strings.Contains(out.String(), "replayed signal") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestSignalCommandMapsStableErrorCodes(t *testing.T) {
	tests := []struct {
		name   string
		code   string
		want   string
		status int
	}{
		{name: "conflict", code: protocol.SignalCodeConflict, status: http.StatusConflict, want: "conflicts with an existing delivery"},
		{name: "ambiguous", code: protocol.SignalCodeAmbiguousWait, status: http.StatusConflict, want: "provide --correlation-key"},
		{name: "no wait", code: protocol.SignalCodeNoMatchingWait, status: http.StatusNotFound, want: "no waiting capability"},
		{name: "expired", code: protocol.SignalCodeExpired, status: http.StatusGone, want: "no longer waiting"},
		{name: "not found", code: protocol.SignalCodeNotFound, status: http.StatusNotFound, want: "was not found"},
		{name: "invalid request", code: protocol.SignalCodeInvalidRequest, status: http.StatusBadRequest, want: "invalid signal request"},
		{name: "invalid payload", code: protocol.SignalCodeInvalidPayload, status: http.StatusBadRequest, want: "invalid signal payload"},
		{name: "unavailable", code: protocol.SignalCodeUnavailable, status: http.StatusServiceUnavailable, want: "signal delivery is unavailable"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/health" {
					_ = json.NewEncoder(w).Encode(protocol.Response{Status: http.StatusOK, Message: "ok"})
					return
				}
				w.WriteHeader(tt.status)
				_ = json.NewEncoder(w).Encode(protocol.Response{Status: tt.status, Message: "detail", Code: tt.code})
			}))
			t.Cleanup(srv.Close)

			cmd := newSignalCmd()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetContext(config.NewContext(context.Background(), config.Config{Daemon: config.DaemonConfig{Endpoint: srv.URL}}))
			cmd.SetArgs([]string{"exec_1", "approved", "--delivery-id", "del_1"})

			err := cmd.Execute()
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestSignalCommandRejectsInvalidPayloadJSON(t *testing.T) {
	cmd := newSignalCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(config.NewContext(context.Background(), config.Config{}))
	cmd.SetArgs([]string{"exec_1", "approved", "--delivery-id", "del_1", "--payload", "[]"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "JSON object") {
		t.Fatalf("error = %v", err)
	}
}
