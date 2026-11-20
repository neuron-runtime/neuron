package capabilityruntime

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestAdditiveEnvelopeAbsentFields proves the new suspend/resume fields do not
// change the wire for runtimes or hosts that do not use them. This is the
// compatibility guarantee that makes the JSON change additive.
func TestAdditiveEnvelopeAbsentFields(t *testing.T) {
	var resp Response
	if err := json.Unmarshal([]byte(`{"result":{"value":"ok"}}`), &resp); err != nil {
		t.Fatalf("unmarshal old response: %v", err)
	}
	if resp.Suspend != nil {
		t.Fatalf("old response suspend = %#v, want nil", resp.Suspend)
	}
	if got := resp.Result["value"]; got != "ok" {
		t.Errorf("old response result = %v, want ok", got)
	}

	var req Request
	if err := json.Unmarshal([]byte(`{"params":{"x":1}}`), &req); err != nil {
		t.Fatalf("unmarshal old request: %v", err)
	}
	if req.Resume != nil {
		t.Fatalf("old request resume = %#v, want nil", req.Resume)
	}

	encoded, err := json.Marshal(Request{Params: map[string]any{"x": 1}})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	if strings.Contains(string(encoded), "resume") {
		t.Errorf("request without resume emitted a resume field: %s", encoded)
	}

	encoded, err = json.Marshal(Response{Result: map[string]any{"x": 1}})
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	if strings.Contains(string(encoded), "suspend") {
		t.Errorf("response without suspend emitted a suspend field: %s", encoded)
	}
}

// TestAdditiveEnvelopeRoundTrip proves suspend and resume metadata survives the
// envelope with the documented camelCase wire names.
func TestAdditiveEnvelopeRoundTrip(t *testing.T) {
	resp := Response{
		Suspend: &Suspend{
			CorrelationKey: "corr-1",
			WaitType:       WaitTypeSignal,
			ExpectedSignal: "approval",
			DeadlineUnixMs: int64(4242),
			PayloadSchema:  "approval/v1",
		},
	}
	encoded, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	for _, field := range []string{"correlationKey", "waitType", "expectedSignal", "deadlineUnixMs", "payloadSchema"} {
		if !strings.Contains(string(encoded), field) {
			t.Errorf("encoded response %s missing %q", encoded, field)
		}
	}
	var decoded Response
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if decoded.Suspend == nil || decoded.Suspend.CorrelationKey != "corr-1" || decoded.Suspend.DeadlineUnixMs != 4242 {
		t.Fatalf("decoded suspend = %#v", decoded.Suspend)
	}

	req := Request{Resume: &ResumeInput{
		Cause:      ResumeCauseSignal,
		SignalName: "approval",
		DeliveryID: "delivery-1",
		Payload:    map[string]any{"by": "ada"},
	}}
	encoded, err = json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	for _, field := range []string{"cause", "signalName", "deliveryId", "payload"} {
		if !strings.Contains(string(encoded), field) {
			t.Errorf("encoded request %s missing %q", encoded, field)
		}
	}
	var decodedReq Request
	if err := json.Unmarshal(encoded, &decodedReq); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if decodedReq.Resume == nil || decodedReq.Resume.DeliveryID != "delivery-1" || decodedReq.Resume.Payload["by"] != "ada" {
		t.Fatalf("decoded resume = %#v", decodedReq.Resume)
	}
}

func TestValidateSuspend(t *testing.T) {
	deadline := time.Now().Add(time.Hour).UnixMilli()
	cases := []struct {
		name    string
		suspend Suspend
		wantErr bool
	}{
		{"signal", Suspend{WaitType: WaitTypeSignal, ExpectedSignal: "approval"}, false},
		{"signal with deadline", Suspend{WaitType: WaitTypeSignal, ExpectedSignal: "approval", DeadlineUnixMs: deadline}, false},
		{"timer", Suspend{WaitType: WaitTypeTimer, DeadlineUnixMs: deadline}, false},
		{"reserved deadline wait type", Suspend{WaitType: "deadline", DeadlineUnixMs: deadline}, true},
		{"unknown wait type", Suspend{WaitType: "anything"}, true},
		{"empty wait type", Suspend{}, true},
		{"signal without expected signal", Suspend{WaitType: WaitTypeSignal}, true},
		{"timer without deadline", Suspend{WaitType: WaitTypeTimer}, true},
		{"negative deadline", Suspend{WaitType: WaitTypeSignal, ExpectedSignal: "s", DeadlineUnixMs: -1}, true},
		{"oversized correlation key", Suspend{WaitType: WaitTypeSignal, ExpectedSignal: "s", CorrelationKey: strings.Repeat("x", maxMetadataFieldBytes+1)}, true},
		{"oversized expected signal", Suspend{WaitType: WaitTypeSignal, ExpectedSignal: strings.Repeat("x", maxMetadataFieldBytes+1)}, true},
		{"oversized payload schema", Suspend{WaitType: WaitTypeSignal, ExpectedSignal: "s", PayloadSchema: strings.Repeat("x", maxMetadataFieldBytes+1)}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSuspend(&tc.suspend)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateSuspend(%#v) = %v, wantErr %v", tc.suspend, err, tc.wantErr)
			}
		})
	}
	if err := ValidateSuspend(nil); err == nil {
		t.Fatal("ValidateSuspend(nil) should fail")
	}
}

func TestValidateResumeInput(t *testing.T) {
	if err := ValidateResumeInput(nil); err != nil {
		t.Fatalf("nil resume should be allowed: %v", err)
	}
	valid := &ResumeInput{
		Cause:      ResumeCauseSignal,
		SignalName: "approval",
		DeliveryID: "delivery-1",
		Payload:    map[string]any{"by": "ada"},
	}
	if err := ValidateResumeInput(valid); err != nil {
		t.Fatalf("valid resume rejected: %v", err)
	}

	cases := []struct {
		name    string
		resume  ResumeInput
		wantErr bool
	}{
		{"oversized cause", ResumeInput{Cause: strings.Repeat("x", maxResumeCauseBytes+1)}, true},
		{"oversized signal name", ResumeInput{SignalName: strings.Repeat("x", maxMetadataFieldBytes+1)}, true},
		{"oversized delivery id", ResumeInput{DeliveryID: strings.Repeat("x", maxMetadataFieldBytes+1)}, true},
		{"too many payload entries", ResumeInput{Payload: map[string]any(func() map[string]any {
			m := map[string]any{}
			for i := 0; i <= maxResumePayloadEntries; i++ {
				m[string(rune('a'+i))] = i
			}
			return m
		}())}, true},
		{"oversized payload key", ResumeInput{Payload: map[string]any{strings.Repeat("k", maxResumePayloadKeyBytes+1): "v"}}, true},
		{"oversized payload value", ResumeInput{Payload: map[string]any{"k": strings.Repeat("v", maxResumePayloadValueJSON+1)}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateResumeInput(&tc.resume)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateResumeInput(%s) = %v, wantErr %v", tc.name, err, tc.wantErr)
			}
		})
	}
}
