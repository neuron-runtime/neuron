package engine

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
	"github.com/neuron-runtime/neuron/nore/internal/event"
	exec "github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/runtimeconfig"
	"github.com/neuron-runtime/neuron/nore/internal/types"
	core "github.com/neuron-runtime/neuron/shared/types/core"
)

// scriptedRuntime returns one scripted attempt per call and a plain success
// afterwards. A retry that was deferred must never reach a later script entry,
// which the tests observe through callCount.
type scriptedAttempt struct {
	outcome contracts.CapabilityOutcome
	err     error
}

type scriptedRuntime struct {
	mu      sync.Mutex
	script  []scriptedAttempt
	calls   int
	started chan struct{}
	once    sync.Once
}

func newScriptedRuntime(script ...scriptedAttempt) *scriptedRuntime {
	return &scriptedRuntime{script: script, started: make(chan struct{})}
}

func (r *scriptedRuntime) Execute(context.Context, contracts.ExecutionContext) (contracts.CapabilityOutcome, error) {
	r.mu.Lock()
	index := r.calls
	r.calls++
	var attempt scriptedAttempt
	if index < len(r.script) {
		attempt = r.script[index]
	}
	r.mu.Unlock()
	r.once.Do(func() { close(r.started) })
	if index < len(r.script) {
		return attempt.outcome, attempt.err
	}
	return contracts.CapabilityOutcome{Result: map[string]any{"done": true}}, nil
}

func (r *scriptedRuntime) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

type waitEnterCall struct {
	executionID  core.ID
	capabilityID core.ID
	spec         exec.WaitSpec
}

type waitRetryCall struct {
	executionID   core.ID
	capabilityID  core.ID
	nextAttemptAt time.Time
}

// recordingWaitLifecycle is the engine's consumer-side substitute. Every call
// is recorded, and when bound by the harness it performs the real domain
// transition so tests pin validation and worker release at the same boundary
// the host will use.
type recordingWaitLifecycle struct {
	mu        sync.Mutex
	execution *exec.Execution
	enterErr  error
	retryErr  error
	enters    []waitEnterCall
	retries   []waitRetryCall
}

func (l *recordingWaitLifecycle) EnterWait(_ context.Context, executionID, capabilityID core.ID, spec exec.WaitSpec) error {
	l.mu.Lock()
	l.enters = append(l.enters, waitEnterCall{executionID: executionID, capabilityID: capabilityID, spec: spec})
	domain, injected := l.execution, l.enterErr
	l.mu.Unlock()
	if injected != nil {
		return injected
	}
	return domain.EnterWait(capabilityID, spec)
}

func (l *recordingWaitLifecycle) BeginRetryWait(_ context.Context, executionID, capabilityID core.ID, nextAttemptAt time.Time) error {
	l.mu.Lock()
	l.retries = append(l.retries, waitRetryCall{executionID: executionID, capabilityID: capabilityID, nextAttemptAt: nextAttemptAt})
	domain, injected := l.execution, l.retryErr
	l.mu.Unlock()
	if injected != nil {
		return injected
	}
	return domain.BeginRetryWait(capabilityID, nextAttemptAt)
}

func (l *recordingWaitLifecycle) enterCalls() []waitEnterCall {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]waitEnterCall(nil), l.enters...)
}

func (l *recordingWaitLifecycle) retryCalls() []waitRetryCall {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]waitRetryCall(nil), l.retries...)
}

// recordingClaimCommitter counts completed claims on top of the in-memory
// claim lifecycle used by the dispatch tests.
type recordingClaimCommitter struct {
	*claimCommitter
	mu        sync.Mutex
	completed int
}

func newRecordingClaimCommitter() *recordingClaimCommitter {
	return &recordingClaimCommitter{claimCommitter: newClaimCommitter()}
}

func (c *recordingClaimCommitter) CompleteDispatchIntent(ctx context.Context, executionID, capabilityID, attemptID core.ID, owner string) error {
	if err := c.claimCommitter.CompleteDispatchIntent(ctx, executionID, capabilityID, attemptID, owner); err != nil {
		return err
	}
	c.mu.Lock()
	c.completed++
	c.mu.Unlock()
	return nil
}

func (c *recordingClaimCommitter) completionCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.completed
}

type waitHarness struct {
	ctx          context.Context
	capabilityID core.ID
	execution    *exec.Execution
	events       event.Subscription
	started      time.Time
}

func waitBlueprint(capabilityID core.ID, runtimeConfig *core.RuntimeConfig) *types.ExecutionBlueprint {
	return &types.ExecutionBlueprint{
		Metadata: core.Metadata{ID: "bp", Name: "wait", Version: "1.0.0"},
		Nodes: map[core.ID]types.ExecutionNode{capabilityID: {
			Capability: core.Capability{
				Metadata:      core.Metadata{ID: capabilityID, Name: string(capabilityID), Version: "1.0.0"},
				Type:          core.CapabilityRuntimeType("test:wait"),
				RuntimeConfig: runtimeConfig,
			},
			Configurations: emptyConfigurations{},
		}},
		EntryCapabilityIDs: []core.ID{capabilityID},
	}
}

// startWaitHarness drives one ready capability through a real engine. Events
// are collected at the execution subscription, so tests assert the engine's
// public output rather than its internal bookkeeping.
func startWaitHarness(t *testing.T, capabilityID core.ID, blueprint *types.ExecutionBlueprint, runtime *scriptedRuntime, lifecycle *recordingWaitLifecycle, committer *recordingClaimCommitter) *waitHarness {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())

	bus := event.NewBus()
	store := exec.NewMemoryStore()
	scopes := exec.NewScopeRegistry(ctx)
	var opts []Option
	if lifecycle != nil {
		opts = append(opts, WithWaitLifecycle(lifecycle))
	}
	if committer != nil {
		opts = append(opts, WithTransitionCommitter(committer))
	}
	engineInstance, err := NewCapabilityRuntimeEngine(bus, singleRuntimeRegistry{runtime: runtime}, store, scopes, 4, time.Second, opts...)
	if err != nil {
		cancel()
		t.Fatalf("new engine: %v", err)
	}

	execution, err := exec.NewExecution(blueprint, core.NewID("corr_"), "inst_1", "", "", "", "")
	if err != nil {
		cancel()
		t.Fatalf("new execution: %v", err)
	}
	if err := store.Add(context.Background(), execution); err != nil {
		cancel()
		t.Fatalf("store.Add: %v", err)
	}
	subscription, err := bus.SubscribeExecution(execution.ID, 64)
	if err != nil {
		cancel()
		t.Fatalf("SubscribeExecution: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = subscription.Close()
	})

	if err := execution.Start(map[string]any{}, 1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if lifecycle != nil {
		lifecycle.mu.Lock()
		lifecycle.execution = execution
		lifecycle.mu.Unlock()
	}
	input := execution.Params(capabilityID)
	if err := execution.MarkCapabilityReady(capabilityID, input); err != nil {
		t.Fatalf("MarkCapabilityReady: %v", err)
	}
	if committer != nil {
		committer.allow(execution.ID, capabilityID, execution.AttemptID)
	}

	go func() { _ = engineInstance.Run(ctx) }()

	h := &waitHarness{ctx: ctx, capabilityID: capabilityID, execution: execution, events: subscription}
	h.started = time.Now()
	if err := bus.Publish(ctx, event.New(event.CapabilityReady, execution.ID, execution.CorrelationID, capabilityID, event.CapabilityReadyPayload{Params: input})); err != nil {
		t.Fatalf("publish CapabilityReady: %v", err)
	}
	return h
}

// awaitEventsUntil collects the capability's events through the first event of
// the requested type. It never waits for a terminal event that a wait must not
// emit, so callers choose the boundary they are testing.
func (h *waitHarness) awaitEventsUntil(t *testing.T, boundary event.Type) []event.Event {
	t.Helper()
	var collected []event.Event
	deadline := time.After(5 * time.Second)
	for {
		select {
		case received, open := <-h.events.Events():
			if !open {
				t.Fatal("subscription closed before the capability reported progress")
			}
			if received.Metadata.CapabilityID != h.capabilityID {
				continue
			}
			collected = append(collected, received)
			if received.Type == boundary {
				return collected
			}
		case <-deadline:
			t.Fatalf("no %s event for capability %s", boundary, h.capabilityID)
		}
	}
}

// drainEvents watches for events that must not follow a wait. Terminal events
// and retry announcements are both forbidden here: a suspend adds no event, and
// a deferred retry announces its intent exactly once.
func (h *waitHarness) drainEvents(t *testing.T) {
	t.Helper()
	deadline := time.After(250 * time.Millisecond)
	for {
		select {
		case received, open := <-h.events.Events():
			if !open {
				return
			}
			if received.Metadata.CapabilityID != h.capabilityID {
				continue
			}
			switch received.Type {
			case event.CapabilityRetry:
				t.Fatalf("a second CapabilityRetry was published: %+v", received.Payload)
			case event.CapabilityCompleted, event.CapabilityFailed, event.CapabilityCancelled:
				t.Fatalf("unexpected terminal event %s after a wait: %+v", received.Type, received.Payload)
			}
		case <-deadline:
			return
		}
	}
}

func awaitEnterCall(t *testing.T, lifecycle *recordingWaitLifecycle) waitEnterCall {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if calls := lifecycle.enterCalls(); len(calls) > 0 {
			return calls[0]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("EnterWait was never called")
	return waitEnterCall{}
}

func awaitRetryCall(t *testing.T, lifecycle *recordingWaitLifecycle) waitRetryCall {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if calls := lifecycle.retryCalls(); len(calls) > 0 {
			return calls[0]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("BeginRetryWait was never called")
	return waitRetryCall{}
}

func awaitClaimCompletion(t *testing.T, committer *recordingClaimCommitter) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if committer.completionCount() > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the dispatch claim was never completed")
}

func countEvents(events []event.Event, wanted event.Type) int {
	count := 0
	for _, received := range events {
		if received.Type == wanted {
			count++
		}
	}
	return count
}

func TestEngineMapsSignalSuspendToWaitSpecAndCompletesNoWork(t *testing.T) {
	capabilityID := core.ID("signaller")
	until := time.Now().UTC().Add(time.Minute).Truncate(time.Millisecond)
	suspend := &contracts.CapabilitySuspend{
		CorrelationKey: "approval-wait",
		WaitType:       exec.WaitTypeSignal,
		ExpectedSignal: "approval",
		WaitUntil:      &until,
		PayloadSchema:  `{"approved":true}`,
	}
	lifecycle := &recordingWaitLifecycle{}
	committer := newRecordingClaimCommitter()
	runtime := newScriptedRuntime(scriptedAttempt{outcome: contracts.CapabilityOutcome{Suspend: suspend}})
	h := startWaitHarness(t, capabilityID, waitBlueprint(capabilityID, nil), runtime, lifecycle, committer)

	call := awaitEnterCall(t, lifecycle)
	if call.executionID != h.execution.ID || call.capabilityID != capabilityID {
		t.Fatalf("EnterWait IDs = %q/%q, want %q/%q", call.executionID, call.capabilityID, h.execution.ID, capabilityID)
	}
	spec := call.spec
	if spec.Type != exec.WaitTypeSignal {
		t.Errorf("wait type = %q, want %q", spec.Type, exec.WaitTypeSignal)
	}
	if spec.CorrelationKey != suspend.CorrelationKey || spec.ExpectedSignal != suspend.ExpectedSignal || spec.PayloadSchema != suspend.PayloadSchema {
		t.Errorf("wait mapping = %+v, want correlation/signal/schema from the suspend", spec)
	}
	if spec.WaitUntil == nil || !spec.WaitUntil.Equal(until) {
		t.Errorf("WaitUntil = %v, want %v", spec.WaitUntil, until)
	}
	if spec.Reason != suspendWaitReason || spec.Reason == "" {
		t.Errorf("Reason = %q, want the engine-supplied reason", spec.Reason)
	}
	if spec.DeadlinePolicy != "" {
		t.Errorf("DeadlinePolicy = %q, want it left empty for the engine", spec.DeadlinePolicy)
	}

	state := h.execution.CapabilityState(capabilityID)
	if state.Status != exec.CapabilityWaiting {
		t.Errorf("capability status = %q, want %q", state.Status, exec.CapabilityWaiting)
	}
	if state.WaitType != exec.WaitTypeSignal || state.ExpectedSignal != suspend.ExpectedSignal {
		t.Errorf("recorded wait state = %+v, want the mapped signal wait", state)
	}
	if state.WaitDeadlinePolicy != exec.WaitDeadlinePolicyFail {
		t.Errorf("effective deadline policy = %q, want the domain default", state.WaitDeadlinePolicy)
	}
	if inFlight := h.execution.InFlight(); inFlight != 0 {
		t.Errorf("in-flight = %d, want the wait to release the worker", inFlight)
	}
	awaitClaimCompletion(t, committer)
	if count := committer.completionCount(); count != 1 {
		t.Errorf("claim completions = %d, want 1", count)
	}
	if calls := runtime.callCount(); calls != 1 {
		t.Errorf("runtime calls = %d, want only the suspending invocation", calls)
	}
	h.drainEvents(t)
}

func TestEngineMapsTimerSuspendToWaitSpec(t *testing.T) {
	capabilityID := core.ID("timer")
	until := time.Now().UTC().Add(2 * time.Minute).Truncate(time.Millisecond)
	suspend := &contracts.CapabilitySuspend{WaitType: exec.WaitTypeTimer, WaitUntil: &until, PayloadSchema: `{}`}
	lifecycle := &recordingWaitLifecycle{}
	runtime := newScriptedRuntime(scriptedAttempt{outcome: contracts.CapabilityOutcome{Suspend: suspend}})
	h := startWaitHarness(t, capabilityID, waitBlueprint(capabilityID, nil), runtime, lifecycle, nil)

	call := awaitEnterCall(t, lifecycle)
	if call.spec.Type != exec.WaitTypeTimer {
		t.Errorf("wait type = %q, want %q", call.spec.Type, exec.WaitTypeTimer)
	}
	if call.spec.WaitUntil == nil || !call.spec.WaitUntil.Equal(until) {
		t.Errorf("WaitUntil = %v, want %v", call.spec.WaitUntil, until)
	}
	if call.spec.CorrelationKey != "" {
		t.Errorf("CorrelationKey = %q, want the domain to derive it", call.spec.CorrelationKey)
	}
	if call.spec.DeadlinePolicy != "" {
		t.Errorf("DeadlinePolicy = %q, want it left empty", call.spec.DeadlinePolicy)
	}
	if state := h.execution.CapabilityState(capabilityID); state.Status != exec.CapabilityWaiting {
		t.Errorf("capability status = %q, want %q", state.Status, exec.CapabilityWaiting)
	}
	if inFlight := h.execution.InFlight(); inFlight != 0 {
		t.Errorf("in-flight = %d, want the wait to release the worker", inFlight)
	}
	h.drainEvents(t)
}

func TestEngineRejectsExecutorSuppliedDeadlineWaitBeforeLifecycle(t *testing.T) {
	capabilityID := core.ID("deadline")
	suspend := &contracts.CapabilitySuspend{WaitType: exec.WaitTypeDeadline, WaitUntil: func() *time.Time { t := time.Now().Add(time.Hour); return &t }()}
	lifecycle := &recordingWaitLifecycle{}
	runtime := newScriptedRuntime(scriptedAttempt{outcome: contracts.CapabilityOutcome{Suspend: suspend}})
	h := startWaitHarness(t, capabilityID, waitBlueprint(capabilityID, nil), runtime, lifecycle, nil)

	events := h.awaitEventsUntil(t, event.CapabilityFailed)
	payload, ok := events[len(events)-1].Payload.(event.CapabilityFailedPayload)
	if !ok {
		t.Fatalf("failure payload has unexpected type %T", events[len(events)-1].Payload)
	}
	if !strings.Contains(payload.Message, "deadline") {
		t.Errorf("failure message = %q, want it to reject an executor deadline", payload.Message)
	}
	if state := h.execution.CapabilityState(capabilityID); state.Status != exec.CapabilityFailed {
		t.Errorf("capability status = %q, want %q", state.Status, exec.CapabilityFailed)
	}
	if calls := lifecycle.enterCalls(); len(calls) != 0 {
		t.Fatalf("EnterWait was called %d times, want 0", len(calls))
	}
	h.drainEvents(t)
}

func TestEngineRejectsInvalidSuspendWaitTypeBeforeLifecycle(t *testing.T) {
	capabilityID := core.ID("invalid-type")
	suspend := &contracts.CapabilitySuspend{WaitType: exec.WaitType("bogus")}
	lifecycle := &recordingWaitLifecycle{}
	runtime := newScriptedRuntime(scriptedAttempt{outcome: contracts.CapabilityOutcome{Suspend: suspend}})
	h := startWaitHarness(t, capabilityID, waitBlueprint(capabilityID, nil), runtime, lifecycle, nil)

	events := h.awaitEventsUntil(t, event.CapabilityFailed)
	payload := events[len(events)-1].Payload.(event.CapabilityFailedPayload)
	if !strings.Contains(payload.Message, "invalid suspend wait type") {
		t.Errorf("failure message = %q, want an invalid wait type", payload.Message)
	}
	if calls := lifecycle.enterCalls(); len(calls) != 0 {
		t.Fatalf("EnterWait was called %d times, want 0", len(calls))
	}
	h.drainEvents(t)
}

func TestEngineFailsWhenSuspendLifecycleErrors(t *testing.T) {
	capabilityID := core.ID("lifecycle-error")
	lifecycle := &recordingWaitLifecycle{enterErr: errors.New("wait store unavailable")}
	until := time.Now().UTC().Add(time.Minute)
	suspend := &contracts.CapabilitySuspend{WaitType: exec.WaitTypeTimer, WaitUntil: &until}
	runtime := newScriptedRuntime(scriptedAttempt{outcome: contracts.CapabilityOutcome{Suspend: suspend}})
	h := startWaitHarness(t, capabilityID, waitBlueprint(capabilityID, nil), runtime, lifecycle, nil)

	events := h.awaitEventsUntil(t, event.CapabilityFailed)
	payload := events[len(events)-1].Payload.(event.CapabilityFailedPayload)
	if !strings.Contains(payload.Message, lifecycle.enterErr.Error()) {
		t.Errorf("failure message = %q, want it to name the lifecycle error", payload.Message)
	}
	if calls := lifecycle.enterCalls(); len(calls) != 1 {
		t.Fatalf("EnterWait calls = %d, want 1", len(calls))
	}
	if state := h.execution.CapabilityState(capabilityID); state.Status != exec.CapabilityFailed {
		t.Errorf("capability status = %q, want %q", state.Status, exec.CapabilityFailed)
	}
	h.drainEvents(t)
}

func TestEngineLeavesInvalidWaitMetadataToDomainValidator(t *testing.T) {
	capabilityID := core.ID("invalid-metadata")
	// A signal without an expected signal is a domain-level validation error;
	// the engine must pass it through EnterWait rather than invent its own rule.
	suspend := &contracts.CapabilitySuspend{WaitType: exec.WaitTypeSignal, CorrelationKey: "missing-signal"}
	lifecycle := &recordingWaitLifecycle{}
	runtime := newScriptedRuntime(scriptedAttempt{outcome: contracts.CapabilityOutcome{Suspend: suspend}})
	h := startWaitHarness(t, capabilityID, waitBlueprint(capabilityID, nil), runtime, lifecycle, nil)

	events := h.awaitEventsUntil(t, event.CapabilityFailed)
	payload := events[len(events)-1].Payload.(event.CapabilityFailedPayload)
	if !strings.Contains(payload.Message, "expected signal") {
		t.Errorf("failure message = %q, want the domain validation error", payload.Message)
	}
	if calls := lifecycle.enterCalls(); len(calls) != 1 {
		t.Fatalf("EnterWait calls = %d, want 1", len(calls))
	}
	if state := h.execution.CapabilityState(capabilityID); state.Status != exec.CapabilityFailed {
		t.Errorf("capability status = %q, want %q", state.Status, exec.CapabilityFailed)
	}
	h.drainEvents(t)
}

func TestEngineDefersDurableRetryWithoutInlineSleep(t *testing.T) {
	capabilityID := core.ID("durable-retry")
	const backoff = 10 * time.Second
	runtimeConfig := runtimeconfig.Resolve(&core.RuntimeConfig{
		Retry: &core.RuntimeRetry{Policy: core.RetryPolicyFixed, MaxAttempts: 3, InitialBackoff: backoff.String()},
	})
	lifecycle := &recordingWaitLifecycle{}
	committer := newRecordingClaimCommitter()
	runtime := newScriptedRuntime(scriptedAttempt{err: errors.New("transient")})
	h := startWaitHarness(t, capabilityID, waitBlueprint(capabilityID, runtimeConfig), runtime, lifecycle, committer)

	events := h.awaitEventsUntil(t, event.CapabilityRetry)
	if countEvents(events, event.CapabilityRetry) != 1 {
		t.Fatalf("CapabilityRetry published %d times before the lifecycle call, want 1", countEvents(events, event.CapabilityRetry))
	}
	call := awaitRetryCall(t, lifecycle)
	if call.executionID != h.execution.ID || call.capabilityID != capabilityID {
		t.Fatalf("BeginRetryWait IDs = %q/%q, want %q/%q", call.executionID, call.capabilityID, h.execution.ID, capabilityID)
	}
	if elapsed := time.Since(h.started); elapsed >= 2*time.Second {
		t.Fatalf("durable retry took %s; it must not sleep the %s backoff inline", elapsed, backoff)
	}
	if call.nextAttemptAt.Before(h.started.Add(backoff)) || call.nextAttemptAt.After(time.Now().Add(backoff)) {
		t.Fatalf("nextAttemptAt = %s, want now + %s", call.nextAttemptAt, backoff)
	}
	if state := h.execution.CapabilityState(capabilityID); state.Status != exec.CapabilityRetryWait {
		t.Errorf("capability status = %q, want %q", state.Status, exec.CapabilityRetryWait)
	} else if state.NextAttemptAt == nil || !state.NextAttemptAt.Equal(call.nextAttemptAt) {
		t.Errorf("NextAttemptAt = %v, want %v", state.NextAttemptAt, call.nextAttemptAt)
	}
	if inFlight := h.execution.InFlight(); inFlight != 0 {
		t.Errorf("in-flight = %d, want the retry wait to release the worker", inFlight)
	}
	awaitClaimCompletion(t, committer)
	if calls := runtime.callCount(); calls != 1 {
		t.Fatalf("runtime calls = %d, want only the failed attempt", calls)
	}
	h.drainEvents(t)
}

func TestEngineFailsWhenRetryLifecycleErrors(t *testing.T) {
	capabilityID := core.ID("retry-error")
	runtimeConfig := runtimeconfig.Resolve(&core.RuntimeConfig{
		Retry: &core.RuntimeRetry{Policy: core.RetryPolicyFixed, MaxAttempts: 3, InitialBackoff: "1ms"},
	})
	lifecycle := &recordingWaitLifecycle{retryErr: errors.New("wait store unavailable")}
	runtime := newScriptedRuntime(scriptedAttempt{err: errors.New("transient")})
	h := startWaitHarness(t, capabilityID, waitBlueprint(capabilityID, runtimeConfig), runtime, lifecycle, nil)

	events := h.awaitEventsUntil(t, event.CapabilityFailed)
	if countEvents(events, event.CapabilityRetry) != 1 {
		t.Fatalf("CapabilityRetry count = %d, want 1", countEvents(events, event.CapabilityRetry))
	}
	payload := events[len(events)-1].Payload.(event.CapabilityFailedPayload)
	if !strings.Contains(payload.Message, "begin retry wait") || !strings.Contains(payload.Message, lifecycle.retryErr.Error()) {
		t.Errorf("failure message = %q, want the wrapped lifecycle error", payload.Message)
	}
	if state := h.execution.CapabilityState(capabilityID); state.Status != exec.CapabilityFailed {
		t.Errorf("capability status = %q, want %q", state.Status, exec.CapabilityFailed)
	}
	if calls := runtime.callCount(); calls != 1 {
		t.Fatalf("runtime calls = %d, want the error to stop the retry loop", calls)
	}
	h.drainEvents(t)
}

func TestEngineKeepsInlineRetryWithoutLifecycle(t *testing.T) {
	capabilityID := core.ID("inline-retry")
	runtimeConfig := runtimeconfig.Resolve(&core.RuntimeConfig{
		Retry: &core.RuntimeRetry{Policy: core.RetryPolicyFixed, MaxAttempts: 2, InitialBackoff: "30ms"},
	})
	runtime := newScriptedRuntime(scriptedAttempt{err: errors.New("transient")})
	h := startWaitHarness(t, capabilityID, waitBlueprint(capabilityID, runtimeConfig), runtime, nil, nil)

	events := h.awaitEventsUntil(t, event.CapabilityCompleted)
	if countEvents(events, event.CapabilityRetry) != 1 {
		t.Fatalf("CapabilityRetry count = %d, want 1", countEvents(events, event.CapabilityRetry))
	}
	if elapsed := time.Since(h.started); elapsed < 10*time.Millisecond {
		t.Errorf("non-durable retry took %s; the inline backoff should still be observed", elapsed)
	}
	if calls := runtime.callCount(); calls != 2 {
		t.Fatalf("runtime calls = %d, want the second attempt", calls)
	}
	if state := h.execution.CapabilityState(capabilityID); state.Status != exec.CapabilityCompleted {
		t.Errorf("capability status = %q, want %q", state.Status, exec.CapabilityCompleted)
	}
	h.drainEvents(t)
}
