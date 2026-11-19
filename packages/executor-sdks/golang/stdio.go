package capabilityruntime

import (
	"context"
	"encoding/json"
	"io"
	"os"

	capabilityrt "github.com/neuron-runtime/neuron/shared/types/capabilityruntime"
)

// readRequestFromStdin reads a single JSON Request document from stdin.
func readRequestFromStdin() (*capabilityrt.Request, error) {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, err
	}
	var req capabilityrt.Request
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, err
	}
	return &req, nil
}

// executeStdio runs one stdin/stdout execution. The legacy JSON transport has
// no field for a resume input or a suspend outcome, so a suspend is reported as
// an explicit failure instead of being treated as an empty success.
func executeStdio(ctx context.Context, h Handler, req *capabilityrt.Request) capabilityrt.Response {
	outcome, err := invokeHandler(ctx, h, ExecutionRequest{Params: req.Params})
	if err != nil {
		return capabilityrt.Response{Result: map[string]any{}, Error: err.Error()}
	}
	if msg := validateSuspendOutcome(outcome); msg != "" {
		return capabilityrt.Response{Result: map[string]any{}, Error: msg}
	}
	if outcome.errText != "" {
		return capabilityrt.Response{Result: map[string]any{}, Error: outcome.errText}
	}
	if outcome.suspend != nil {
		return capabilityrt.Response{
			Result: map[string]any{},
			Error:  "unsupported_suspend_outcome: the stdin/stdout JSON transport cannot carry a suspend outcome",
		}
	}
	return capabilityrt.Response{Result: outcome.result}
}

// writeStdioOutput writes a successful JSON Response to stdout.
func writeStdioOutput(output map[string]any) error {
	resp := capabilityrt.Response{Result: output}
	if err := json.NewEncoder(os.Stdout).Encode(resp); err != nil {
		return err
	}
	return nil
}

// writeStdioError writes a controlled failure JSON Response to stdout.
func writeStdioError(message string) {
	_ = json.NewEncoder(os.Stdout).Encode(capabilityrt.Response{
		Result: map[string]any{},
		Error:  message,
	})
}
