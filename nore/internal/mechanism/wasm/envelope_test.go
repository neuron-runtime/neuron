package wasm

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
)

// TestEnvelopeAbsentFields proves old WASM modules that never emit or read the
// additive fields see the original JSON envelope: no suspend is inferred and no
// resume field is emitted.
func TestEnvelopeAbsentFields(t *testing.T) {
	resp, err := decodeResponse([]byte(`{"result":{"value":"ok"}}`))
	if err != nil {
		t.Fatalf("decode old response: %v", err)
	}
	if resp.Suspend != nil {
		t.Fatalf("old response suspend = %#v, want nil", resp.Suspend)
	}
	if got := resp.Result["value"]; got != "ok" {
		t.Errorf("old response result = %v, want ok", got)
	}

	encoded, err := encodeRequest(&capabilityrt.Request{Params: map[string]any{"x": 1}})
	if err != nil {
		t.Fatalf("encode request without resume: %v", err)
	}
	if bytes.Contains(encoded, []byte("resume")) {
		t.Errorf("request without resume emitted resume: %s", encoded)
	}
}

// TestEnvelopeSuspendSignal proves a WASM module's signal suspend survives the
// decode path with metadata intact and no result.
func TestEnvelopeSuspendSignal(t *testing.T) {
	resp, err := decodeResponse([]byte(`{"result":{},"suspend":{"correlationKey":"corr-1","waitType":"signal","expectedSignal":"approval","payloadSchema":"approval/v1"}}`))
	if err != nil {
		t.Fatalf("decode suspend: %v", err)
	}
	if resp.Suspend == nil || *resp.Suspend != (capabilityrt.Suspend{
		CorrelationKey: "corr-1",
		WaitType:       "signal",
		ExpectedSignal: "approval",
		PayloadSchema:  "approval/v1",
	}) {
		t.Fatalf("suspend = %#v", resp.Suspend)
	}
	if len(resp.Result) != 0 {
		t.Errorf("result = %#v, want empty", resp.Result)
	}
}

// TestEnvelopeSuspendTimerDeadline proves deadline_unix_ms is preserved on the
// WASM wire value, which the process mapping converts into a WaitUntil.
func TestEnvelopeSuspendTimerDeadline(t *testing.T) {
	resp, err := decodeResponse([]byte(`{"result":{},"suspend":{"correlationKey":"t-1","waitType":"timer","deadlineUnixMs":1760000000123}}`))
	if err != nil {
		t.Fatalf("decode timer suspend: %v", err)
	}
	if resp.Suspend == nil || resp.Suspend.WaitType != "timer" || resp.Suspend.DeadlineUnixMs != 1_760_000_000_123 {
		t.Fatalf("timer suspend = %#v", resp.Suspend)
	}
}

// TestEnvelopeMalformedSuspend exercises untrusted executor metadata on the
// WASM boundary. Each case must be rejected explicitly.
func TestEnvelopeMalformedSuspend(t *testing.T) {
	long := strings.Repeat("x", 257)
	cases := []struct {
		name string
		body string
	}{
		{"reserved deadline wait type", `{"result":{},"suspend":{"waitType":"deadline","deadlineUnixMs":1}}`},
		{"unknown wait type", `{"result":{},"suspend":{"waitType":"anything"}}`},
		{"empty wait type", `{"result":{},"suspend":{}}`},
		{"signal without expected signal", `{"result":{},"suspend":{"waitType":"signal"}}`},
		{"timer without deadline", `{"result":{},"suspend":{"waitType":"timer"}}`},
		{"negative deadline", `{"result":{},"suspend":{"waitType":"signal","expectedSignal":"s","deadlineUnixMs":-1}}`},
		{"oversized correlation key", `{"result":{},"suspend":{"waitType":"signal","expectedSignal":"s","correlationKey":"` + long + `"}}`},
		{"suspend with result", `{"result":{"x":1},"suspend":{"waitType":"signal","expectedSignal":"s"}}`},
		{"suspend with error", `{"result":{},"error":"also failed","suspend":{"waitType":"signal","expectedSignal":"s"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := decodeResponse([]byte(tc.body))
			if err == nil {
				t.Fatalf("decodeResponse() = %#v, want explicit rejection", resp)
			}
		})
	}
}

// TestEnvelopeResumeRoundTripAndDuplicateDelivery proves the WASM envelope
// carries signal/timer resume inputs with payload and delivery ID intact, and
// that repeated delivery of the same ID produces identical bytes: the transport
// never dedupes or mutates the capability's idempotency key.
func TestEnvelopeResumeRoundTripAndDuplicateDelivery(t *testing.T) {
	for _, cause := range []string{"signal", "timer"} {
		t.Run(cause, func(t *testing.T) {
			req := &capabilityrt.Request{
				Params: map[string]any{"x": "y"},
				Resume: &capabilityrt.ResumeInput{
					Cause:      cause,
					SignalName: "approval",
					DeliveryID: "delivery-1",
					Payload:    map[string]any{"by": "ada"},
				},
			}
			first, err := encodeRequest(req)
			if err != nil {
				t.Fatalf("encode resume: %v", err)
			}
			second, err := encodeRequest(req)
			if err != nil {
				t.Fatalf("encode duplicate resume: %v", err)
			}
			if !bytes.Equal(first, second) {
				t.Errorf("duplicate delivery changed the wire: %s vs %s", first, second)
			}

			var decoded capabilityrt.Request
			if err := json.Unmarshal(first, &decoded); err != nil {
				t.Fatalf("decode encoded resume: %v", err)
			}
			if decoded.Resume == nil {
				t.Fatal("decoded request has no resume input")
			}
			got, want := decoded.Resume, req.Resume
			if got.Cause != want.Cause || got.SignalName != want.SignalName || got.DeliveryID != want.DeliveryID || got.Payload["by"] != want.Payload["by"] {
				t.Fatalf("decoded resume = %#v, want %#v", got, want)
			}
		})
	}
}

// TestEnvelopeOversizedResumeRejected bounds untrusted wait metadata before it
// is handed to a WASM module.
func TestEnvelopeOversizedResumeRejected(t *testing.T) {
	_, err := encodeRequest(&capabilityrt.Request{
		Resume: &capabilityrt.ResumeInput{DeliveryID: strings.Repeat("x", 257)},
	})
	if err == nil {
		t.Fatal("oversized delivery ID was accepted")
	}
}
