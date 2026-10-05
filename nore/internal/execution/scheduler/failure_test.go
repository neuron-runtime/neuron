package scheduler

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/event"
	executionmodel "github.com/neuron-runtime/neuron/nore/internal/execution"
)

// TestPointerCapabilityFailedPayloadKeepsItsMessage is a regression test for a
// defect that hid the reason a capability failed. The scheduler asserted the
// payload to a single type, so a publisher that sent the pointer form lost the
// message and the execution failed with generic text instead. Whoever reads the
// run is then told nothing about what actually went wrong.
func TestPointerCapabilityFailedPayloadKeepsItsMessage(t *testing.T) {
	cases := []struct {
		name    string
		payload any
		want    string
	}{
		{
			name:    "value payload",
			payload: event.CapabilityFailedPayload{Message: "connection refused by runtime"},
			want:    "connection refused by runtime",
		},
		{
			name:    "pointer payload",
			payload: &event.CapabilityFailedPayload{Message: "connection refused by runtime"},
			want:    "connection refused by runtime",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			bus := event.NewBus()
			store := executionmodel.NewMemoryStore()
			scopes := executionmodel.NewScopeRegistry(ctx)

			scheduler, err := New(bus, store, scopes)
			if err != nil {
				t.Fatalf("New scheduler: %v", err)
			}
			go func() { _ = scheduler.Run(ctx) }()

			exec := startExecution(t, ctx, bus, store, singleCapabilityBlueprint("cap_a"))

			subscription := subscribeToExecution(t, bus, exec.ID)
			defer func() { _ = subscription.Close() }()

			if err := bus.Publish(ctx, event.New(event.CapabilityFailed, exec.ID, exec.CorrelationID, "cap_a", tc.payload)); err != nil {
				t.Fatalf("publish CapabilityFailed: %v", err)
			}

			reason := awaitExecutionFailure(t, subscription)
			if !strings.Contains(reason, tc.want) {
				t.Errorf("execution failed with %q, want it to contain %q", reason, tc.want)
			}
		})
	}
}

// awaitExecutionFailure reads events until the execution reports a failure and
// returns the reason it gave.
func awaitExecutionFailure(t *testing.T, subscription event.Subscription) string {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case received, open := <-subscription.Events():
			if !open {
				t.Fatal("subscription closed before the execution failed")
			}
			if received.Type != event.ExecutionFailed {
				continue
			}
			payload, ok := received.Payload.(event.ExecutionFailedPayload)
			if !ok {
				t.Fatalf("ExecutionFailed carried %T, want event.ExecutionFailedPayload", received.Payload)
			}
			return payload.Message
		case <-deadline:
			t.Fatal("the execution never reported a failure")
			return ""
		}
	}
}
