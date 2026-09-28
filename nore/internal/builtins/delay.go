package builtins

import (
	"context"
	"fmt"
	"time"

	"github.com/Muhammad-Jay/neuron/nore/internal/contracts"
)

// Delay blocks for a configured "duration" (e.g. "5s"), honouring context
// cancellation.
type Delay struct{}

func (Delay) Execute(ctx context.Context, execution contracts.ExecutionContext) (map[string]any, error) {

	// Look for duration in params or config
	var durationStr string
	if d, ok := execution.Params["duration"].(string); ok {
		durationStr = d
	} else if d, ok := execution.CapabilityConfigurations["duration"].(string); ok {
		durationStr = d
	} else {
		return nil, fmt.Errorf("delay runtime requires a 'duration' string (e.g., '5s', '2m')")
	}

	duration, err := time.ParseDuration(durationStr)
	if err != nil {
		return nil, fmt.Errorf("invalid duration format: %w", err)
	}

	// Wait for the duration, or until the context is cancelled
	select {
	case <-time.After(duration):
		// Delay completed successfully
		return map[string]any{
			"delayed_for": duration.String(),
		}, nil

	case <-ctx.Done():
		// Execution was cancelled (e.g., HTTP timeout) before delay finished
		return nil, ctx.Err()
	}
}