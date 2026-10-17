package invoke

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
)

// The end-to-end tests exec a freshly built `nore --invoke` binary against a
// mock capability runtime store laid out the way the CLI installs artifacts.
// Running the real binary — rather than calling Invoke in-process — is what
// exercises the actual one-shot contract: stdin decoding, provider hosting
// through the registry, stdout framing, stderr diagnostics, and the process exit
// code. It also keeps each case isolated, because the provider backends hold
// process-global state a single test binary must not share across cases.

const (
	echoSourceDir = "../../../examples/capability-runtimes/echo"
	noreSourceDir = "../../cmd/nore"
	testDigest    = "sha256:test"
)

var e2e struct {
	noreBin  string
	store    string
	procType string
	wasmType string
}

func buildGo(dir, out string, env []string) error {
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), append([]string{"GOWORK=off"}, env...)...)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go build (%s): %v\n%s", out, err, stderr.String())
	}
	return nil
}

func buildNore(out string) error {
	// The nore binary is built inside the nore module, so the workspace is left
	// enabled; only the standalone echo fixture is built with GOWORK=off.
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Dir = noreSourceDir

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go build nore: %v\n%s", err, stderr.String())
	}
	return nil
}

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "neuron-invoke-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)

	native := filepath.Join(dir, "echo")
	wasm := filepath.Join(dir, "echo.wasm")
	if err := buildGo(echoSourceDir, native, nil); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := buildGo(echoSourceDir, wasm, []string{"GOOS=wasip1", "GOARCH=wasm"}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	noreBin := filepath.Join(dir, "nore")
	if err := buildNore(noreBin); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	store := filepath.Join(dir, "store")

	procType := "example:echo"
	wasmType := "example:echo-wasm"

	procDir, err := installedDir(store, procType)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	wasmDir, err := installedDir(store, wasmType)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.MkdirAll(procDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.MkdirAll(wasmDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := copyFile(native, filepath.Join(procDir, "echo")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := copyFile(wasm, filepath.Join(wasmDir, "echo.wasm")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	e2e.noreBin = noreBin
	e2e.store = store
	e2e.procType = procType
	e2e.wasmType = wasmType

	os.Exit(m.Run())
}

func installedDir(store, typ string) (string, error) {
	path, err := capabilityrt.TypePath(typ)
	if err != nil {
		return "", err
	}
	return filepath.Join(store, path, "1.0.0", testDigest), nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o755)
}

func resolved(typ, entrypoint, kind string) capabilityrt.ResolvedCapabilityRuntime {
	return capabilityrt.ResolvedCapabilityRuntime{
		Type:            typ,
		ResolvedVersion: "1.0.0",
		Registry:        "local",
		Digest:          testDigest,
		Runtime: capabilityrt.RuntimeInfo{
			Type:       kind,
			Protocol:   capabilityrt.ProtocolJSONV1,
			Entrypoint: entrypoint,
		},
	}
}

// runInvoke execs `nore --invoke` with raw stdin and returns stdout, stderr, and
// the exit code.
func runInvoke(t *testing.T, stdin string) (string, string, int) {
	t.Helper()

	cmd := exec.Command(e2e.noreBin, "--invoke", "--capability-runtime-store", e2e.store)
	cmd.Stdin = bytes.NewReader([]byte(stdin))

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	_ = cmd.Run()
	return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
}

func invokeJSON(t *testing.T, req capabilityrt.InvokeRequest) string {
	t.Helper()
	buf, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return string(buf)
}

func decodeResponse(t *testing.T, stdout string) capabilityrt.InvokeResponse {
	t.Helper()
	var resp capabilityrt.InvokeResponse
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("stdout is not a valid InvokeResponse: %v\nstdout: %s", err, stdout)
	}
	return resp
}

func TestDirectInvocationProcessRuntime(t *testing.T) {
	req := capabilityrt.InvokeRequest{
		CapabilityRuntime: resolved(e2e.procType, "echo", capabilityrt.RuntimeKindProcess),
		Params:            map[string]any{"value": "hello"},
	}

	stdout, _, code := runInvoke(t, invokeJSON(t, req))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s", code, stdout)
	}

	resp := decodeResponse(t, stdout)
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if resp.Result["type"] != e2e.procType {
		t.Errorf("result.type = %v, want %s", resp.Result["type"], e2e.procType)
	}
	if resp.Result["value"] != "hello" {
		t.Errorf("result.value = %v, want hello", resp.Result["value"])
	}
}

func TestDirectInvocationWasmRuntime(t *testing.T) {
	req := capabilityrt.InvokeRequest{
		CapabilityRuntime: resolved(e2e.wasmType, "echo.wasm", capabilityrt.RuntimeKindWasm),
		Params:            map[string]any{"value": "hello"},
	}

	stdout, _, code := runInvoke(t, invokeJSON(t, req))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s", code, stdout)
	}

	resp := decodeResponse(t, stdout)
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if resp.Result["type"] != e2e.wasmType {
		t.Errorf("result.type = %v, want %s", resp.Result["type"], e2e.wasmType)
	}
}

func TestDirectInvocationFailures(t *testing.T) {
	cases := []struct {
		name  string
		stdin string
	}{
		{
			name: "unknown provider",
			stdin: invokeJSON(t, capabilityrt.InvokeRequest{
				CapabilityRuntime: resolved(e2e.procType, "echo", "container"),
				Params:            map[string]any{"value": "hello"},
			}),
		},
		{
			name: "missing installed artifact",
			stdin: func() string {
				res := resolved(e2e.procType, "echo", capabilityrt.RuntimeKindProcess)
				res.Digest = "sha256:missing"
				return invokeJSON(t, capabilityrt.InvokeRequest{CapabilityRuntime: res})
			}(),
		},
		{
			name:  "malformed request",
			stdin: "not json",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, code := runInvoke(t, tc.stdin)
			if code == 0 {
				t.Fatalf("exit code = 0, want non-zero\nstdout: %s", stdout)
			}

			resp := decodeResponse(t, stdout)
			if resp.Error == "" {
				t.Errorf("response.error is empty, want a structured error\nstdout: %s", stdout)
			}
			if stderr == "" {
				t.Errorf("stderr is empty, want a diagnostic")
			}
		})
	}
}
