package engine

import (
	"context"
	"errors"
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

// identityClaimCommitter records the generation used for every claim and its
// completion. It accepts each claim so the test can drive many captures while a
// peer rotates the shared generation in another goroutine.
type identityClaimCommitter struct {
	mu        sync.Mutex
	claimed   []core.ID
	completed []core.ID
}

func (c *identityClaimCommitter) SaveTransition(context.Context, *exec.Execution, []*exec.DispatchIntent) error {
	return nil
}

func (c *identityClaimCommitter) ClaimDispatchIntent(_ context.Context, intent *exec.DispatchIntent, _ string, _ time.Duration) (*exec.DispatchIntent, bool, error) {
	c.mu.Lock()
	c.claimed = append(c.claimed, intent.AttemptID)
	c.mu.Unlock()
	return intent, true, nil
}

func (c *identityClaimCommitter) ReleaseDispatchIntent(context.Context, core.ID, core.ID, core.ID, string) error {
	return nil
}

func (c *identityClaimCommitter) CompleteDispatchIntent(_ context.Context, _, _, attemptID core.ID, _ string) error {
	c.mu.Lock()
	c.completed = append(c.completed, attemptID)
	c.mu.Unlock()
	return nil
}

func (c *identityClaimCommitter) snapshot() ([]core.ID, []core.ID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]core.ID(nil), c.claimed...), append([]core.ID(nil), c.completed...)
}

// retryIdentityRuntime records the wire identity of every attempt and fails the
// first one, so an inline retry must reuse the invocation's captured identity.
type retryIdentityRuntime struct {
	mu       sync.Mutex
	contexts []contracts.ExecutionContext
}

func (r *retryIdentityRuntime) Execute(_ context.Context, executionContext contracts.ExecutionContext) (contracts.CapabilityOutcome, error) {
	r.mu.Lock()
	r.contexts = append(r.contexts, executionContext)
	call := len(r.contexts)
	r.mu.Unlock()
	if call == 1 {
		return contracts.CapabilityOutcome{}, errors.New("transient")
	}
	return contracts.CapabilityOutcome{Result: map[string]any{}}, nil
}

func (r *retryIdentityRuntime) snapshots() []contracts.ExecutionContext {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]contracts.ExecutionContext(nil), r.contexts...)
}

// TestEngineAttemptIdentityCapturedUnderConcurrentRotation proves ADR 0010 D3:
// resolving a retry rotates AttemptID under the domain lock while the engine
// captures claim and wire identity. Each claim and each invocation must keep the
// generation it captured, and neither capture may read the field unsynchronized.
func TestEngineAttemptIdentityCapturedUnderConcurrentRotation(t *testing.T) {
	const capabilityID, peerID = core.ID("cap"), core.ID("peer")

	bp := waitBlueprint(capabilityID, runtimeconfig.Resolve(&core.RuntimeConfig{
		Retry: &core.RuntimeRetry{Policy: core.RetryPolicyFixed, MaxAttempts: 2, InitialBackoff: "1ms"},
	}))
	bp.Nodes[peerID] = types.ExecutionNode{
		Capability: core.Capability{
			Metadata: core.Metadata{ID: peerID, Name: string(peerID), Version: "1.0.0"},
			Type:     core.CapabilityRuntimeType("test:peer"),
		},
		Configurations: emptyConfigurations{},
	}
	bp.EntryCapabilityIDs = append(bp.EntryCapabilityIDs, peerID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bus := event.NewBus()
	store := exec.NewMemoryStore()
	scopes := exec.NewScopeRegistry(ctx)
	runtime := &retryIdentityRuntime{}
	committer := &identityClaimCommitter{}
	engineInstance, err := NewCapabilityRuntimeEngine(bus, singleRuntimeRegistry{runtime: runtime}, store, scopes, 4, time.Second, WithTransitionCommitter(committer))
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	execution, err := exec.NewExecution(bp, core.NewID("corr_"), "inst_1", "", "", "", "")
	if err != nil {
		t.Fatalf("new execution: %v", err)
	}
	if err := store.Add(context.Background(), execution); err != nil {
		t.Fatalf("store.Add: %v", err)
	}
	if err := execution.Start(map[string]any{}, 2); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := execution.MarkCapabilityReady(capabilityID, map[string]any{}); err != nil {
		t.Fatalf("MarkCapabilityReady: %v", err)
	}
	if err := execution.BeginRetryWait(peerID, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("BeginRetryWait: %v", err)
	}
	_, logicalID := execution.AttemptIdentity()

	stop := make(chan struct{})
	var stopOnce sync.Once
	var rotatedOnce sync.Once
	rotated := make(chan struct{})
	rotationErr := make(chan error, 1)
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if err := execution.ResumeRetryWait(peerID); err != nil {
				rotationErr <- err
				return
			}
			rotatedOnce.Do(func() { close(rotated) })
			if err := execution.BeginRetryWait(peerID, time.Now().UTC().Add(time.Hour)); err != nil {
				rotationErr <- err
				return
			}
		}
	}()
	stopRotation := func() {
		stopOnce.Do(func() { close(stop) })
		workers.Wait()
	}
	defer stopRotation()

	select {
	case <-rotated:
	case <-time.After(5 * time.Second):
		t.Fatal("peer never rotated the generation")
	}

	for i := 0; i < 100; i++ {
		release, claimed, err := engineInstance.claimDispatch(ctx, execution, capabilityID, invocation{})
		if err != nil || !claimed {
			t.Fatalf("claimDispatch #%d = (claimed %v, err %v), want a claim", i, claimed, err)
		}
		release()
	}

	engineInstance.executeCapability(ctx, event.New(event.CapabilityReady, execution.ID, execution.CorrelationID, capabilityID, event.CapabilityReadyPayload{Params: execution.Params(capabilityID)}))
	if state := execution.CapabilityState(capabilityID); state.Status != exec.CapabilityCompleted {
		t.Fatalf("capability status = %q, want %q", state.Status, exec.CapabilityCompleted)
	}

	stopRotation()
	select {
	case err := <-rotationErr:
		t.Fatalf("peer rotation failed: %v", err)
	default:
	}

	claimed, completed := committer.snapshot()
	if len(claimed) == 0 || len(claimed) != len(completed) {
		t.Fatalf("claim/completion records = %d/%d, want equal non-empty sets", len(claimed), len(completed))
	}
	for i := range claimed {
		if claimed[i] == "" || claimed[i] != completed[i] {
			t.Fatalf("claim %d used %q and completion used %q; both must be the captured generation", i, claimed[i], completed[i])
		}
	}

	contexts := runtime.snapshots()
	if len(contexts) != 2 {
		t.Fatalf("runtime attempts = %d, want the failure and its inline retry", len(contexts))
	}
	if contexts[0].AttemptID == "" || contexts[0].AttemptID != contexts[1].AttemptID {
		t.Fatalf("wire attempt IDs = %q/%q, want one stable invocation identity", contexts[0].AttemptID, contexts[1].AttemptID)
	}
	if contexts[0].LogicalOperationID != logicalID || contexts[1].LogicalOperationID != logicalID {
		t.Fatalf("wire logical IDs = %q/%q, want %q", contexts[0].LogicalOperationID, contexts[1].LogicalOperationID, logicalID)
	}
}
