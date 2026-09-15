package executor

import (
	"encoding/json"
	"io"
	"os"

	shadexec "github.com/Muhammad-Jay/neuron/shared/types/executor"
)

// readRequestFromStdin reads a single JSON Request document from stdin.
func readRequestFromStdin() (*shadexec.Request, error) {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, err
	}
	var req shadexec.Request
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, err
	}
	return &req, nil
}

// writeStdioOutput writes a successful JSON Response to stdout.
func writeStdioOutput(output map[string]any) error {
	resp := shadexec.Response{Output: output}
	if err := json.NewEncoder(os.Stdout).Encode(resp); err != nil {
		return err
	}
	return nil
}

// writeStdioError writes a controlled failure JSON Response to stdout.
func writeStdioError(message string) {
	_ = json.NewEncoder(os.Stdout).Encode(shadexec.Response{
		Output: map[string]any{},
		Error:  message,
	})
}
