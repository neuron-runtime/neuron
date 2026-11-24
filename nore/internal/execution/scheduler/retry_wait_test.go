package scheduler

import (
	"errors"
	"testing"
	"time"

	executionmodel "github.com/neuron-runtime/neuron/nore/internal/execution"
	core "github.com/neuron-runtime/neuron/shared/types/core"
)

// TestBeginRetryWaitCommitsTimerAndState proves that a deferred retry is one
// durable transition: the retry state and its wake-up timer are committed
// together, and the timer payload identifies the same capability and attempt.
func TestBeginRetryWaitCommitsTimerAndState(t *testing.T) {
	h := newWaitHarness(t)
	sched := h.scheduler(t)
	next := time.Now().UTC().Add(time.Hour)

	if err := sched.BeginRetryWait(h.ctx, RetryWaitRequest{
		Execution:     h.exec,
		CapabilityID:  "cap",
		NextAttemptAt: next,
	}); err != nil {
		t.Fatalf("BeginRetryWait: %v", err)
	}

	state := h.exec.CapabilityState("cap")
	if state.Status != executionmodel.CapabilityRetryWait {
		t.Fatalf("capability status = %s, want %s", state.Status, executionmodel.CapabilityRetryWait)
	}
	if state.NextAttemptAt == nil || !state.NextAttemptAt.Equal(next) {
		t.Fatalf("NextAttemptAt = %v, want %v", state.NextAttemptAt, next)
	}

	stored, err := h.store.Get(h.ctx, h.exec.ID)
	if err != nil {
		t.Fatalf("load persisted execution: %v", err)
	}
	storedState := stored.CapabilityState("cap")
	if storedState.Status != executionmodel.CapabilityRetryWait || storedState.NextAttemptAt == nil || !storedState.NextAttemptAt.Equal(next) {
		t.Fatalf("persisted retry state = %+v, want retry wait at %v", storedState, next)
	}

	key, err := executionmodel.WaitTimerKey(h.exec.ID, "cap", h.exec.AttemptID)
	if err != nil {
		t.Fatalf("WaitTimerKey: %v", err)
	}
	data, err := h.raw.GetTimer(h.ctx, key)
	if err != nil {
		t.Fatalf("retry wake-up timer was not committed: %v", err)
	}
	payload, err := executionmodel.DecodeWaitTimer(data)
	if err != nil {
		t.Fatalf("DecodeWaitTimer: %v", err)
	}
	if payload.ExecutionID != h.exec.ID || payload.CapabilityID != core.ID("cap") || payload.AttemptID != h.exec.AttemptID {
		t.Fatalf("timer payload = %+v, want execution %s / cap / %s", payload, h.exec.ID, h.exec.AttemptID)
	}
	if payload.WaitType != executionmodel.WaitTypeTimer {
		t.Fatalf("timer wait type = %s, want %s", payload.WaitType, executionmodel.WaitTypeTimer)
	}
}

// TestBeginRetryWaitFailsClosedWithoutWaitStore ensures a host without durable
// waits never records a retry it cannot later resume.
func TestBeginRetryWaitFailsClosedWithoutWaitStore(t *testing.T) {
	h := newWaitHarness(t)
	sched := h.scheduler(t)
	sched.waitStore = nil
	next := time.Now().UTC().Add(time.Hour)

	err := sched.BeginRetryWait(h.ctx, RetryWaitRequest{
		Execution:     h.exec,
		CapabilityID:  "cap",
		NextAttemptAt: next,
	})
	if !errors.Is(err, ErrWaitStoreUnavailable) {
		t.Fatalf("BeginRetryWait error = %v, want ErrWaitStoreUnavailable", err)
	}
	if state := h.exec.CapabilityState("cap"); state.Status == executionmodel.CapabilityRetryWait {
		t.Fatal("capability entered retry wait without a wait store")
	}
	key, err := executionmodel.WaitTimerKey(h.exec.ID, "cap", h.exec.AttemptID)
	if err != nil {
		t.Fatalf("WaitTimerKey: %v", err)
	}
	if _, err := h.raw.GetTimer(h.ctx, key); err == nil {
		t.Fatal("retry timer was written without a wait store")
	}
}

func TestBeginRetryWaitValidatesRequest(t *testing.T) {
	h := newWaitHarness(t)
	sched := h.scheduler(t)

	cases := []struct {
		name string
		req  RetryWaitRequest
	}{
		{"nil execution", RetryWaitRequest{CapabilityID: "cap", NextAttemptAt: time.Now().UTC().Add(time.Hour)}},
		{"empty capability", RetryWaitRequest{Execution: h.exec, NextAttemptAt: time.Now().UTC().Add(time.Hour)}},
		{"zero next attempt", RetryWaitRequest{Execution: h.exec, CapabilityID: "cap"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := sched.BeginRetryWait(h.ctx, tc.req); err == nil {
				t.Fatal("BeginRetryWait error = nil, want validation failure")
			}
		})
	}
}
