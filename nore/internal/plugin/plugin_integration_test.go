package plugin

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Muhammad-Jay/neuron/nore/internal/contracts"
	"github.com/Muhammad-Jay/neuron/nore/internal/registry"
	core "github.com/Muhammad-Jay/neuron/shared/types/core"
	shadexec "github.com/Muhammad-Jay/neuron/shared/types/executor"
)

const (
	echoSourceDir = "../../../examples/executors/echo"
	spinSourceDir = "testdata/spin"
)

// fixtures are compiled exactly once per test binary (see TestMain) so the
// suite spends most of its time executing, not rebuilding modules.
var fixtures struct {
	native, wasm, spin string
}

func buildGo(srcDir, out string, env []string) error {
	cmd := exec.Command("go", "build", "-C", srcDir, "-o", out, ".")
	cmd.Env = append(os.Environ(), append([]string{"GOWORK=off"}, env...)...)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go build (%s): %v\n%s", out, err, stderr.String())
	}
	return nil
}

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "neuron-plugin-fixtures-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	steps := []struct {
		src string
		out string
		env []string
	}{
		{echoSourceDir, filepath.Join(dir, "echo"), nil},
		{echoSourceDir, filepath.Join(dir, "echo.wasm"), []string{"GOOS=wasip1", "GOARCH=wasm"}},
		{spinSourceDir, filepath.Join(dir, "spin.wasm"), []string{"GOOS=wasip1", "GOARCH=wasm"}},
	}
	for _, s := range steps {
		if err := buildGo(s.src, s.out, s.env); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	fixtures.native = filepath.Join(dir, "echo")
	fixtures.wasm = filepath.Join(dir, "echo.wasm")
	fixtures.spin = filepath.Join(dir, "spin.wasm")

	code := m.Run()

	if reg, regerr := sharedRuntimes(); regerr == nil {
		_ = reg.CloseAll(context.Background())
	}
	os.Exit(code)
}

// resolvedExecutor builds a frozen ResolvedExecutor whose runtime kind and
// declared protocol reflect the transport the fixture actually speaks. The
// echo/echo-wasm fixtures speak the legacy stdin/stdout JSON protocol, so they
// declare neuron/executor-v1-json (see ProtocolJSONV1).
func resolvedExecutor(t *testing.T, typ, entrypoint, rootDir, runtimeKind string) shadexec.ResolvedExecutor {
	t.Helper()
	return shadexec.ResolvedExecutor{
		Type:             typ,
		RequestedVersion: "1.0.0",
		ResolvedVersion:  "1.0.0",
		Registry:         "local",
		Runtime: shadexec.RuntimeInfo{
			Type:       runtimeKind,
			Protocol:   shadexec.ProtocolJSONV1,
			Entrypoint: entrypoint,
		},
		RootDir: rootDir,
	}
}

func echoResolved(t *testing.T, typ, kind string) shadexec.ResolvedExecutor {
	t.Helper()
	entry, root := fixtures.wasm, filepath.Dir(fixtures.wasm)
	if kind == shadexec.RuntimeKindProcess {
		entry, root = fixtures.native, filepath.Dir(fixtures.native)
	}
	return resolvedExecutor(t, typ, filepath.Base(entry), root, kind)
}

func executionContext(input map[string]any) contracts.ExecutionContext {
	return contracts.ExecutionContext{
		ExecutionID:   "exec-1",
		CorrelationID: "corr-1",
		Service: core.Service{
			Metadata: core.Metadata{Name: "echo", Version: "1.0.0"},
			Type:     "example:echo",
		},
		Input: input,
	}
}

func assertEchoOutput(t *testing.T, got map[string]any, expectedType string) {
	t.Helper()

	if got["type"] != expectedType {
		t.Errorf("output.type = %v, want %s", got["type"], expectedType)
	}
	if got["protocol"] != shadexec.ProtocolJSONV1 {
		t.Errorf("output.protocol = %v, want %s", got["protocol"], shadexec.ProtocolJSONV1)
	}
	if got["version"] != "1.0.0" {
		t.Errorf("output.version = %v, want 1.0.0", got["version"])
	}
	if got["value"] != "hello" {
		t.Errorf("output.value = %v, want hello", got["value"])
	}
}

func TestRegisterResolvedExecutorsDispatch(t *testing.T) {
	cases := []struct {
		name string
		kind string
		res  shadexec.ResolvedExecutor
	}{
		{"process", shadexec.RuntimeKindProcess, echoResolved(t, "example:echo", shadexec.RuntimeKindProcess)},
		{"process_implicit_default", "", echoResolved(t, "example:echo", "")},
		{"wasm", shadexec.RuntimeKindWasm, echoResolved(t, "example:echo-wasm", shadexec.RuntimeKindWasm)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Dispatch through the public registry path.
			reg := registry.New()
			if err := RegisterResolvedExecutors(reg, []shadexec.ResolvedExecutor{tc.res}); err != nil {
				t.Fatalf("RegisterResolvedExecutors: %v", err)
			}
			ex, err := reg.Resolve(core.ExecutorType(tc.res.Type))
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if ex == nil {
				t.Fatal("resolved executor is nil")
			}
			defer closeIfCloser(t, ex)
		})
	}
}

func TestNewAdapterRejectsUnknownRuntime(t *testing.T) {
	res := shadexec.ResolvedExecutor{
		Type:    "example:echo",
		Runtime: shadexec.RuntimeInfo{Type: "container", Entrypoint: "echo"},
	}
	_, err := NewAdapter(res)
	if err == nil {
		t.Fatal("expected error for unsupported runtime kind")
	}
	if !strings.Contains(err.Error(), "no runtime backend registered") {
		t.Errorf("error = %q, want unsupported runtime kind", err)
	}
}

func TestProcessExecutorRoundTripWithEnv(t *testing.T) {
	adapter, err := NewAdapter(echoResolved(t, "example:echo", shadexec.RuntimeKindProcess))
	if err != nil {
		t.Fatal(err)
	}
	defer closeIfCloser(t, adapter)

	got, err := adapter.Execute(context.Background(), executionContext(map[string]any{"value": "hello"}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertEchoOutput(t, got, "example:echo")
}

func TestWasmExecutorRoundTripWithEnv(t *testing.T) {
	adapter, err := NewAdapter(echoResolved(t, "example:echo-wasm", shadexec.RuntimeKindWasm))
	if err != nil {
		t.Fatal(err)
	}
	defer closeIfCloser(t, adapter)

	got, err := adapter.Execute(context.Background(), executionContext(map[string]any{"value": "hello"}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertEchoOutput(t, got, "example:echo-wasm")
}

func TestAdaptersSurfaceControlledError(t *testing.T) {
	for _, tc := range []struct {
		name string
		res  shadexec.ResolvedExecutor
	}{
		{"process", echoResolved(t, "example:echo", shadexec.RuntimeKindProcess)},
		{"wasm", echoResolved(t, "example:echo-wasm", shadexec.RuntimeKindWasm)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapter, err := NewAdapter(tc.res)
			if err != nil {
				t.Fatal(err)
			}
			defer closeIfCloser(t, adapter)

			_, err = adapter.Execute(context.Background(), executionContext(map[string]any{"error": "boom"}))
			if err == nil {
				t.Fatal("expected controlled error from executor")
			}
			if !strings.Contains(err.Error(), "boom") {
				t.Errorf("error = %q, want boom", err)
			}
		})
	}
}

func TestAdapterMissingEntrypoint(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind string
	}{
		{"process", shadexec.RuntimeKindProcess},
		{"wasm", shadexec.RuntimeKindWasm},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := resolvedExecutor(t, "example:echo", "does-not-exist", t.TempDir(), tc.kind)
			if _, err := NewAdapter(res); err == nil {
				t.Fatal("expected error for missing entrypoint")
			}
		})
	}
}

func TestDecodeResolvedExecutorsNil(t *testing.T) {
	got, err := DecodeResolvedExecutors(nil)
	if err != nil {
		t.Fatalf("DecodeResolvedExecutors: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d executors, want 0", len(got))
	}
}

func TestDecodeResolvedExecutorsRoundTrip(t *testing.T) {
	res := echoResolved(t, "example:echo", shadexec.RuntimeKindProcess)
	payload := map[string]any{"resolved_executors": []any{
		map[string]any{
			"type":             res.Type,
			"requestedVersion": res.RequestedVersion,
			"resolvedVersion":  res.ResolvedVersion,
			"registry":         res.Registry,
			"runtime": map[string]any{
				"type":       res.Runtime.Type,
				"protocol":   res.Runtime.Protocol,
				"entrypoint": res.Runtime.Entrypoint,
			},
			"rootDir": res.RootDir,
		},
	}}

	got, err := DecodeResolvedExecutors(payload)
	if err != nil {
		t.Fatalf("DecodeResolvedExecutors: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d executors, want 1", len(got))
	}
	if got[0].Type != res.Type || got[0].Runtime.Protocol != res.Runtime.Protocol || got[0].RootDir != res.RootDir {
		t.Errorf("round-trip mismatch: %+v", got[0])
	}
}

func closeIfCloser(t *testing.T, ex any) {
	t.Helper()
	closer, ok := ex.(contracts.ExecutorCloser)
	if !ok {
		return
	}
	if err := closer.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}
