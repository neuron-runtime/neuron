package builtins

import (
	"context"
	"fmt"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
)

// AIMock simulates an AI capability: it resolves a "prompt" from the
// capability configuration and returns a canned response. In a real
// deployment this would be replaced by a runtime that calls an LLM provider.
type AIMock struct{}

// Execute resolves the configured prompt and emits it as a CapabilityLog
// before returning a mock response.
func (AIMock) Execute(ctx context.Context, execution contracts.ExecutionContext) (map[string]any, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	prompt, ok := execution.CapabilityConfigurations["prompt"].(string)
	if !ok || prompt == "" {
		return nil, fmt.Errorf("AI capability requires a resolved prompt string")
	}
	execution.Logger.Info(ctx, "resolved AI prompt", contracts.F("prompt", prompt))

	// Simulate AI processing delay
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(500 * time.Millisecond):
	}

	return map[string]any{"content": "[mock AI response] " + prompt}, nil
}
