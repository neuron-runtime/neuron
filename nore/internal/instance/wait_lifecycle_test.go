package instance

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/event"
	"github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/execution/scheduler"
	"github.com/neuron-runtime/neuron/nore/internal/resolver"
	"github.com/neuron-runtime/neuron/nore/internal/runtimeconfig"
	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/nore/internal/storage/sqlite"
	core "github.com/neuron-runtime/neuron/shared/types/core"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

// emptyTestConfigurations is a valid implementation of the engine's
// configuration program. A timer-loop fixture whose synthetic capability can be
// dispatched must not leave this nil: the engine resolves configurations before
// invoking a capability and a nil program panics.
type emptyTestConfigurations struct{}

func (emptyTestConfigurations) Resolve(context.Context, resolver.CapabilityEnvironment) (map[string]any, error) {
	return map[string]any{}, nil
}

func TestWaitDeadlinePolicyMapping(t *testing.T) {
	cases := []struct {
		input string
		want  execution.WaitDeadlinePolicy
	}{
		{"fail", execution.WaitDeadlinePolicyFail},
		{"cancel", execution.WaitDeadlinePolicyCancel},
		{"continue", execution.WaitDeadlinePolicyContinue},
		{"", execution.WaitDeadlinePolicyFail},
		{"unrecognized", execution.WaitDeadlinePolicyFail},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			if got := waitDeadlinePolicy(tc.input); got != tc.want {
				t.Fatalf("waitDeadlinePolicy(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func policyTestInstance(t *testing.T, durability string, policy runtimeconfig.ExecutionPolicy) *Instance {
	t.Helper()
	return policyTestInstanceWithStore(t, newTestStore(t), durability, policy)
}

// policyTestInstanceWithStore keeps the durable KV handle available to tests
// that must inspect persisted bytes rather than the in-memory adopted object.
func policyTestInstanceWithStore(t *testing.T, raw storage.Store, durability string, policy runtimeconfig.ExecutionPolicy) *Instance {
	t.Helper()
	ctx := context.Background()
	key := protocol.InstanceKey{AssemblyID: "sys_policy_test", Version: "1.0.0", Hash: "policy_hash"}
	assembly := recoveryAssembly(key, "cap")
	inst, err := New(ctx, "inst_policy_test", key, &assembly, 1, raw,
		WithDurability(durability), WithExecutionPolicy(policy))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = inst.Stop() })
	return inst
}

func runningExecutionForPolicyTest(t *testing.T, inst *Instance, capabilityID core.ID) *execution.Execution {
	t.Helper()
	// The recovery test blueprint is deliberately minimal. A capability that
	// may be dispatched still needs a compiled configuration program: the engine
	// resolves it before every invocation, and a nil program panics there.
	blueprint := recoveryBlueprint(capabilityID)
	node := blueprint.Nodes[capabilityID]
	node.Configurations = emptyTestConfigurations{}
	blueprint.Nodes[capabilityID] = node
	exec, err := execution.NewExecution(blueprint, core.NewID("corr_"), core.ID(inst.ID), "", "", "", "")
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	if err := exec.Start(map[string]any{}, 1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := exec.MarkCapabilityReady(capabilityID, map[string]any{}); err != nil {
		t.Fatalf("MarkCapabilityReady: %v", err)
	}
	if err := exec.MarkCapabilityRunning(capabilityID); err != nil {
		t.Fatalf("MarkCapabilityRunning: %v", err)
	}
	if err := inst.Store().Add(context.Background(), exec); err != nil {
		t.Fatalf("store.Add: %v", err)
	}
	return exec
}

// TestEnterWaitPersistsResolvedDeadlinePolicy proves the instance adapter
// applies the operator's wait-deadline policy over an empty engine-supplied
// spec and commits it with the wait, rather than trusting the capability.
func TestEnterWaitPersistsResolvedDeadlinePolicy(t *testing.T) {
	policy := runtimeconfig.ExecutionPolicy{
		WaitDeadlinePolicy: "continue",
		TimerPollInterval:  time.Second,
		TimerPollBatchSize: 8,
	}
	inst := policyTestInstance(t, DurabilityDurable, policy)
	if inst.waitLifecycle == nil {
		t.Fatal("durable instance has no wait lifecycle")
	}
	exec := runningExecutionForPolicyTest(t, inst, "cap")
	due := time.Now().UTC().Add(time.Hour)

	err := inst.waitLifecycle.EnterWait(context.Background(), exec.ID, "cap", execution.WaitSpec{
		Type:           execution.WaitTypeSignal,
		Reason:         "test",
		ExpectedSignal: "approved",
		WaitUntil:      &due,
		// DeadlinePolicy is left empty, as the engine leaves it.
	})
	if err != nil {
		t.Fatalf("EnterWait: %v", err)
	}

	stored, err := inst.Store().Get(context.Background(), exec.ID)
	if err != nil {
		t.Fatalf("load persisted execution: %v", err)
	}
	state := stored.CapabilityState("cap")
	if state.Status != execution.CapabilityWaiting {
		t.Fatalf("capability status = %s, want waiting", state.Status)
	}
	if state.WaitDeadlinePolicy != execution.WaitDeadlinePolicyContinue {
		t.Fatalf("persisted deadline policy = %q, want %q", state.WaitDeadlinePolicy, execution.WaitDeadlinePolicyContinue)
	}
}

// TestWaitLifecycleOnlyInDurableMode verifies the adapter is owned only by
// durable instances, without reaching into engine internals.
func TestWaitLifecycleOnlyInDurableMode(t *testing.T) {
	policy := runtimeconfig.ExecutionPolicy{
		WaitDeadlinePolicy: "fail",
		TimerPollInterval:  time.Second,
		TimerPollBatchSize: 8,
	}
	durable := policyTestInstance(t, DurabilityDurable, policy)
	if durable.waitLifecycle == nil {
		t.Fatal("durable instance must own a wait lifecycle")
	}
	if durable.waitLifecycle.deadlinePolicy != execution.WaitDeadlinePolicyFail {
		t.Fatalf("durable policy = %q, want fail", durable.waitLifecycle.deadlinePolicy)
	}

	nonDurable := policyTestInstance(t, DurabilityNone, policy)
	if nonDurable.waitLifecycle != nil {
		t.Fatal("non-durable instance must not own a wait lifecycle")
	}
}

// TestDurableTimerLoopPollsAndStops proves the host timer loop consumes a due
// retry timer after Start and exits cleanly on Stop.
func TestDurableTimerLoopPollsAndStops(t *testing.T) {
	policy := runtimeconfig.ExecutionPolicy{
		WaitDeadlinePolicy: "fail",
		TimerPollInterval:  core.MinTimerPollInterval,
		TimerPollBatchSize: 8,
	}
	inst := policyTestInstance(t, DurabilityDurable, policy)
	exec := runningExecutionForPolicyTest(t, inst, "cap")
	due := time.Now().UTC().Add(-time.Minute)
	if err := inst.scheduler.EnterWait(context.Background(), scheduler.WaitEntryRequest{
		Execution: exec, CapabilityID: "cap",
		Spec: execution.WaitSpec{Type: execution.WaitTypeTimer, Reason: "test", WaitUntil: &due},
	}); err != nil {
		t.Fatalf("EnterWait: %v", err)
	}

	ready, err := inst.Bus().SubscribeExecution(exec.ID, 8)
	if err != nil {
		t.Fatalf("SubscribeExecution: %v", err)
	}
	defer func() { _ = ready.Close() }()

	if err := inst.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	select {
	case evt := <-ready.Events():
		if evt.Type != event.CapabilityReady {
			t.Fatalf("published event = %s, want %s", evt.Type, event.CapabilityReady)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("durable timer loop did not consume the due timer")
	}
	execStore, ok := inst.Store().(*execution.ExecutionStore)
	if !ok {
		t.Fatalf("store type = %T, want *execution.ExecutionStore", inst.Store())
	}
	keys, err := execStore.WaitTimerKeys(context.Background(), exec.ID, "cap")
	if err != nil {
		t.Fatalf("WaitTimerKeys: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("wait timer keys after consumption = %v, want none", keys)
	}

	stopped := make(chan error, 1)
	go func() { stopped <- inst.Stop() }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return after the timer loop was started")
	}
}

// TestExecuteStampsMaxLifetimeDeadline proves admission stamps the operator's
// maximum-lifetime deadline before the first persist.
func TestExecuteStampsMaxLifetimeDeadline(t *testing.T) {
	policy := runtimeconfig.ExecutionPolicy{
		WaitDeadlinePolicy: "fail",
		MaxLifetime:        time.Hour,
		TimerPollInterval:  time.Second,
		TimerPollBatchSize: 8,
	}
	inst := policyTestInstance(t, DurabilityNone, policy)
	if err := inst.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	before := time.Now().UTC()
	exec, err := inst.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	after := time.Now().UTC()
	deadline := exec.ExecutionDeadline()
	if deadline == nil {
		t.Fatal("Execute did not stamp a maximum-lifetime deadline")
	}
	if deadline.Before(before.Add(policy.MaxLifetime)) || deadline.After(after.Add(policy.MaxLifetime)) {
		t.Fatalf("deadline = %v, want between %v and %v", *deadline, before.Add(policy.MaxLifetime), after.Add(policy.MaxLifetime))
	}
}

// TestRecoverExecutionBackfillsMissingDeadline proves a recovered execution that
// predates the policy gets a deadline, while one that already has a deadline is
// not granted extra lifetime by a restart.
func TestRecoverExecutionBackfillsMissingDeadline(t *testing.T) {
	policy := runtimeconfig.ExecutionPolicy{
		WaitDeadlinePolicy: "fail",
		MaxLifetime:        time.Hour,
		TimerPollInterval:  time.Second,
		TimerPollBatchSize: 8,
	}
	ctx := context.Background()
	raw := newTestStore(t)
	inst := policyTestInstanceWithStore(t, raw, DurabilityDurable, policy)

	newExec := func(t *testing.T) *execution.Execution {
		t.Helper()
		exec, err := execution.NewExecution(recoveryBlueprint("cap"), core.NewID("corr_"), core.ID(inst.ID), "", "", "", "")
		if err != nil {
			t.Fatalf("NewExecution: %v", err)
		}
		if err := inst.Store().Add(ctx, exec); err != nil {
			t.Fatalf("store.Add: %v", err)
		}
		return exec
	}

	t.Run("missing deadline", func(t *testing.T) {
		exec := newExec(t)
		if err := inst.RecoverExecution(ctx, exec, recoveryBlueprint("cap"), nil); err != nil {
			t.Fatalf("RecoverExecution: %v", err)
		}
		stamped := exec.ExecutionDeadline()
		if stamped == nil {
			t.Fatal("recovery did not stamp a missing maximum-lifetime deadline")
		}

		// Store().Get returns the adopted in-memory object, so it cannot prove
		// what a restart would load. Read the durable snapshot and index from KV.
		key, err := storage.Key("executions", string(exec.ID))
		if err != nil {
			t.Fatalf("execution key: %v", err)
		}
		data, err := raw.Get(ctx, key)
		if err != nil {
			t.Fatalf("read durable execution: %v", err)
		}
		durable, err := execution.UnmarshalExecution(data)
		if err != nil {
			t.Fatalf("decode durable execution: %v", err)
		}
		if durable.ExecutionDeadline() != nil {
			t.Fatalf("durable snapshot deadline = %v, want nil: Adopt does not persist the re-stamp", durable.ExecutionDeadline())
		}

		prefix, err := storage.ExecutionDeadlinePrefix(string(inst.ID))
		if err != nil {
			t.Fatalf("deadline prefix: %v", err)
		}
		keys, err := raw.List(ctx, prefix)
		if err != nil {
			t.Fatalf("list durable deadline index: %v", err)
		}
		if len(keys) != 1 {
			t.Fatalf("durable deadline index = %v, want one entry", keys)
		}
		_, gotDue, gotID, err := storage.ParseExecutionDeadlineKey(keys[0])
		if err != nil {
			t.Fatalf("parse deadline index key %q: %v", keys[0], err)
		}
		if gotID != string(exec.ID) || !gotDue.Equal(*stamped) {
			t.Fatalf("durable deadline index = (%s, %v), want (%s, %v)", gotID, gotDue, exec.ID, *stamped)
		}
	})

	t.Run("existing deadline preserved", func(t *testing.T) {
		exec := newExec(t)
		original := time.Now().UTC().Add(5 * time.Minute)
		exec.SetExecutionDeadline(&original)
		if err := inst.RecoverExecution(ctx, exec, recoveryBlueprint("cap"), nil); err != nil {
			t.Fatalf("RecoverExecution: %v", err)
		}
		got := exec.ExecutionDeadline()
		if got == nil || !got.Equal(original) {
			t.Fatalf("deadline = %v, want preserved %v", got, original)
		}
	})
}

// TestRecoverExecutionRepeatedRestartsKeepOneDeadlineIndexEntry reproduces the
// review finding at the instance boundary: a legacy pending snapshot has no
// durable deadline, so every restart re-stamps one while Adopt leaves the
// snapshot unchanged. The durable index must still hold exactly the newest
// entry, with no entries from earlier restarts.
func TestRecoverExecutionRepeatedRestartsKeepOneDeadlineIndexEntry(t *testing.T) {
	ctx := context.Background()
	raw := newTestStore(t)
	policy := runtimeconfig.ExecutionPolicy{
		WaitDeadlinePolicy: "fail",
		MaxLifetime:        time.Hour,
		TimerPollInterval:  time.Second,
		TimerPollBatchSize: 8,
	}
	key := protocol.InstanceKey{AssemblyID: "sys_restart_deadline", Version: "1.0.0", Hash: "restart_deadline_hash"}
	assembly := recoveryAssembly(key, "cap")
	const instanceID = "inst_restart_deadline"

	// A legacy pending snapshot predates the deadline policy: Adopt adds it to
	// the index but deliberately does not write the snapshot back.
	legacy, err := execution.NewExecution(recoveryBlueprint("cap"), core.NewID("corr_"), instanceID, key.AssemblyID, key.Version, key.Hash, key.Env)
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	if err := execution.NewExecutionStore(raw).Add(ctx, legacy); err != nil {
		t.Fatalf("store.Add: %v", err)
	}

	var stamped *time.Time
	for restart := 1; restart <= 3; restart++ {
		// A new instance over the same durable store models a process restart.
		inst, err := New(ctx, instanceID, key, &assembly, 1, raw,
			WithDurability(DurabilityDurable), WithExecutionPolicy(policy))
		if err != nil {
			t.Fatalf("restart %d New: %v", restart, err)
		}
		loaded, err := execution.NewExecutionStore(raw).Get(ctx, legacy.ID)
		if err != nil {
			t.Fatalf("restart %d load durable execution: %v", restart, err)
		}
		if loaded.ExecutionDeadline() != nil {
			t.Fatalf("restart %d loaded a durable deadline; fixture is invalid", restart)
		}
		if err := inst.RecoverExecution(ctx, loaded, recoveryBlueprint("cap"), nil); err != nil {
			t.Fatalf("restart %d RecoverExecution: %v", restart, err)
		}
		stamped = loaded.ExecutionDeadline()
		if stamped == nil {
			t.Fatalf("restart %d did not stamp a maximum-lifetime deadline", restart)
		}
		// Do not call Stop: shutdown sweeps terminalize pending executions and
		// a crash restart does not run that sweep either.
		if restart < 3 {
			time.Sleep(5 * time.Millisecond)
		}
	}

	// Read durable state directly rather than the last adopted object.
	execKey, err := storage.Key("executions", string(legacy.ID))
	if err != nil {
		t.Fatalf("execution key: %v", err)
	}
	data, err := raw.Get(ctx, execKey)
	if err != nil {
		t.Fatalf("read durable execution: %v", err)
	}
	durable, err := execution.UnmarshalExecution(data)
	if err != nil {
		t.Fatalf("decode durable execution: %v", err)
	}
	if durable.ExecutionDeadline() != nil {
		t.Fatalf("durable snapshot deadline = %v, want nil after three restarts", durable.ExecutionDeadline())
	}

	prefix, err := storage.ExecutionDeadlinePrefix(instanceID)
	if err != nil {
		t.Fatalf("deadline prefix: %v", err)
	}
	keys, err := raw.List(ctx, prefix)
	if err != nil {
		t.Fatalf("list durable deadline index: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("durable deadline index after three restarts = %v, want exactly one entry", keys)
	}
	_, gotDue, gotID, err := storage.ParseExecutionDeadlineKey(keys[0])
	if err != nil {
		t.Fatalf("parse deadline index key %q: %v", keys[0], err)
	}
	if gotID != string(legacy.ID) || !gotDue.Equal(*stamped) {
		t.Fatalf("durable deadline index = (%s, %v), want current (%s, %v)", gotID, gotDue, legacy.ID, *stamped)
	}
}

// pollRecordingStore records the instance-scoped List prefixes a timer tick
// performs. Discovery now goes through dedicated indexes, so observing List is
// the stable boundary for proving which polls ran, which instance they read, and
// that no global execution listing was used.
type pollRecordingStore struct {
	*sqlite.Store
	mu        sync.Mutex
	lists     map[string]int
	afterList func(prefix string)
}

func (s *pollRecordingStore) List(ctx context.Context, prefix string) ([]string, error) {
	s.mu.Lock()
	if s.lists == nil {
		s.lists = make(map[string]int)
	}
	s.lists[prefix]++
	after := s.afterList
	s.mu.Unlock()

	if after != nil {
		after(prefix)
	}
	return s.Store.List(ctx, prefix)
}

func (s *pollRecordingStore) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lists = make(map[string]int)
}

func (s *pollRecordingStore) count(prefix string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lists[prefix]
}

func (s *pollRecordingStore) prefixes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.lists))
	for prefix := range s.lists {
		out = append(out, prefix)
	}
	return out
}

// cancelledTickContext reports cancellation through Err but never closes Done,
// forcing a ticker-driven loop to reach its tick branch before the guard runs.
type cancelledTickContext struct{}

func (cancelledTickContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (cancelledTickContext) Done() <-chan struct{}       { return nil }
func (cancelledTickContext) Err() error                  { return context.Canceled }
func (cancelledTickContext) Value(any) any               { return nil }

// cancelAfterWaitDiscoveryContext becomes cancelled once the first
// instance-scoped wait-timer discovery has run. Its bounded context also lets an
// unfixed loop terminate, so the test fails on the recorded prefixes rather than
// hanging until the test timeout.
type cancelAfterWaitDiscoveryContext struct {
	context.Context
	cancelled atomic.Bool
}

func (c *cancelAfterWaitDiscoveryContext) Err() error {
	if c.cancelled.Load() {
		return context.Canceled
	}
	return c.Context.Err()
}

func timerPollShutdownInstance(t *testing.T, recording *pollRecordingStore) *Instance {
	t.Helper()
	key := protocol.InstanceKey{AssemblyID: "inst_poll_shutdown", Version: "1.0.0", Hash: "poll_shutdown_hash"}
	assembly := recoveryAssembly(key, "cap")
	policy := runtimeconfig.ExecutionPolicy{TimerPollInterval: core.MinTimerPollInterval, TimerPollBatchSize: 8}
	inst, err := New(context.Background(), "inst_poll_shutdown", key, &assembly, 1, recording,
		WithDurability(DurabilityDurable), WithExecutionPolicy(policy))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	recording.reset()
	return inst
}

func scopedDiscoveryPrefixes(t *testing.T, instanceID string) (string, string) {
	t.Helper()
	waitPrefix, err := storage.WaitDuePrefix(instanceID)
	if err != nil {
		t.Fatalf("WaitDuePrefix: %v", err)
	}
	deadlinePrefix, err := storage.ExecutionDeadlinePrefix(instanceID)
	if err != nil {
		t.Fatalf("ExecutionDeadlinePrefix: %v", err)
	}
	return waitPrefix, deadlinePrefix
}

// TestPollTimersStopsBeforeWorkAfterCancellation proves a tick that observes an
// already-cancelled context starts no instance-scoped discovery at all.
func TestPollTimersStopsBeforeWorkAfterCancellation(t *testing.T) {
	recording := &pollRecordingStore{Store: newTestStore(t)}
	inst := timerPollShutdownInstance(t, recording)

	returned := make(chan struct{})
	go func() {
		inst.pollTimers(cancelledTickContext{})
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("pollTimers kept polling with an already-cancelled context")
	}
	if got := recording.prefixes(); len(got) != 0 {
		t.Fatalf("discovery prefixes after cancellation = %v, want none", got)
	}
}

// TestPollTimersStopsBeforeExpiryAfterCancellation proves cancellation after
// wait-timer discovery prevents deadline-expiry discovery on the same tick.
func TestPollTimersStopsBeforeExpiryAfterCancellation(t *testing.T) {
	base, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ctx := &cancelAfterWaitDiscoveryContext{Context: base}
	recording := &pollRecordingStore{Store: newTestStore(t)}
	inst := timerPollShutdownInstance(t, recording)
	waitPrefix, deadlinePrefix := scopedDiscoveryPrefixes(t, inst.ID)
	recording.afterList = func(prefix string) {
		if prefix == waitPrefix {
			ctx.cancelled.Store(true)
		}
	}

	inst.pollTimers(ctx)

	if got := recording.count(waitPrefix); got == 0 {
		t.Fatal("expected the first instance-scoped wait-timer discovery before cancellation")
	}
	if got := recording.count(deadlinePrefix); got != 0 {
		t.Fatalf("deadline-expiry discovery after cancellation = %d, want 0", got)
	}
}

// TestPollTimersScopesDiscoveryToInstance proves both host polls carry this
// instance's identity and neither falls back to a global execution listing.
func TestPollTimersScopesDiscoveryToInstance(t *testing.T) {
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	recording := &pollRecordingStore{Store: newTestStore(t)}
	inst := timerPollShutdownInstance(t, recording)
	waitPrefix, deadlinePrefix := scopedDiscoveryPrefixes(t, inst.ID)
	recording.afterList = func(prefix string) {
		if prefix == deadlinePrefix {
			cancel()
		}
	}

	inst.pollTimers(base)

	if got := recording.count(waitPrefix); got == 0 {
		t.Fatalf("wait-timer discovery prefix %q was not read", waitPrefix)
	}
	if got := recording.count(deadlinePrefix); got == 0 {
		t.Fatalf("deadline discovery prefix %q was not read", deadlinePrefix)
	}
	executionsPrefix, err := storage.Prefix("executions")
	if err != nil {
		t.Fatalf("Prefix(executions): %v", err)
	}
	if got := recording.count(executionsPrefix); got != 0 {
		t.Fatalf("global execution listing count = %d, want 0", got)
	}
	for _, prefix := range recording.prefixes() {
		if strings.HasPrefix(prefix, "wait_due/") && prefix != waitPrefix {
			t.Fatalf("foreign wait-timer discovery prefix %q", prefix)
		}
		if strings.HasPrefix(prefix, "execution_deadlines/") && prefix != deadlinePrefix {
			t.Fatalf("foreign deadline discovery prefix %q", prefix)
		}
	}
}
