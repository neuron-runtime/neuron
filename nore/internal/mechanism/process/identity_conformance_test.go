package process

import (
	"context"
	"testing"

	v1 "github.com/neuron-runtime/neuron/shared/protocol/capabilityruntime/v1"
	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
)

// TestConformanceIdentityContextValuesReachWireRequest proves the worker maps
// the adapter's plain-string context values onto ExecuteRequest. A capability
// runtime relies on these fields for idempotency, so an empty value here is the
// defect the plugin normalization exists to prevent.
func TestConformanceIdentityContextValuesReachWireRequest(t *testing.T) {
	w, server := newConformanceWorker(t, nil, &v1.ExecuteResponse{})
	ctx := context.WithValue(context.Background(), attemptKey, "attempt-42")
	ctx = context.WithValue(ctx, logicalOperationKey, "logical-7")

	if _, err := w.execute(ctx, &capabilityrt.Request{}); err != nil {
		t.Fatalf("execute: %v", err)
	}

	received := server.received()
	if len(received) != 1 {
		t.Fatalf("runtime received %d requests, want 1", len(received))
	}
	if got := received[0].AttemptId; got != "attempt-42" {
		t.Errorf("ExecuteRequest.AttemptId = %q, want %q", got, "attempt-42")
	}
	if got := received[0].LogicalOperationId; got != "logical-7" {
		t.Errorf("ExecuteRequest.LogicalOperationId = %q, want %q", got, "logical-7")
	}
}

// TestConformanceIdentityContextAbsentYieldsEmptyWireIDs pins the transport's
// failure path: without identity values in the context it forwards empty fields
// instead of inventing an identifier.
func TestConformanceIdentityContextAbsentYieldsEmptyWireIDs(t *testing.T) {
	w, server := newConformanceWorker(t, nil, &v1.ExecuteResponse{})

	if _, err := w.execute(context.Background(), &capabilityrt.Request{}); err != nil {
		t.Fatalf("execute: %v", err)
	}

	received := server.received()
	if len(received) != 1 {
		t.Fatalf("runtime received %d requests, want 1", len(received))
	}
	if got := received[0].AttemptId; got != "" {
		t.Errorf("ExecuteRequest.AttemptId = %q, want empty", got)
	}
	if got := received[0].LogicalOperationId; got != "" {
		t.Errorf("ExecuteRequest.LogicalOperationId = %q, want empty", got)
	}
}
