package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
	"github.com/neuron-runtime/neuron/nore/internal/event"
	exec "github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/resolver"
	"github.com/neuron-runtime/neuron/nore/internal/runtimeconfig"
	"github.com/neuron-runtime/neuron/nore/internal/types"
	core "github.com/neuron-runtime/neuron/shared/types/core"
)

// startedSignal is a capability runtime that can prove it was actually invoked,
// so a test never passes without the work having run.
type startedSignal interface {
	contracts.CapabilityRuntime
	awaitStart(t *testing.T)
}

// blockingCapabilityRuntime never returns on its own, so the invocation ends only
// when a context ends.
type blockingCapabilityRuntime struct {
	started chan struct{}
	once    sync.Once
}

func newBlockingCapabilityRuntime() *blockingCapabilityRuntime {
	return &blockingCapabilityRuntime{started: make(chan struct{}, 4)}
}

func (r *blockingCapabilityRuntime) Execute(ctx context.Context, _ contracts.ExecutionContext) (map[string]any, error) {
	r.once.Do(func() { close(r.started) })
	<-ctx.Done()
	return nil, ctx.Err()
}

func (r *blockingCapabilityRuntime) awaitStart(t *testing.T) {
	t.Helper()
	select {
	case <-r.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the capability was never invoked")
	}
}

// selfTimingOutRuntime reports a deadline of its own rather than waiting for one.
// A capability runtime that enforces its own internal timeout returns a raw
// context.DeadlineExceeded while the caller's context is still live, which is
// exactly the case a `errors.Is(err, context.DeadlineExceeded)` test would
// misclassify as a capability that had been stopped.
type selfTimingOutRuntime struct {
	started chan struct{}
	once    sync.Once
}

func newSelfTimingOutRuntime() *selfTimingOutRuntime {
	return &selfTimingOutRuntime{started: make(chan struct{}, 4)}
}

func (r *selfTimingOutRuntime) Execute(_ context.Context, _ contracts.ExecutionContext) (map[string]any, error) {
	r.once.Do(func() { close(r.started) })
	return nil, context.DeadlineExceeded
}

func (r *selfTimingOutRuntime) awaitStart(t *testing.T) {
	t.Helper()
	select {
	case <-r.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the capability was never invoked")
	}
}

type singleRuntimeRegistry struct{ runtime contracts.CapabilityRuntime }

func (r singleRuntimeRegistry) Register(core.CapabilityRuntimeType, contracts.CapabilityRuntime) error {
	return nil
}

func (r singleRuntimeRegistry) Resolve(core.CapabilityRuntimeType) (contracts.CapabilityRuntime, error) {
	return r.runtime, nil
}

// emptyConfigurations stands in for the planner-compiled configuration program
// of a capability that declares no configuration templates.
type emptyConfigurations struct{}

func (emptyConfigurations) Resolve(context.Context, resolver.CapabilityEnvironment) (map[string]any, error) {
	return nil, nil
}

// timeoutBlueprint gives its single capability the declared execution timeout.
func timeoutBlueprint(capabilityID core.ID, timeout string) *types.ExecutionBlueprint {
	return &types.ExecutionBlueprint{
		Metadata: core.Metadata{ID: "bp", Name: "timeout", Version: "1.0.0"},
		Nodes: map[core.ID]types.ExecutionNode{capabilityID: {
			Capability: core.Capability{
				Metadata:      core.Metadata{ID: capabilityID, Name: string(capabilityID), Version: "1.0.0"},
				Type:          core.CapabilityRuntimeType("test:blocking"),
				RuntimeConfig: runtimeconfig.Resolve(&core.RuntimeConfig{Execution: &core.RuntimeExecution{Timeout: timeout}}),
			},
			Configurations: emptyConfigurations{},
		}},
		EntryCapabilityIDs: []core.ID{capabilityID},
	}
}

// TestCapabilityTimeoutIsAFailureNotACancellation draws the line that separates a
// fault from a consequence.
//
// The engine tells the two apart by asking whether the context the capability ran
// under was cancelled. A deadline does not cancel that context -- it ends the
// attempt -- so both forms of timeout must still be recorded as failures.
// Classifying either as a cancellation would silently reclassify every slow
// capability in a system as merely stopped, and nobody would be left to fix them.
func TestCapabilityTimeoutIsAFailureNotACancellation(t *testing.T) {
	cases := []struct {
		name    string
		timeout string
		runtime startedSignal
	}{
		{
			// N.O.R.E.'s own timeout fires while the capability is still running.
			name:    "timeout imposed by the runtime",
			timeout: "60ms",
			runtime: newBlockingCapabilityRuntime(),
		},
		{
			// The capability runtime enforces its own deadline and reports it
			// directly, while N.O.R.E.'s longer timeout never fires.
			name:    "timeout reported by the capability runtime",
			timeout: "30s",
			runtime: newSelfTimingOutRuntime(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			bus := event.NewBus()
			store := exec.NewMemoryStore()
			scopes := exec.NewScopeRegistry(ctx)

			engineInstance, err := NewCapabilityRuntimeEngine(bus, singleRuntimeRegistry{runtime: tc.runtime}, store, scopes, 2, time.Second)
			if err != nil {
				t.Fatalf("New engine: %v", err)
			}
			go func() { _ = engineInstance.Run(ctx) }()

			execution, err := exec.NewExecution(timeoutBlueprint("slow", tc.timeout), core.NewID("corr_"), "inst_1")
			if err != nil {
				t.Fatalf("NewExecution: %v", err)
			}
			if err := store.Add(execution); err != nil {
				t.Fatalf("store.Add: %v", err)
			}

			subscription, err := bus.SubscribeExecution(execution.ID, 32)
			if err != nil {
				t.Fatalf("SubscribeExecution: %v", err)
			}
			defer func() { _ = subscription.Close() }()

			// Normally the scheduler starts the execution and announces its entry
			// capabilities as ready, and the engine subscribes to that announcement.
			// Both steps are reproduced here so the test stays on the engine's own
			// boundary instead of standing up the scheduler it does not exercise.
			if err := execution.Start(map[string]any{}, 1); err != nil {
				t.Fatalf("Start: %v", err)
			}
			input := execution.Params("slow")
			if err := execution.MarkCapabilityReady("slow", input); err != nil {
				t.Fatalf("MarkCapabilityReady: %v", err)
			}
			if err := bus.Publish(ctx, event.New(event.ExecutionStarted, execution.ID, execution.CorrelationID, "", event.ExecutionStartedPayload{Params: map[string]any{}})); err != nil {
				t.Fatalf("publish ExecutionStarted: %v", err)
			}
			if err := bus.Publish(ctx, event.New(event.CapabilityReady, execution.ID, execution.CorrelationID, "slow", event.CapabilityReadyPayload{Params: input})); err != nil {
				t.Fatalf("publish CapabilityReady: %v", err)
			}

			tc.runtime.awaitStart(t)

			deadline := time.After(5 * time.Second)
			for {
				select {
				case received, open := <-subscription.Events():
					if !open {
						t.Fatal("subscription closed before the capability finished")
					}
					if received.Metadata.CapabilityID != "slow" {
						continue
					}
					switch received.Type {
					case event.CapabilityCancelled:
						t.Fatalf("a timed-out capability was published as cancelled: %v", received.Payload)
					case event.CapabilityFailed:
						if state := execution.CapabilityState("slow"); state.Status != exec.CapabilityFailed {
							t.Errorf("capability status = %q, want %q", state.Status, exec.CapabilityFailed)
						}
						return
					}
				case <-deadline:
					t.Fatal("the capability never reported an outcome")
				}
			}
		})
	}
}
