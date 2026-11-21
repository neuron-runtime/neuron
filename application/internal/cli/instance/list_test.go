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

func TestListCommandStatusWaitingShowsWaitDetails(t *testing.T) {
	var gotPath, gotQuery string
	deadline := int64(1716200000000000000)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			_ = json.NewEncoder(w).Encode(protocol.Response{Status: http.StatusOK, Message: "ok"})
			return
		}
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(protocol.Response{
			Status:  http.StatusOK,
			Message: "executions",
			Data: []protocol.ExecutionItem{
				{
					ID:     "exec_wait",
					Status: "waiting",
					Waits: []protocol.WaitItem{
						{
							CapabilityID:   "cap_1",
							WaitType:       "signal",
							ExpectedSignal: "approved",
							WaitReason:     "approval required",
							CorrelationKey: "order-42",
							Deadline:       &deadline,
						},
					},
				},
			},
		})
	}))
	t.Cleanup(srv.Close)

	cmd := newListCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(config.NewContext(context.Background(), config.Config{Daemon: config.DaemonConfig{Endpoint: srv.URL}}))
	cmd.SetArgs([]string{"--target", "inst_1", "--status", "waiting"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, out.String())
	}

	if gotPath != protocol.InstanceExecutions("inst_1") {
		t.Fatalf("path = %s", gotPath)
	}
	if gotQuery != "status=waiting" {
		t.Fatalf("query = %q", gotQuery)
	}
	output := out.String()
	for _, want := range []string{"exec_wait", "waiting", "approved", "order-42", "approval", "Waits:"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}
