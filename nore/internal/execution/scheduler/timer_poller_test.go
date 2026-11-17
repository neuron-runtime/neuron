package scheduler

import (
	"context"
	"encoding/hex"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/event"
	executionmodel "github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/storage"
)

func enterDueWait(t *testing.T, h *waitHarness, sched *Scheduler, waitType executionmodel.WaitType, policy executionmodel.WaitDeadlinePolicy, due time.Time) {
	t.Helper()
	spec := executionmodel.WaitSpec{Type: waitType, Reason: "test", WaitUntil: &due, DeadlinePolicy: policy}
	if waitType == executionmodel.WaitTypeSignal {
		spec.ExpectedSignal = "approved"
	}
	if err := sched.EnterWait(h.ctx, WaitEntryRequest{Execution: h.exec, CapabilityID: "cap", Spec: spec}); err != nil {
		t.Fatalf("EnterWait: %v", err)
	}
}

func expectReadyEvent(t *testing.T, sub event.Subscription) event.Event {
	t.Helper()
	select {
	case received := <-sub.Events():
		return received
	case <-time.After(3 * time.Second):
		t.Fatal("CapabilityReady was not published")
		return event.Event{}
	}
}

func expectTerminalEvent(t *testing.T, sub event.Subscription, want event.Type) event.Event {
	t.Helper()
	for i := 0; i < 4; i++ {
		select {
		case received := <-sub.Events():
			if received.Type == want {
				return received
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("event %s was not published", want)
		}
	}
	t.Fatalf("event %s was not published", want)
	return event.Event{}
}

type fencingCaptureStore struct {
	*executionmodel.ExecutionStore
	claimOwner   string
	deleteOwners []string
}

func (s *fencingCaptureStore) ClaimWaitTimer(ctx context.Context, key, owner string, lease time.Duration) (bool, error) {
	claimed, err := s.ExecutionStore.ClaimWaitTimer(ctx, key, owner, lease)
	if claimed {
		s.claimOwner = owner
	}
	return claimed, err
}

func (s *fencingCaptureStore) SaveWaitTransition(ctx context.Context, req executionmodel.WaitTransitionRequest) (*executionmodel.WaitTransitionResult, error) {
	for _, del := range req.Effects.DeleteTimers {
		s.deleteOwners = append(s.deleteOwners, del.Owner)
	}
	return s.ExecutionStore.SaveWaitTransition(ctx, req)
}

// TestPollDueTimersResumesElapsedTimer proves the basic due-timer contract: a
// past-due timer resumes the capability, removes only its own claimed timer, and
// leaves a discoverable dispatch intent after publishing readiness.
func TestPollDueTimersResumesElapsedTimer(t *testing.T) {
	h := newWaitHarness(t)
	capture := &fencingCaptureStore{ExecutionStore: h.store}
	h.waitStore = capture
	sched := h.scheduler(t)
	due := time.Now().UTC().Add(-time.Minute)
	enterDueWait(t, h, sched, executionmodel.WaitTypeTimer, "", due)

	ready, err := h.bus.SubscribeExecution(h.exec.ID, 8)
	if err != nil {
		t.Fatalf("SubscribeExecution: %v", err)
	}
	defer func() { _ = ready.Close() }()

	count, err := sched.PollDueTimers(h.ctx)
	if err != nil || count != 1 {
		t.Fatalf("PollDueTimers = (%d, %v), want (1, nil)", count, err)
	}
	if state := h.exec.CapabilityState("cap"); state.Status != executionmodel.CapabilityReady {
		t.Fatalf("capability status = %s, want ready", state.Status)
	}
	if evt := expectReadyEvent(t, ready); evt.Type != event.CapabilityReady {
		t.Fatalf("published event = %s, want %s", evt.Type, event.CapabilityReady)
	}
	key, err := executionmodel.WaitTimerKey(h.exec.ID, "cap", h.exec.AttemptID)
	if err != nil {
		t.Fatalf("WaitTimerKey: %v", err)
	}
	if _, err := h.raw.GetTimer(h.ctx, key); err == nil {
		t.Fatal("claimed timer was not removed by the committed transition")
	}
	if len(capture.deleteOwners) != 1 || capture.deleteOwners[0] != capture.claimOwner {
		t.Fatalf("fenced delete owners = %v, want the claimed owner %q", capture.deleteOwners, capture.claimOwner)
	}
	raw, err := hex.DecodeString(capture.claimOwner)
	if err != nil || len(raw) != 32 {
		t.Fatalf("owner token %q is not 32 random bytes", capture.claimOwner)
	}
	intent, ok, err := h.store.ClaimDispatchIntent(h.ctx, executionmodel.NewDispatchIntent(h.exec.ID, "cap", h.exec.AttemptID), "probe", time.Minute)
	if err != nil || !ok || intent == nil {
		t.Fatalf("resume intent was not committed: intent=%v ok=%v err=%v", intent, ok, err)
	}
}

func TestPollDueTimersAppliesDeadlinePolicy(t *testing.T) {
	cases := []struct {
		name       string
		policy     executionmodel.WaitDeadlinePolicy
		wantStatus executionmodel.Status
		wantEvent  event.Type
	}{
		{"continue", executionmodel.WaitDeadlinePolicyContinue, executionmodel.StatusRunning, event.CapabilityReady},
		{"fail", executionmodel.WaitDeadlinePolicyFail, executionmodel.StatusFailed, event.ExecutionFailed},
		{"cancel", executionmodel.WaitDeadlinePolicyCancel, executionmodel.StatusCancelled, event.ExecutionCancelled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newWaitHarness(t)
			sched := h.scheduler(t)
			due := time.Now().UTC().Add(-time.Minute)
			enterDueWait(t, h, sched, executionmodel.WaitTypeSignal, tc.policy, due)
			sub, err := h.bus.SubscribeExecution(h.exec.ID, 8)
			if err != nil {
				t.Fatalf("SubscribeExecution: %v", err)
			}
			defer func() { _ = sub.Close() }()

			count, err := sched.PollDueTimers(h.ctx)
			if err != nil || count != 1 {
				t.Fatalf("PollDueTimers = (%d, %v), want (1, nil)", count, err)
			}
			if h.exec.Status() != tc.wantStatus {
				t.Fatalf("execution status = %s, want %s", h.exec.Status(), tc.wantStatus)
			}
			expectTerminalEvent(t, sub, tc.wantEvent)
			if state := h.exec.CapabilityState("cap"); tc.wantStatus != executionmodel.StatusRunning && state.WaitDeadlineReason == "" {
				t.Fatal("deadline policy outcome was not recorded on the capability")
			}
		})
	}
}

func TestPollDueTimersDoesNotWakeFutureOrAbsentDeadlines(t *testing.T) {
	t.Run("signal wait without deadline", func(t *testing.T) {
		h := newWaitHarness(t)
		sched := h.scheduler(t)
		if err := sched.EnterWait(h.ctx, WaitEntryRequest{
			Execution: h.exec, CapabilityID: "cap",
			Spec: executionmodel.WaitSpec{Type: executionmodel.WaitTypeSignal, ExpectedSignal: "approved"},
		}); err != nil {
			t.Fatalf("EnterWait: %v", err)
		}
		count, err := sched.PollDueTimers(h.ctx)
		if err != nil || count != 0 {
			t.Fatalf("PollDueTimers = (%d, %v), want (0, nil)", count, err)
		}
		if state := h.exec.CapabilityState("cap"); state.Status != executionmodel.CapabilityWaiting {
			t.Fatalf("capability status = %s, want waiting", state.Status)
		}
	})

	t.Run("timer due but snapshot deadline in future", func(t *testing.T) {
		h := newWaitHarness(t)
		sched := h.scheduler(t)
		due := time.Now().UTC().Add(-time.Minute)
		enterDueWait(t, h, sched, executionmodel.WaitTypeTimer, "", due)

		snapshot := h.exec.Snapshot()
		future := time.Now().UTC().Add(time.Hour)
		state := snapshot.States["cap"]
		state.WaitUntil = &future
		snapshot.States["cap"] = state
		h.exec.Restore(snapshot)

		count, err := sched.PollDueTimers(h.ctx)
		if err != nil || count != 0 {
			t.Fatalf("PollDueTimers = (%d, %v), want (0, nil)", count, err)
		}
		if state := h.exec.CapabilityState("cap"); state.Status != executionmodel.CapabilityWaiting {
			t.Fatalf("capability status = %s, want waiting", state.Status)
		}
	})
}

type faultWaitStore struct {
	*executionmodel.ExecutionStore
	renewErr error
}

func (s *faultWaitStore) RenewWaitTimer(_ context.Context, _, _ string, _ time.Duration) error {
	return s.renewErr
}

type commitFaultStore struct {
	*executionmodel.ExecutionStore
	err error
}

func (s *commitFaultStore) SaveWaitTransition(_ context.Context, _ executionmodel.WaitTransitionRequest) (*executionmodel.WaitTransitionResult, error) {
	return nil, s.err
}

func TestPollDueTimersAbortsSafelyOnLeaseLoss(t *testing.T) {
	t.Run("renewal reports lease loss", func(t *testing.T) {
		h := newWaitHarness(t)
		sched := h.scheduler(t)
		due := time.Now().UTC().Add(-time.Minute)
		enterDueWait(t, h, sched, executionmodel.WaitTypeTimer, "", due)
		sched.waitStore = &faultWaitStore{ExecutionStore: h.store, renewErr: executionmodel.ErrWaitTimerLeaseLost}

		count, err := sched.PollDueTimers(h.ctx)
		if err != nil || count != 0 {
			t.Fatalf("PollDueTimers = (%d, %v), want (0, nil) after lease loss", count, err)
		}
		if state := h.exec.CapabilityState("cap"); state.Status != executionmodel.CapabilityWaiting {
			t.Fatalf("capability status = %s, want waiting after lease loss", state.Status)
		}
		key, err := executionmodel.WaitTimerKey(h.exec.ID, "cap", h.exec.AttemptID)
		if err != nil {
			t.Fatalf("WaitTimerKey: %v", err)
		}
		if _, err := h.raw.GetTimer(h.ctx, key); err != nil {
			t.Fatalf("timer was removed despite lease loss: %v", err)
		}
	})

	t.Run("fenced commit reports lease loss", func(t *testing.T) {
		h := newWaitHarness(t)
		sched := h.scheduler(t)
		due := time.Now().UTC().Add(-time.Minute)
		enterDueWait(t, h, sched, executionmodel.WaitTypeTimer, "", due)
		sub, err := h.bus.SubscribeExecution(h.exec.ID, 8)
		if err != nil {
			t.Fatalf("SubscribeExecution: %v", err)
		}
		defer func() { _ = sub.Close() }()
		sched.waitStore = &commitFaultStore{ExecutionStore: h.store, err: executionmodel.ErrWaitTimerLeaseLost}

		count, err := sched.PollDueTimers(h.ctx)
		if err != nil || count != 0 {
			t.Fatalf("PollDueTimers = (%d, %v), want (0, nil) after lease loss", count, err)
		}
		if state := h.exec.CapabilityState("cap"); state.Status != executionmodel.CapabilityWaiting {
			t.Fatalf("capability status = %s, want restored waiting state", state.Status)
		}
		select {
		case evt := <-sub.Events():
			t.Fatalf("event %s was published for a transition that did not commit", evt.Type)
		case <-time.After(50 * time.Millisecond):
		}
	})
}

type ownerRecordingStore struct {
	*executionmodel.ExecutionStore
	due      []storage.Timer
	owners   []string
	leases   []time.Duration
	gotLimit int
}

func (s *ownerRecordingStore) DueWaitTimers(_ context.Context, _ time.Time, limit int) ([]storage.Timer, error) {
	s.gotLimit = limit
	return s.due, nil
}

func (s *ownerRecordingStore) ClaimWaitTimer(_ context.Context, _, owner string, lease time.Duration) (bool, error) {
	s.owners = append(s.owners, owner)
	s.leases = append(s.leases, lease)
	return false, nil
}

func TestPollDueTimersUsesFreshOwnersAndBoundedBatch(t *testing.T) {
	h := newWaitHarness(t)
	recorder := &ownerRecordingStore{ExecutionStore: h.store}
	for i := 0; i < 2; i++ {
		data, err := executionmodel.EncodeWaitTimer(executionmodel.WaitTimerPayload{
			ExecutionID: h.exec.ID, CapabilityID: "cap", WaitType: executionmodel.WaitTypeTimer,
		})
		if err != nil {
			t.Fatalf("EncodeWaitTimer: %v", err)
		}
		recorder.due = append(recorder.due, storage.Timer{Key: "fake_" + string(rune('a'+i)), DueAt: time.Now().UTC().Add(-time.Minute), Data: data})
	}
	h.waitStore = recorder
	sched := h.scheduler(t, func(s *Scheduler) { s.pollConfig.BatchSize = 2; s.pollConfig.Lease = 10 * time.Second })

	count, err := sched.PollDueTimers(h.ctx)
	if err != nil || count != 0 {
		t.Fatalf("PollDueTimers = (%d, %v), want (0, nil)", count, err)
	}
	if recorder.gotLimit != 2 {
		t.Fatalf("due query limit = %d, want configured batch 2", recorder.gotLimit)
	}
	if len(recorder.owners) != 2 {
		t.Fatalf("recorded %d claims, want 2", len(recorder.owners))
	}
	if recorder.owners[0] == recorder.owners[1] {
		t.Fatal("consecutive claims reused an owner token")
	}
	for i, owner := range recorder.owners {
		raw, err := hex.DecodeString(owner)
		if err != nil || len(raw) != 32 {
			t.Fatalf("owner %d = %q, want 32 random bytes", i, owner)
		}
		if recorder.leases[i] != 10*time.Second {
			t.Fatalf("lease %d = %s, want 10s", i, recorder.leases[i])
		}
	}
}

func TestPollDueTimersRejectsMalformedPayload(t *testing.T) {
	h := newWaitHarness(t)
	recorder := &ownerRecordingStore{
		ExecutionStore: h.store,
		due:            []storage.Timer{{Key: "bad", DueAt: time.Now().UTC().Add(-time.Minute), Data: []byte("not-json")}},
	}
	h.waitStore = recorder
	sched := h.scheduler(t)
	if _, err := sched.PollDueTimers(h.ctx); err == nil {
		t.Fatal("malformed timer payload was accepted")
	}
	if len(recorder.owners) != 0 {
		t.Fatal("a malformed timer was claimed")
	}
}
