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
	capabilityrt "github.com/Muhammad-Jay/neuron/shared/types/capabilityruntime"
)

const (
	echoSourceDir = "../../../examples/capability-runtimes/echo"
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

// resolvedCapabilityRuntime builds a frozen ResolvedCapabilityRuntime whose runtime kind and
// declared protocol reflect the transport the fixture actually speaks. The
// echo/echo-wasm fixtures speak the legacy stdin/stdout JSON protocol, so they
// declare neuron/capability-runtime-v1-json (see ProtocolJSONV1).
func resolvedCapabilityRuntime(t *testing.T, typ, entrypoint, rootDir, runtimeKind string) capabilityrt.ResolvedCapabilityRuntime {
	t.Helper()
	return capabilityrt.ResolvedCapabilityRuntime{
		Type:             typ,
		RequestedVersion: "1.0.0",
		ResolvedVersion:  "1.0.0",
		Registry:         "local",
		Runtime: capabilityrt.RuntimeInfo{
			Type:       runtimeKind,
			Protocol:   capabilityrt.ProtocolJSONV1,
			Entrypoint: entrypoint,
		},
		RootDir: rootDir,
	}
}

func echoResolved(t *testing.T, typ, kind string) capabilityrt.ResolvedCapabilityRuntime {
	t.Helper()
	entry, root := fixtures.wasm, filepath.Dir(fixtures.wasm)
	if kind == capabilityrt.RuntimeKindProcess {
		entry, root = fixtures.native, filepath.Dir(fixtures.native)
	}
	return resolvedCapabilityRuntime(t, typ, filepath.Base(entry), root, kind)
}

func executionContext(input map[string]any) contracts.ExecutionContext {
	return contracts.ExecutionContext{
		ExecutionID:   "exec-1",
		CorrelationID: "corr-1",
		Capability: core.Capability{
			Metadata: core.Metadata{Name: "echo", Version: "1.0.0"},
			Type:     "example:echo",
		},
		Params: input,
	}
}

func assertEchoOutput(t *testing.T, got map[string]any, expectedType string) {
	t.Helper()

	if got["type"] != expectedType {
		t.Errorf("output.type = %v, want %s", got["type"], expectedType)
	}
	if got["protocol"] != capabilityrt.ProtocolJSONV1 {
		t.Errorf("output.protocol = %v, want %s", got["protocol"], capabilityrt.ProtocolJSONV1)
	}
	if got["version"] != "1.0.0" {
		t.Errorf("output.version = %v, want 1.0.0", got["version"])
	}
	if got["value"] != "hello" {
		t.Errorf("output.value = %v, want hello", got["value"])
	}
}

func TestRegisterResolvedCapabilityRuntimesDispatch(t *testing.T) {
	cases := []struct {
		name string
		kind string
		res  capabilityrt.ResolvedCapabilityRuntime
	}{
		{"process", capabilityrt.RuntimeKindProcess, echoResolved(t, "example:echo", capabilityrt.RuntimeKindProcess)},
		{"process_implicit_default", "", echoResolved(t, "example:echo", "")},
		{"wasm", capabilityrt.RuntimeKindWasm, echoResolved(t, "example:echo-wasm", capabilityrt.RuntimeKindWasm)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Dispatch through the public registry path.
			reg := registry.New()
			if err := RegisterResolvedCapabilityRuntimes(reg, []capabilityrt.ResolvedCapabilityRuntime{tc.res}); err != nil {
				t.Fatalf("RegisterResolvedCapabilityRuntimes: %v", err)
			}
			ex, err := reg.Resolve(core.CapabilityRuntimeType(tc.res.Type))
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if ex == nil {
				t.Fatal("resolved capability runtime is nil")
			}
			defer closeIfCloser(t, ex)
		})
	}
}

func TestNewAdapterRejectsUnknownRuntime(t *testing.T) {
	res := capabilityrt.ResolvedCapabilityRuntime{
		Type:    "example:echo",
		Runtime: capabilityrt.RuntimeInfo{Type: "container", Entrypoint: "echo"},
	}
	_, err := NewAdapter(res)
	if err == nil {
		t.Fatal("expected error for unsupported runtime kind")
	}
	if !strings.Contains(err.Error(), "no runtime backend registered") {
		t.Errorf("error = %q, want unsupported runtime kind", err)
	}
}

func TestProcessCapabilityRuntimeRoundTripWithEnv(t *testing.T) {
	adapter, err := NewAdapter(echoResolved(t, "example:echo", capabilityrt.RuntimeKindProcess))
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

func TestWasmCapabilityRuntimeRoundTripWithEnv(t *testing.T) {
	adapter, err := NewAdapter(echoResolved(t, "example:echo-wasm", capabilityrt.RuntimeKindWasm))
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
		res  capabilityrt.ResolvedCapabilityRuntime
	}{
		{"process", echoResolved(t, "example:echo", capabilityrt.RuntimeKindProcess)},
		{"wasm", echoResolved(t, "example:echo-wasm", capabilityrt.RuntimeKindWasm)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapter, err := NewAdapter(tc.res)
			if err != nil {
				t.Fatal(err)
			}
			defer closeIfCloser(t, adapter)

			_, err = adapter.Execute(context.Background(), executionContext(map[string]any{"error": "boom"}))
			if err == nil {
				t.Fatal("expected controlled error from capability runtime")
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
		{"process", capabilityrt.RuntimeKindProcess},
		{"wasm", capabilityrt.RuntimeKindWasm},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := resolvedCapabilityRuntime(t, "example:echo", "does-not-exist", t.TempDir(), tc.kind)
			if _, err := NewAdapter(res); err == nil {
				t.Fatal("expected error for missing entrypoint")
			}
		})
	}
}

func TestDecodeResolvedCapabilityRuntimesNil(t *testing.T) {
	got, err := DecodeResolvedCapabilityRuntimes(nil)
	if err != nil {
		t.Fatalf("DecodeResolvedCapabilityRuntimes: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d capability runtimes, want 0", len(got))
	}
}

func TestDecodeResolvedCapabilityRuntimesRoundTrip(t *testing.T) {
	res := echoResolved(t, "example:echo", capabilityrt.RuntimeKindProcess)
	payload := map[string]any{"resolved_capability_runtimes": []any{
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

	got, err := DecodeResolvedCapabilityRuntimes(payload)
	if err != nil {
		t.Fatalf("DecodeResolvedCapabilityRuntimes: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d capability runtimes, want 1", len(got))
	}
	if got[0].Type != res.Type || got[0].Runtime.Protocol != res.Runtime.Protocol || got[0].RootDir != res.RootDir {
		t.Errorf("round-trip mismatch: %+v", got[0])
	}
}

func closeIfCloser(t *testing.T, ex any) {
	t.Helper()
	closer, ok := ex.(contracts.CapabilityRuntimeCloser)
	if !ok {
		return
	}
	if err := closer.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}
