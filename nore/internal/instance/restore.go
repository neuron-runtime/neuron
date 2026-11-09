package instance

import (
	"context"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
	"github.com/neuron-runtime/neuron/nore/internal/event"
	"github.com/neuron-runtime/neuron/nore/internal/types"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

// recordFor builds the durable metadata for a live instance at status.
func recordFor(i *Instance, status Status) metadata {
	return metadata{
		ID:                i.ID,
		AssemblyID:        i.Key.AssemblyID,
		Version:           i.Key.Version,
		Hash:              i.Key.Hash,
		Env:               i.Key.Env,
		Status:            status,
		BlueprintMetadata: i.Blueprint.Metadata,
		CreatedAt:         i.createdAt,
		UpdatedAt:         time.Now().UTC(),
	}
}

// restoreInstance reconstructs a metadata-only Instance after a restart.
// Runtime machinery (bus, scheduler, engine) is not recreated; persisted
// executions and events remain queryable through the shared storage.Store.
//
// Instances that were interrupted mid-run are coerced to failed: their
// in-memory state is not trustworthy, and they must not be reported as
// running when they have no runtime.
//
// resumeDurable selects whether unfinished executions are left non-terminal for
// recovery. In durable mode they are left untouched so the recovery protocol can
// resume them. In "none" and "history" modes their unfinished executions are
// drained: nothing in this process can advance them, so reporting them as
// running would be a permanent lie rather than a temporary inaccuracy.
func restoreInstance(
	parent context.Context,
	rec metadata,
	store contracts.ExecutionRepository,
	eventStore *event.Store,
	resumeDurable bool,
) (*Instance, error) {
	status := rec.Status
	if status == StatusStarting || status == StatusRunning {
		status = StatusFailed
	}

	if !resumeDurable {
		if err := sweepUnresumableExecutions(parent, store, shared.ID(rec.ID), restoredWithoutRuntime(rec.ID)); err != nil {
			return nil, err
		}
	}

	ctx, cancel := context.WithCancel(parent)
	return &Instance{
		ID: rec.ID,
		Key: protocol.InstanceKey{
			AssemblyID: rec.AssemblyID,
			Version:    rec.Version,
			Hash:       rec.Hash,
			Env:        rec.Env,
		},
		Blueprint:  &types.ExecutionBlueprint{Metadata: rec.BlueprintMetadata},
		status:     status,
		ctx:        ctx,
		cancel:     cancel,
		createdAt:  rec.CreatedAt,
		store:      store,
		eventStore: eventStore,
	}, nil
}
