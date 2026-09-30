package process

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
)

const (
	echoSourceDir  = "../../../../examples/capability-runtimes/echo"
	grpcEchoSource = "testdata/grpcecho"
	stubbornSource = "testdata/stubborn"
)

// fixtures are built once per test binary (see TestMain).
var fixtures struct {
	echoNative string
	grpcEcho   string
	stubborn   string
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
		{stubbornSource, filepath.Join(dir, "stubborn"), nil},
	}
	for _, s := range steps {
		if err := buildGo(s.src, s.out, s.env); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	fixtures.echoNative = filepath.Join(dir, "echo")
	fixtures.grpcEcho = filepath.Join(dir, "grpcecho")
	fixtures.stubborn = filepath.Join(dir, "stubborn")

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

	if got := pool.workerCount(); got != 1 {
		t.Errorf("pool has %d workers, want 1 (reuse across executions)", got)
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
	if got := pool.workerCount(); got != 1 {
		t.Fatalf("pool has %d workers, want 1", got)
	}

	if err := inst.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if got := pool.workerCount(); got != 0 {
		t.Errorf("pool retains %d workers after Close, want 0", got)
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

// TestPoolIsSharedAcrossInstancesAndSurvivesHolderRelease is a regression test
// for the shared-pool lifetime defect: every instance of an assembly is handed
// the same worker pool, and releasing one holder must not terminate workers that
// another holder is still using. Before the fix, starting a second instance
// closed and replaced the first instance's pool, so the first instance's
// in-flight executions were killed.
func TestPoolIsSharedAcrossInstancesAndSurvivesHolderRelease(t *testing.T) {
	rt := New(nil)
	defer func() { _ = rt.Close(context.Background()) }()

	spec := newSpec("example:grpc-echo", fixtures.grpcEcho, capabilityrt.ProtocolV1, 1)

	first, err := rt.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	second, err := rt.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}

	firstPool, ok := first.(*workerPool)
	if !ok {
		t.Fatalf("first instance is %T, want *workerPool", first)
	}
	secondPool, ok := second.(*workerPool)
	if !ok {
		t.Fatalf("second instance is %T, want *workerPool", second)
	}

	if firstPool != secondPool {
		t.Fatal("two Start calls for the same capability runtime returned different pools; workers are not being shared")
	}

	// Warm the pool so there is a worker to preserve.
	if _, err := first.Execute(context.Background(), &capabilityrt.Request{Params: map[string]any{"value": "hi"}}); err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	if got := firstPool.workerCount(); got != 1 {
		t.Fatalf("pool has %d workers, want 1", got)
	}

	// Releasing one holder must leave the pool usable for the other holder.
	if err := first.Close(context.Background()); err != nil {
		t.Fatalf("closing first holder: %v", err)
	}

	if got := firstPool.workerCount(); got != 1 {
		t.Fatalf("pool has %d workers after one holder released, want 1 (second holder still active)", got)
	}

	resp, err := second.Execute(context.Background(), &capabilityrt.Request{Params: map[string]any{"value": "still-here"}})
	if err != nil {
		t.Fatalf("second Execute after first holder released: %v", err)
	}
	if resp.Result["value"] != "still-here" {
		t.Errorf("value = %v, want still-here", resp.Result["value"])
	}

	// Releasing the last holder tears the pool down.
	if err := second.Close(context.Background()); err != nil {
		t.Fatalf("closing second holder: %v", err)
	}
	if got := firstPool.workerCount(); got != 0 {
		t.Errorf("pool has %d workers after last holder released, want 0", got)
	}
}

// TestPoolNeverExceedsMaxWorkers is a regression test for the capacity
// check-then-act defect: the pool used to read len(workers) < maxWorkers, drop
// the lock, and only then start a worker, so concurrent leasers could each
// observe spare capacity and collectively oversubscribe the pool.
func TestPoolNeverExceedsMaxWorkers(t *testing.T) {
	const (
		maxWorkers  = 2
		concurrency = 12
		iterations  = 4
	)

	rt := New(nil)
	defer func() { _ = rt.Close(context.Background()) }()

	inst, err := rt.Start(context.Background(), newSpec("example:grpc-echo", fixtures.grpcEcho, capabilityrt.ProtocolV1, maxWorkers))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = inst.Close(context.Background()) }()

	pool := inst.(*workerPool)

	var (
		mu      sync.Mutex
		highest int
		wg      sync.WaitGroup
	)
	errs := make(chan error, concurrency)

	for g := 0; g < concurrency; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				if _, err := inst.Execute(context.Background(), &capabilityrt.Request{Params: map[string]any{"value": "hi"}}); err != nil {
					errs <- fmt.Errorf("Execute: %w", err)
					return
				}
				mu.Lock()
				if n := pool.workerCount(); n > highest {
					highest = n
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}

	if highest > maxWorkers {
		t.Errorf("pool grew to %d workers, exceeding maxWorkers=%d", highest, maxWorkers)
	}
	if highest == 0 {
		t.Error("pool reported zero workers; the concurrency test did not exercise the pool")
	}
}

// TestWorkerEnvIsAllowlisted verifies that capability runtime child processes
// do not inherit the daemon's full environment. External capability runtimes
// are untrusted code, so handing them os.Environ() would leak whatever ambient
// secrets the daemon happens to hold.
func TestWorkerEnvIsAllowlisted(t *testing.T) {
	t.Setenv("NEURON_TEST_SECRET", "must-not-be-inherited")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "must-not-be-inherited")

	pool := newWorkerPool(
		capabilityrt.BackendSpec{Type: "example:echo", Version: "1.0.0"},
		capabilityrt.ProtocolV1,
		1,
		slog.Default(),
	)

	env := pool.workerEnv()

	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "NEURON_TEST_SECRET", "AWS_SECRET_ACCESS_KEY":
			t.Errorf("worker environment leaked %s", key)
		}
	}

	// PATH is allowlisted and must be forwarded, or a capability runtime cannot
	// resolve its own interpreter or loader.
	if !slices.ContainsFunc(env, func(e string) bool { return strings.HasPrefix(e, "PATH=") }) {
		t.Error("worker environment is missing PATH")
	}
}

// TestShutdownIsBoundedWhenWorkerRefusesToExit is a regression test for a
// shutdown hang. A worker process that ignores the graceful stop can only be
// reclaimed by the forced-kill path. Shutdown must still complete in bounded
// time, and must not leave the child process behind.
//
// It also covers the signal that the wait goroutine always closes its done
// channel: a wait error that skipped the close would block shutdown forever
// after the kill, because nothing would ever signal that the process is gone.
func TestShutdownIsBoundedWhenWorkerRefusesToExit(t *testing.T) {
	pool := newWorkerPool(
		capabilityrt.BackendSpec{Type: "example:stubborn", Version: "1.0.0", Entrypoint: fixtures.stubborn},
		capabilityrt.ProtocolV1,
		1,
		slog.Default(),
	)

	// The fixture signals readiness but never serves the capability runtime
	// protocol, so the pool handshake would never complete. Spawn the process
	// exactly as startWorker does and stop before the handshake: the behaviour
	// under test is shutdown, not the protocol.
	socketDir := t.TempDir()
	readyFile := filepath.Join(socketDir, "ready")

	cmd := exec.Command(fixtures.stubborn)
	cmd.Env = append(pool.workerEnv(),
		"NEURON_CAPABILITY_RUNTIME_READY="+readyFile,
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start stubborn process: %v", err)
	}

	// Wait for the child to signal readiness, so we are killing a live process
	// rather than racing an exit.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(readyFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatal("stubborn process never signalled readiness")
		}
		time.Sleep(10 * time.Millisecond)
	}

	_, stopProcess := context.WithCancel(context.Background())
	w := &worker{
		process:    cmd.Process,
		cancelProc: stopProcess,
		socketDir:  socketDir,
		readyFile:  readyFile,
		type_:      "example:stubborn",
		version:    "1.0.0",
		logger:     slog.Default(),
	}

	start := time.Now()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := w.shutdown(shutdownCtx); err != nil {
		_ = cmd.Process.Kill()
		t.Fatalf("shutdown: %v", err)
	}
	elapsed := time.Since(start)

	// shutdown waits defaultShutdownTimeout, then kills. Allow slack for a
	// loaded machine, but it must terminate rather than hang.
	if max := 2*defaultShutdownTimeout + 20*time.Second; elapsed > max {
		t.Fatalf("shutdown took %v, exceeding %v: shutdown is not bounded", elapsed, max)
	}
	t.Logf("shutdown reclaimed a refusing worker in %v", elapsed)

	// The child must actually be gone. shutdown's wait goroutine already reaped
	// it, so re-waiting reports that the pid was collected rather than a live
	// process. Either a collected child or a killed-and-reaped one is a pass;
	// a still-running child is not.
	if state, err := cmd.Process.Wait(); err == nil {
		if state != nil && state.Exited() && state.ExitCode() == 0 {
			t.Error("stubborn process exited 0; expected the forced-kill path to terminate it")
		}
	} else if !errors.Is(err, os.ErrProcessDone) && !strings.Contains(err.Error(), "no child processes") {
		t.Fatalf("unexpected error re-checking child process: %v", err)
	}
}

// TestRestartAfterFullReleaseGetsFreshPool is a regression test for a stale
// pool left in the backend's index. When the last holder releases a pool, the
// pool is torn down. If it stayed reachable by capability runtime identity, the
// next Start would hand back a closed pool with no workers, and every execution
// would fail instead of starting fresh capacity.
//
// Restarting an instance is an ordinary operation, so this path must recover.
func TestRestartAfterFullReleaseGetsFreshPool(t *testing.T) {
	rt := New(nil)
	defer func() { _ = rt.Close(context.Background()) }()

	spec := newSpec("example:grpc-echo", fixtures.grpcEcho, capabilityrt.ProtocolV1, 1)

	first, err := rt.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	firstPool, ok := first.(*workerPool)
	if !ok {
		t.Fatalf("instance is %T, want *workerPool", first)
	}

	if _, err := first.Execute(context.Background(), &capabilityrt.Request{Params: map[string]any{"value": "first"}}); err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	if got := firstPool.workerCount(); got != 1 {
		t.Fatalf("pool has %d workers, want 1", got)
	}

	// Release the only holder; the pool is now torn down.
	if err := first.Close(context.Background()); err != nil {
		t.Fatalf("closing instance: %v", err)
	}

	// Restarting must yield a working pool, not the closed one.
	second, err := rt.Start(context.Background(), spec)
	if err != nil {
		t.Fatalf("restarting: %v", err)
	}
	secondPool, ok := second.(*workerPool)
	if !ok {
		t.Fatalf("instance is %T, want *workerPool", second)
	}

	if secondPool == firstPool {
		t.Fatal("restart returned the already-closed pool; it was never deregistered")
	}

	resp, err := second.Execute(context.Background(), &capabilityrt.Request{Params: map[string]any{"value": "second"}})
	if err != nil {
		t.Fatalf("Execute after restart: %v", err)
	}
	if resp.Result["value"] != "second" {
		t.Errorf("value = %v, want second", resp.Result["value"])
	}
	if err := second.Close(context.Background()); err != nil {
		t.Fatalf("closing restarted instance: %v", err)
	}
}
