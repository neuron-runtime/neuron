package wasm

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
)

const (
	echoSourceDir = "../../../../examples/capability-runtimes/echo"
	spinSourceDir = "../../plugin/testdata/spin"
)

var fixtures struct {
	echo string
	spin string
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
	dir, err := os.MkdirTemp("", "neuron-wasm-fixtures-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	steps := []struct {
		src string
		out string
		env []string
	}{
		{echoSourceDir, filepath.Join(dir, "echo.wasm"), []string{"GOOS=wasip1", "GOARCH=wasm"}},
		{spinSourceDir, filepath.Join(dir, "spin.wasm"), []string{"GOOS=wasip1", "GOARCH=wasm"}},
	}
	for _, s := range steps {
		if err := buildGo(s.src, s.out, s.env); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	fixtures.echo = filepath.Join(dir, "echo.wasm")
	fixtures.spin = filepath.Join(dir, "spin.wasm")

	code := m.Run()
	if rt, rterr := sharedProcessRuntime(); rterr == nil {
		_ = rt.close(context.Background())
	}
	os.Exit(code)
}

func newSpec(typ, entrypoint string) capabilityrt.BackendSpec {
	return capabilityrt.BackendSpec{
		Type:       typ,
		Version:    "1.0.0",
		Protocol:   capabilityrt.ProtocolJSONV1,
		Entrypoint: entrypoint,
		RootDir:    filepath.Dir(entrypoint),
	}
}

func TestSharedRuntimeIsProcessGlobal(t *testing.T) {
	rt, err := New()
	if err != nil {
		t.Fatal(err)
	}
	again, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if rt.shared != again.shared {
		t.Fatal("wazero runtime is recreated per Runtime; want one per process")
	}

	cm1, err := rt.shared.compiledModule(context.Background(), fixtures.echo)
	if err != nil {
		t.Fatal(err)
	}
	cm2, err := again.shared.compiledModule(context.Background(), fixtures.echo)
	if err != nil {
		t.Fatal(err)
	}
	if cm1 != cm2 {
		t.Fatal("compiled module is not cached; want one per frozen module")
	}
}

func TestStartRejectsMissingModule(t *testing.T) {
	rt, err := New()
	if err != nil {
		t.Fatal(err)
	}

	spec := newSpec("example:echo", filepath.Join(t.TempDir(), "missing.wasm"))
	if _, err := rt.Start(context.Background(), spec); err == nil {
		t.Fatal("expected error for missing wasm module")
	}
}

func TestRoundTrip(t *testing.T) {
	rt, err := New()
	if err != nil {
		t.Fatal(err)
	}

	inst, err := rt.Start(context.Background(), newSpec("example:echo", fixtures.echo))
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Close(context.Background())

	resp, err := inst.Execute(context.Background(), &capabilityrt.Request{
		Params: map[string]any{"value": "hello"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if resp.Result["value"] != "hello" {
		t.Errorf("value = %v, want hello", resp.Result["value"])
	}
	if resp.Result["type"] != "example:echo" {
		t.Errorf("type = %v, want example:echo", resp.Result["type"])
	}
}

func TestTimesOut(t *testing.T) {
	rt, err := New()
	if err != nil {
		t.Fatal(err)
	}

	spec := newSpec("example:spin", fixtures.spin)
	inst, err := rt.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Close(context.Background())

	inst.(*instance).SetTimeout(300 * time.Millisecond)

	start := time.Now()
	_, err = inst.Execute(context.Background(), &capabilityrt.Request{Params: map[string]any{"value": "x"}})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("timed out")) {
		t.Errorf("error = %q, want timed out", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("timeout took too long: %v", elapsed)
	}
}

func TestReusesRuntimeAcrossExecutions(t *testing.T) {
	rt, err := New()
	if err != nil {
		t.Fatal(err)
	}

	inst, err := rt.Start(context.Background(), newSpec("example:echo", fixtures.echo))
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Close(context.Background())

	for i := 0; i < 3; i++ {
		resp, err := inst.Execute(context.Background(), &capabilityrt.Request{Params: map[string]any{"value": "hi"}})
		if err != nil {
			t.Fatalf("Execute #%d: %v", i, err)
		}
		if resp.Result["value"] != "hi" {
			t.Errorf("Execute #%d: value = %v, want hi", i, resp.Result["value"])
		}
	}
}

// TestConcurrentExecutions proves executions do not serialize: many adapters
// (instances) hot-sharing one runtime and one compiled module must serve
// concurrent executions from their own sandboxed modules.
func TestConcurrentExecutions(t *testing.T) {
	rt, err := New()
	if err != nil {
		t.Fatal(err)
	}

	instances := make([]capabilityrt.BackendInstance, 4)
	for i := range instances {
		inst, err := rt.Start(context.Background(), newSpec("example:echo", fixtures.echo))
		if err != nil {
			t.Fatal(err)
		}
		instances[i] = inst
		defer inst.Close(context.Background())
	}

	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(inst capabilityrt.BackendInstance, g int) {
			defer wg.Done()
			for i := 0; i < 5; i++ {
				resp, err := inst.Execute(context.Background(), &capabilityrt.Request{Params: map[string]any{"value": "hi"}})
				if err != nil {
					t.Errorf("goroutine %d Execute #%d: %v", g, i, err)
					return
				}
				if resp.Result["value"] != "hi" {
					t.Errorf("goroutine %d Execute #%d: value = %v, want hi", g, i, resp.Result["value"])
				}
			}
		}(instances[g], g)
	}
	wg.Wait()
}

// TestRuntimeSurvivesInstanceClose verifies Close is a no-op: the shared
// runtime keeps serving other instances after one is closed.
func TestRuntimeSurvivesInstanceClose(t *testing.T) {
	rt, err := New()
	if err != nil {
		t.Fatal(err)
	}

	inst, err := rt.Start(context.Background(), newSpec("example:echo", fixtures.echo))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inst.Execute(context.Background(), &capabilityrt.Request{Params: map[string]any{"value": "hi"}}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if err := inst.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	again, err := rt.Start(context.Background(), newSpec("example:echo", fixtures.echo))
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close(context.Background())
	resp, err := again.Execute(context.Background(), &capabilityrt.Request{Params: map[string]any{"value": "hi"}})
	if err != nil {
		t.Fatalf("Execute after Close: %v", err)
	}
	if resp.Result["value"] != "hi" {
		t.Errorf("value = %v, want hi", resp.Result["value"])
	}
}
