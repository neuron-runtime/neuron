package contracts

import (
	"context"
	"time"

	executionmodel "github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/shared/types/core"
)

// TransitionCommitter is the durable-mode commit boundary shared by the
// scheduler and the engine.
//
// In durable mode a state transition and the dispatch intents it produced must
// become durable together, before any event announces them: a caller that
// observes "capability ready" must be able to find the work that readiness
// created, even if the process stops immediately afterwards. The scheduler
// commits transitions through this interface instead of the instance writing
// snapshots opportunistically, and the engine claims the resulting intents so
// the same ready work is not executed twice.
//
// In the other durability modes no committer is supplied and both components
// behave exactly as before: the instance's event-driven persister owns
// snapshots and no intents exist.
type TransitionCommitter interface {
	// SaveTransition atomically persists execution's next revision together
	// with the intents the transition produced. A successful return is the
	// commit-ownership boundary: the snapshot and every intent are durable.
	SaveTransition(ctx context.Context, execution *executionmodel.Execution, intents []*executionmodel.DispatchIntent) error

	// ClaimDispatchIntent attempts to take ownership of an intent under a
	// lease. It reports false when the intent is missing, not ready, already
	// held under a live lease, or lost the claim to another owner.
	ClaimDispatchIntent(ctx context.Context, intent *executionmodel.DispatchIntent, owner string, lease time.Duration) (*executionmodel.DispatchIntent, bool, error)

	// ReleaseDispatchIntent clears a claim held by owner, returning the intent
	// to the claimable pool. It is a no-op for a missing intent or a different
	// owner.
	ReleaseDispatchIntent(ctx context.Context, executionID, capabilityID, attemptID core.ID, owner string) error

	// CompleteDispatchIntent removes an intent once owner has finished with
	// it. It is a no-op for a missing intent or a different owner.
	CompleteDispatchIntent(ctx context.Context, executionID, capabilityID, attemptID core.ID, owner string) error
}
