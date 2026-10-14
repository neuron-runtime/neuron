package run

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neuron-runtime/neuron/application/config"
	"github.com/neuron-runtime/neuron/shared/types/projectruntime"
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
// live would send `neuron run` to a dead port.
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

func TestDiscoverLocalRuntimeOverridePortStillFindsToken(t *testing.T) {
	root := t.TempDir()
	writeRecord(t, root, projectruntime.New("127.0.0.1", "41003", root, os.Getpid(), "tok"))

	t.Chdir(root)
	cfg := config.Config{Runtime: config.RuntimeConfig{Host: "127.0.0.1"}}

	endpoint, token, err := discoverLocalRuntime(cfg, "6000")
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

func TestDiscoverLocalRuntimeReportsNoRuntime(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := config.Config{Runtime: config.RuntimeConfig{Host: "127.0.0.1"}}

	endpoint, token, err := discoverLocalRuntime(cfg, "")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if endpoint != "" || token != "" {
		t.Errorf("discover returned endpoint=%q token=%q, want empty", endpoint, token)
	}
}

func TestDiscoverLocalRuntimeUsesDiscoveredRecord(t *testing.T) {
	root := t.TempDir()
	writeRecord(t, root, projectruntime.New("127.0.0.1", "41009", root, os.Getpid(), "tok"))

	t.Chdir(root)
	cfg := config.Config{Runtime: config.RuntimeConfig{Host: "127.0.0.1"}}

	endpoint, token, err := discoverLocalRuntime(cfg, "")
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
