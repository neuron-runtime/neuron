package acceptance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/shared/types/projectruntime"
)

const (
	neuronSourceDir = "../../cmd/neuron"
	noreSourceDir   = "../../../nore/cmd/nore"
)

var harness struct {
	neuronBin string
	noreBin   string
	home      string
}

var projectSeq atomic.Int64

// TestMain builds the product binaries once and points HOME at a disposable
// directory. Every process the tests spawn — including the detached runtime the
// build command may start — therefore reads and writes only throwaway state, and
// is shut down before the process exits.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "neuron-acceptance-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	harness.neuronBin = filepath.Join(dir, "neuron")
	harness.noreBin = filepath.Join(dir, "nore")
	harness.home = filepath.Join(dir, "home")

	if err := os.MkdirAll(harness.home, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, build := range []struct{ src, out string }{
		{neuronSourceDir, harness.neuronBin},
		{noreSourceDir, harness.noreBin},
	} {
		if err := goBuild(build.src, build.out); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	// The fake HOME isolates every default path: the daemon socket, data
	// directory, PID file, capability runtime store, and provider store.
	env := map[string]string{
		"HOME":                  harness.home,
		"NEURON_SOCKET":         filepath.Join(harness.home, "nore.sock"),
		"NEURON_DATA_DIR":       filepath.Join(harness.home, "data"),
		"NEURON_DAEMON_PIDFILE": filepath.Join(harness.home, "nore.pid"),
	}
	for key, value := range env {
		if err := os.Setenv(key, value); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	code := m.Run()
	stopDaemon()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func goBuild(src, out string) error {
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Dir = src
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go build %s: %v\n%s", out, err, stderr.String())
	}
	return nil
}

// stopDaemon interrupts the detached runtime the build command starts, if it is
// still running. The PID file lives under the fake HOME, so only that daemon is
// ever touched.
func stopDaemon() {
	pidPath := filepath.Join(harness.home, "nore.pid")
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return
	}
	var pid int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &pid); err != nil || pid <= 0 {
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	_ = proc.Signal(syscall.SIGTERM)
	for i := 0; i < 50; i++ {
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = proc.Kill()
}

// project is one generated project fixture: a project config, a working
// Assembly, and a failing Assembly (used to prove failed executions exit
// non-zero). Assembly names are unique per test so registrations in the shared
// fake runtime store never collide.
type project struct {
	root       string
	assembly   string
	alt        string
	failing    string
	capability string
}

// writeProject materializes the fixture under a temporary directory.
//
// Both Assemblies share two in-process core capabilities, so builds and runs are
// fully offline: no registry, network, Node.js, or external artifact is
// involved.
func writeProject(t *testing.T) project {
	t.Helper()

	suffix := strings.NewReplacer("/", "-", "_", "-", " ", "-").Replace(t.Name())
	name := fmt.Sprintf("acc-%s-%d", strings.ToLower(suffix), projectSeq.Add(1))

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "neuron.config.yaml"), fmt.Sprintf(`name: %s
version: 0.1.0
lang: yaml
assemblies:
  - name: %s
    entry: assemblies/%s/assembly.yaml
    default: true
  - name: %s-alt
    entry: assemblies/%s-alt/assembly.yaml
  - name: %s-fail
    entry: assemblies/%s-fail/assembly.yaml
`, name, name, name, name, name, name, name))

	writeFile(t, filepath.Join(root, "capabilities", "greet.yaml"), greetCapability)
	writeFile(t, filepath.Join(root, "capabilities", "punctuate.yaml"), punctuateCapability)
	writeFile(t, filepath.Join(root, "assemblies", name, "assembly.yaml"), assemblyYAML(validBinding))
	writeFile(t, filepath.Join(root, "assemblies", name+"-alt", "assembly.yaml"), altAssemblyYAML)
	writeFile(t, filepath.Join(root, "assemblies", name+"-fail", "assembly.yaml"), assemblyYAML(failingBinding))

	return project{
		root:       root,
		assembly:   name,
		alt:        name + "-alt",
		failing:    name + "-fail",
		capability: "greet",
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const greetCapability = `apiVersion: neuron/v1
kind: Capability

metadata:
  name: greet
  version: 0.1.0

spec:
  capability runtime:
    type: neuron:core:set

  config:
    greeting: hello

  mappings:
    - direction: output
      source: greeting
      target: greeting
`

const punctuateCapability = `apiVersion: neuron/v1
kind: Capability

metadata:
  name: punctuate
  version: 0.1.0

spec:
  capability runtime:
    type: neuron:core:set

  config:
    punctuation: "!"

  mappings:
    - direction: input
      source: text
      target: text
    - direction: output
      source: text
      target: text
    - direction: output
      source: punctuation
      target: punctuation
`

const validBinding = `source.result.greeting != ''`
const failingBinding = `source.result.greeting == 'impossible'`

func assemblyYAML(validation string) string {
	return fmt.Sprintf(`apiVersion: neuron/v1
kind: Assembly

capabilities:
  - ref: greet
    entry: ../../capabilities/greet.yaml
  - ref: punctuate
    entry: ../../capabilities/punctuate.yaml

bindings:
  - from: greet
    to: punctuate
    mappings:
      - target: text
        expression: "source.result.greeting"
    validations:
      - expression: "%s"
        message: "greeting validation failed"

result:
  schema:
    - name: greeting
      type: string
    - name: echoed
      type: string
    - name: mark
      type: string
    - name: caller
      type: string
  fields:
    - name: greeting
      source:
        kind: capabilityResult
        capability: greet
        path: [greeting]
    - name: echoed
      source:
        kind: capabilityResult
        capability: punctuate
        path: [text]
    - name: mark
      source:
        kind: capabilityResult
        capability: punctuate
        path: [punctuation]
    - name: caller
      source:
        kind: assemblyParams
        path: [name]
`, validation)
}

// altAssemblyYAML is a second valid Assembly in the same project. It reuses the
// same capabilities but declares a different result contract, so a test can
// prove one project runtime serves each Assembly with its own result shape.
const altAssemblyYAML = `apiVersion: neuron/v1
kind: Assembly

capabilities:
  - ref: greet
    entry: ../../capabilities/greet.yaml

result:
  schema:
    - name: greeting
      type: string
    - name: caller
      type: string
  fields:
    - name: greeting
      source:
        kind: capabilityResult
        capability: greet
        path: [greeting]
    - name: caller
      source:
        kind: assemblyParams
        path: [name]
`

// execResult is one completed `neuron` invocation.
type execResult struct {
	stdout string
	stderr string
	code   int
}

func runNeuron(t *testing.T, dir string, env []string, args ...string) execResult {
	t.Helper()

	cmd := exec.Command(harness.neuronBin, append([]string{"--nore-path", harness.noreBin}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run()

	return execResult{stdout: stdout.String(), stderr: stderr.String(), code: cmd.ProcessState.ExitCode()}
}

// buildProject runs the real build command and fails the test when it does not
// succeed.
func buildProject(t *testing.T, root string) {
	t.Helper()
	res := runNeuron(t, root, nil, "build")
	if res.code != 0 {
		t.Fatalf("neuron build exit = %d\nstdout: %s\nstderr: %s", res.code, res.stdout, res.stderr)
	}
}

// runtimeSession is one foreground `neuron` process serving a project.
type runtimeSession struct {
	record  projectruntime.Record
	logPath string

	cmd  *exec.Cmd
	done chan error
}

// startRuntime launches bare `neuron` in the project and waits until its
// discovery record exists and its published endpoint answers a health probe.
func startRuntime(t *testing.T, dir string, args ...string) *runtimeSession {
	t.Helper()

	logPath := filepath.Join(t.TempDir(), "runtime.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(harness.neuronBin, append([]string{"--nore-path", harness.noreBin}, args...)...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	// Own process group so the cleanup can interrupt the CLI and its child
	// runtime together without touching the test process.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		logFile.Close()
		t.Fatal(err)
	}

	session := &runtimeSession{logPath: logPath, cmd: cmd, done: make(chan error, 1)}
	go func() { session.done <- cmd.Wait() }()
	t.Cleanup(func() {
		session.stop()
		logFile.Close()
	})

	// Another session for the same project may already be running. Wait for
	// this session's own record, not whichever record happens to be newest.
	existing, err := projectruntime.ReadAll(dir)
	if err != nil {
		t.Fatal(err)
	}
	previous := make(map[string]bool, len(existing))
	for _, record := range existing {
		previous[record.SessionID] = true
	}

	deadline := time.Now().Add(30 * time.Second)
	for {
		select {
		case err := <-session.done:
			t.Fatalf("project runtime exited before becoming ready: %v\n%s", err, session.log())
		default:
		}

		records, err := projectruntime.ReadAll(dir)
		if err != nil {
			t.Fatalf("read discovery record: %v", err)
		}
		for _, record := range records {
			if previous[record.SessionID] {
				continue
			}
			if health(record) == nil {
				session.record = record
				return session
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("project runtime did not become ready\n%s", session.log())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (s *runtimeSession) stop() {
	if s.cmd.Process == nil {
		return
	}
	// Interrupt the whole group: the CLI and the runtime child it started.
	_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGINT)
	select {
	case <-s.done:
	case <-time.After(10 * time.Second):
		_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
		<-s.done
	}
}

func (s *runtimeSession) log() string {
	data, err := os.ReadFile(s.logPath)
	if err != nil {
		return ""
	}
	return string(data)
}

// health probes the unauthenticated liveness endpoint of a runtime record.
func health(record projectruntime.Record) error {
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get(record.Endpoint() + "/health")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health status %d", resp.StatusCode)
	}
	return nil
}

// decodeSingleJSON decodes exactly one JSON value from stdout and rejects any
// trailing output, proving stdout carried only the Assembly result.
func decodeSingleJSON(t *testing.T, stdout string) map[string]any {
	t.Helper()

	dec := json.NewDecoder(strings.NewReader(stdout))
	var result map[string]any
	if err := dec.Decode(&result); err != nil {
		t.Fatalf("stdout is not JSON: %v\nstdout: %s", err, stdout)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		t.Fatalf("stdout contains more than one value: %v\nstdout: %s", err, stdout)
	}
	return result
}

// freePort asks the OS for an available loopback TCP port.
func freePort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return fmt.Sprintf("%d", listener.Addr().(*net.TCPAddr).Port)
}
