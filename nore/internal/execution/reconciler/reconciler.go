// Package reconciler republishes durable dispatch intents that were committed
// but never announced. It is host-local: each instance owns one reconciler bound
// to its own event bus and its own durable identity.
package reconciler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
	"github.com/neuron-runtime/neuron/nore/internal/event"
	executionmodel "github.com/neuron-runtime/neuron/nore/internal/execution"
	core "github.com/neuron-runtime/neuron/shared/types/core"
)

// Claimer is the consumer-local dispatch claim port. It deliberately excludes
// CompleteDispatchIntent: the engine owns completion after it executes the
// republished capability.
type Claimer interface {
	// ClaimReadyDispatchIntents claims at most limit ready intents owned by
	// instanceID under a fresh fencing token and lease.
	ClaimReadyDispatchIntents(ctx context.Context, instanceID core.ID, owner string, lease time.Duration, limit int) ([]*executionmodel.DispatchIntent, error)
	// ReleaseDispatchIntent clears a claim held by owner, returning the intent to
	// the discoverable pool.
	ReleaseDispatchIntent(ctx context.Context, executionID, capabilityID, attemptID core.ID, owner string) error
}

// ExecutionLookup is the narrow read surface needed to assert durable ownership
// and rebuild the capability input for the generic readiness event. It is kept
// separate from Claimer so the claim port carries no other concern.
type ExecutionLookup interface {
	Get(ctx context.Context, executionID core.ID) (*executionmodel.Execution, error)
}

// Config bounds one reconciler. Interval is the ticker cadence, BatchSize caps
// examined ready intents per pass, and Lease is the fencing lease handed to the
// claim primitive. The host supplies all three from the resolved policy and the
// scheduler's validated lease authority.
type Config struct {
	InstanceID core.ID
	Interval   time.Duration
	BatchSize  int
	Lease      time.Duration
	Logger     *slog.Logger
}

func (c Config) validated() (Config, error) {
	if c.InstanceID == "" {
		return c, fmt.Errorf("dispatch reconciler requires an instance ID")
	}
	if c.Interval <= 0 {
		return c, fmt.Errorf("dispatch reconciler interval must be positive")
	}
	if c.BatchSize <= 0 {
		return c, fmt.Errorf("dispatch reconciler batch size must be positive")
	}
	if c.Lease <= 0 {
		return c, fmt.Errorf("dispatch reconciler lease must be positive")
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	return c, nil
}

// Reconciler claims ready intents owned by one instance and republishes them on
// the bus bound at construction. It performs no state transition: a claim is
// released before its event is published, so a publish failure leaves the work
// discoverable for the next tick.
type Reconciler struct {
	cfg        Config
	claims     Claimer
	executions ExecutionLookup
	bus        contracts.EventBus
}

// New validates cfg and binds a reconciler to one bus. A nil claimer, execution
// lookup, or bus is rejected rather than tolerated at runtime.
func New(cfg Config, claims Claimer, executions ExecutionLookup, bus contracts.EventBus) (*Reconciler, error) {
	validated, err := cfg.validated()
	if err != nil {
		return nil, err
	}
	if claims == nil {
		return nil, fmt.Errorf("dispatch reconciler requires a claim port")
	}
	if executions == nil {
		return nil, fmt.Errorf("dispatch reconciler requires an execution lookup")
	}
	if bus == nil {
		return nil, fmt.Errorf("dispatch reconciler requires an event bus")
	}
	return &Reconciler{
		cfg:        validated,
		claims:     claims,
		executions: executions,
		bus:        bus,
	}, nil
}

// Config returns the validated bounds this reconciler runs with.
func (r *Reconciler) Config() Config {
	return r.cfg
}

// Run wakes on a bounded periodic ticker until ctx is cancelled. There is no
// event-driven wake-up: every pass is the same durable claim-and-republish path,
// so an in-memory publish error cannot become a second dispatch implementation.
func (r *Reconciler) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			// A tick and cancellation can become ready together. Never start a
			// pass with an already-cancelled context: shutdown awaits this loop
			// before closing the store and bus.
			if ctx.Err() != nil {
				return nil
			}
			if err := r.Reconcile(ctx); err != nil {
				if errors.Is(err, context.Canceled) && ctx.Err() != nil {
					return nil
				}
				r.cfg.Logger.Warn("reconcile dispatch intents",
					slog.String("instance", string(r.cfg.InstanceID)),
					slog.String("error", err.Error()))
			}
		}
	}
}

// Reconcile performs one bounded claim-and-republish pass. It generates a new
// fencing token for the pass, claims only intents the store attributes to this
// instance, releases each claim before publishing, and never completes an
// intent. An execution whose durable instance identity disagrees with the claim
// is an invariant violation: the batch stops without a release or a publish.
func (r *Reconciler) Reconcile(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	owner := string(r.cfg.InstanceID) + "/reconciler/" + string(core.NewID("claim_"))
	intents, err := r.claims.ClaimReadyDispatchIntents(ctx, r.cfg.InstanceID, owner, r.cfg.Lease, r.cfg.BatchSize)
	if err != nil {
		return fmt.Errorf("claim dispatch intents: %w", err)
	}

	for _, intent := range intents {
		if intent == nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		exec, err := r.executions.Get(ctx, intent.ExecutionID)
		if err != nil {
			// The claim is still held by this pass. Release it so the intent
			// remains discoverable instead of waiting for lease expiry.
			r.cfg.Logger.Warn("load execution for dispatch reconciliation",
				slog.String("execution_id", string(intent.ExecutionID)),
				slog.String("error", err.Error()))
			if releaseErr := r.claims.ReleaseDispatchIntent(ctx, intent.ExecutionID, intent.CapabilityID, intent.AttemptID, owner); releaseErr != nil {
				r.cfg.Logger.Error("release dispatch claim after lookup failure",
					slog.String("execution_id", string(intent.ExecutionID)),
					slog.String("error", releaseErr.Error()))
			}
			continue
		}
		if exec.InstanceID != r.cfg.InstanceID {
			// The store filter is the sole foreign-filter authority. Do not
			// release or publish here: that would mutate another instance's
			// claim or leak Params onto this instance's bus.
			r.cfg.Logger.Error("foreign dispatch intent returned by claim",
				slog.String("instance", string(r.cfg.InstanceID)),
				slog.String("execution_id", string(intent.ExecutionID)),
				slog.String("owner_instance", string(exec.InstanceID)))
			return nil
		}
		if err := r.claims.ReleaseDispatchIntent(ctx, intent.ExecutionID, intent.CapabilityID, intent.AttemptID, owner); err != nil {
			r.cfg.Logger.Error("release dispatch claim",
				slog.String("execution_id", string(intent.ExecutionID)),
				slog.String("capability_id", string(intent.CapabilityID)),
				slog.String("error", err.Error()))
			continue
		}
		// Cancellation can land while the release is in flight. The claim is
		// already released, so stop here rather than announce work during
		// shutdown; the intent stays discoverable for the next host.
		if err := ctx.Err(); err != nil {
			return err
		}
		ready := event.New(
			event.CapabilityReady,
			exec.ID,
			exec.CorrelationID,
			intent.CapabilityID,
			event.CapabilityReadyPayload{Params: exec.Params(intent.CapabilityID)},
		)
		if err := r.bus.Publish(ctx, ready); err != nil {
			if errors.Is(err, context.Canceled) && ctx.Err() != nil {
				return err
			}
			// Release already succeeded, so the ready index still points at
			// this intent and the next tick can retry the announcement.
			r.cfg.Logger.Error("publish reconciled capability readiness",
				slog.String("execution_id", string(exec.ID)),
				slog.String("capability_id", string(intent.CapabilityID)),
				slog.String("error", err.Error()))
		}
	}
	return nil
}
