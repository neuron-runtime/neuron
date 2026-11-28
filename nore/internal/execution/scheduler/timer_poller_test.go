package scheduler

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/event"
	executionmodel "github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/storage"
	core "github.com/neuron-runtime/neuron/shared/types/core"
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

// indexRawWaitTimer publishes a directly written timer through the instance's
// due index. Tests that inject stale timers must write both records because the
// instance-scoped poller discovers by index, not by scanning the timer space.
func indexRawWaitTimer(t *testing.T, h *waitHarness, timerKey string, due time.Time) {
	t.Helper()
	indexKey, err := storage.WaitDueKey(string(h.exec.InstanceID), timerKey)
	if err != nil {
		t.Fatalf("WaitDueKey: %v", err)
	}
	lookupKey, err := storage.WaitDueLookupKey(timerKey)
	if err != nil {
		t.Fatalf("WaitDueLookupKey: %v", err)
	}
	if err := h.raw.Put(h.ctx, indexKey, []byte(due.UTC().Format(time.RFC3339Nano))); err != nil {
		t.Fatalf("Put wait-due index: %v", err)
	}
	if err := h.raw.Put(h.ctx, lookupKey, []byte(indexKey)); err != nil {
		t.Fatalf("Put wait-due lookup: %v", err)
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

	count, err := sched.PollDueTimers(h.ctx, h.exec.InstanceID)
	if err != nil || count != 1 {
		t.Fatalf("PollDueTimers = (%d, %v), want (1, nil)", count, err)
	}
	if state := h.exec.CapabilityState("cap"); state.Status != executionmodel.CapabilityReady {
		t.Fatalf("capability status = %s, want ready", state.Status)
	}
	if got := h.exec.CapabilityState("cap").Resume; got == nil || got.Cause != executionmodel.ResumeCauseTimer {
		t.Fatalf("resume = %+v, want a timer resume descriptor", got)
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

// TestPollDueTimersIgnoresStalePayloadAttemptForIntent proves a due timer whose
// payload still names an older attempt is deleted under its own key, while the
// resumed dispatch is committed only under the rotated execution generation.
func TestPollDueTimersIgnoresStalePayloadAttemptForIntent(t *testing.T) {
	h := newWaitHarness(t)
	sched := h.scheduler(t)
	due := time.Now().UTC().Add(-time.Minute)
	prior := h.exec.AttemptID
	if err := h.exec.BeginRetryWait("cap", due); err != nil {
		t.Fatalf("BeginRetryWait: %v", err)
	}
	key, err := executionmodel.WaitTimerKey(h.exec.ID, "cap", prior)
	if err != nil {
		t.Fatalf("WaitTimerKey: %v", err)
	}
	data, err := executionmodel.EncodeWaitTimer(executionmodel.WaitTimerPayload{
		ExecutionID: h.exec.ID, CapabilityID: "cap", WaitType: executionmodel.WaitTypeTimer,
		AttemptID: "attempt_stale",
	})
	if err != nil {
		t.Fatalf("EncodeWaitTimer: %v", err)
	}
	if err := h.raw.PutTimer(h.ctx, key, due, data); err != nil {
		t.Fatalf("PutTimer: %v", err)
	}
	indexRawWaitTimer(t, h, key, due)

	count, err := sched.PollDueTimers(h.ctx, h.exec.InstanceID)
	if err != nil || count != 1 {
		t.Fatalf("PollDueTimers = (%d, %v), want (1, nil)", count, err)
	}
	if h.exec.AttemptID == prior {
		t.Fatal("retry resumption did not rotate the attempt ID")
	}
	if _, err := h.raw.GetTimer(h.ctx, key); err == nil {
		t.Fatal("claimed timer was not removed under its own key")
	}
	intent, ok, err := h.store.ClaimDispatchIntent(h.ctx, executionmodel.NewDispatchIntent(h.exec.ID, "cap", h.exec.AttemptID), "probe", time.Minute)
	if err != nil || !ok || intent == nil {
		t.Fatalf("post-rotation intent not claimable: intent=%v ok=%v err=%v", intent, ok, err)
	}
	stale, ok, err := h.store.ClaimDispatchIntent(h.ctx, executionmodel.NewDispatchIntent(h.exec.ID, "cap", "attempt_stale"), "probe", time.Minute)
	if err != nil || ok {
		t.Fatalf("stale-payload intent remained claimable: intent=%v ok=%v err=%v", stale, ok, err)
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

			count, err := sched.PollDueTimers(h.ctx, h.exec.InstanceID)
			if err != nil || count != 1 {
				t.Fatalf("PollDueTimers = (%d, %v), want (1, nil)", count, err)
			}
			if h.exec.Status() != tc.wantStatus {
				t.Fatalf("execution status = %s, want %s", h.exec.Status(), tc.wantStatus)
			}
			expectTerminalEvent(t, sub, tc.wantEvent)
			state := h.exec.CapabilityState("cap")
			if tc.wantStatus != executionmodel.StatusRunning && state.WaitDeadlineReason == "" {
				t.Fatal("deadline policy outcome was not recorded on the capability")
			}
			if tc.wantStatus == executionmodel.StatusRunning {
				if state.Resume == nil || state.Resume.Cause != executionmodel.ResumeCauseDeadline {
					t.Fatalf("resume = %+v, want a deadline resume descriptor", state.Resume)
				}
			} else if state.Resume != nil {
				t.Fatalf("resume = %+v, want nil for a terminal deadline policy", state.Resume)
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
		count, err := sched.PollDueTimers(h.ctx, h.exec.InstanceID)
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

		count, err := sched.PollDueTimers(h.ctx, h.exec.InstanceID)
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

		count, err := sched.PollDueTimers(h.ctx, h.exec.InstanceID)
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

		count, err := sched.PollDueTimers(h.ctx, h.exec.InstanceID)
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
	due           []storage.Timer
	owners        []string
	leases        []time.Duration
	gotLimit      int
	gotInstanceID core.ID
}

func (s *ownerRecordingStore) DueWaitTimersForInstance(_ context.Context, instanceID core.ID, _ time.Time, limit int) ([]storage.Timer, error) {
	s.gotInstanceID = instanceID
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

	count, err := sched.PollDueTimers(h.ctx, h.exec.InstanceID)
	if err != nil || count != 0 {
		t.Fatalf("PollDueTimers = (%d, %v), want (0, nil)", count, err)
	}
	if recorder.gotLimit != 2 {
		t.Fatalf("due query limit = %d, want configured batch 2", recorder.gotLimit)
	}
	if recorder.gotInstanceID != h.exec.InstanceID {
		t.Fatalf("due query instance = %q, want %q", recorder.gotInstanceID, h.exec.InstanceID)
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
	if _, err := sched.PollDueTimers(h.ctx, h.exec.InstanceID); err == nil {
		t.Fatal("malformed timer payload was accepted")
	}
	if len(recorder.owners) != 0 {
		t.Fatal("a malformed timer was claimed")
	}
}

// A due timer whose execution no longer exists can never resume anything. The
// poller must discard it under its claim fence instead of leaving a timer it
// will reclaim on every pass.
func TestPollDueTimersDiscardsTimerForMissingExecution(t *testing.T) {
	h := newWaitHarness(t)
	sched := h.scheduler(t)
	missingID := core.ID("exec_missing")
	due := time.Now().UTC().Add(-time.Minute)
	key, err := executionmodel.WaitTimerKey(missingID, "cap", "attempt_1")
	if err != nil {
		t.Fatalf("WaitTimerKey: %v", err)
	}
	data, err := executionmodel.EncodeWaitTimer(executionmodel.WaitTimerPayload{
		ExecutionID: missingID, CapabilityID: "cap", WaitType: executionmodel.WaitTypeTimer,
	})
	if err != nil {
		t.Fatalf("EncodeWaitTimer: %v", err)
	}
	if err := h.raw.PutTimer(h.ctx, key, due, data); err != nil {
		t.Fatalf("PutTimer: %v", err)
	}
	indexRawWaitTimer(t, h, key, due)

	count, err := sched.PollDueTimers(h.ctx, h.exec.InstanceID)
	if err != nil || count != 0 {
		t.Fatalf("PollDueTimers = (%d, %v), want (0, nil)", count, err)
	}
	if _, err := h.raw.GetTimer(h.ctx, key); err == nil {
		t.Fatal("timer for a missing execution survived the poll")
	}
}

// A terminal execution's timers belong to its own terminal transition. One that
// is still claimable is stale and must be cleaned up without a second snapshot.
func TestPollDueTimersDiscardsTimerForTerminalExecution(t *testing.T) {
	h := newWaitHarness(t)
	sched := h.scheduler(t)
	if !h.exec.MarkCancelled(errors.New("operator stop")) {
		t.Fatal("MarkCancelled = false, want true")
	}
	due := time.Now().UTC().Add(-time.Minute)
	key := putWaitTimer(t, h, "cap", string(h.exec.AttemptID), due)
	indexRawWaitTimer(t, h, key, due)

	count, err := sched.PollDueTimers(h.ctx, h.exec.InstanceID)
	if err != nil || count != 0 {
		t.Fatalf("PollDueTimers = (%d, %v), want (0, nil)", count, err)
	}
	if _, err := h.raw.GetTimer(h.ctx, key); err == nil {
		t.Fatal("timer for a terminal execution survived the poll")
	}
}

// A timer is only useful while its capability is waiting. Once the wait has
// resolved, a leftover timer must be removed rather than reclaimed forever.
func TestPollDueTimersDiscardsTimerForCapabilityNoLongerWaiting(t *testing.T) {
	h := newWaitHarness(t)
	sched := h.scheduler(t)
	if err := sched.EnterWait(h.ctx, WaitEntryRequest{
		Execution: h.exec, CapabilityID: "cap",
		Spec: executionmodel.WaitSpec{Type: executionmodel.WaitTypeSignal, ExpectedSignal: "approved"},
	}); err != nil {
		t.Fatalf("EnterWait: %v", err)
	}
	if _, err := sched.ResolveSignal(h.ctx, signalRequest(h.exec, "del_1", map[string]any{"ok": true})); err != nil {
		t.Fatalf("ResolveSignal: %v", err)
	}
	if state := h.exec.CapabilityState("cap"); state.Status != executionmodel.CapabilityReady {
		t.Fatalf("test setup: capability status = %s, want ready", state.Status)
	}

	// A leftover timer from a superseded attempt of the now-resolved capability.
	due := time.Now().UTC().Add(-time.Minute)
	key := putWaitTimer(t, h, "cap", "stale_attempt", due)
	indexRawWaitTimer(t, h, key, due)

	count, err := sched.PollDueTimers(h.ctx, h.exec.InstanceID)
	if err != nil || count != 0 {
		t.Fatalf("PollDueTimers = (%d, %v), want (0, nil)", count, err)
	}
	if _, err := h.raw.GetTimer(h.ctx, key); err == nil {
		t.Fatal("timer for a no-longer-waiting capability survived the poll")
	}
}

// TestPollDueTimersLeavesRetryWaitTimerIntact proves a due timer for a
// capability in CapabilityRetryWait is not consumed or deleted by the poller;
// retry resumption is handled elsewhere and the timer must remain claimable.
func TestPollDueTimersLeavesRetryWaitTimerIntact(t *testing.T) {
	h := newWaitHarness(t)
	sched := h.scheduler(t)

	due := time.Now().UTC().Add(-time.Minute)
	if err := h.exec.BeginRetryWait("cap", due.Add(5*time.Minute)); err != nil {
		t.Fatalf("BeginRetryWait: %v", err)
	}
	key := putWaitTimer(t, h, "cap", string(h.exec.AttemptID), due)
	indexRawWaitTimer(t, h, key, due)

	count, err := sched.PollDueTimers(h.ctx, h.exec.InstanceID)
	if err != nil {
		t.Fatalf("PollDueTimers = (%d, %v), want (0, nil error)", count, err)
	}
	if count != 0 {
		t.Fatalf("PollDueTimers processed = %d, want 0", count)
	}
	if _, err := h.raw.GetTimer(h.ctx, key); err != nil {
		t.Fatalf("retry-wait timer was removed: %v", err)
	}
	if state := h.exec.CapabilityState("cap"); state.Status != executionmodel.CapabilityRetryWait {
		t.Fatalf("capability status = %s, want %s", state.Status, executionmodel.CapabilityRetryWait)
	}
}

// staleCleanupLeaseLossStore models losing the claim between the poller's claim
// and the fenced stale-timer delete: another owner now holds the timer, so the
// delete reports no removal without an error.
type staleCleanupLeaseLossStore struct {
	*executionmodel.ExecutionStore
	deletes int
}

func (s *staleCleanupLeaseLossStore) DeleteWaitTimerOwned(_ context.Context, _, _ string) (bool, error) {
	s.deletes++
	return false, nil
}

// Losing the fence during stale cleanup is a safe abort: the new owner reaches
// its own decision about the timer. It is not a poll error and must not mutate
// the execution.
func TestPollDueTimersStaleCleanupLeaseLossIsSafeNoOp(t *testing.T) {
	h := newWaitHarness(t)
	loss := &staleCleanupLeaseLossStore{ExecutionStore: h.store}
	h.waitStore = loss
	sched := h.scheduler(t)

	// A capability that is not waiting makes the timer stale, so cleanup reaches
	// the fenced delete.
	due := time.Now().UTC().Add(-time.Minute)
	key := putWaitTimer(t, h, "cap", "stale_attempt", due)
	indexRawWaitTimer(t, h, key, due)

	count, err := sched.PollDueTimers(h.ctx, h.exec.InstanceID)
	if err != nil || count != 0 {
		t.Fatalf("PollDueTimers = (%d, %v), want (0, nil) after lease loss", count, err)
	}
	if loss.deletes != 1 {
		t.Fatalf("fenced deletes = %d, want 1", loss.deletes)
	}
	if state := h.exec.CapabilityState("cap"); state.Status != executionmodel.CapabilityRunning {
		t.Fatalf("capability status = %s, want unchanged running", state.Status)
	}
}

// A due timer that conflicts with a future snapshot deadline may be the only
// timer the wait still has. The poller must leave it intact rather than strip
// the wait of its deadline.
func TestPollDueTimersKeepsTimerForFutureSnapshotDeadline(t *testing.T) {
	h := newWaitHarness(t)
	sched := h.scheduler(t)
	due := time.Now().UTC().Add(-time.Minute)
	enterDueWait(t, h, sched, executionmodel.WaitTypeTimer, "", due)
	key, err := executionmodel.WaitTimerKey(h.exec.ID, "cap", h.exec.AttemptID)
	if err != nil {
		t.Fatalf("WaitTimerKey: %v", err)
	}

	snapshot := h.exec.Snapshot()
	future := time.Now().UTC().Add(time.Hour)
	state := snapshot.States["cap"]
	state.WaitUntil = &future
	snapshot.States["cap"] = state
	h.exec.Restore(snapshot)

	count, err := sched.PollDueTimers(h.ctx, h.exec.InstanceID)
	if err != nil || count != 0 {
		t.Fatalf("PollDueTimers = (%d, %v), want (0, nil)", count, err)
	}
	if _, err := h.raw.GetTimer(h.ctx, key); err != nil {
		t.Fatalf("timer required by a future snapshot deadline was removed: %v", err)
	}
	if state := h.exec.CapabilityState("cap"); state.Status != executionmodel.CapabilityWaiting {
		t.Fatalf("capability status = %s, want waiting", state.Status)
	}
}

// TestPollDueTimersDiscardsStaleTimerWhenWaitUntilNil proves a due timer whose
// claimed execution snapshot has WaitUntil == nil is fenced-deleted and does not
// resume the execution.
func TestPollDueTimersDiscardsStaleTimerWhenWaitUntilNil(t *testing.T) {
	h := newWaitHarness(t)
	sched := h.scheduler(t)
	due := time.Now().UTC().Add(-time.Minute)
	enterDueWait(t, h, sched, executionmodel.WaitTypeTimer, "", due)
	key, err := executionmodel.WaitTimerKey(h.exec.ID, "cap", h.exec.AttemptID)
	if err != nil {
		t.Fatalf("WaitTimerKey: %v", err)
	}

	snapshot := h.exec.Snapshot()
	state := snapshot.States["cap"]
	if state.Status != executionmodel.CapabilityWaiting {
		t.Fatalf("test setup: capability status = %s, want waiting", state.Status)
	}
	state.WaitUntil = nil
	snapshot.States["cap"] = state
	h.exec.Restore(snapshot)

	count, err := sched.PollDueTimers(h.ctx, h.exec.InstanceID)
	if err != nil || count != 0 {
		t.Fatalf("PollDueTimers = (%d, %v), want (0, nil)", count, err)
	}
	if state := h.exec.CapabilityState("cap"); state.Status != executionmodel.CapabilityWaiting {
		t.Fatalf("capability status = %s, want waiting (not resumed)", state.Status)
	}
	if _, err := h.raw.GetTimer(h.ctx, key); err == nil {
		t.Fatal("stale claimed timer with nil WaitUntil survived poll")
	}
}

// retryCaptureStore records the fencing and commit order for a retry wake-up
// in one place, so a single scheduler poll can be checked end to end.
type retryCaptureStore struct {
	*executionmodel.ExecutionStore
	log          *orderLog
	claimOwner   string
	deleteOwners []string
}

func (s *retryCaptureStore) ClaimWaitTimer(ctx context.Context, key, owner string, lease time.Duration) (bool, error) {
	claimed, err := s.ExecutionStore.ClaimWaitTimer(ctx, key, owner, lease)
	if claimed {
		s.claimOwner = owner
	}
	return claimed, err
}

func (s *retryCaptureStore) SaveWaitTransition(ctx context.Context, req executionmodel.WaitTransitionRequest) (*executionmodel.WaitTransitionResult, error) {
	s.log.add("commit")
	for _, del := range req.Effects.DeleteTimers {
		s.deleteOwners = append(s.deleteOwners, del.Owner)
	}
	return s.ExecutionStore.SaveWaitTransition(ctx, req)
}

// TestPollDueTimersResumesElapsedRetryWait proves live retry resumption: an
// elapsed NextAttemptAt becomes ready exactly once, its claimed timer is deleted
// under the same fence, a fresh dispatch intent is committed, and readiness is
// published only after the commit.
func TestPollDueTimersResumesElapsedRetryWait(t *testing.T) {
	h := newWaitHarness(t)
	log := &orderLog{}
	capture := &retryCaptureStore{ExecutionStore: h.store, log: log}
	h.waitStore = capture
	h.eventBus = &recordingBus{Bus: h.bus, log: log}
	sched := h.scheduler(t)
	next := time.Now().UTC().Add(-time.Minute)
	if err := sched.BeginRetryWait(h.ctx, RetryWaitRequest{
		Execution: h.exec, CapabilityID: "cap", NextAttemptAt: next,
	}); err != nil {
		t.Fatalf("BeginRetryWait: %v", err)
	}
	log.reset()

	ready, err := h.bus.SubscribeExecution(h.exec.ID, 8)
	if err != nil {
		t.Fatalf("SubscribeExecution: %v", err)
	}
	defer func() { _ = ready.Close() }()

	count, err := sched.PollDueTimers(h.ctx, h.exec.InstanceID)
	if err != nil || count != 1 {
		t.Fatalf("PollDueTimers = (%d, %v), want (1, nil)", count, err)
	}
	state := h.exec.CapabilityState("cap")
	if state.Status != executionmodel.CapabilityReady || state.NextAttemptAt != nil {
		t.Fatalf("retry state = %+v, want ready with NextAttemptAt cleared", state)
	}
	if state.Resume == nil || state.Resume.Cause != executionmodel.ResumeCauseTimer {
		t.Fatalf("resume = %+v, want a timer resume descriptor", state.Resume)
	}
	if evt := expectReadyEvent(t, ready); evt.Type != event.CapabilityReady {
		t.Fatalf("published event = %s, want %s", evt.Type, event.CapabilityReady)
	}

	key, err := executionmodel.WaitTimerKey(h.exec.ID, "cap", h.exec.AttemptID)
	if err != nil {
		t.Fatalf("WaitTimerKey: %v", err)
	}
	if _, err := h.raw.GetTimer(h.ctx, key); err == nil {
		t.Fatal("claimed retry timer was not removed by the committed transition")
	}
	if len(capture.deleteOwners) != 1 || capture.deleteOwners[0] != capture.claimOwner {
		t.Fatalf("fenced delete owners = %v, want the claimed owner %q", capture.deleteOwners, capture.claimOwner)
	}

	items := log.snapshot()
	if len(items) != 2 || items[0] != "commit" || !strings.HasPrefix(items[1], "publish:") {
		t.Fatalf("commit/publication order = %v, want commit then publish", items)
	}

	intent, ok, err := h.store.ClaimDispatchIntent(h.ctx, executionmodel.NewDispatchIntent(h.exec.ID, "cap", h.exec.AttemptID), "probe", time.Minute)
	if err != nil || !ok || intent == nil {
		t.Fatalf("retry resume intent was not committed: intent=%v ok=%v err=%v", intent, ok, err)
	}

	// The wake-up is one-shot: once the timer is gone a second poll does nothing.
	count, err = sched.PollDueTimers(h.ctx, h.exec.InstanceID)
	if err != nil || count != 0 {
		t.Fatalf("second PollDueTimers = (%d, %v), want (0, nil)", count, err)
	}
}

// TestPollDueTimersLeavesFutureRetryWaitIntact proves a timer that is due by
// its own record but whose NextAttemptAt is still in the future is not consumed.
func TestPollDueTimersLeavesFutureRetryWaitIntact(t *testing.T) {
	h := newWaitHarness(t)
	sched := h.scheduler(t)
	next := time.Now().UTC().Add(time.Hour)
	if err := sched.BeginRetryWait(h.ctx, RetryWaitRequest{
		Execution: h.exec, CapabilityID: "cap", NextAttemptAt: next,
	}); err != nil {
		t.Fatalf("BeginRetryWait: %v", err)
	}
	key, err := executionmodel.WaitTimerKey(h.exec.ID, "cap", h.exec.AttemptID)
	if err != nil {
		t.Fatalf("WaitTimerKey: %v", err)
	}

	count, err := sched.PollDueTimers(h.ctx, h.exec.InstanceID)
	if err != nil || count != 0 {
		t.Fatalf("PollDueTimers = (%d, %v), want (0, nil)", count, err)
	}
	state := h.exec.CapabilityState("cap")
	if state.Status != executionmodel.CapabilityRetryWait || state.NextAttemptAt == nil || !state.NextAttemptAt.Equal(next) {
		t.Fatalf("retry state = %+v, want unchanged retry wait at %v", state, next)
	}
	if _, err := h.raw.GetTimer(h.ctx, key); err != nil {
		t.Fatalf("future retry timer was removed: %v", err)
	}
}

// TestPollDueTimersDiscardsRetryWaitWithoutNextAttempt proves a malformed retry
// wait is fenced-cleaned instead of being resumed without a schedule.
func TestPollDueTimersDiscardsRetryWaitWithoutNextAttempt(t *testing.T) {
	h := newWaitHarness(t)
	sched := h.scheduler(t)
	if err := sched.BeginRetryWait(h.ctx, RetryWaitRequest{
		Execution: h.exec, CapabilityID: "cap", NextAttemptAt: time.Now().UTC().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("BeginRetryWait: %v", err)
	}
	key, err := executionmodel.WaitTimerKey(h.exec.ID, "cap", h.exec.AttemptID)
	if err != nil {
		t.Fatalf("WaitTimerKey: %v", err)
	}

	snapshot := h.exec.Snapshot()
	state := snapshot.States["cap"]
	if state.Status != executionmodel.CapabilityRetryWait {
		t.Fatalf("test setup: status = %s, want retry wait", state.Status)
	}
	state.NextAttemptAt = nil
	snapshot.States["cap"] = state
	h.exec.Restore(snapshot)

	count, err := sched.PollDueTimers(h.ctx, h.exec.InstanceID)
	if err != nil || count != 0 {
		t.Fatalf("PollDueTimers = (%d, %v), want (0, nil)", count, err)
	}
	if state := h.exec.CapabilityState("cap"); state.Status != executionmodel.CapabilityRetryWait || state.NextAttemptAt != nil {
		t.Fatalf("retry state = %+v, want unchanged retry wait without a schedule", state)
	}
	if _, err := h.raw.GetTimer(h.ctx, key); err == nil {
		t.Fatal("retry timer without NextAttemptAt was not fenced-deleted")
	}
}

// TestPollDueTimersIsInstanceScoped proves two instances sharing one store each
// consume only their own due timers. Instance A's poll leaves B's timer claimable
// and B's capability waiting until B polls.
func TestPollDueTimersIsInstanceScoped(t *testing.T) {
	h := newWaitHarness(t)
	sched := h.scheduler(t)
	otherInstance := core.ID("inst_2")
	other, err := executionmodel.NewExecution(singleCapabilityBlueprint("cap"), core.NewID("corr_"), otherInstance, "", "", "", "")
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	if err := h.store.Add(h.ctx, other); err != nil {
		t.Fatalf("store.Add: %v", err)
	}
	if err := other.Start(map[string]any{}, 1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := other.MarkCapabilityReady("cap", map[string]any{}); err != nil {
		t.Fatalf("MarkCapabilityReady: %v", err)
	}
	if err := other.MarkCapabilityRunning("cap"); err != nil {
		t.Fatalf("MarkCapabilityRunning: %v", err)
	}

	due := time.Now().UTC().Add(-time.Minute)
	enterDueWait(t, h, sched, executionmodel.WaitTypeTimer, "", due)
	if err := sched.EnterWait(h.ctx, WaitEntryRequest{
		Execution: other, CapabilityID: "cap",
		Spec: executionmodel.WaitSpec{Type: executionmodel.WaitTypeTimer, Reason: "test", WaitUntil: &due},
	}); err != nil {
		t.Fatalf("EnterWait(B): %v", err)
	}
	otherKey, err := executionmodel.WaitTimerKey(other.ID, "cap", other.AttemptID)
	if err != nil {
		t.Fatalf("WaitTimerKey(B): %v", err)
	}

	subA, err := h.bus.SubscribeExecution(h.exec.ID, 8)
	if err != nil {
		t.Fatalf("SubscribeExecution(A): %v", err)
	}
	defer func() { _ = subA.Close() }()
	subB, err := h.bus.SubscribeExecution(other.ID, 8)
	if err != nil {
		t.Fatalf("SubscribeExecution(B): %v", err)
	}
	defer func() { _ = subB.Close() }()

	count, err := sched.PollDueTimers(h.ctx, h.exec.InstanceID)
	if err != nil || count != 1 {
		t.Fatalf("poll instance A = (%d, %v), want (1, nil)", count, err)
	}
	if evt := expectReadyEvent(t, subA); evt.Type != event.CapabilityReady {
		t.Fatalf("instance A event = %s, want %s", evt.Type, event.CapabilityReady)
	}
	if state := other.CapabilityState("cap"); state.Status != executionmodel.CapabilityWaiting {
		t.Fatalf("instance B capability = %s, want waiting", state.Status)
	}
	if _, err := h.raw.GetTimer(h.ctx, otherKey); err != nil {
		t.Fatalf("instance B timer was consumed by instance A's poll: %v", err)
	}

	count, err = sched.PollDueTimers(h.ctx, otherInstance)
	if err != nil || count != 1 {
		t.Fatalf("poll instance B = (%d, %v), want (1, nil)", count, err)
	}
	if evt := expectReadyEvent(t, subB); evt.Type != event.CapabilityReady {
		t.Fatalf("instance B event = %s, want %s", evt.Type, event.CapabilityReady)
	}
	if state := other.CapabilityState("cap"); state.Status != executionmodel.CapabilityReady {
		t.Fatalf("instance B capability = %s, want ready", state.Status)
	}
}

// foreignTimerWaitStore injects a stamped timer owned by another instance into
// one instance's discovery result, proving the payload check runs before claim.
type foreignTimerWaitStore struct {
	*executionmodel.ExecutionStore
	timer   storage.Timer
	claimed bool
}

func (s *foreignTimerWaitStore) DueWaitTimersForInstance(context.Context, core.ID, time.Time, int) ([]storage.Timer, error) {
	return []storage.Timer{s.timer}, nil
}

func (s *foreignTimerWaitStore) ClaimWaitTimer(context.Context, string, string, time.Duration) (bool, error) {
	s.claimed = true
	return false, nil
}

func TestPollDueTimersSkipsForeignStampedTimer(t *testing.T) {
	h := newWaitHarness(t)
	data, err := executionmodel.EncodeWaitTimer(executionmodel.WaitTimerPayload{
		InstanceID: "inst_other", ExecutionID: h.exec.ID, CapabilityID: "cap", WaitType: executionmodel.WaitTypeTimer,
	})
	if err != nil {
		t.Fatalf("EncodeWaitTimer: %v", err)
	}
	foreign := &foreignTimerWaitStore{
		ExecutionStore: h.store,
		timer:          storage.Timer{Key: "foreign", DueAt: time.Now().UTC().Add(-time.Minute), Data: data},
	}
	sched, err := New(h.eventBus, h.store, h.scopes, WithWaitStore(foreign, TimerPollConfig{}))
	if err != nil {
		t.Fatalf("New scheduler: %v", err)
	}

	count, err := sched.PollDueTimers(h.ctx, h.exec.InstanceID)
	if err != nil || count != 0 {
		t.Fatalf("PollDueTimers = (%d, %v), want (0, nil)", count, err)
	}
	if foreign.claimed {
		t.Fatal("foreign stamped timer was claimed")
	}
}

// staleLeaseWaitStore models one recovered poller whose claim expires while it
// prepares a transition: it claims with a short lease, never renews, and holds
// its fenced commit until the competing owner has taken the timer over.
type staleLeaseWaitStore struct {
	*executionmodel.ExecutionStore
	atCommit     chan struct{}
	takeover     chan struct{}
	claimOwner   string
	deleteOwners []string
}

func (s *staleLeaseWaitStore) ClaimWaitTimer(ctx context.Context, key, owner string, _ time.Duration) (bool, error) {
	claimed, err := s.ExecutionStore.ClaimWaitTimer(ctx, key, owner, time.Millisecond)
	if claimed {
		s.claimOwner = owner
	}
	return claimed, err
}

func (s *staleLeaseWaitStore) RenewWaitTimer(context.Context, string, string, time.Duration) error {
	return nil
}

func (s *staleLeaseWaitStore) SaveWaitTransition(ctx context.Context, req executionmodel.WaitTransitionRequest) (*executionmodel.WaitTransitionResult, error) {
	for _, del := range req.Effects.DeleteTimers {
		s.deleteOwners = append(s.deleteOwners, del.Owner)
	}
	close(s.atCommit)
	select {
	case <-s.takeover:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return s.ExecutionStore.SaveWaitTransition(ctx, req)
}

// takeoverWaitStore models the peer that reclaims the expired timer. Claims are
// retried inside a bounded window, so takeover does not depend on a sleep race.
type takeoverWaitStore struct {
	*executionmodel.ExecutionStore
	claimed chan string
}

func (s *takeoverWaitStore) ClaimWaitTimer(ctx context.Context, key, owner string, lease time.Duration) (bool, error) {
	deadline := time.Now().Add(5 * time.Second)
	for {
		claimed, err := s.ExecutionStore.ClaimWaitTimer(ctx, key, owner, lease)
		if err != nil || claimed {
			if claimed {
				select {
				case s.claimed <- owner:
				default:
				}
			}
			return claimed, err
		}
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if time.Now().After(deadline) {
			return false, nil
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestTwoPollersWithStaleTimerLeaseCommitOneResume proves duplicate-timer
// fencing at the scheduler boundary. Two recovered pollers adopt independent
// execution copies from one durable store. The first claims the due timer under
// a lease that expires before its commit; the second takes over and commits the
// resume. The delayed fenced commit must lose without publishing another
// CapabilityReady or writing another dispatch intent.
func TestTwoPollersWithStaleTimerLeaseCommitOneResume(t *testing.T) {
	h := newWaitHarness(t)
	seed := h.scheduler(t)
	due := time.Now().UTC().Add(-time.Minute)
	enterDueWait(t, h, seed, executionmodel.WaitTypeTimer, "", due)
	timerKey, err := executionmodel.WaitTimerKey(h.exec.ID, "cap", h.exec.AttemptID)
	if err != nil {
		t.Fatalf("WaitTimerKey: %v", err)
	}

	// Each recovered view reads its own execution copy from durable storage and
	// adopts it, so no two pollers share a mutable snapshot or revision counter.
	firstStore := executionmodel.NewExecutionStore(h.raw)
	secondStore := executionmodel.NewExecutionStore(h.raw)
	firstExec, err := h.store.Reload(h.ctx, h.exec.ID)
	if err != nil {
		t.Fatalf("Reload(first): %v", err)
	}
	secondExec, err := h.store.Reload(h.ctx, h.exec.ID)
	if err != nil {
		t.Fatalf("Reload(second): %v", err)
	}
	if firstExec == secondExec {
		t.Fatal("both views adopted the same execution value")
	}
	if err := firstStore.Adopt(h.ctx, firstExec); err != nil {
		t.Fatalf("Adopt(first): %v", err)
	}
	if err := secondStore.Adopt(h.ctx, secondExec); err != nil {
		t.Fatalf("Adopt(second): %v", err)
	}

	stale := &staleLeaseWaitStore{
		ExecutionStore: firstStore,
		atCommit:       make(chan struct{}),
		takeover:       make(chan struct{}),
	}
	replacement := &takeoverWaitStore{ExecutionStore: secondStore, claimed: make(chan string, 1)}
	firstBus := event.NewBus()
	secondBus := event.NewBus()
	firstSub, err := firstBus.SubscribeExecution(h.exec.ID, 8)
	if err != nil {
		t.Fatalf("SubscribeExecution(first): %v", err)
	}
	defer func() { _ = firstSub.Close() }()
	secondSub, err := secondBus.SubscribeExecution(h.exec.ID, 8)
	if err != nil {
		t.Fatalf("SubscribeExecution(second): %v", err)
	}
	defer func() { _ = secondSub.Close() }()
	firstScheduler, err := New(firstBus, firstStore, h.scopes, WithWaitStore(stale, TimerPollConfig{}))
	if err != nil {
		t.Fatalf("New scheduler(first): %v", err)
	}
	secondScheduler, err := New(secondBus, secondStore, h.scopes, WithWaitStore(replacement, TimerPollConfig{}))
	if err != nil {
		t.Fatalf("New scheduler(second): %v", err)
	}

	type pollOutcome struct {
		count int
		err   error
	}
	firstDone := make(chan pollOutcome, 1)
	go func() {
		n, err := firstScheduler.PollDueTimers(h.ctx, h.exec.InstanceID)
		firstDone <- pollOutcome{count: n, err: err}
	}()
	select {
	case <-stale.atCommit:
	case <-time.After(5 * time.Second):
		t.Fatal("stale poll never reached its gated commit")
	}

	secondDone := make(chan pollOutcome, 1)
	go func() {
		n, err := secondScheduler.PollDueTimers(h.ctx, h.exec.InstanceID)
		secondDone <- pollOutcome{count: n, err: err}
	}()
	var replacementOwner string
	select {
	case replacementOwner = <-replacement.claimed:
	case <-time.After(5 * time.Second):
		t.Fatal("replacement poller never took over the expired lease")
	}
	if replacementOwner == stale.claimOwner {
		t.Fatalf("replacement poller reused stale owner %q", stale.claimOwner)
	}
	close(stale.takeover)

	select {
	case got := <-secondDone:
		if got.err != nil || got.count != 1 {
			t.Fatalf("replacement PollDueTimers = (%d, %v), want (1, nil)", got.count, got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("replacement poll did not finish")
	}
	select {
	case got := <-firstDone:
		if got.err != nil || got.count != 0 {
			t.Fatalf("stale PollDueTimers = (%d, %v), want (0, nil) after lease loss", got.count, got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stale poll did not finish")
	}

	countReady := func(sub event.Subscription) int {
		n := 0
		for {
			select {
			case evt := <-sub.Events():
				if evt.Type == event.CapabilityReady {
					n++
				}
			default:
				return n
			}
		}
	}
	if n := countReady(firstSub); n != 0 {
		t.Fatalf("stale poller published %d CapabilityReady events, want 0", n)
	}
	if n := countReady(secondSub); n != 1 {
		t.Fatalf("replacement poller published %d CapabilityReady events, want 1", n)
	}

	if len(stale.deleteOwners) != 1 || stale.deleteOwners[0] != stale.claimOwner {
		t.Fatalf("stale fenced delete owners = %v, want only %q", stale.deleteOwners, stale.claimOwner)
	}
	if _, err := h.raw.GetTimer(h.ctx, timerKey); !errors.Is(err, storage.ErrTimerNotFound) {
		t.Fatalf("timer after takeover = %v, want ErrTimerNotFound", err)
	}

	intents, err := secondStore.ClaimReadyDispatchIntents(h.ctx, h.exec.InstanceID, "probe", time.Minute, 10)
	if err != nil {
		t.Fatalf("ClaimReadyDispatchIntents: %v", err)
	}
	if len(intents) != 1 {
		t.Fatalf("claimable dispatch intents = %d, want 1", len(intents))
	}
	state := secondExec.CapabilityState("cap")
	if state.Resume == nil || state.Resume.Cause != executionmodel.ResumeCauseTimer {
		t.Fatalf("resume = %+v, want a timer resume descriptor", state.Resume)
	}
	if intents[0].CapabilityID != "cap" || intents[0].AttemptID != secondExec.AttemptID {
		t.Fatalf("dispatch intent = %+v, want cap at resumed attempt %q", intents[0], secondExec.AttemptID)
	}
}
