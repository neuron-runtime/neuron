package plugin

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
	executionmodel "github.com/neuron-runtime/neuron/nore/internal/execution"
	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
)

// instanceAdapter.Execute is the production boundary between a hosted capability
// runtime and the engine. These tests drive a Response through that boundary so a
// dropped suspend is observable as an empty successful outcome.

// TestInstanceAdapterExecuteSuspendOutcome proves a negotiated suspend response
// becomes a typed CapabilityOutcome.Suspend rather than an empty success. It
// covers both wait kinds and the deadline conversion at the adapter boundary.
func TestInstanceAdapterExecuteSuspendOutcome(t *testing.T) {
	const deadlineMs int64 = 1_760_000_000_123
	for _, tc := range []struct {
		name     string
		wire     *capabilityrt.Suspend
		wantType executionmodel.WaitType
		wantWait *time.Time
	}{
		{
			name: "signal",
			wire: &capabilityrt.Suspend{
				CorrelationKey: "corr-approval",
				WaitType:       capabilityrt.WaitTypeSignal,
				ExpectedSignal: "approval",
				PayloadSchema:  "approval/v1",
			},
			wantType: executionmodel.WaitTypeSignal,
		},
		{
			name: "timer",
			wire: &capabilityrt.Suspend{
				CorrelationKey: "corr-timer",
				WaitType:       capabilityrt.WaitTypeTimer,
				DeadlineUnixMs: deadlineMs,
			},
			wantType: executionmodel.WaitTypeTimer,
			wantWait: func() *time.Time { v := time.UnixMilli(deadlineMs).UTC(); return &v }(),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			instance := &recordingInstance{response: &capabilityrt.Response{
				Result:  map[string]any{},
				Suspend: tc.wire,
			}}
			adapter := &instanceAdapter{instance: instance, typ: "acme:approval"}

			outcome, err := adapter.Execute(context.Background(), contracts.ExecutionContext{
				Params: map[string]any{"x": "y"},
			})
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if outcome.Suspend == nil {
				t.Fatalf("suspend outcome was dropped: %+v", outcome)
			}
			if len(outcome.Result) != 0 {
				t.Fatalf("suspend outcome carried a result: %+v", outcome.Result)
			}

			got := outcome.Suspend
			if got.CorrelationKey != tc.wire.CorrelationKey ||
				got.WaitType != tc.wantType ||
				got.ExpectedSignal != tc.wire.ExpectedSignal ||
				got.PayloadSchema != tc.wire.PayloadSchema {
				t.Errorf("suspend = %+v, want fields from %+v", got, tc.wire)
			}
			if tc.wantWait == nil {
				if got.WaitUntil != nil {
					t.Errorf("WaitUntil = %v, want nil", got.WaitUntil)
				}
			} else if got.WaitUntil == nil || !got.WaitUntil.Equal(*tc.wantWait) {
				t.Errorf("WaitUntil = %v, want %v", got.WaitUntil, *tc.wantWait)
			}
		})
	}
}

// TestInstanceAdapterExecuteNoSuspendOutcomeUnchanged pins that an ordinary
// response still yields only its result.
func TestInstanceAdapterExecuteNoSuspendOutcomeUnchanged(t *testing.T) {
	result := map[string]any{"value": "hello"}
	instance := &recordingInstance{response: &capabilityrt.Response{Result: result}}
	adapter := &instanceAdapter{instance: instance, typ: "acme:echo"}

	outcome, err := adapter.Execute(context.Background(), contracts.ExecutionContext{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if outcome.Suspend != nil {
		t.Fatalf("unexpected suspend outcome: %+v", outcome.Suspend)
	}
	if outcome.Result["value"] != "hello" {
		t.Fatalf("result = %+v, want value=hello", outcome.Result)
	}
}

// TestInstanceAdapterExecuteErrorUnchanged pins that a controlled error remains
// an explicit error at the boundary.
func TestInstanceAdapterExecuteErrorOutcomeUnchanged(t *testing.T) {
	instance := &recordingInstance{response: &capabilityrt.Response{Error: "boom"}}
	adapter := &instanceAdapter{instance: instance, typ: "acme:echo"}

	_, err := adapter.Execute(context.Background(), contracts.ExecutionContext{})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error = %v, want controlled error containing boom", err)
	}
}

// TestInstanceAdapterExecuteRejectsMalformedSuspendOutcome proves an invalid
// suspend never degrades into an empty success at the adapter boundary.
func TestInstanceAdapterExecuteRejectsMalformedSuspendOutcome(t *testing.T) {
	instance := &recordingInstance{response: &capabilityrt.Response{
		Suspend: &capabilityrt.Suspend{WaitType: capabilityrt.WaitTypeSignal},
	}}
	adapter := &instanceAdapter{instance: instance, typ: "acme:approval"}

	_, err := adapter.Execute(context.Background(), contracts.ExecutionContext{})
	if err == nil || !strings.Contains(err.Error(), "invalid_suspend_outcome") {
		t.Fatalf("error = %v, want explicit invalid_suspend_outcome rejection", err)
	}
}
