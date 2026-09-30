package plugin

import (
	"encoding/json"
	"reflect"
	"testing"

	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
)

// The frozen capability runtime set is the only part of the execution
// configuration payload that crosses the module boundary. These tests pin the
// wire key from the consumer side; application/compiler pins the same constant
// from the producer side, so neither module can drift alone.

func TestDecodeResolvedCapabilityRuntimesReadsFrozenSet(t *testing.T) {
	frozen := capabilityrt.ResolvedCapabilityRuntime{
		Type:             "example:echo",
		RequestedVersion: "^1.0.0",
		ResolvedVersion:  "1.0.0",
		Registry:         "local",
		Digest:           "sha256:abc",
		Runtime: capabilityrt.RuntimeInfo{
			Type:       capabilityrt.RuntimeKindProcess,
			Protocol:   capabilityrt.ProtocolJSONV1,
			Entrypoint: "echo",
		},
		Capabilities: []string{"example:echo"},
		RootDir:      "/home/user/.neuron/capabilityRuntimes/example/echo/1.0.0",
	}

	// A payload shaped exactly like the one the CLI persists: neighbouring
	// configuration keys must not interfere with the frozen set.
	payload := map[string]any{
		"capabilityRuntime_registries":   []any{map[string]any{"name": "local", "url": "/catalog"}},
		"capabilityRuntime_requirements": []any{map[string]any{"name": "example:echo", "version": "^1.0.0"}},
		capabilityrt.ResolvedCapabilityRuntimesKey: []any{
			map[string]any{
				"type":             frozen.Type,
				"requestedVersion": frozen.RequestedVersion,
				"resolvedVersion":  frozen.ResolvedVersion,
				"registry":         frozen.Registry,
				"digest":           frozen.Digest,
				"runtime": map[string]any{
					"type":       frozen.Runtime.Type,
					"protocol":   frozen.Runtime.Protocol,
					"entrypoint": frozen.Runtime.Entrypoint,
				},
				"capabilities": []any{"example:echo"},
				"rootDir":      frozen.RootDir,
			},
		},
		"inspector": map[string]any{"enabled": true},
	}

	got, err := DecodeResolvedCapabilityRuntimes(payload)
	if err != nil {
		t.Fatalf("DecodeResolvedCapabilityRuntimes: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d frozen capability runtimes, want 1", len(got))
	}
	if !reflect.DeepEqual(frozen, got[0]) {
		t.Fatalf("frozen capability runtime mismatch:\n got %+v\nwant %+v", got[0], frozen)
	}
	if got[0].EntrypointPath() != frozen.RootDir+"/echo" {
		t.Fatalf("entrypoint path = %q, want %q", got[0].EntrypointPath(), frozen.RootDir+"/echo")
	}
}

// A value re-read from SQLite arrives as map[string]any, never as the producer's
// typed struct, so the decode must work from either shape.
func TestDecodeResolvedCapabilityRuntimesFromTypedPayload(t *testing.T) {
	typed := struct {
		Resolved []capabilityrt.ResolvedCapabilityRuntime `json:"resolved_capabilityRuntimes"`
	}{
		Resolved: []capabilityrt.ResolvedCapabilityRuntime{{Type: "example:echo", ResolvedVersion: "1.0.0"}},
	}

	got, err := DecodeResolvedCapabilityRuntimes(typed)
	if err != nil {
		t.Fatalf("DecodeResolvedCapabilityRuntimes: %v", err)
	}
	if len(got) != 1 || got[0].Type != "example:echo" {
		t.Fatalf("unexpected decode result: %+v", got)
	}
}

func TestDecodeResolvedCapabilityRuntimesWithoutFrozenSet(t *testing.T) {
	for name, payload := range map[string]any{
		"nil":    nil,
		"no key": map[string]any{"inspector": map[string]any{"enabled": true}},
		"empty":  map[string]any{capabilityrt.ResolvedCapabilityRuntimesKey: nil},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := DecodeResolvedCapabilityRuntimes(payload)
			if err != nil {
				t.Fatalf("DecodeResolvedCapabilityRuntimes: %v", err)
			}
			if len(got) != 0 {
				t.Fatalf("got %+v, want no frozen capability runtimes", got)
			}
		})
	}
}

// A payload that is not a JSON object is a corrupt registration, not an empty
// one: it must surface instead of silently yielding no capability runtimes.
func TestDecodeResolvedCapabilityRuntimesRejectsNonObjectPayload(t *testing.T) {
	if _, err := DecodeResolvedCapabilityRuntimes("resolved set"); err == nil {
		t.Fatal("expected an error decoding a non-object payload")
	}
}

func TestDecodeResolvedCapabilityRuntimesRejectsMalformedFrozenSet(t *testing.T) {
	payload := map[string]any{
		capabilityrt.ResolvedCapabilityRuntimesKey: json.RawMessage(`{"type":`),
	}

	if _, err := DecodeResolvedCapabilityRuntimes(payload); err == nil {
		t.Fatal("expected an error decoding a malformed frozen set")
	}
}
