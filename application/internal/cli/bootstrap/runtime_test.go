package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neuron-runtime/neuron/application/config"
	"github.com/neuron-runtime/neuron/shared/types/apitoken"
	"github.com/neuron-runtime/neuron/shared/types/projectruntime"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

// writeRecord publishes a discovery record for a project root. It uses the
// shared writer so the test exercises the same contract the runtime writes.
func writeRecord(t *testing.T, root string, record projectruntime.Record) {
	t.Helper()
	if err := projectruntime.Write(root, record); err != nil {
		t.Fatalf("write record: %v", err)
	}
}

// deadPID returns a PID that is guaranteed not to be running: it starts a
// trivial process and reaps it, so the OS has reclaimed the PID.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper process: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("reap helper process: %v", err)
	}
	return pid
}

func TestFindRuntimeRecordWalksUp(t *testing.T) {
	root := t.TempDir()
	writeRecord(t, root, projectruntime.New("127.0.0.1", "41001", root, os.Getpid(), "tok"))

	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}

	record, found, err := findRuntimeRecord(nested)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if !found {
		t.Fatal("record not found from nested directory")
	}
	if record.Endpoint() != "http://127.0.0.1:41001" {
		t.Errorf("endpoint = %q, want http://127.0.0.1:41001", record.Endpoint())
	}
}

func TestFindRuntimeRecordMissingIsNotAnError(t *testing.T) {
	record, found, err := findRuntimeRecord(t.TempDir())
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if found {
		t.Errorf("found a record where none exists: %+v", record)
	}
}

// A record left behind by a crashed runtime must be rejected: believing it is
// live would send commands to a dead port.
func TestFindRuntimeRecordRejectsStaleProcess(t *testing.T) {
	root := t.TempDir()
	writeRecord(t, root, projectruntime.New("127.0.0.1", "41002", root, deadPID(t), "tok"))

	_, found, err := findRuntimeRecord(root)
	if err == nil {
		t.Fatal("expected an error for a stale record")
	}
	if found {
		t.Error("stale record reported as found")
	}
	if !strings.Contains(err.Error(), "not alive") {
		t.Errorf("error = %v, want it to mention the dead process", err)
	}
}

// A record whose project root does not match the directory it was found in is a
// foreign or misplaced record and must not be trusted.
func TestFindRuntimeRecordRejectsForeignRoot(t *testing.T) {
	root := t.TempDir()
	writeRecord(t, root, projectruntime.New("127.0.0.1", "41003", filepath.Join(root, "elsewhere"), os.Getpid(), "tok"))

	_, _, err := findRuntimeRecord(root)
	if err == nil {
		t.Fatal("expected an error for a root mismatch")
	}
	if !strings.Contains(err.Error(), "mismatch") {
		t.Errorf("error = %v, want a root mismatch", err)
	}
}

// Two projects running at once each own their own record; discovery from one
// project must never see the other's endpoint.
func TestConcurrentProjectsAreIsolated(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()
	writeRecord(t, rootA, projectruntime.New("127.0.0.1", "41003", rootA, os.Getpid(), "a"))
	writeRecord(t, rootB, projectruntime.New("127.0.0.1", "41004", rootB, os.Getpid(), "b"))

	a, _, err := findRuntimeRecord(projectA(t, rootA))
	if err != nil {
		t.Fatalf("find A: %v", err)
	}
	b, _, err := findRuntimeRecord(rootB)
	if err != nil {
		t.Fatalf("find B: %v", err)
	}

	if a.Port != "41003" {
		t.Errorf("project A resolved port %q, want its own 41003", a.Port)
	}
	if b.Port != "41004" {
		t.Errorf("project B resolved port %q, want its own 41004", b.Port)
	}
	if a.Token == b.Token {
		t.Error("two projects resolved the same token")
	}
}

// projectA returns a nested directory inside root so the walk must stop at the
// correct project rather than at the filesystem root.
func projectA(t *testing.T, root string) string {
	t.Helper()
	nested := filepath.Join(root, "assembly")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return nested
}

func TestDiscoverProjectRuntimeOverridePortStillFindsToken(t *testing.T) {
	root := t.TempDir()
	writeRecord(t, root, projectruntime.New("127.0.0.1", "41003", root, os.Getpid(), "tok"))

	cfg := config.Config{ProjectDir: root, Runtime: config.RuntimeConfig{Host: "127.0.0.1"}}
	endpoint, token, err := DiscoverProjectRuntime(cfg, "6000")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if endpoint != "http://127.0.0.1:6000" {
		t.Errorf("endpoint = %q, want the override port", endpoint)
	}
	if token != "tok" {
		t.Errorf("token = %q, want the record's token", token)
	}
}

func TestDiscoverProjectRuntimeReportsNoRuntime(t *testing.T) {
	cfg := config.Config{ProjectDir: t.TempDir(), Runtime: config.RuntimeConfig{Host: "127.0.0.1"}}

	_, _, err := DiscoverProjectRuntime(cfg, "")
	if !errors.Is(err, ErrNoProjectRuntime) {
		t.Fatalf("error = %v, want ErrNoProjectRuntime", err)
	}
	if !strings.Contains(err.Error(), "start one with `neuron`") {
		t.Errorf("error = %v, want the run-style start hint", err)
	}
}

func TestDiscoverProjectRuntimeUsesDiscoveredRecord(t *testing.T) {
	root := t.TempDir()
	writeRecord(t, root, projectruntime.New("127.0.0.1", "41009", root, os.Getpid(), "tok"))

	cfg := config.Config{ProjectDir: root, Runtime: config.RuntimeConfig{Host: "127.0.0.1"}}
	endpoint, token, err := DiscoverProjectRuntime(cfg, "")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if endpoint != "http://127.0.0.1:41009" {
		t.Errorf("endpoint = %q, want http://127.0.0.1:41009", endpoint)
	}
	if token != "tok" {
		t.Errorf("token = %q, want tok", token)
	}
}

// runtimeServer answers the CLI's instance queries and records whether it was
// reached and with which credential.
func runtimeServer(t *testing.T, hits *int, auth *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == protocol.HealthPath {
			_ = jsonResponse(w, protocol.Response{Status: http.StatusOK, Message: "ok"})
			return
		}
		*hits = *hits + 1
		*auth = r.Header.Get(apitoken.AuthorizationHeader)
		var data any
		if r.Method == http.MethodGet {
			data = []protocol.InstanceResponse{}
		} else {
			data = map[string]any{}
		}
		_ = jsonResponse(w, protocol.Response{Status: http.StatusOK, Message: "ok", Data: data})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func jsonResponse(w http.ResponseWriter, value any) error {
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(value)
}

// portOf extracts the TCP port of an httptest server URL for a discovery record.
func portOf(t *testing.T, endpoint string) string {
	t.Helper()
	u, err := url.Parse(endpoint)
	if err != nil {
		t.Fatalf("parse %q: %v", endpoint, err)
	}
	return u.Port()
}

func TestSetupInstanceClientPrefersDiscoveredRuntime(t *testing.T) {
	var hits int
	var gotAuth string
	srv := runtimeServer(t, &hits, &gotAuth)

	root := t.TempDir()
	writeRecord(t, root, projectruntime.New("127.0.0.1", portOf(t, srv.URL), root, os.Getpid(), "runtime-token"))

	// A configured socket and nore path must not be consulted when a live
	// project runtime is discovered: the build daemon does not own executions.
	cfg := config.Config{
		ProjectDir: root,
		Runtime:    config.RuntimeConfig{Host: "127.0.0.1"},
		Daemon: config.DaemonConfig{
			Socket:   filepath.Join(root, "unused.sock"),
			NorePath: filepath.Join(root, "missing-nore"),
		},
	}
	c, cleanup, err := SetupInstanceClient(context.Background(), Options{Config: cfg})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	defer cleanup()

	if _, err := c.ListInstances(context.Background(), "?status=running"); err != nil {
		t.Fatalf("list instances: %v", err)
	}
	if hits != 1 {
		t.Fatalf("project runtime hits = %d, want 1", hits)
	}
	if gotAuth != apitoken.Header("runtime-token") {
		t.Fatalf("authorization = %q, want the discovery record token", gotAuth)
	}
}

func TestSetupInstanceClientHonorsExplicitEndpoint(t *testing.T) {
	var explicitHits, discoveredHits int
	var ignoredAuth string
	explicit := runtimeServer(t, &explicitHits, &ignoredAuth)
	decoy := runtimeServer(t, &discoveredHits, &ignoredAuth)

	root := t.TempDir()
	writeRecord(t, root, projectruntime.New("127.0.0.1", portOf(t, decoy.URL), root, os.Getpid(), "decoy-token"))

	cfg := config.Config{
		ProjectDir: root,
		Daemon:     config.DaemonConfig{Endpoint: explicit.URL},
	}
	c, cleanup, err := SetupInstanceClient(context.Background(), Options{Config: cfg})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	defer cleanup()

	if _, err := c.ListInstances(context.Background(), "?status=running"); err != nil {
		t.Fatalf("list instances: %v", err)
	}
	if explicitHits != 1 {
		t.Fatalf("explicit endpoint hits = %d, want 1", explicitHits)
	}
	if discoveredHits != 0 {
		t.Fatalf("discovered runtime hits = %d, want 0 with an explicit endpoint", discoveredHits)
	}
}

func TestSetupInstanceClientStaleRecordDoesNotLeakToken(t *testing.T) {
	root := t.TempDir()
	writeRecord(t, root, projectruntime.New("127.0.0.1", "41099", root, deadPID(t), "secret-record-token"))

	cfg := config.Config{ProjectDir: root}
	_, _, err := SetupInstanceClient(context.Background(), Options{Config: cfg})
	if err == nil {
		t.Fatal("expected an error for a stale runtime record")
	}
	if !strings.Contains(err.Error(), "not alive") {
		t.Errorf("error = %v, want the stale-process reason", err)
	}
	if strings.Contains(err.Error(), "secret-record-token") {
		t.Fatalf("error leaked the discovery token: %v", err)
	}
}
