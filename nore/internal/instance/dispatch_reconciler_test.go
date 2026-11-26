package instance

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/event"
	"github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/execution/scheduler"
	"github.com/neuron-runtime/neuron/nore/internal/runtimeconfig"
	"github.com/neuron-runtime/neuron/nore/internal/storage"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

const reconcileCapabilityID shared.ID = "cap"

func reconcilePolicy(t *testing.T, interval string, batch int) runtimeconfig.ExecutionPolicy {
	t.Helper()
	policy, err := runtimeconfig.ResolveExecutionPolicy(runtimeconfig.ExecutionPolicyInput{
		TimerPollInterval:  interval,
		TimerPollBatchSize: batch,
	})
	if err != nil {
		t.Fatalf("resolve execution policy: %v", err)
	}
	return policy
}

func newReconcileInstance(
	t *testing.T,
	ctx context.Context,
	store storage.Store,
	id string,
	key protocol.InstanceKey,
	durability string,
	policy runtimeconfig.ExecutionPolicy,
) *Instance {
	t.Helper()
	assembly := recoveryAssembly(key, reconcileCapabilityID)
	inst, err := New(ctx, id, key, &assembly, 1, store, WithDurability(durability), WithExecutionPolicy(policy))
	if err != nil {
		t.Fatalf("create %s instance %s: %v", durability, id, err)
	}
	t.Cleanup(func() { _ = inst.Stop() })
	return inst
}

// seedReadyDispatch persists a durable execution owned by inst and one ready
// intent for its capability. The intent is written through the same store the
// instance hosts use, so the reconciler must discover and fence it exactly as it
// would in production.
func seedReadyDispatch(t *testing.T, ctx context.Context, store *execution.ExecutionStore, inst *Instance, params map[string]any) *execution.DispatchIntent {
	t.Helper()
	exec, err := execution.NewExecution(
		recoveryBlueprint(reconcileCapabilityID),
		shared.NewID("request_"),
		shared.ID(inst.ID),
		inst.Key.AssemblyID, inst.Key.Version, inst.Key.Hash, inst.Key.Env,
	)
	if err != nil {
		t.Fatalf("new execution: %v", err)
	}
	if err := exec.Start(params, 1); err != nil {
		t.Fatalf("start execution: %v", err)
	}
	if err := exec.MarkCapabilityReady(reconcileCapabilityID, params); err != nil {
		t.Fatalf("mark capability ready: %v", err)
	}
	if err := store.Add(ctx, exec); err != nil {
		t.Fatalf("persist execution: %v", err)
	}
	intent := execution.NewDispatchIntent(exec.ID, reconcileCapabilityID, exec.AttemptID)
	if err := store.EnqueueDispatchIntent(ctx, intent); err != nil {
		t.Fatalf("enqueue dispatch intent: %v", err)
	}
	return intent
}

func subscribeReconcileReady(t *testing.T, inst *Instance) event.Subscription {
	t.Helper()
	sub, err := inst.Bus().Subscribe(event.CapabilityReady, 16)
	if err != nil {
		t.Fatalf("subscribe CapabilityReady: %v", err)
	}
	t.Cleanup(func() { _ = sub.Close() })
	return sub
}

func drainReconcileReady(t *testing.T, sub event.Subscription) []event.Event {
	t.Helper()
	got := make([]event.Event, 0)
	for {
		select {
		case evt, open := <-sub.Events():
			if !open {
				return got
			}
			got = append(got, evt)
		default:
			return got
		}
	}
}

// assertReconcileIntentDiscoverable proves the reconciler released its claim and
// never completed the intent: the engine still owns completion after dispatch.
func assertReconcileIntentDiscoverable(t *testing.T, store storage.Store, want *execution.DispatchIntent, label string) {
	t.Helper()
	var found *execution.DispatchIntent
	for _, intent := range recoveredWaitDispatchIntents(t, store) {
		if intent.ExecutionID == want.ExecutionID && intent.CapabilityID == want.CapabilityID && intent.AttemptID == want.AttemptID {
			found = intent
			break
		}
	}
	if found == nil {
		t.Fatalf("%s: intent %+v was completed", label, want)
	}
	if !found.ReadyToRun || found.Owner != "" || found.LeaseExpiresAt != nil {
		t.Fatalf("%s: intent = %+v, want ready and unclaimed", label, found)
	}
}

// TestDurableInstancesReconcileOnlyTheirOwnReadyIntents exercises the instance-
// owned reconciler against one real durable store with two instance buses. Each
// pass must claim only its own instance's intents, release them before publishing
// on its own bus, and leave foreign records untouched.
func TestDurableInstancesReconcileOnlyTheirOwnReadyIntents(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	policy := reconcilePolicy(t, "1s", 8)
	keyA := protocol.InstanceKey{AssemblyID: "sys_reconcile_a", Version: "1.0.0", Hash: "hash_a"}
	keyB := protocol.InstanceKey{AssemblyID: "sys_reconcile_b", Version: "1.0.0", Hash: "hash_b"}

	instA := newReconcileInstance(t, ctx, store, "inst_reconcile_a", keyA, DurabilityDurable, policy)
	instB := newReconcileInstance(t, ctx, store, "inst_reconcile_b", keyB, DurabilityDurable, policy)
	if instA.dispatchReconciler == nil || instB.dispatchReconciler == nil {
		t.Fatal("durable instances must own a dispatch reconciler")
	}

	execStore := execution.NewExecutionStore(store)
	intentA := seedReadyDispatch(t, ctx, execStore, instA, map[string]any{"seed": "own"})
	intentB := seedReadyDispatch(t, ctx, execStore, instB, map[string]any{"secret": "foreign"})

	subA := subscribeReconcileReady(t, instA)
	subB := subscribeReconcileReady(t, instB)

	if err := instA.dispatchReconciler.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile instance A: %v", err)
	}
	eventsA := drainReconcileReady(t, subA)
	if len(eventsA) != 1 {
		t.Fatalf("instance A published %d CapabilityReady events, want 1: %v", len(eventsA), eventsA)
	}
	got := eventsA[0]
	if got.Type != event.CapabilityReady || got.Metadata.ExecutionID != intentA.ExecutionID || got.Metadata.CapabilityID != reconcileCapabilityID {
		t.Fatalf("instance A published %+v, want CapabilityReady for %s on %s", got, reconcileCapabilityID, intentA.ExecutionID)
	}
	payload, ok := got.Payload.(event.CapabilityReadyPayload)
	if !ok {
		t.Fatalf("payload type = %T, want event.CapabilityReadyPayload", got.Payload)
	}
	if payload.Params["seed"] != "own" {
		t.Fatalf("instance A published params %v, want the owning execution's input", payload.Params)
	}
	if foreign := drainReconcileReady(t, subB); len(foreign) != 0 {
		t.Fatalf("instance B bus received %d foreign events: %v", len(foreign), foreign)
	}
	assertReconcileIntentDiscoverable(t, store, intentA, "own intent after instance A pass")
	assertReconcileIntentDiscoverable(t, store, intentB, "foreign intent after instance A pass")

	if err := instB.dispatchReconciler.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile instance B: %v", err)
	}
	if later := drainReconcileReady(t, subA); len(later) != 0 {
		t.Fatalf("instance A bus received %d events from instance B's pass: %v", len(later), later)
	}
	eventsB := drainReconcileReady(t, subB)
	if len(eventsB) != 1 {
		t.Fatalf("instance B published %d CapabilityReady events, want 1: %v", len(eventsB), eventsB)
	}
	if eventsB[0].Metadata.ExecutionID != intentB.ExecutionID {
		t.Fatalf("instance B published execution %s, want %s", eventsB[0].Metadata.ExecutionID, intentB.ExecutionID)
	}
	payloadB, ok := eventsB[0].Payload.(event.CapabilityReadyPayload)
	if !ok {
		t.Fatalf("instance B payload type = %T, want event.CapabilityReadyPayload", eventsB[0].Payload)
	}
	if payloadB.Params["secret"] != "foreign" {
		t.Fatalf("instance B published params %v, want its own execution's input", payloadB.Params)
	}
	assertReconcileIntentDiscoverable(t, store, intentA, "instance A intent after instance B pass")
	assertReconcileIntentDiscoverable(t, store, intentB, "instance B intent after instance B pass")
}

// TestDispatchReconcilerWiringFollowsDurabilityAndPolicy proves the host creates
// the loop only for durable instances and takes cadence, batch and fencing lease
// from the resolved policy and the scheduler's validated authority.
func TestDispatchReconcilerWiringFollowsDurabilityAndPolicy(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	key := protocol.InstanceKey{AssemblyID: "sys_reconcile_policy", Version: "1.0.0"}
	policy := reconcilePolicy(t, "250ms", 9)

	durable := newReconcileInstance(t, ctx, store, "inst_reconcile_policy", key, DurabilityDurable, policy)
	if durable.dispatchReconciler == nil {
		t.Fatal("durable instance has no dispatch reconciler")
	}
	cfg := durable.dispatchReconciler.Config()
	if cfg.InstanceID != shared.ID(durable.ID) {
		t.Errorf("reconciler instance = %q, want %q", cfg.InstanceID, durable.ID)
	}
	if cfg.Interval != policy.TimerPollInterval || cfg.BatchSize != policy.TimerPollBatchSize {
		t.Errorf("reconciler bounds = (%s, %d), want (%s, %d)", cfg.Interval, cfg.BatchSize, policy.TimerPollInterval, policy.TimerPollBatchSize)
	}
	if cfg.Lease != scheduler.DefaultTimerPollConfig().Lease {
		t.Errorf("reconciler lease = %s, want the scheduler authority %s", cfg.Lease, scheduler.DefaultTimerPollConfig().Lease)
	}

	for _, mode := range []string{DurabilityNone, DurabilityHistory} {
		t.Run(mode, func(t *testing.T) {
			inst := newReconcileInstance(t, ctx, store, "inst_reconcile_"+mode, key, mode, policy)
			if inst.dispatchReconciler != nil {
				t.Fatalf("%s instance created a dispatch reconciler", mode)
			}
		})
	}

	t.Run("unset policy fields use the canonical defaults", func(t *testing.T) {
		inst := newReconcileInstance(t, ctx, store, "inst_reconcile_defaults", key, DurabilityDurable, runtimeconfig.ExecutionPolicy{})
		if inst.dispatchReconciler == nil {
			t.Fatal("durable instance has no dispatch reconciler")
		}
		defaults := runtimeconfig.DefaultExecutionPolicy()
		cfg := inst.dispatchReconciler.Config()
		if cfg.Interval != defaults.TimerPollInterval || cfg.BatchSize != defaults.TimerPollBatchSize {
			t.Errorf("reconciler bounds = (%s, %d), want defaults (%s, %d)", cfg.Interval, cfg.BatchSize, defaults.TimerPollInterval, defaults.TimerPollBatchSize)
		}
	})
}

// gatedReadyListStore blocks the first and later ready-index discovery calls so
// the lifecycle test can prove that only one reconciler pass can be in flight.
// It delegates every operation to a real store.
type gatedReadyListStore struct {
	storage.Store
	mu          sync.Mutex
	readyPrefix string
	gate        <-chan struct{}
	entered     chan<- struct{}
	lists       int
}

func (s *gatedReadyListStore) List(ctx context.Context, prefix string) ([]string, error) {
	if prefix == s.readyPrefix {
		s.mu.Lock()
		s.lists++
		s.mu.Unlock()
		select {
		case s.entered <- struct{}{}:
		default:
		}
		<-s.gate
	}
	return s.Store.List(ctx, prefix)
}

func (s *gatedReadyListStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lists
}

// TestDurableInstanceReconcilerLifetimeIsSingleAndStopsCleanly proves Start owns
// one reconciler loop, Stop awaits it, and no discovery happens after shutdown.
func TestDurableInstanceReconcilerLifetimeIsSingleAndStopsCleanly(t *testing.T) {
	ctx := context.Background()
	base := newTestStore(t)
	readyPrefix, err := storage.Prefix("dispatch_ready")
	if err != nil {
		t.Fatalf("ready prefix: %v", err)
	}
	gate := make(chan struct{})
	entered := make(chan struct{}, 4)
	store := &gatedReadyListStore{Store: base, readyPrefix: readyPrefix, gate: gate, entered: entered}
	policy := reconcilePolicy(t, "100ms", 4)
	key := protocol.InstanceKey{AssemblyID: "sys_reconcile_lifetime", Version: "1.0.0"}
	inst := newReconcileInstance(t, ctx, store, "inst_reconcile_lifetime", key, DurabilityDurable, policy)
	if inst.dispatchReconciler == nil {
		t.Fatal("durable instance has no dispatch reconciler")
	}

	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(gate) }) }
	defer release()

	if err := inst.Start(); err != nil {
		t.Fatalf("start instance: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("reconciler did not reach the store on its first tick")
	}
	interval := inst.dispatchReconciler.Config().Interval
	// A second loop would enter List on its own first tick while this one is
	// blocked. Three intervals is enough for that difference to be observable.
	time.Sleep(3 * interval)
	if got := store.count(); got != 1 {
		t.Fatalf("concurrent reconciler passes = %d, want 1", got)
	}

	release()
	if err := inst.Stop(); err != nil {
		t.Fatalf("stop instance: %v", err)
	}
	if inst.Status() != StatusStopped {
		t.Fatalf("status after Stop = %s, want %s", inst.Status(), StatusStopped)
	}
	afterStop := store.count()
	time.Sleep(3 * interval)
	if got := store.count(); got != afterStop {
		t.Fatalf("ready discovery continued after Stop: %d -> %d", afterStop, got)
	}
	if _, err := inst.Bus().Subscribe(event.CapabilityReady, 1); !errors.Is(err, event.ErrBusClosed) {
		t.Fatalf("subscribe after Stop error = %v, want %v", err, event.ErrBusClosed)
	}
}
