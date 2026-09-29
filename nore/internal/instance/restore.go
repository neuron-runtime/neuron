package instance

import (
	"context"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
	"github.com/neuron-runtime/neuron/nore/internal/event"
	"github.com/neuron-runtime/neuron/nore/internal/types"
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
func restoreInstance(
	parent context.Context,
	rec metadata,
	store contracts.ExecutionRepository,
	eventStore *event.Store,
) *Instance {
	status := rec.Status
	if status == StatusStarting || status == StatusRunning {
		status = StatusFailed
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
	}
}
