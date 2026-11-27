package run

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/neuron-runtime/neuron/application/config"
	"github.com/neuron-runtime/neuron/shared/types/apitoken"
	"github.com/neuron-runtime/neuron/shared/types/projectruntime"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

// TestRunUsesDiscoveredProjectRuntimeToken keeps the assembly execution path on
// the same shared discovery and token-adoption helper as the instance commands.
// A run must reach the project runtime that owns executions and present the
// credential published in its discovery record.
func TestRunUsesDiscoveredProjectRuntimeToken(t *testing.T) {
	var gotPath, gotQuery, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get(apitoken.AuthorizationHeader)
		_ = json.NewEncoder(w).Encode(protocol.Response{
			Status:  http.StatusOK,
			Message: "ok",
			Data: protocol.ExecutionResult{
				Result: map[string]any{"source": "project-runtime"},
			},
		})
	}))
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	root := t.TempDir()
	if err := projectruntime.Write(root, projectruntime.New("127.0.0.1", u.Port(), root, os.Getpid(), "run-token")); err != nil {
		t.Fatalf("write discovery record: %v", err)
	}

	cfg := config.Config{ProjectDir: root, Runtime: config.RuntimeConfig{Host: "127.0.0.1"}}
	cmd := New()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(config.NewContext(context.Background(), cfg))
	cmd.SetArgs([]string{"inst_1"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute run: %v\n%s", err, out.String())
	}
	if gotPath != protocol.RunPath {
		t.Fatalf("path = %q, want %q", gotPath, protocol.RunPath)
	}
	if !strings.Contains(gotQuery, "assembly=inst_1") {
		t.Errorf("query = %q, want the explicit selector", gotQuery)
	}
	if gotAuth != apitoken.Header("run-token") {
		t.Errorf("authorization = %q, want the discovery record token", gotAuth)
	}
	if !strings.Contains(out.String(), "project-runtime") {
		t.Errorf("output = %q, want the discovered runtime's result", out.String())
	}
}
