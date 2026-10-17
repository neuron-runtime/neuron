package mechanism

import (
	"context"
	"errors"
	"strings"
	"testing"

	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
	"github.com/neuron-runtime/neuron/shared/types/provider"
)

// stubProvider is a provider.Provider whose only job is to record how the
// registry dispatched to it.
type stubProvider struct {
	descriptor provider.Descriptor
	starts     []provider.Spec
	startErr   error
	closes     int
	closeErr   error
}

func (p *stubProvider) Descriptor() provider.Descriptor { return p.descriptor }

func (p *stubProvider) Start(ctx context.Context, spec provider.Spec) (provider.Instance, error) {
	if p.startErr != nil {
		return nil, p.startErr
	}
	p.starts = append(p.starts, spec)
	return &stubInstance{}, nil
}

func (p *stubProvider) Close(ctx context.Context) error {
	p.closes++
	return p.closeErr
}

type stubInstance struct{}

func (i *stubInstance) Execute(ctx context.Context, req *capabilityrt.Request) (*capabilityrt.Response, error) {
	return &capabilityrt.Response{}, nil
}

func (i *stubInstance) Health(ctx context.Context) error { return nil }
func (i *stubInstance) Close(ctx context.Context) error  { return nil }

func processDescriptor() provider.Descriptor {
	return provider.Descriptor{
		Name:                       capabilityrt.RuntimeKindProcess,
		ProtocolVersion:            provider.ProtocolVersion,
		RuntimeKinds:               []string{capabilityrt.RuntimeKindProcess},
		CapabilityRuntimeProtocols: []string{capabilityrt.ProtocolV1, capabilityrt.ProtocolJSONV1},
	}
}

func testSpec(kind string) provider.Spec {
	return provider.Spec{
		Type:        "example:echo",
		Version:     "1.0.0",
		RuntimeKind: kind,
		RootDir:     "/store/echo",
		Entrypoint:  "/store/echo/echo",
		Protocol:    capabilityrt.ProtocolV1,
	}
}

func TestStartDispatchesToTheProviderForTheKind(t *testing.T) {
	process := &stubProvider{descriptor: processDescriptor()}
	reg := New()
	if err := reg.Register(process); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if _, err := reg.Start(context.Background(), testSpec(capabilityrt.RuntimeKindProcess)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(process.starts) != 1 {
		t.Fatalf("process provider launches = %d, want 1", len(process.starts))
	}
	if process.starts[0].Type != "example:echo" {
		t.Errorf("the provider received spec for %q, want the caller's spec passed through unchanged", process.starts[0].Type)
	}
}

// Two launches for the same runtime must reach the provider every time. The
// registry does not collapse them, because whether they can share one underlying
// runtime is the provider's call: the process provider shares a refcounted worker
// pool, the legacy JSON transport has nothing to share.
func TestStartDoesNotCollapseRepeatedLaunches(t *testing.T) {
	process := &stubProvider{descriptor: processDescriptor()}
	reg := New()
	if err := reg.Register(process); err != nil {
		t.Fatalf("Register: %v", err)
	}

	ctx := context.Background()
	for range 3 {
		if _, err := reg.Start(ctx, testSpec(capabilityrt.RuntimeKindProcess)); err != nil {
			t.Fatalf("Start: %v", err)
		}
	}
	if len(process.starts) != 3 {
		t.Errorf("provider launches = %d, want 3", len(process.starts))
	}
}

// A missing provider is reported as a normalized provider.Error so callers can
// classify it, and the message names the kinds this build does host.
func TestStartReportsAnUnregisteredKind(t *testing.T) {
	reg := New()
	if err := reg.Register(&stubProvider{descriptor: processDescriptor()}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	_, err := reg.Start(context.Background(), testSpec(capabilityrt.RuntimeKindContainer))
	if err == nil {
		t.Fatal("Start for an unregistered kind = nil error, want a rejection")
	}
	var perr *provider.Error
	if !errors.As(err, &perr) || perr.Kind != provider.ErrMissingProvider {
		t.Fatalf("error = %v, want a missing-provider failure", err)
	}
	if !strings.Contains(err.Error(), capabilityrt.RuntimeKindProcess) {
		t.Errorf("error %q does not name the kinds this build supports", err)
	}
}

func TestStartPropagatesTheProviderFailure(t *testing.T) {
	reg := New()
	if err := reg.Register(&stubProvider{
		descriptor: processDescriptor(),
		startErr:   errors.New("entrypoint missing"),
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if _, err := reg.Start(context.Background(), testSpec(capabilityrt.RuntimeKindProcess)); err == nil {
		t.Fatal("Start = nil error, want the provider failure reported")
	}
}

// Select refuses a provider whose declared invocation protocols do not include
// the Capability Runtime's, so an incompatible provider is rejected before it is
// asked to launch anything.
func TestSelectRejectsUnsupportedProtocol(t *testing.T) {
	reg := New()
	if err := reg.Register(&stubProvider{descriptor: provider.Descriptor{
		Name:                       capabilityrt.RuntimeKindProcess,
		RuntimeKinds:               []string{capabilityrt.RuntimeKindProcess},
		CapabilityRuntimeProtocols: []string{capabilityrt.ProtocolJSONV1},
	}}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	spec := testSpec(capabilityrt.RuntimeKindProcess)
	_, err := reg.Select(spec)
	var perr *provider.Error
	if !errors.As(err, &perr) || perr.Kind != provider.ErrUnsupportedProtocol {
		t.Fatalf("Select = %v, want unsupported-protocol", err)
	}
}

// Select refuses a provider that does not run on the Capability Runtime's
// platform, so a platform-bound provider cannot be selected on the wrong host.
func TestSelectRejectsUnsupportedPlatform(t *testing.T) {
	reg := New()
	if err := reg.Register(&stubProvider{descriptor: provider.Descriptor{
		Name:         capabilityrt.RuntimeKindProcess,
		RuntimeKinds: []string{capabilityrt.RuntimeKindProcess},
		Platforms:    []string{"plan9-386"},
	}}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	_, err := reg.Select(testSpec(capabilityrt.RuntimeKindProcess))
	if err == nil {
		t.Fatal("Select = nil error, want a platform rejection")
	}
	var perr *provider.Error
	if !errors.As(err, &perr) || perr.Kind != provider.ErrUnsupportedPlatform {
		t.Errorf("error = %v, want ErrUnsupportedPlatform", err)
	}
}

// Close is the shutdown path, and the WASM provider's compiled-module cache and
// sandbox runtime are reachable only through it.
func TestCloseReleasesEveryProvider(t *testing.T) {
	process := &stubProvider{descriptor: processDescriptor()}
	wasm := &stubProvider{descriptor: provider.Descriptor{
		Name:         capabilityrt.RuntimeKindWasm,
		RuntimeKinds: []string{capabilityrt.RuntimeKindWasm},
	}}
	reg := New()
	if err := reg.Register(process); err != nil {
		t.Fatalf("Register process: %v", err)
	}
	if err := reg.Register(wasm); err != nil {
		t.Fatalf("Register wasm: %v", err)
	}

	if err := reg.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if process.closes != 1 || wasm.closes != 1 {
		t.Errorf("close counts = process %d, wasm %d; want 1 each", process.closes, wasm.closes)
	}
}

// A provider registered for several kinds is closed exactly once, because it is
// one value with one set of process-global resources.
func TestCloseReleasesASharedProviderOnce(t *testing.T) {
	shared := &stubProvider{descriptor: provider.Descriptor{
		Name:         "multi",
		RuntimeKinds: []string{capabilityrt.RuntimeKindProcess, capabilityrt.RuntimeKindWasm},
	}}
	reg := New()
	if err := reg.Register(shared); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if err := reg.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if shared.closes != 1 {
		t.Errorf("close count = %d, want 1", shared.closes)
	}
}

func TestCloseReportsTheFailingProviderAndStillClosesOthers(t *testing.T) {
	process := &stubProvider{descriptor: processDescriptor()}
	wasm := &stubProvider{
		descriptor: provider.Descriptor{
			Name:         capabilityrt.RuntimeKindWasm,
			RuntimeKinds: []string{capabilityrt.RuntimeKindWasm},
		},
		closeErr: errors.New("engine already closed"),
	}
	reg := New()
	if err := reg.Register(process); err != nil {
		t.Fatalf("Register process: %v", err)
	}
	if err := reg.Register(wasm); err != nil {
		t.Fatalf("Register wasm: %v", err)
	}

	err := reg.Close(context.Background())
	if err == nil {
		t.Fatal("Close = nil, want the failure reported")
	}
	if !strings.Contains(err.Error(), "wasm") {
		t.Errorf("error %q does not name the provider that failed", err)
	}
	if process.closes != 1 {
		t.Errorf("the provider that could close was skipped: close count = %d, want 1", process.closes)
	}
}

func TestCloseOnAnEmptyRegistryIsANoOp(t *testing.T) {
	if err := New().Close(context.Background()); err != nil {
		t.Fatalf("Close on an empty registry = %v, want nil", err)
	}
}

func TestRegisteredKindsIsSorted(t *testing.T) {
	reg := New()
	if err := reg.Register(&stubProvider{descriptor: provider.Descriptor{
		Name:         "p",
		RuntimeKinds: []string{capabilityrt.RuntimeKindWasm, capabilityrt.RuntimeKindProcess},
	}}); err != nil {
		t.Fatalf("Register: %v", err)
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

	if err := reg.Register(nil); err == nil {
		t.Error("Register with a nil provider = nil error, want a rejection")
	}
	if err := reg.Register(&stubProvider{descriptor: provider.Descriptor{Name: "empty"}}); err == nil {
		t.Error("Register with no runtime kinds = nil error, want a rejection")
	}
	if err := reg.Register(&stubProvider{descriptor: provider.Descriptor{
		Name:            "future",
		ProtocolVersion: "neuron/provider-v99",
		RuntimeKinds:    []string{capabilityrt.RuntimeKindProcess},
	}}); err == nil {
		t.Error("Register with an unknown provider protocol version = nil error, want a rejection")
	}
}
