package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
	"github.com/neuron-runtime/neuron/nore/internal/event"
	exec "github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/types"
	core "github.com/neuron-runtime/neuron/shared/types/core"
)

// claimCommitter models the intent lifecycle of the durable store: an intent is
// claimable only while it is available and unheld, and completing it removes it.
// A claim succeeds at most once, which is exactly the guarantee the engine relies
// on to avoid dispatching the same ready capability twice.
type claimCommitter struct {
	mu        sync.Mutex
	available map[string]bool
	claimed   map[string]bool
}

func newClaimCommitter() *claimCommitter {
	return &claimCommitter{available: map[string]bool{}, claimed: map[string]bool{}}
}

func claimKey(executionID, capabilityID, attemptID core.ID) string {
	return string(executionID) + "/" + string(capabilityID) + "/" + string(attemptID)
}

func (c *claimCommitter) allow(executionID, capabilityID, attemptID core.ID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.available[claimKey(executionID, capabilityID, attemptID)] = true
}

func (c *claimCommitter) SaveTransition(context.Context, *exec.Execution, []*exec.DispatchIntent) error {
	return nil
}

func (c *claimCommitter) ClaimDispatchIntent(_ context.Context, intent *exec.DispatchIntent, _ string, _ time.Duration) (*exec.DispatchIntent, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := claimKey(intent.ExecutionID, intent.CapabilityID, intent.AttemptID)
	if !c.available[key] || c.claimed[key] {
		return nil, false, nil
	}
	c.claimed[key] = true
	return intent, true, nil
}

func (c *claimCommitter) ReleaseDispatchIntent(_ context.Context, executionID, capabilityID, attemptID core.ID, _ string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.claimed, claimKey(executionID, capabilityID, attemptID))
	return nil
}

func (c *claimCommitter) CompleteDispatchIntent(_ context.Context, executionID, capabilityID, attemptID core.ID, _ string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := claimKey(executionID, capabilityID, attemptID)
	delete(c.claimed, key)
	delete(c.available, key)
	return nil
}

// gatedRuntime blocks inside Execute until the test releases it and counts every
// invocation, so a duplicate dispatch is observable as a second call.
type gatedRuntime struct {
	mu      sync.Mutex
	calls   int
	started chan struct{}
	release chan struct{}
}

func newGatedRuntime() *gatedRuntime {
	return &gatedRuntime{started: make(chan struct{}, 8), release: make(chan struct{})}
}

func (r *gatedRuntime) Execute(ctx context.Context, _ contracts.ExecutionContext) (map[string]any, error) {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	select {
	case r.started <- struct{}{}:
	default:
	}
	select {
	case <-r.release:
		return map[string]any{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *gatedRuntime) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func claimBlueprint(capabilityID core.ID) *types.ExecutionBlueprint {
	return &types.ExecutionBlueprint{
		Metadata: core.Metadata{ID: "bp", Name: "claim", Version: "1.0.0"},
		Nodes: map[core.ID]types.ExecutionNode{capabilityID: {
			Capability: core.Capability{
				Metadata: core.Metadata{ID: capabilityID, Name: string(capabilityID), Version: "1.0.0"},
				Type:     core.CapabilityRuntimeType("test:claim"),
			},
			Configurations: emptyConfigurations{},
		}},
		EntryCapabilityIDs: []core.ID{capabilityID},
	}
}

// TestEngineDispatchesAClaimedCapabilityOnce proves that the engine treats the
// dispatch intent as the unit of ownership: two announcements of the same
// readiness are dispatched once, because the first claim holds the intent and the
// second loses it. Without the claim, a duplicated or replayed ready event would
// run the capability twice.
func TestEngineDispatchesAClaimedCapabilityOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus := event.NewBus()
	store := exec.NewMemoryStore()
	scopes := exec.NewScopeRegistry(ctx)
	runtime := newGatedRuntime()
	committer := newClaimCommitter()

	engineInstance, err := NewCapabilityRuntimeEngine(bus, singleRuntimeRegistry{runtime: runtime}, store, scopes, 4, time.Second, WithTransitionCommitter(committer))
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}

	execution, err := exec.NewExecution(claimBlueprint("cap"), core.NewID("corr_"), "inst_1", "", "", "", "")
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	if err := store.Add(context.Background(), execution); err != nil {
		t.Fatalf("store.Add: %v", err)
	}
	if err := execution.Start(map[string]any{}, 1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	input := execution.Params("cap")
	if err := execution.MarkCapabilityReady("cap", input); err != nil {
		t.Fatalf("MarkCapabilityReady: %v", err)
	}
	committer.allow(execution.ID, "cap", execution.AttemptID)

	go func() { _ = engineInstance.Run(ctx) }()

	payload := event.New(event.CapabilityReady, execution.ID, execution.CorrelationID, "cap", event.CapabilityReadyPayload{Params: input})
	if err := bus.Publish(ctx, payload); err != nil {
		t.Fatalf("publish CapabilityReady: %v", err)
	}
	if err := bus.Publish(ctx, payload); err != nil {
		t.Fatalf("publish duplicate CapabilityReady: %v", err)
	}

	select {
	case <-runtime.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the capability was never invoked")
	}

	select {
	case <-runtime.started:
		t.Fatal("the same ready capability was dispatched more than once")
	case <-time.After(200 * time.Millisecond):
	}

	if got := runtime.count(); got != 1 {
		t.Fatalf("invocations = %d, want 1", got)
	}
	close(runtime.release)
}
