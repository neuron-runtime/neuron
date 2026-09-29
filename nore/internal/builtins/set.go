package builtins

import (
	"context"
	"maps"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
)

// Set merges the capability's params with its configuration and returns the
// combination as its result. It is the canonical way to assign or override
// values for downstream capabilities.
type Set struct{}

func (Set) Execute(ctx context.Context, execution contracts.ExecutionContext) (map[string]any, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	result := make(map[string]any, len(execution.Params)+len(execution.CapabilityConfigurations))
	maps.Copy(result, execution.Params)
	maps.Copy(result, execution.CapabilityConfigurations)
	return result, nil
}
