// Command blockingruntime is a stdio JSON capability runtime used by the
// acceptance suite. It blocks for the requested duration and leaves a marker
// file when execution has started, so a test can cancel an execution that is
// certainly in flight.
package main

import (
	"bufio"
	"encoding/json"
	"os"
	"time"
)

type request struct {
	Params map[string]any `json:"params"`
}

type response struct {
	Result map[string]any `json:"result,omitempty"`
	Error  string         `json:"error,omitempty"`
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)

	for scanner.Scan() {
		if len(scanner.Bytes()) == 0 {
			continue
		}

		var req request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			_ = encoder.Encode(response{Error: err.Error()})
			return
		}

		if marker, ok := req.Params["marker"].(string); ok && marker != "" {
			if err := os.WriteFile(marker, []byte("started"), 0o644); err != nil {
				_ = encoder.Encode(response{Error: err.Error()})
				return
			}
		}

		if ms, ok := req.Params["sleep_ms"].(float64); ok && ms > 0 {
			time.Sleep(time.Duration(ms) * time.Millisecond)
		}

		_ = encoder.Encode(response{Result: map[string]any{"done": true}})
		return
	}
}
