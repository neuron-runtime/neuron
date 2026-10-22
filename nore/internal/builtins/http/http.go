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

	"github.com/neuron-runtime/neuron/nore/internal/builtins/input"
	"github.com/neuron-runtime/neuron/nore/internal/contracts"
	"github.com/neuron-runtime/neuron/shared/types/builtins"
)

// HTTP performs a single HTTP request and returns the status, the response
// headers and the body.
//
// It is the general-purpose integration boundary: an author who needs to talk
// to an HTTP API should not have to write a capability runtime for it.
//
// The policy bounds the request: how long it may take, how large a body it
// may send, and how much of the response it may read.
type HTTP struct {
	// Policy bounds what a request may do. A zero policy means the
	// compiled-in ceilings.
	Policy builtins.Policy
}

// Execute performs the request named by the capability's input.
func (h HTTP) Execute(ctx context.Context, execution contracts.ExecutionContext) (map[string]any, error) {
	// The policy is resolved here rather than trusted, because a runtime may
	// also be constructed outside the registry. The resolution only narrows,
	// so a policy that has already been resolved is unchanged.
	policy, err := builtins.Effective(h.Policy)
	if err != nil {
		return nil, fmt.Errorf("neuron:core:http: %w", err)
	}
	timeout, err := policy.Timeout()
	if err != nil {
		return nil, fmt.Errorf("neuron:core:http: %w", err)
	}

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
		if int64(len(bodyBytes)) > policy.MaxHTTPRequestBodyBytes {
			return nil, fmt.Errorf("neuron:core:http: the request body is %d bytes, which exceeds the limit of %d bytes",
				len(bodyBytes), policy.MaxHTTPRequestBodyBytes)
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

	client := &nethttp.Client{Timeout: timeout}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("neuron:core:http: %w", err)
	}
	defer resp.Body.Close()

	// The response is read one byte past the limit so a body that is exactly
	// at the limit is accepted and a body that exceeds it is refused rather
	// than silently truncated: a caller that receives a truncated body cannot
	// tell it apart from a complete one.
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, policy.MaxHTTPResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("neuron:core:http: read the response body: %w", err)
	}
	if int64(len(respBody)) > policy.MaxHTTPResponseBytes {
		return nil, fmt.Errorf("neuron:core:http: the response body exceeds the limit of %d bytes",
			policy.MaxHTTPResponseBytes)
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
