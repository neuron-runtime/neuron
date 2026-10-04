package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
	"github.com/neuron-runtime/neuron/nore/internal/event"
	executionmodel "github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/execution/engine"
	"github.com/neuron-runtime/neuron/nore/internal/types"
	core "github.com/neuron-runtime/neuron/shared/types/core"
)

// oneFailsOneBlocks runs two capabilities concurrently inside a single
// execution: `broken` fails immediately, `victim` blocks until its context is
// cancelled. Both are entry capabilities, so the scheduler genuinely has them in
// flight at the same time.
type oneFailsOneBlocks struct {
	victimStarted chan struct{}
	victimStopped chan struct{}

	once sync.Once
}

func newOneFailsOneBlocks() *oneFailsOneBlocks {
	return &oneFailsOneBlocks{
		victimStarted: make(chan struct{}, 4),
		victimStopped: make(chan struct{}, 4),
	}
}

func (r *oneFailsOneBlocks) Execute(ctx context.Context, execution contracts.ExecutionContext) (map[string]any, error) {
	if execution.Capability.Metadata.ID == "broken" {
		// Fail only once the sibling is genuinely running. Returning an error
		// immediately would race the sibling's dispatch, and the test would then
		// be testing "cancelled before it ever started" instead of "stopped
		// because a peer failed".
		select {
		case <-r.victimStarted:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return nil, errors.New("capability is genuinely broken")
	}
	r.once.Do(func() { close(r.victimStarted) })
	<-ctx.Done()
	select {
	case r.victimStopped <- struct{}{}:
	default:
	}
	return nil, ctx.Err()
}

func (r *oneFailsOneBlocks) awaitVictimStart(t *testing.T) {
	t.Helper()
	select {
	case <-r.victimStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("the sibling capability never started")
	}
}

func (r *oneFailsOneBlocks) awaitVictimStop(t *testing.T) {
	t.Helper()
	select {
	case <-r.victimStopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the sibling capability was never stopped")
	}
}

// TestSiblingStoppedByAFailureIsRecordedAsCancelled pins down how the engine
// records a capability that was stopped only because another capability in the
// same execution failed.
//
// The distinction matters: a capability the author wrote correctly is not
// broken. Cancelling an execution records its untouched capabilities as
// `cancelled`, so a failure must not report the innocent sibling as `failed` --
// which both blames an implementation for a decision it did not make and, with no
// distinguishing event type, leaves a consumer no way to tell the two apart
// except by reading the message text.
func TestSiblingStoppedByAFailureIsRecordedAsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus := event.NewBus()
	store := executionmodel.NewMemoryStore()
	scopes := executionmodel.NewScopeRegistry(ctx)
	runtime := newOneFailsOneBlocks()

	scheduler, err := New(bus, store, scopes)
	if err != nil {
		t.Fatalf("New scheduler: %v", err)
	}
	engineInstance, err := engine.NewCapabilityRuntimeEngine(bus, stubRegistry{runtime: runtime}, store, scopes, 2, 10*time.Second)
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}
	go func() { _ = scheduler.Run(ctx) }()
	go func() { _ = engineInstance.Run(ctx) }()

	blueprint := &types.ExecutionBlueprint{
		Metadata: core.Metadata{ID: "bp", Name: "sibling", Version: "1.0.0"},
		Nodes: map[core.ID]types.ExecutionNode{
			"broken": stubNode("broken", core.RuntimeExecutionModeWait),
			"victim": stubNode("victim", core.RuntimeExecutionModeWait),
		},
		EntryCapabilityIDs: []core.ID{"broken", "victim"},
	}
	exec := startExecution(t, ctx, bus, store, blueprint)

	subscription := subscribeToExecution(t, bus, exec.ID)
	defer func() { _ = subscription.Close() }()

	runtime.awaitVictimStart(t)
	awaitExecutionFailure(t, subscription)
	runtime.awaitVictimStop(t)

	if state := exec.CapabilityState("broken"); state.Status != executionmodel.CapabilityFailed {
		t.Errorf("the capability that actually broke is recorded as %q, want %q", state.Status, executionmodel.CapabilityFailed)
	}

	state := exec.CapabilityState("victim")
	if state.Status != executionmodel.CapabilityCancelled {
		t.Errorf("sibling capability status = %q (error %q), want %q: it was stopped by another capability's failure and is not itself broken",
			state.Status, state.Error, executionmodel.CapabilityCancelled)
	}
}

// TestStoppedCapabilityPublishesCancellationNotFailure asserts the observable
// half of the same contract: a consumer watching the event stream receives
// capability.cancelled and never capability.failed for a capability it did not
// break.
func TestStoppedCapabilityPublishesCancellationNotFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus := event.NewBus()
	store := executionmodel.NewMemoryStore()
	scopes := executionmodel.NewScopeRegistry(ctx)
	runtime := newOneFailsOneBlocks()

	scheduler, err := New(bus, store, scopes)
	if err != nil {
		t.Fatalf("New scheduler: %v", err)
	}
	engineInstance, err := engine.NewCapabilityRuntimeEngine(bus, stubRegistry{runtime: runtime}, store, scopes, 2, 10*time.Second)
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}
	go func() { _ = scheduler.Run(ctx) }()
	go func() { _ = engineInstance.Run(ctx) }()

	blueprint := &types.ExecutionBlueprint{
		Metadata: core.Metadata{ID: "bp", Name: "sibling-events", Version: "1.0.0"},
		Nodes: map[core.ID]types.ExecutionNode{
			"broken": stubNode("broken", core.RuntimeExecutionModeWait),
			"victim": stubNode("victim", core.RuntimeExecutionModeWait),
		},
		EntryCapabilityIDs: []core.ID{"broken", "victim"},
	}
	exec := startExecution(t, ctx, bus, store, blueprint)

	subscription := subscribeToExecution(t, bus, exec.ID)
	defer func() { _ = subscription.Close() }()

	runtime.awaitVictimStart(t)
	awaitExecutionFailure(t, subscription)
	runtime.awaitVictimStop(t)

	// Wait for the cancellation to be announced, then keep reading briefly: a
	// capability.failed for the same capability arriving afterwards would mean
	// both outcomes were published for one capability.
	sawCancelled := awaitCapabilityEvent(t, subscription, event.CapabilityCancelled, "victim")

	grace := time.After(250 * time.Millisecond)
	for {
		select {
		case received, open := <-subscription.Events():
			if !open {
				return
			}
			if received.Type == event.CapabilityFailed && received.Metadata.CapabilityID == "victim" {
				t.Fatalf("the stopped sibling published capability.failed as well as capability.cancelled (%v); a correct capability was reported as broken", sawCancelled)
			}
		case <-grace:
			return
		}
	}
}

// awaitCapabilityEvent reads until one event of the given type arrives for the
// given capability.
func awaitCapabilityEvent(t *testing.T, subscription event.Subscription, want event.Type, capabilityID core.ID) bool {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case received, open := <-subscription.Events():
			if !open {
				t.Fatalf("subscription closed before %s for %s arrived", want, capabilityID)
			}
			if received.Type == want && received.Metadata.CapabilityID == capabilityID {
				return true
			}
		case <-deadline:
			t.Fatalf("%s for %s never arrived", want, capabilityID)
			return false
		}
	}
}
