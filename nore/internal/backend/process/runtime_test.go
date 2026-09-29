package process

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
)

const (
	echoSourceDir  = "../../../../examples/capability-runtimes/echo"
	grpcEchoSource = "testdata/grpcecho"
)

// fixtures are built once per test binary (see TestMain).
var fixtures struct {
	echoNative string
	grpcEcho   string
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
	dir, err := os.MkdirTemp("", "neuron-process-fixtures-")
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
		{grpcEchoSource, filepath.Join(dir, "grpcecho"), nil},
	}
	for _, s := range steps {
		if err := buildGo(s.src, s.out, s.env); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	fixtures.echoNative = filepath.Join(dir, "echo")
	fixtures.grpcEcho = filepath.Join(dir, "grpcecho")

	code := m.Run()
	os.Exit(code)
}

func newSpec(typ, entrypoint string, protocol string, maxWorkers int) capabilityrt.BackendSpec {
	return capabilityrt.BackendSpec{
		Type:       typ,
		Version:    "1.0.0",
		Protocol:   protocol,
		Entrypoint: entrypoint,
		RootDir:    filepath.Dir(entrypoint),
		MaxWorkers: maxWorkers,
	}
}

func TestStartRejectsMissingEntrypoint(t *testing.T) {
	rt := New(nil)
	spec := newSpec("example:echo", filepath.Join(t.TempDir(), "missing"), capabilityrt.ProtocolV1, 0)
	if _, err := rt.Start(context.Background(), spec); err == nil {
		t.Fatal("expected error for missing entrypoint")
	}
}

func TestStartRejectsUnknownProtocol(t *testing.T) {
	rt := New(nil)
	spec := newSpec("example:echo", fixtures.echoNative, "neuron/capability runtime-v9", 0)
	if _, err := rt.Start(context.Background(), spec); err == nil {
		t.Fatal("expected error for unknown protocol")
	}
}

func TestLegacyJSONRoundTrip(t *testing.T) {
	rt := New(nil)
	defer rt.Close(context.Background())

	inst, err := rt.Start(context.Background(), newSpec("example:echo", fixtures.echoNative, capabilityrt.ProtocolJSONV1, 0))
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
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if resp.Result["value"] != "hello" {
		t.Errorf("value = %v, want hello", resp.Result["value"])
	}
}

func TestGRPCWorkerPoolRoundTrip(t *testing.T) {
	rt := New(nil)
	defer rt.Close(context.Background())

	inst, err := rt.Start(context.Background(), newSpec("example:grpc-echo", fixtures.grpcEcho, capabilityrt.ProtocolV1, 0))
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
	if resp.Result["protocol"] != "grpc-test" {
		t.Errorf("protocol = %v, want grpc-test", resp.Result["protocol"])
	}
	if err := inst.Health(context.Background()); err != nil {
		t.Errorf("Health after execute: %v", err)
	}
}

func TestGRPCWorkerPoolReusesWorker(t *testing.T) {
	rt := New(nil)
	defer rt.Close(context.Background())

	inst, err := rt.Start(context.Background(), newSpec("example:grpc-echo", fixtures.grpcEcho, capabilityrt.ProtocolV1, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Close(context.Background())

	pool, ok := inst.(*workerPool)
	if !ok {
		t.Fatalf("expected *workerPool, got %T", inst)
	}

	for i := 0; i < 3; i++ {
		if _, err := inst.Execute(context.Background(), &capabilityrt.Request{Params: map[string]any{"i": float64(i)}}); err != nil {
			t.Fatalf("Execute #%d: %v", i, err)
		}
	}

	if len(pool.workers) != 1 {
		t.Errorf("pool has %d workers, want 1 (reuse across executions)", len(pool.workers))
	}
}

func TestGRPCWorkerPoolConcurrent(t *testing.T) {
	rt := New(nil)
	defer rt.Close(context.Background())

	inst, err := rt.Start(context.Background(), newSpec("example:grpc-echo", fixtures.grpcEcho, capabilityrt.ProtocolV1, 4))
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Close(context.Background())

	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 5; i++ {
				resp, err := inst.Execute(context.Background(), &capabilityrt.Request{Params: map[string]any{"value": "hi"}})
				if err != nil {
					errs <- fmt.Errorf("goroutine %d Execute #%d: %w", g, i, err)
					return
				}
				if resp.Result["value"] != "hi" {
					errs <- fmt.Errorf("goroutine %d value = %v, want hi", g, resp.Result["value"])
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestGRPCWorkerPoolCloseShutsDownWorkers(t *testing.T) {
	rt := New(nil)

	inst, err := rt.Start(context.Background(), newSpec("example:grpc-echo", fixtures.grpcEcho, capabilityrt.ProtocolV1, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = rt.Close(context.Background())
	}()

	if _, err := inst.Execute(context.Background(), &capabilityrt.Request{Params: map[string]any{"value": "hi"}}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	pool := inst.(*workerPool)
	if len(pool.workers) != 1 {
		t.Fatalf("pool has %d workers, want 1", len(pool.workers))
	}

	if err := inst.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if len(pool.workers) != 0 {
		t.Errorf("pool retains %d workers after Close, want 0", len(pool.workers))
	}
}

// TestLegacyJSONCloseIsNoOp verifies closing a legacy instance does not break
// the runtime (the process is owned by the execution, not the instance).
func TestLegacyJSONCloseIsNoOp(t *testing.T) {
	rt := New(nil)
	defer rt.Close(context.Background())

	inst, err := rt.Start(context.Background(), newSpec("example:echo", fixtures.echoNative, capabilityrt.ProtocolJSONV1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := inst.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	resp, err := inst.Execute(context.Background(), &capabilityrt.Request{Params: map[string]any{"value": "after"}})
	if err != nil {
		t.Fatalf("Execute after Close: %v", err)
	}
	if resp.Result["value"] != "after" {
		t.Errorf("value = %v, want after", resp.Result["value"])
	}
}
