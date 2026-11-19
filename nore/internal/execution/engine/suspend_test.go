package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
	"github.com/neuron-runtime/neuron/nore/internal/event"
	exec "github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/types"
	core "github.com/neuron-runtime/neuron/shared/types/core"
)

// suspendingRuntime returns a suspend outcome rather than an error. A caller
// that looks only at the error would mistake this for an empty success.
type suspendingRuntime struct{}

func (suspendingRuntime) Execute(_ context.Context, _ contracts.ExecutionContext) (contracts.CapabilityOutcome, error) {
	return contracts.CapabilityOutcome{
		Suspend: &contracts.CapabilitySuspend{
			CorrelationKey: "wait-for-approval",
			WaitType:       exec.WaitTypeSignal,
			ExpectedSignal: "approval",
			PayloadSchema:  `{"approved":true}`,
		},
	}, nil
}

func suspendBlueprint(capabilityID core.ID) *types.ExecutionBlueprint {
	return &types.ExecutionBlueprint{
		Metadata: core.Metadata{ID: "bp", Name: "suspend", Version: "1.0.0"},
		Nodes: map[core.ID]types.ExecutionNode{capabilityID: {
			Capability: core.Capability{
				Metadata: core.Metadata{ID: capabilityID, Name: string(capabilityID), Version: "1.0.0"},
				Type:     core.CapabilityRuntimeType("test:suspend"),
			},
			Configurations: emptyConfigurations{},
		}},
		EntryCapabilityIDs: []core.ID{capabilityID},
	}
}

// TestEngineFailsClosedOnSuspendWithoutLifecycle proves a suspend cannot be
// mistaken for a completion. Until a wait lifecycle exists there is no honest
// execution state to enter, so the engine must fail with a sentinel that names
// the missing lifecycle rather than complete with the empty result.
func TestEngineFailsClosedOnSuspendWithoutLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus := event.NewBus()
	store := exec.NewMemoryStore()
	scopes := exec.NewScopeRegistry(ctx)

	engineInstance, err := NewCapabilityRuntimeEngine(bus, singleRuntimeRegistry{runtime: suspendingRuntime{}}, store, scopes, 2, time.Second)
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}
	go func() { _ = engineInstance.Run(ctx) }()

	execution, err := exec.NewExecution(suspendBlueprint("suspender"), core.NewID("corr_"), "inst_1", "", "", "", "")
	if err != nil {
		t.Fatalf("NewExecution: %v", err)
	}
	if err := store.Add(context.Background(), execution); err != nil {
		t.Fatalf("store.Add: %v", err)
	}

	subscription, err := bus.SubscribeExecution(execution.ID, 32)
	if err != nil {
		t.Fatalf("SubscribeExecution: %v", err)
	}
	defer func() { _ = subscription.Close() }()

	if err := execution.Start(map[string]any{}, 1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	input := execution.Params("suspender")
	if err := execution.MarkCapabilityReady("suspender", input); err != nil {
		t.Fatalf("MarkCapabilityReady: %v", err)
	}
	if err := bus.Publish(ctx, event.New(event.ExecutionStarted, execution.ID, execution.CorrelationID, "", event.ExecutionStartedPayload{Params: map[string]any{}})); err != nil {
		t.Fatalf("publish ExecutionStarted: %v", err)
	}
	if err := bus.Publish(ctx, event.New(event.CapabilityReady, execution.ID, execution.CorrelationID, "suspender", event.CapabilityReadyPayload{Params: input})); err != nil {
		t.Fatalf("publish CapabilityReady: %v", err)
	}

	failed := false
	deadline := time.After(5 * time.Second)
	for !failed {
		select {
		case received, open := <-subscription.Events():
			if !open {
				t.Fatal("subscription closed before the capability reported an outcome")
			}
			if received.Metadata.CapabilityID != "suspender" {
				continue
			}
			switch received.Type {
			case event.CapabilityCompleted:
				t.Fatalf("a suspend was published as a completion: %+v", received.Payload)
			case event.CapabilityCancelled:
				t.Fatalf("a suspend was published as a cancellation: %+v", received.Payload)
			case event.CapabilityFailed:
				payload, ok := received.Payload.(event.CapabilityFailedPayload)
				if !ok {
					t.Fatalf("failure payload has unexpected type %T", received.Payload)
				}
				if !strings.Contains(payload.Message, ErrSuspendLifecycleUnavailable.Error()) {
					t.Errorf("failure message = %q, want it to name %q", payload.Message, ErrSuspendLifecycleUnavailable.Error())
				}
				state := execution.CapabilityState("suspender")
				if state.Status != exec.CapabilityFailed {
					t.Errorf("capability status = %q, want %q", state.Status, exec.CapabilityFailed)
				}
				if !strings.Contains(state.Error, ErrSuspendLifecycleUnavailable.Error()) {
					t.Errorf("recorded error = %q, want it to name the missing lifecycle", state.Error)
				}
				failed = true
			}
		case <-deadline:
			t.Fatal("the capability never reported an outcome")
		}
	}

	// No completion may follow the failure: a suspend with an empty result must
	// never be recorded as a successful capability.
	select {
	case received, open := <-subscription.Events():
		if open && received.Type == event.CapabilityCompleted {
			t.Fatal("a completion was published after the suspend failure")
		}
	case <-time.After(200 * time.Millisecond):
	}
}
