package execution

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/storage"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

func TestResolveWaitUsesDeliveredPayloadAsParams(t *testing.T) {
	capabilityID := shared.ID("cap_a")
	exec := newWaitExecution(t, capabilityID)
	makeRunning(t, exec, capabilityID)
	if err := exec.EnterWait(capabilityID, signalWaitSpec(nil, WaitDeadlinePolicyFail)); err != nil {
		t.Fatalf("EnterWait: %v", err)
	}
	if err := exec.ResolveWait(capabilityID, map[string]any{"approved": true, "nested": map[string]any{"key": "value"}}); err != nil {
		t.Fatalf("ResolveWait: %v", err)
	}
	params := exec.Params(capabilityID)
	if params["approved"] != true {
		t.Fatalf("resume params = %v, want the delivered payload", params)
	}
	nested, ok := params["nested"].(map[string]any)
	if !ok || nested["key"] != "value" {
		t.Fatalf("nested resume param = %v, want the delivered nested payload", params["nested"])
	}
	state := exec.CapabilityState(capabilityID)
	if state.DeliveredPayload["approved"] != true {
		t.Fatalf("delivered payload = %v, want the delivered payload", state.DeliveredPayload)
	}
}

// TestSaveWaitTransitionRefusesToOverwriteLiveTimer protects the fencing
// invariant at the store boundary: a timer under a live claim cannot be replaced
// by a later transition, and the failed transition must leave durable state and
// the timer bytes untouched.
func TestSaveWaitTransitionRefusesToOverwriteLiveTimer(t *testing.T) {
	ctx := context.Background()
	capabilityID := shared.ID("cap_a")
	attemptID := shared.NewID("attempt_")
	store, exec, raw := newStoredWaitExecution(t, capabilityID)
	makeRunning(t, exec, capabilityID)

	due := time.Now().UTC().Add(-time.Minute)
	data, err := EncodeWaitTimer(WaitTimerPayload{
		ExecutionID: exec.ID, CapabilityID: capabilityID, WaitType: WaitTypeTimer, AttemptID: attemptID,
	})
	if err != nil {
		t.Fatalf("EncodeWaitTimer: %v", err)
	}
	key, write := waitTimerFixture(t, exec, capabilityID, attemptID, due, data)
	if err := exec.EnterWait(capabilityID, signalWaitSpec(&due, WaitDeadlinePolicyFail)); err != nil {
		t.Fatalf("EnterWait: %v", err)
	}
	if _, err := store.SaveWaitTransition(ctx, WaitTransitionRequest{
		Execution: exec,
		Effects:   WaitEffects{PutTimers: []WaitTimerWrite{write}},
	}); err != nil {
		t.Fatalf("initial SaveWaitTransition: %v", err)
	}
	before, err := raw.GetTimer(ctx, key)
	if err != nil {
		t.Fatalf("GetTimer: %v", err)
	}
	claimed, err := store.ClaimWaitTimer(ctx, key, "owner_1", 30*time.Second)
	if err != nil || !claimed {
		t.Fatalf("ClaimWaitTimer = (%v, %v), want a successful claim", claimed, err)
	}

	if err := exec.ResolveWait(capabilityID, nil); err != nil {
		t.Fatalf("ResolveWait: %v", err)
	}
	if _, err := store.SaveWaitTransition(ctx, WaitTransitionRequest{
		Execution: exec,
		Effects:   WaitEffects{PutTimers: []WaitTimerWrite{write}},
	}); !errors.Is(err, storage.ErrTimerLeased) {
		t.Fatalf("second SaveWaitTransition error = %v, want %v", err, storage.ErrTimerLeased)
	}
	after, err := raw.GetTimer(ctx, key)
	if err != nil || string(after) != string(before) {
		t.Fatalf("timer after failed commit = %q, %v; want unchanged %q", after, err, before)
	}
	if revision := durableRevision(t, store, exec.ID); revision != 2 {
		t.Fatalf("durable revision after failed commit = %d, want 2", revision)
	}
}
