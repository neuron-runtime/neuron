package backend

import (
	"context"
	"errors"
	"strings"
	"testing"

	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
)

// The registry's job is to route a launch to the backend registered for a runtime
// kind, and to release the backends at shutdown. It owns no instances: sharing
// them is each backend's own decision, and this package deliberately keeps no map
// of them that it could not honour.

type stubBackend struct {
	kind      string
	starts    []capabilityrt.BackendSpec
	startErr  error
	closes    int
	closeErr  error
	closeable bool
}

func (b *stubBackend) Start(ctx context.Context, spec capabilityrt.BackendSpec) (capabilityrt.BackendInstance, error) {
	if b.startErr != nil {
		return nil, b.startErr
	}
	b.starts = append(b.starts, spec)
	return &stubInstance{}, nil
}

func (b *stubBackend) BackendName() string { return b.kind }

// Close exists only on the backends that opt in, so the registry's type
// assertion is exercised in both directions.
func (b *stubBackend) Close(ctx context.Context) error {
	b.closes++
	return b.closeErr
}

type stubInstance struct{}

func (i *stubInstance) Execute(ctx context.Context, req *capabilityrt.Request) (*capabilityrt.Response, error) {
	return &capabilityrt.Response{}, nil
}

func (i *stubInstance) Health(ctx context.Context) error { return nil }

func (i *stubInstance) Close(ctx context.Context) error { return nil }

// A backend that must opt into being closed cannot be distinguished by a type
// assertion on a type that always has the method, so the stub above is replaced by
// one that genuinely lacks it.
type plainBackend struct {
	kind   string
	starts int
}

func (b *plainBackend) Start(ctx context.Context, spec capabilityrt.BackendSpec) (capabilityrt.BackendInstance, error) {
	b.starts++
	return &stubInstance{}, nil
}

func (b *plainBackend) BackendName() string { return b.kind }

func testSpec() capabilityrt.BackendSpec {
	return capabilityrt.BackendSpec{
		Type:       "example:echo",
		Version:    "1.0.0",
		RootDir:    "/store/echo",
		Entrypoint: "/store/echo/echo",
		Protocol:   capabilityrt.ProtocolV1,
	}
}

func TestStartDispatchesToTheBackendForTheKind(t *testing.T) {
	process := &stubBackend{kind: capabilityrt.RuntimeKindProcess}
	reg := New()
	if err := reg.Register(capabilityrt.RuntimeKindProcess, process); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if _, err := reg.Start(context.Background(), capabilityrt.RuntimeKindProcess, testSpec()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(process.starts) != 1 {
		t.Fatalf("process backend launches = %d, want 1", len(process.starts))
	}
	if process.starts[0].Type != "example:echo" {
		t.Errorf("the backend received spec for %q, want the caller's spec passed through unchanged", process.starts[0].Type)
	}
}

// Two launches for the same runtime must reach the backend every time. The
// registry does not collapse them, because whether they can share one underlying
// runtime is the backend's call: the process backend shares a refcounted worker
// pool, the legacy JSON transport has nothing to share.
func TestStartDoesNotCollapseRepeatedLaunches(t *testing.T) {
	process := &stubBackend{kind: capabilityrt.RuntimeKindProcess}
	reg := New()
	if err := reg.Register(capabilityrt.RuntimeKindProcess, process); err != nil {
		t.Fatalf("Register: %v", err)
	}

	ctx := context.Background()
	for range 3 {
		if _, err := reg.Start(ctx, capabilityrt.RuntimeKindProcess, testSpec()); err != nil {
			t.Fatalf("Start: %v", err)
		}
	}
	if len(process.starts) != 3 {
		t.Errorf("backend launches = %d, want 3", len(process.starts))
	}
}

func TestStartReportsAnUnregisteredKind(t *testing.T) {
	reg := New()
	// One kind is registered, so the rejection can name what this build does host.
	if err := reg.Register(capabilityrt.RuntimeKindProcess, &plainBackend{kind: capabilityrt.RuntimeKindProcess}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	_, err := reg.Start(context.Background(), capabilityrt.RuntimeKindContainer, testSpec())
	if err == nil {
		t.Fatal("Start for an unregistered kind = nil error, want a rejection")
	}
	if !strings.Contains(err.Error(), capabilityrt.RuntimeKindProcess) {
		t.Errorf("error %q does not name the kinds this build supports", err)
	}
}

func TestStartPropagatesTheBackendFailure(t *testing.T) {
	reg := New()
	if err := reg.Register(capabilityrt.RuntimeKindProcess, &stubBackend{
		kind:     capabilityrt.RuntimeKindProcess,
		startErr: errors.New("entrypoint missing"),
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if _, err := reg.Start(context.Background(), capabilityrt.RuntimeKindProcess, testSpec()); err == nil {
		t.Fatal("Start = nil error, want the backend failure reported")
	}
}

// CloseBackends is the shutdown path, and the WASM backend's compiled-module
// cache and sandbox runtime are reachable only through it.
func TestCloseBackendsReleasesEveryBackend(t *testing.T) {
	process := &stubBackend{kind: capabilityrt.RuntimeKindProcess}
	wasm := &stubBackend{kind: capabilityrt.RuntimeKindWasm}
	reg := New()
	if err := reg.Register(capabilityrt.RuntimeKindProcess, process); err != nil {
		t.Fatalf("Register process: %v", err)
	}
	if err := reg.Register(capabilityrt.RuntimeKindWasm, wasm); err != nil {
		t.Fatalf("Register wasm: %v", err)
	}

	if err := reg.CloseBackends(context.Background()); err != nil {
		t.Fatalf("CloseBackends: %v", err)
	}
	if process.closes != 1 || wasm.closes != 1 {
		t.Errorf("close counts = process %d, wasm %d; want 1 each", process.closes, wasm.closes)
	}
}

// A backend that does not hold process-global state has nothing to release, and
// must not be forced to implement a Close it has no use for.
func TestCloseBackendsSkipsBackendsThatDoNotHoldProcessState(t *testing.T) {
	reg := New()
	if err := reg.Register(capabilityrt.RuntimeKindProcess, &plainBackend{kind: capabilityrt.RuntimeKindProcess}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if err := reg.CloseBackends(context.Background()); err != nil {
		t.Fatalf("CloseBackends: %v", err)
	}
}

// One backend failing to close must not stop the others, and the failure has to
// reach the caller so shutdown can report it instead of appearing clean.
func TestCloseBackendsReportsFailuresAndStillClosesTheRest(t *testing.T) {
	process := &stubBackend{kind: capabilityrt.RuntimeKindProcess}
	wasm := &stubBackend{kind: capabilityrt.RuntimeKindWasm, closeErr: errors.New("runtime already closed")}
	reg := New()
	if err := reg.Register(capabilityrt.RuntimeKindProcess, process); err != nil {
		t.Fatalf("Register process: %v", err)
	}
	if err := reg.Register(capabilityrt.RuntimeKindWasm, wasm); err != nil {
		t.Fatalf("Register wasm: %v", err)
	}

	err := reg.CloseBackends(context.Background())
	if err == nil {
		t.Fatal("CloseBackends = nil, want the failure reported")
	}
	if !strings.Contains(err.Error(), "wasm") {
		t.Errorf("error %q does not name the backend that failed", err)
	}
	if process.closes != 1 {
		t.Errorf("the backend that could close was skipped: close count = %d, want 1", process.closes)
	}
}

func TestCloseBackendsOnAnEmptyRegistryIsANoOp(t *testing.T) {
	if err := New().CloseBackends(context.Background()); err != nil {
		t.Fatalf("CloseBackends on an empty registry = %v, want nil", err)
	}
}

func TestRegisteredKindsIsSorted(t *testing.T) {
	reg := New()
	for _, kind := range []string{capabilityrt.RuntimeKindWasm, capabilityrt.RuntimeKindProcess} {
		if err := reg.Register(kind, &plainBackend{kind: kind}); err != nil {
			t.Fatalf("Register %s: %v", kind, err)
		}
	}

	got := reg.RegisteredKinds()
	want := []string{capabilityrt.RuntimeKindProcess, capabilityrt.RuntimeKindWasm}
	if len(got) != len(want) {
		t.Fatalf("RegisteredKinds() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("RegisteredKinds() = %v, want %v", got, want)
		}
	}
}

func TestRegisterRejectsInvalidInput(t *testing.T) {
	reg := New()

	if err := reg.Register("", &plainBackend{}); err == nil {
		t.Error("Register with an empty kind = nil error, want a rejection")
	}
	if err := reg.Register(capabilityrt.RuntimeKindProcess, nil); err == nil {
		t.Error("Register with a nil backend = nil error, want a rejection")
	}
}
