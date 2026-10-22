// Package set implements the neuron:core:set capability runtime.
package set

import (
	"context"

	"github.com/neuron-runtime/neuron/nore/internal/builtins/input"
	"github.com/neuron-runtime/neuron/nore/internal/contracts"
)

// Set materializes a capability's value set.
//
// It combines the capability's own configuration with the params bound to the
// invocation and returns the combination. It is the default runtime for a
// capability that declares none, which makes it the way an assembly supplies a
// value set to a downstream capability without writing a capability runtime.
//
// The merge follows the shared rule: params override configuration. A param is
// an explicit argument at the call site, and a configuration value is the
// ambient default declared alongside the capability, so the explicit value
// wins.
type Set struct{}

// Execute returns the merged value set.
func (Set) Execute(ctx context.Context, execution contracts.ExecutionContext) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	values := input.New("neuron:core:set", execution.CapabilityConfigurations, execution.Params)
	return values.All(), nil
}
