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

func (r *gatedRuntime) Execute(ctx context.Context, _ contracts.ExecutionContext) (contracts.CapabilityOutcome, error) {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	select {
	case r.started <- struct{}{}:
	default:
	}
	select {
	case <-r.release:
		return contracts.CapabilityOutcome{Result: map[string]any{}}, nil
	case <-ctx.Done():
		return contracts.CapabilityOutcome{}, ctx.Err()
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
	attemptID, _ := execution.AttemptIdentity()
	committer.allow(execution.ID, "cap", attemptID)

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

// rotatingClaimCommitter models a peer capability resuming between a claim and
// its deferred completion. The peer rotation changes the execution's attempt
// generation while this worker still owns the intent it originally claimed.
type rotatingClaimCommitter struct {
	*claimCommitter
	execution       *exec.Execution
	claimAttempt    core.ID
	completeAttempt core.ID
}

func (c *rotatingClaimCommitter) ClaimDispatchIntent(ctx context.Context, intent *exec.DispatchIntent, owner string, lease time.Duration) (*exec.DispatchIntent, bool, error) {
	claimed, won, err := c.claimCommitter.ClaimDispatchIntent(ctx, intent, owner, lease)
	if err != nil || !won {
		return claimed, won, err
	}
	c.claimAttempt = intent.AttemptID
	if err := c.execution.ResumeRetryWait("peer"); err != nil {
		return nil, false, err
	}
	return claimed, won, nil
}

func (c *rotatingClaimCommitter) CompleteDispatchIntent(ctx context.Context, executionID, capabilityID, attemptID core.ID, owner string) error {
	c.completeAttempt = attemptID
	return c.claimCommitter.CompleteDispatchIntent(ctx, executionID, capabilityID, attemptID, owner)
}

// TestClaimDispatchKeepsClaimedAttemptAcrossPeerRotation proves ADR 0010 D3:
// completion addresses the generation that was claimed even when another
// capability rotates the shared execution before the worker finishes.
func TestClaimDispatchKeepsClaimedAttemptAcrossPeerRotation(t *testing.T) {
	bp := claimBlueprint("cap")
	bp.Nodes["peer"] = types.ExecutionNode{
		Capability: core.Capability{
			Metadata: core.Metadata{ID: "peer", Name: "peer", Version: "1.0.0"},
			Type:     core.CapabilityRuntimeType("test:rotation"),
		},
		Configurations: emptyConfigurations{},
	}
	bp.EntryCapabilityIDs = append(bp.EntryCapabilityIDs, "peer")

	execution, err := exec.NewExecution(bp, core.NewID("corr_"), "inst_1", "", "", "", "")
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	if err := execution.Start(map[string]any{}, 2); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := execution.MarkCapabilityReady("cap", map[string]any{}); err != nil {
		t.Fatalf("MarkCapabilityReady: %v", err)
	}
	if err := execution.MarkCapabilityRunning("cap"); err != nil {
		t.Fatalf("MarkCapabilityRunning: %v", err)
	}
	if err := execution.BeginRetryWait("peer", time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("BeginRetryWait: %v", err)
	}
	prior, _ := execution.AttemptIdentity()

	committer := &rotatingClaimCommitter{claimCommitter: newClaimCommitter(), execution: execution}
	committer.allow(execution.ID, "cap", prior)
	engineInstance := &CapabilityRuntimeEngine{committer: committer, owner: "engine_test"}

	release, claimed, err := engineInstance.claimDispatch(context.Background(), execution, "cap", invocation{})
	if err != nil || !claimed {
		t.Fatalf("claimDispatch = (claimed %v, err %v), want a claim", claimed, err)
	}
	if current, _ := execution.AttemptIdentity(); current == prior {
		t.Fatal("peer resume did not rotate the generation")
	}
	release()
	if committer.claimAttempt != prior {
		t.Fatalf("claimed attempt = %q, want %q", committer.claimAttempt, prior)
	}
	if committer.completeAttempt != committer.claimAttempt {
		t.Fatalf("completed attempt = %q, want the claimed attempt %q", committer.completeAttempt, committer.claimAttempt)
	}
	_, orphaned, err := committer.ClaimDispatchIntent(context.Background(), exec.NewDispatchIntent(execution.ID, "cap", committer.claimAttempt), "probe", time.Minute)
	if err != nil || orphaned {
		t.Fatalf("claimed generation still has a ready entry: ok=%v err=%v", orphaned, err)
	}
}
