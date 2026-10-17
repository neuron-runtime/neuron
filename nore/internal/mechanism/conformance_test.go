package mechanism

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/neuron-runtime/neuron/nore/internal/mechanism/process"
	"github.com/neuron-runtime/neuron/nore/internal/mechanism/wasm"
	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
	"github.com/neuron-runtime/neuron/shared/types/provider"
)

// The conformance suite hosts the same capability runtime artifact through every
// first-party provider and asserts the same observable behaviour. It is what
// makes "the provider is an implementation detail of hosting" a testable claim
// rather than a comment: a caller holding only a provider.Instance cannot tell
// the process provider from the WASM provider.

const echoSourceDir = "../../../examples/capability-runtimes/echo"

var fixture struct {
	native string
	wasm   string
}

// testRegistry is the single registry all conformance tests share. It exists for
// the whole test binary because the WASM provider owns a process-global wazero
// runtime that can only be closed once, at the very end.
var testRegistry *Registry

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "neuron-provider-conformance-")
	if err != nil {
		os.Exit(1)
	}
	native := filepath.Join(dir, "echo")
	wasmModule := filepath.Join(dir, "echo.wasm")

	if err := buildEcho(echoSourceDir, native, nil); err != nil {
		os.Exit(1)
	}
	if err := buildEcho(echoSourceDir, wasmModule, []string{"GOOS=wasip1", "GOARCH=wasm"}); err != nil {
		os.Exit(1)
	}
	fixture.native = native
	fixture.wasm = wasmModule

	reg, err := firstPartyRegistry()
	if err != nil {
		os.Exit(1)
	}
	testRegistry = reg

	code := m.Run()

	// The providers keep process-global state -- the process provider's worker
	// pools and the WASM provider's sandbox runtime -- so the whole set is
	// released once, after every test has finished.
	_ = reg.Close(context.Background())
	os.Exit(code)
}

func buildEcho(srcDir, out string, env []string) error {
	cmd := exec.Command("go", "build", "-C", srcDir, "-o", out, ".")
	cmd.Env = append(os.Environ(), append([]string{"GOWORK=off"}, env...)...)
	return cmd.Run()
}

// firstPartyRegistry returns a registry with both shipped providers registered.
// The caller owns closing it.
func firstPartyRegistry() (*Registry, error) {
	reg := New()
	if err := reg.Register(process.New(slog.Default())); err != nil {
		return nil, err
	}
	wm, err := wasm.New()
	if err != nil {
		return nil, err
	}
	if err := reg.Register(wm); err != nil {
		return nil, err
	}
	return reg, nil
}

// TestFirstPartyProvidersConform proves the Process and WASM providers satisfy
// the provider contract identically: each is selected for its runtime kind,
// launches the same artifact, executes the same request to the same result, and
// reports its own health.
func TestFirstPartyProvidersConform(t *testing.T) {
	cases := []struct {
		name        string
		runtimeKind string
		entrypoint  string
	}{
		{"process", capabilityrt.RuntimeKindProcess, fixture.native},
		{"wasm", capabilityrt.RuntimeKindWasm, fixture.wasm},
	}

	// One registry for the whole test: the WASM provider's sandbox runtime is a
	// process-global singleton, and closing it is irreversible, so the providers
	// are created and closed once around every subtest rather than per kind.
	reg := testRegistry

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := provider.Spec{
				Type:        "example:echo",
				Version:     "1.0.0",
				RuntimeKind: tc.runtimeKind,
				Protocol:    capabilityrt.ProtocolJSONV1,
				Entrypoint:  tc.entrypoint,
				RootDir:     filepath.Dir(tc.entrypoint),
			}

			if _, err := reg.Select(spec); err != nil {
				t.Fatalf("Select rejected a compatible spec: %v", err)
			}

			inst, err := reg.Start(context.Background(), spec)
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			defer func() { _ = inst.Close(context.Background()) }()

			if err := inst.Health(context.Background()); err != nil {
				t.Fatalf("Health: %v", err)
			}

			resp, err := inst.Execute(context.Background(), &capabilityrt.Request{
				Params: map[string]any{"value": "conformance"},
			})
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if resp.Error != "" {
				t.Fatalf("Execute returned an error response: %s", resp.Error)
			}
			if got := resp.Result["value"]; got != "conformance" {
				t.Errorf("result value = %v, want %q", got, "conformance")
			}
			if got := resp.Result["type"]; got != "example:echo" {
				t.Errorf("runtime type reported = %v, want %q", got, "example:echo")
			}
		})
	}
}

// TestProvidersDeclareConformingDescriptors asserts every registered kind is
// served by a provider that identifies itself, implements this provider contract
// version, and declares the kind it is registered for.
func TestProvidersDeclareConformingDescriptors(t *testing.T) {
	reg := testRegistry

	for _, kind := range reg.RegisteredKinds() {
		selected, err := reg.Select(provider.Spec{RuntimeKind: kind, Protocol: capabilityrt.ProtocolJSONV1})
		if err != nil {
			t.Fatalf("Select %s: %v", kind, err)
		}
		d := selected.Descriptor()
		if d.Name == "" {
			t.Errorf("provider for %q has an empty name", kind)
		}
		if d.Version == "" {
			t.Errorf("provider %q has an empty version", kind)
		}
		if err := d.CheckProviderProtocol(); err != nil {
			t.Errorf("provider %q: %v", kind, err)
		}
		if !d.SupportsRuntimeKind(kind) {
			t.Errorf("provider %q is registered for %q but does not declare it", d.Name, kind)
		}
	}
}

// TestWasmProviderRejectsGRPCProtocol is a compatibility test: the WASM provider
// cannot carry the gRPC protocol, so selection must reject it with a typed
// provider error rather than failing later inside the engine.
func TestWasmProviderRejectsGRPCProtocol(t *testing.T) {
	reg := testRegistry

	_, err := reg.Select(provider.Spec{
		RuntimeKind: capabilityrt.RuntimeKindWasm,
		Protocol:    capabilityrt.ProtocolV1,
	})
	if err == nil {
		t.Fatal("Select accepted the gRPC protocol for the WASM provider")
	}
	var perr *provider.Error
	if !errors.As(err, &perr) || perr.Kind != provider.ErrUnsupportedProtocol {
		t.Errorf("error = %v, want ErrUnsupportedProtocol", err)
	}
}

// TestContainerKindHasNoProvider is a compatibility test for an unimplemented
// kind: container is in the contract but no first-party provider hosts it, so
// selection must report it as a missing provider.
func TestContainerKindHasNoProvider(t *testing.T) {
	reg := testRegistry

	_, err := reg.Select(provider.Spec{RuntimeKind: capabilityrt.RuntimeKindContainer})
	if err == nil {
		t.Fatal("Select accepted the container kind, which has no provider")
	}
	var perr *provider.Error
	if !errors.As(err, &perr) || perr.Kind != provider.ErrMissingProvider {
		t.Errorf("error = %v, want ErrMissingProvider", err)
	}
}
