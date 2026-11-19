package invoke

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
)

func TestDecodeInvokeRequest(t *testing.T) {
	in := `{"capabilityRuntime":{"type":"example:echo","resolvedVersion":"1.0.0","runtime":{"type":"process","entrypoint":"echo"}},"params":{"value":"hello"}}`

	req, err := decode(strings.NewReader(in))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if req.CapabilityRuntime.Type != "example:echo" {
		t.Errorf("type = %q, want example:echo", req.CapabilityRuntime.Type)
	}
	if req.CapabilityRuntime.Runtime.Entrypoint != "echo" {
		t.Errorf("entrypoint = %q, want echo", req.CapabilityRuntime.Runtime.Entrypoint)
	}
	if req.Params["value"] != "hello" {
		t.Errorf("params.value = %v, want hello", req.Params["value"])
	}
}

func TestDecodeInvokeRequestRejectsMalformed(t *testing.T) {
	if _, err := decode(strings.NewReader("not json")); err == nil {
		t.Fatal("expected error for malformed request")
	}
}

func TestWriteInvokeResponse(t *testing.T) {
	var buf bytes.Buffer
	resp := &capabilityrt.InvokeResponse{Result: map[string]any{"value": "hello"}}
	if err := write(&buf, resp); err != nil {
		t.Fatalf("write: %v", err)
	}

	// The response is a single line-terminated JSON document so a parent can
	// read it with a line scanner.
	out := buf.String()
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("response is not newline-terminated: %q", out)
	}

	var got capabilityrt.InvokeResponse
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Result["value"] != "hello" {
		t.Errorf("result.value = %v, want hello", got.Result["value"])
	}
}

// TestOutcomeResponseRejectsSuspend proves a one-shot invocation fails closed
// when a capability suspends. InvokeResponse has no suspend field, so an empty
// success would silently hide the handoff the capability asked for.
func TestOutcomeResponseRejectsSuspend(t *testing.T) {
	outcome := contracts.CapabilityOutcome{
		Result: map[string]any{"partial": "work"},
		Suspend: &contracts.CapabilitySuspend{
			CorrelationKey: "wait-for-approval",
			ExpectedSignal: "approval",
			PayloadSchema:  `{"approved":true}`,
		},
	}

	got := outcomeResponse(outcome)
	if got.Error != unsupportedSuspendOutcome {
		t.Errorf("error = %q, want %q", got.Error, unsupportedSuspendOutcome)
	}
	if !strings.HasPrefix(got.Error, "unsupported_suspend_outcome:") {
		t.Errorf("error = %q, want the unsupported_suspend_outcome prefix", got.Error)
	}
	if got.Result != nil {
		t.Errorf("result = %#v, want no result for a suspend", got.Result)
	}
}

// TestOutcomeResponsePreservesOrdinaryResult proves the mapping leaves a normal
// outcome unchanged, including an empty result.
func TestOutcomeResponsePreservesOrdinaryResult(t *testing.T) {
	cases := []struct {
		name   string
		result map[string]any
	}{
		{name: "with result", result: map[string]any{"value": "hello"}},
		{name: "empty result", result: nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := outcomeResponse(contracts.CapabilityOutcome{Result: tc.result})
			if got.Error != "" {
				t.Errorf("error = %q, want none", got.Error)
			}
			if len(got.Result) != len(tc.result) {
				t.Fatalf("result = %#v, want %#v", got.Result, tc.result)
			}
			for key, value := range tc.result {
				if got.Result[key] != value {
					t.Errorf("result[%q] = %v, want %v", key, got.Result[key], value)
				}
			}
		})
	}
}
