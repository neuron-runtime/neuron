package instance

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/neuron-runtime/neuron/application/config"
	"github.com/neuron-runtime/neuron/shared/types/apitoken"
	"github.com/neuron-runtime/neuron/shared/types/projectruntime"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
	"github.com/spf13/cobra"
)

// TestInstanceCommandsUseDiscoveredProjectRuntime proves that every instance
// domain command shares the bootstrap selection: with no explicit endpoint they
// reach the live project runtime and present its discovery token, not the build
// daemon reachable through the configured socket.
func TestInstanceCommandsUseDiscoveredProjectRuntime(t *testing.T) {
	var hits int
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == protocol.HealthPath {
			_ = json.NewEncoder(w).Encode(protocol.Response{Status: http.StatusOK, Message: "ok"})
			return
		}
		hits++
		gotAuth = r.Header.Get(apitoken.AuthorizationHeader)
		var data any
		if r.Method == http.MethodGet {
			data = []protocol.InstanceResponse{}
		} else {
			data = map[string]any{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(protocol.Response{Status: http.StatusOK, Message: "ok", Data: data})
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	root := t.TempDir()
	if err := projectruntime.Write(root, projectruntime.New("127.0.0.1", u.Port(), root, os.Getpid(), "instance-token")); err != nil {
		t.Fatalf("write discovery record: %v", err)
	}

	cfg := config.Config{
		ProjectDir: root,
		Runtime:    config.RuntimeConfig{Host: "127.0.0.1"},
		Daemon: config.DaemonConfig{
			Socket:   filepath.Join(root, "unused.sock"),
			NorePath: filepath.Join(root, "missing-nore"),
		},
	}

	cases := []struct {
		name   string
		newCmd func() *cobra.Command
		args   []string
	}{
		{name: "list", newCmd: newListCmd},
		{name: "list executions", newCmd: newListCmd, args: []string{"--target", "inst_1", "--status", "waiting"}},
		{name: "signal", newCmd: newSignalCmd, args: []string{"exec_1", "approved", "--delivery-id", "del_1"}},
		{name: "cancel", newCmd: newCancelCmd, args: []string{"inst_1", "exec_1"}},
		{name: "remove", newCmd: newRemoveCmd, args: []string{"inst_1"}},
		{name: "clear", newCmd: newClearCmd},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hits = 0
			gotAuth = ""

			cmd := tc.newCmd()
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetContext(config.NewContext(context.Background(), cfg))
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if hits != 1 {
				t.Fatalf("project runtime hits = %d, want 1", hits)
			}
			if gotAuth != apitoken.Header("instance-token") {
				t.Fatalf("authorization = %q, want the discovery record token", gotAuth)
			}
		})
	}
}
