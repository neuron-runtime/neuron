package capabilityruntime

import (
	"encoding/json"
	"io"
	"os"

	capabilityrt "github.com/Muhammad-Jay/neuron/shared/types/capabilityruntime"
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
