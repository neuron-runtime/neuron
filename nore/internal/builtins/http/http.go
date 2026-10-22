// Package http implements the neuron:core:http capability runtime.
package http

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	nethttp "net/http"
	neturl "net/url"
	"strings"
	"time"

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
// The policy bounds the request: which hosts it may reach, how long it may
// take, and how large a body it may send and read.
type HTTP struct {
	// Policy bounds what a request may do. A zero policy means the
	// compiled-in ceilings.
	Policy builtins.Policy
}

// maxRedirects bounds how many redirects a single request may follow when
// redirects are enabled.
const maxRedirects = 5

// maxRetryDelay bounds how long the runtime waits before a retry.
const maxRetryDelay = 2 * time.Second

// defaultRetryBackoff is how long the runtime waits before the first retry.
const defaultRetryBackoff = 100 * time.Millisecond

// Execute performs the request named by the capability's input.
func (h HTTP) Execute(ctx context.Context, execution contracts.ExecutionContext) (map[string]any, error) {
	// The policy is resolved here rather than trusted, because a runtime may
	// also be constructed outside the registry. The resolution only narrows,
	// so a policy that has already been resolved is unchanged.
	policy, err := builtins.Effective(h.Policy)
	if err != nil {
		return nil, fmt.Errorf("neuron:core:http: %w", err)
	}

	values := input.New("neuron:core:http", execution.CapabilityConfigurations, execution.Params)
	logger := contracts.LoggerOrNop(execution.Logger)

	target, err := values.RequiredString("url")
	if err != nil {
		return nil, err
	}

	query, _, err := values.StringMap("query")
	if err != nil {
		return nil, err
	}
	if len(query) > 0 {
		target, err = appendQuery(target, query)
		if err != nil {
			return nil, err
		}
	}

	if err := validateURL(target); err != nil {
		return nil, err
	}

	format, err := values.Format("format", input.FormatJSON)
	if err != nil {
		return nil, err
	}

	method, _, err := values.String("method")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(method) == "" {
		method = nethttp.MethodGet
	}
	method = strings.ToUpper(strings.TrimSpace(method))

	timeout, err := requestTimeout(values, policy)
	if err != nil {
		return nil, err
	}

	followRedirects, err := values.Bool("followRedirects", false)
	if err != nil {
		return nil, err
	}

	retry, err := readRetry(values)
	if err != nil {
		return nil, err
	}

	contentType, _, err := values.String("contentType")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(contentType) == "" {
		contentType = "application/json"
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

	req, err := nethttp.NewRequestWithContext(ctx, method, target, bodyReader)
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
		req.Header.Set("Content-Type", contentType)
	}

	// The credentials are applied to the request rather than to the URL, so
	// they never end up in a log line or in an error that names the URL.
	auth, hasAuth, err := values.Nested("auth")
	if err != nil {
		return nil, err
	}
	if hasAuth {
		if err := applyAuthentication(req, auth); err != nil {
			return nil, err
		}
	}

	// The deadline bounds the whole operation, retries included, rather than
	// each attempt: a request that is retried must not be able to take
	// longer than the timeout it asked for.
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client := newClient(policy, timeout, followRedirects)

	logger.Debug(ctx, "neuron:core:http: performing the request",
		contracts.F("method", method),
		contracts.F("url", target),
	)

	started := time.Now()
	resp, err := perform(ctx, client, req, retry)
	if err != nil {
		return nil, fmt.Errorf("neuron:core:http: %w", err)
	}
	defer resp.Body.Close()
	elapsed := time.Since(started)

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

	body, err := representBody(respBody, format)
	if err != nil {
		return nil, err
	}

	logger.Debug(ctx, "neuron:core:http: the request completed",
		contracts.F("status_code", resp.StatusCode),
	)

	return map[string]any{
		"status_code":  resp.StatusCode,
		"headers":      responseHeaders(resp.Header),
		"body":         body,
		"content_type": resp.Header.Get("Content-Type"),
		"duration_ms":  float64(elapsed.Milliseconds()),
	}, nil
}

// newClient builds the HTTP client the request is performed with.
//
// The client refuses to reach a host the policy does not permit, and it
// refuses to follow a redirect unless the capability asked for it: a redirect
// is a second request to a host the caller never named, and it is checked
// against the same rules as the first one.
func newClient(policy builtins.Policy, timeout time.Duration, followRedirects bool) *nethttp.Client {
	guard := newHostGuard(policy.AllowedHTTPHosts, policy.AllowPrivateNetworks)

	transport := &nethttp.Transport{
		DialContext:           guard.dialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          16,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	return &nethttp.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(req *nethttp.Request, via []*nethttp.Request) error {
			if !followRedirects {
				// The redirect is not followed: the 3xx response is the
				// answer the caller receives, with the target in its
				// Location header.
				return nethttp.ErrUseLastResponse
			}
			if len(via) >= maxRedirects {
				return fmt.Errorf("neuron:core:http: the request was redirected more than %d times", maxRedirects)
			}
			return guard.refuses(req.URL.Hostname())
		},
	}
}

// perform issues the request, retrying it when the capability asked for
// retries.
//
// A retry is only made for a failure that a second attempt can plausibly
// repair — a transport error, or a status that says the server is busy — and
// only for the methods the capability named.
func perform(ctx context.Context, client *nethttp.Client, req *nethttp.Request, retry retryPolicy) (*nethttp.Response, error) {
	var lastErr error

	for attempt := 0; attempt < retry.attempts; attempt++ {
		if attempt > 0 {
			if err := waitForRetry(ctx, retry.backoff, attempt); err != nil {
				return nil, err
			}
			// The body is read again for the retry: a consumed body cannot
			// be sent a second time.
			if req.GetBody != nil {
				body, err := req.GetBody()
				if err != nil {
					return nil, fmt.Errorf("neuron:core:http: prepare the request body for a retry: %w", err)
				}
				req.Body = body
			}
		}

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			if attempt < retry.attempts-1 && retry.allows(req.Method) {
				continue
			}
			break
		}

		if attempt < retry.attempts-1 && retry.allows(req.Method) && retryableStatus(resp.StatusCode) {
			resp.Body.Close()
			lastErr = fmt.Errorf("the server answered %d", resp.StatusCode)
			continue
		}
		return resp, nil
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("the request was not performed")
	}
	return nil, lastErr
}

// retryPolicy describes how a failed request is retried.
type retryPolicy struct {
	// attempts is how many attempts are made in total, including the first.
	attempts int

	// backoff is how long the runtime waits before the second attempt. The
	// wait grows with the attempt.
	backoff time.Duration

	// methods are the request methods that may be retried.
	methods map[string]bool
}

// allows reports whether the method may be retried.
func (r retryPolicy) allows(method string) bool {
	return r.methods[strings.ToUpper(strings.TrimSpace(method))]
}

// readRetry reads the retry policy from the capability's input.
//
// Retrying is off unless the capability asks for it, and only the methods it
// names may be retried: a retried POST may repeat an effect the server
// already performed.
func readRetry(values input.Values) (retryPolicy, error) {
	policy := retryPolicy{attempts: 1, backoff: defaultRetryBackoff}

	nested, present, err := values.Nested("retry")
	if err != nil || !present {
		return policy, err
	}

	attempts, err := nested.Int("attempts", 1)
	if err != nil {
		return policy, err
	}
	if attempts < 1 {
		return policy, fmt.Errorf("neuron:core:http: the retry attempts must be at least 1, got %d", attempts)
	}
	policy.attempts = attempts

	backoff, err := nested.Duration("backoff", policy.backoff)
	if err != nil {
		return policy, err
	}
	if backoff < 0 {
		return policy, fmt.Errorf("neuron:core:http: the retry backoff must not be negative")
	}
	policy.backoff = backoff

	methods, _, err := nested.StringSlice("methods")
	if err != nil {
		return policy, err
	}
	if len(methods) == 0 {
		policy.methods = defaultRetryMethods()
		return policy, nil
	}

	policy.methods = make(map[string]bool, len(methods))
	for _, method := range methods {
		trimmed := strings.ToUpper(strings.TrimSpace(method))
		if trimmed == "" {
			continue
		}
		policy.methods[trimmed] = true
	}
	if len(policy.methods) == 0 {
		policy.methods = defaultRetryMethods()
	}
	return policy, nil
}

// defaultRetryMethods are the methods that are retried when the capability
// did not name any.
//
// They are the methods that are idempotent by definition: a retried request
// may repeat the effect of the first one, and only for these methods that
// effect is the same.
func defaultRetryMethods() map[string]bool {
	return map[string]bool{
		nethttp.MethodGet:     true,
		nethttp.MethodHead:    true,
		nethttp.MethodPut:     true,
		nethttp.MethodDelete:  true,
		nethttp.MethodOptions: true,
	}
}

// retryableStatus reports whether a status says the server is busy rather
// than that it answered.
func retryableStatus(status int) bool {
	switch status {
	case nethttp.StatusTooManyRequests,
		nethttp.StatusBadGateway,
		nethttp.StatusServiceUnavailable,
		nethttp.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// waitForRetry waits before the next attempt, or returns the reason the wait
// was cut short.
func waitForRetry(ctx context.Context, backoff time.Duration, attempt int) error {
	delay := backoff * time.Duration(attempt)
	if delay > maxRetryDelay {
		delay = maxRetryDelay
	}
	if delay <= 0 {
		return nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// requestTimeout resolves how long the request may take.
//
// A capability may ask for a shorter timeout than the policy allows, but not
// for a longer one: the policy is the ceiling, and a capability is part of the
// assembly's input.
func requestTimeout(values input.Values, policy builtins.Policy) (time.Duration, error) {
	limit, err := policy.Timeout()
	if err != nil {
		return 0, fmt.Errorf("neuron:core:http: %w", err)
	}

	requested, err := values.Duration("timeout", limit)
	if err != nil {
		return 0, err
	}
	if requested <= 0 {
		return 0, fmt.Errorf("neuron:core:http: parameter \"timeout\" must be positive")
	}
	if requested > limit {
		return 0, fmt.Errorf("neuron:core:http: the requested timeout %s exceeds the limit of %s", requested, limit)
	}
	return requested, nil
}

// validateURL rejects a target the request could not be made against.
func validateURL(target string) error {
	parsed, err := neturl.Parse(target)
	if err != nil {
		return fmt.Errorf("neuron:core:http: the url %q is not a valid URL: %w", target, err)
	}
	switch parsed.Scheme {
	case "http", "https":
	default:
		return fmt.Errorf("neuron:core:http: the url scheme %q is not supported; use http or https", parsed.Scheme)
	}
	if parsed.Host == "" {
		return fmt.Errorf("neuron:core:http: the url %q has no host", target)
	}
	return nil
}

// appendQuery adds the query parameters to the URL.
func appendQuery(target string, query map[string]string) (string, error) {
	parsed, err := neturl.Parse(target)
	if err != nil {
		return "", fmt.Errorf("neuron:core:http: the url %q is not a valid URL: %w", target, err)
	}

	values := parsed.Query()
	for name, value := range query {
		values.Set(name, value)
	}
	parsed.RawQuery = values.Encode()

	return parsed.String(), nil
}

// applyAuthentication adds the credentials to the request.
func applyAuthentication(req *nethttp.Request, auth input.Values) error {
	kind, err := auth.RequiredString("type")
	if err != nil {
		return err
	}

	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "bearer":
		token, err := auth.RequiredString("token")
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
	case "basic":
		username, err := auth.RequiredString("username")
		if err != nil {
			return err
		}
		password, _, err := auth.String("password")
		if err != nil {
			return err
		}
		credentials := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
		req.Header.Set("Authorization", "Basic "+credentials)
	case "apikey":
		name, err := auth.RequiredString("name")
		if err != nil {
			return err
		}
		value, err := auth.RequiredString("value")
		if err != nil {
			return err
		}
		req.Header.Set(name, value)
	default:
		return fmt.Errorf("neuron:core:http: unknown authentication type %q; supported types are bearer, basic, apiKey", kind)
	}

	return nil
}

// representBody converts the response body into the representation the caller
// asked for.
//
// A JSON body is returned as the value it represents, never as a stringified
// copy of it, because a stringified object cannot be bound into a downstream
// capability. A body that is not JSON is refused in the JSON format, and
// returned as text in the text format.
func representBody(body []byte, format input.Format) (any, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		if format == input.FormatText {
			return "", nil
		}
		return nil, nil
	}

	if format == input.FormatText {
		return string(body), nil
	}

	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, fmt.Errorf("neuron:core:http: the response body is not JSON (it starts with %q); use format \"text\" to receive it as text",
			preview(body))
	}
	return value, nil
}

// responseHeaders returns the response headers as a plain map.
func responseHeaders(header nethttp.Header) map[string]string {
	headers := make(map[string]string, len(header))
	for name, values := range header {
		headers[name] = strings.Join(values, ", ")
	}
	return headers
}

// preview returns the beginning of a body, for an error message.
func preview(body []byte) string {
	const limit = 40
	text := strings.TrimSpace(string(body))
	if len(text) > limit {
		return text[:limit] + "..."
	}
	return text
}
