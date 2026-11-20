package instance

import (
	"context"
	"errors"
	"fmt"

	"github.com/neuron-runtime/neuron/nore/internal/execution"
	"github.com/neuron-runtime/neuron/nore/internal/storage"
	core "github.com/neuron-runtime/neuron/shared/types/core"
)

// InstanceForExecution returns the live instance that owns executionID. An
// execution-scoped API route uses this to find the owning instance without a
// caller-supplied instance path.
//
// Ownership is read from the durable execution record rather than from any
// in-memory cache or blueprint. A missing execution, an execution whose owning
// instance is not live, and an instance mismatch are all reported as
// ErrExecutionNotFound so a client cannot probe execution existence across
// instances.
func (m *Manager) InstanceForExecution(ctx context.Context, executionID core.ID) (*Instance, error) {
	if executionID == "" {
		return nil, fmt.Errorf("%w: empty execution id", ErrExecutionNotFound)
	}
	exec, err := execution.NewExecutionStore(m.store).Get(ctx, executionID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, fmt.Errorf("%w: %s", ErrExecutionNotFound, executionID)
		}
		return nil, fmt.Errorf("load execution %s: %w", executionID, err)
	}
	m.mu.RLock()
	inst, ok := m.instancesByID[string(exec.InstanceID)]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrExecutionNotFound, executionID)
	}
	return inst, nil
}
