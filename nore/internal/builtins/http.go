package builtins

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/contracts"
)

// HTTP performs a single HTTP request from the merged params/config and
// returns status/body.
type HTTP struct{}

func (HTTP) Execute(ctx context.Context, execution contracts.ExecutionContext) (map[string]any, error) {

	// 1. Merge Config and Params (Params override Config)
	params := make(map[string]any)
	maps.Copy(params, execution.CapabilityConfigurations)
	maps.Copy(params, execution.Params)

	// 2. Extract parameters safely
	url, ok := params["url"].(string)
	if !ok || url == "" {
		return nil, fmt.Errorf("http runtime requires a valid 'url' string")
	}

	method, _ := params["method"].(string)
	if method == "" {
		method = http.MethodGet
	}

	// 3. Handle Body (if provided)
	var bodyReader io.Reader
	if body, exists := params["body"]; exists {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to encode body: %w", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	// 4. Create the Request with Context
	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(method), url, bodyReader)
	if err != nil {
		return nil, err
	}

	// 5. Handle Headers
	if headers, ok := params["headers"].(map[string]any); ok {
		for k, v := range headers {
			if strVal, isStr := v.(string); isStr {
				req.Header.Set(k, strVal)
			}
		}
	}
	if bodyReader != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	// 6. Execute Request
	client := &http.Client{Timeout: 30 * time.Second}

	// Simulate realistic network delay for streaming demo
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(500 * time.Millisecond):
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// 7. Parse Response
	respBody, _ := io.ReadAll(resp.Body)
	var jsonResponse any

	// Try to parse as JSON, fallback to raw string if it's not JSON
	if err := json.Unmarshal(respBody, &jsonResponse); err != nil {
		jsonResponse = string(respBody)
	}

	// 8. Return standardized result
	return map[string]any{
		"status_code": resp.StatusCode,
		"body":        jsonResponse,
	}, nil
}
