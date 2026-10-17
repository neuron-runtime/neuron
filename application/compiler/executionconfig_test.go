package compiler

import (
	"encoding/json"
	"testing"

	"github.com/neuron-runtime/neuron/application/compiler/manifest"
	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
	providertypes "github.com/neuron-runtime/neuron/shared/types/provider"
)

// The frozen capability runtime set is the only part of ExecutionConfigurations
// that N.O.R.E. reads back. N.O.R.E. is a separate Go module that decodes it
// under capabilityrt.ResolvedCapabilityRuntimesKey, so this test pins the key
// the CLI actually marshals against that shared constant: the two sides can no
// longer disagree without a failing test on one of them.
func TestExecutionConfigurationsMarshalFrozenSetUnderSharedKey(t *testing.T) {
	configs := ExecutionConfigurations{
		CapabilityRuntimeRequirements: CapabilityRuntimeRequirements([]manifest.Capability{{
			Name:              "echo",
			CapabilityRuntime: manifest.CapabilityRuntimeSpec{Name: "example:echo", Version: "^1.0.0", Registry: "local"},
		}}),
		ResolvedCapabilityRuntimes: []capabilityrt.ResolvedCapabilityRuntime{{
			Type:            "example:echo",
			ResolvedVersion: "1.0.0",
			Registry:        "local",
			Runtime:         capabilityrt.RuntimeInfo{Type: capabilityrt.RuntimeKindProcess, Entrypoint: "echo"},
		}},
	}

	raw, err := json.Marshal(configs)
	if err != nil {
		t.Fatalf("marshal execution configurations: %v", err)
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("unmarshal execution configurations: %v", err)
	}

	frozen, ok := envelope[capabilityrt.ResolvedCapabilityRuntimesKey]
	if !ok {
		t.Fatalf("frozen capability runtimes missing from the register payload under key %q: %s",
			capabilityrt.ResolvedCapabilityRuntimesKey, raw)
	}

	var decoded []capabilityrt.ResolvedCapabilityRuntime
	if err := json.Unmarshal(frozen, &decoded); err != nil {
		t.Fatalf("decode frozen capability runtimes: %v", err)
	}
	if len(decoded) != 1 || decoded[0].Type != "example:echo" || decoded[0].Runtime.Entrypoint != "echo" {
		t.Fatalf("frozen capability runtimes did not survive the wire: %+v", decoded)
	}

	// An assembly with no external capability runtimes must omit the key rather
	// than send an empty set, so N.O.R.E. keeps treating it as absent.
	raw, err = json.Marshal(ExecutionConfigurations{})
	if err != nil {
		t.Fatalf("marshal empty execution configurations: %v", err)
	}
	envelope = map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("unmarshal empty execution configurations: %v", err)
	}
	if _, ok := envelope[capabilityrt.ResolvedCapabilityRuntimesKey]; ok {
		t.Fatalf("empty payload must omit %q: %s", capabilityrt.ResolvedCapabilityRuntimesKey, raw)
	}
}

// The frozen provider set travels in the same register payload as the frozen
// capability runtimes. It must marshal under its dedicated key and survive
// the wire, and an assembly with no external capability runtimes must omit it
// entirely so N.O.R.E. keeps treating it as absent.
func TestExecutionConfigurationsMarshalFrozenProviders(t *testing.T) {
	configs := ExecutionConfigurations{
		ResolvedProviders: []providertypes.ResolvedProvider{{
			Name:            "process",
			ResolvedVersion: "0.1.0",
			ProtocolVersion: providertypes.ProtocolVersion,
			Registry:        "bootstrap",
			Digest:          "sha256:abc",
			RuntimeKinds:    []string{"process"},
		}},
	}

	raw, err := json.Marshal(configs)
	if err != nil {
		t.Fatalf("marshal execution configurations: %v", err)
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("unmarshal execution configurations: %v", err)
	}

	frozen, ok := envelope[ResolvedProvidersKey]
	if !ok {
		t.Fatalf("frozen providers missing from the register payload under key %q: %s", ResolvedProvidersKey, raw)
	}

	// The struct tag and the exported key constant must agree.
	var tagged map[string]json.RawMessage
	if err := json.Unmarshal(raw, &tagged); err != nil {
		t.Fatalf("unmarshal execution configurations: %v", err)
	}
	if _, ok := tagged["resolved_providers"]; !ok {
		t.Fatalf("struct tag drifted from %q: %s", ResolvedProvidersKey, raw)
	}

	var decoded []providertypes.ResolvedProvider
	if err := json.Unmarshal(frozen, &decoded); err != nil {
		t.Fatalf("decode frozen providers: %v", err)
	}
	if len(decoded) != 1 || decoded[0].Name != "process" || decoded[0].Digest != "sha256:abc" {
		t.Fatalf("frozen providers did not survive the wire: %+v", decoded)
	}

	raw, err = json.Marshal(ExecutionConfigurations{})
	if err != nil {
		t.Fatalf("marshal empty execution configurations: %v", err)
	}
	envelope = map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("unmarshal empty execution configurations: %v", err)
	}
	if _, ok := envelope[ResolvedProvidersKey]; ok {
		t.Fatalf("empty payload must omit %q: %s", ResolvedProvidersKey, raw)
	}
}
