package contracts

import (
	"context"

	executionmodel "github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/shared/types/core"
)

// ExecutionRepository is the durable execution boundary shared by the
// scheduler, the instance persister, and the API.
//
// Reads report storage.ErrNotFound for absence and return backend or decode
// failures as errors. A miss must never be synthesized from a failure.
type ExecutionRepository interface {
	Add(ctx context.Context, execution *executionmodel.Execution) error
	Get(ctx context.Context, executionID core.ID) (*executionmodel.Execution, error)
	Save(ctx context.Context, execution *executionmodel.Execution) error
	Delete(ctx context.Context, executionID core.ID) error
	List(ctx context.Context) ([]*executionmodel.Execution, error)
	ListByInstance(ctx context.Context, instanceID core.ID) ([]*executionmodel.Execution, error)
}
