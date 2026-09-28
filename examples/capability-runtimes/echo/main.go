// Command echo is an example Neuron capability runtime. It is compiled twice
// from this single source:
//
//   - a native binary (runtime type "process")
//   - a WebAssembly module (GOOS=wasip1 GOARCH=wasm, runtime type "wasm")
//
// Both speak the exact same wire protocol: one JSON Request on stdin, one JSON
// Response on stdout, NEURON_CAPABILITY_RUNTIME_* environment variables
// describing the execution. It deliberately imports nothing but the standard
// library so it builds offline and cross-compiles to wasip1 without any
// toolchain besides Go.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// Request mirrors the protocol Request. It is declared locally (instead of
// importing the shared module) to keep this example dependency-free.
type Request struct {
	Params map[string]any `json:"params"`
}

// Response mirrors the protocol Response.
type Response struct {
	Result map[string]any `json:"result"`
	Error  string         `json:"error,omitempty"`
}

func main() {
	req, err := readRequest()
	if err != nil {
		writeError(fmt.Sprintf("invalid request: %v", err))
		os.Exit(1)
	}

	// A caller can trigger a controlled failure through the params contract:
	// {"params": {"error": "message"}}.
	if msg, ok := req.Params["error"].(string); ok && msg != "" {
		if err := writeResponse(Response{Result: map[string]any{}, Error: msg}); err != nil {
			os.Exit(1)
		}
		return
	}

	out := map[string]any{
		"params":   req.Params,
		"protocol": os.Getenv("NEURON_CAPABILITY_RUNTIME_PROTOCOL"),
		"type":     os.Getenv("NEURON_CAPABILITY_RUNTIME_TYPE"),
		"version":  os.Getenv("NEURON_CAPABILITY_RUNTIME_VERSION"),
	}
	if value, ok := req.Params["value"]; ok {
		out["value"] = value
	}

	if err := writeResponse(Response{Result: out}); err != nil {
		os.Exit(1)
	}
}

func readRequest() (Request, error) {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return Request{}, err
	}
	var req Request
	if err := json.Unmarshal(data, &req); err != nil {
		return Request{}, err
	}
	return req, nil
}

func writeResponse(resp Response) error {
	return json.NewEncoder(os.Stdout).Encode(resp)
}

func writeError(message string) {
	_ = writeResponse(Response{Result: map[string]any{}, Error: message})
}