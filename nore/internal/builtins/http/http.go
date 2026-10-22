// Package http implements the neuron:core:http capability runtime.
package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	nethttp "net/http"
	"strings"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/builtins/input"
	"github.com/neuron-runtime/neuron/nore/internal/contracts"
)

// HTTP performs a single HTTP request and returns the status, the response
// headers and the body.
//
// It is the general-purpose integration boundary: an author who needs to talk
// to an HTTP API should not have to write a capability runtime for it.
type HTTP struct{}

// Execute performs the request named by the capability's input.
func (HTTP) Execute(ctx context.Context, execution contracts.ExecutionContext) (map[string]any, error) {
	values := input.New("neuron:core:http", execution.CapabilityConfigurations, execution.Params)

	url, err := values.RequiredString("url")
	if err != nil {
		return nil, err
	}

	method, _, err := values.String("method")
	if err != nil {
		return nil, err
	}
	if method == "" {
		method = nethttp.MethodGet
	}

	var bodyReader io.Reader
	if body, exists := values.Raw("body"); exists && body != nil {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("neuron:core:http: encode the request body: %w", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	req, err := nethttp.NewRequestWithContext(ctx, strings.ToUpper(method), url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("neuron:core:http: %w", err)
	}

	headers, _, err := values.StringMap("headers")
	if err != nil {
		return nil, err
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	if bodyReader != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &nethttp.Client{Timeout: 30 * time.Second}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("neuron:core:http: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("neuron:core:http: read the response body: %w", err)
	}

	// The body is parsed as JSON when it is JSON and kept as text when it is
	// not, so a caller receives the value the server sent rather than a
	// stringified copy of it.
	var responseBody any
	if err := json.Unmarshal(respBody, &responseBody); err != nil {
		responseBody = string(respBody)
	}

	return map[string]any{
		"status_code": resp.StatusCode,
		"body":        responseBody,
	}, nil
}
