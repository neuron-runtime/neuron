package invoke

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

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
