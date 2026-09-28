package builtins

import (
	"context"

	"github.com/Muhammad-Jay/neuron/nore/internal/contracts"
)

// Log surfaces the capability's params as a CapabilityLog event and passes
// the params through unchanged, acting as a pass-through observability node.
type Log struct{}

// Execute emits a structured log of the capability params/config and returns
// the params as its result so downstream capabilities keep receiving the same
// data.
func (Log) Execute(ctx context.Context, execution contracts.ExecutionContext) (map[string]any, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	execution.Logger.Info(ctx,
		"[log] params received",
		contracts.F("params", execution.Params),
		contracts.F("config", execution.CapabilityConfigurations),
	)
	return execution.Params, nil
}