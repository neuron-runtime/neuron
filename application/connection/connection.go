package connection

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	sharedtoken "github.com/neuron-runtime/neuron/shared/types/apitoken"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

type Connection interface {
	Do(ctx context.Context, method, path string, body any, out any) error
	Stream(ctx context.Context, method, path string, body any, emit func([]byte) error) error
	OpenWebSocket(ctx context.Context, requestPath string) (*WebSocketStream, error)
	Health(ctx context.Context) error
	Close() error
}

type Transport interface {
	Do(ctx context.Context, method, path string, body any, out any) error
	Stream(ctx context.Context, method, path string, body any, emit func([]byte) error) error
	OpenWebSocket(ctx context.Context, requestPath string) (*WebSocketStream, error)
	Close() error
}

type connection struct {
	transport Transport
}

func New(transport Transport) Connection {
	return &connection{transport: transport}
}

func (c *connection) Do(ctx context.Context, method, path string, body any, out any) error {
	return c.transport.Do(ctx, method, path, body, out)
}

func (c *connection) Stream(ctx context.Context, method, path string, body any, emit func([]byte) error) error {
	return c.transport.Stream(ctx, method, path, body, emit)
}

func (c *connection) OpenWebSocket(ctx context.Context, requestPath string) (*WebSocketStream, error) {
	return c.transport.OpenWebSocket(ctx, requestPath)
}

func (c *connection) Health(ctx context.Context) error {
	var response protocol.Response
	if err := c.Do(ctx, http.MethodGet, protocol.HealthPath, nil, &response); err != nil {
		return err
	}
	if response.Status < 200 || response.Status >= 300 {
		return fmt.Errorf("nore health check failed: status=%d message=%s", response.Status, response.Message)
	}
	return nil
}

func (c *connection) Close() error {
	return c.transport.Close()
}

// HTTPTransport is the common protocol implementation used by both
// remote HTTP and local Unix-domain-socket connections.
type HTTPTransport struct {
	client  *http.Client
	baseURL string
	// token is the N.O.R.E. API credential presented on every request. An
	// empty token sends no credential, which only works against a daemon that
	// runs unauthenticated.
	token string
}

func NewHTTPTransport(client *http.Client, baseURL string) *HTTPTransport {
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	return &HTTPTransport{
		client:  client,
		baseURL: strings.TrimRight(baseURL, "/"),
	}
}

// WithToken returns the transport configured to authenticate with the given API
// token.
func (t *HTTPTransport) WithToken(token string) *HTTPTransport {
	t.token = token
	return t
}

// authorize attaches the API credential to a request. The token travels in a
// header rather than the URL so it cannot leak into server access logs.
func (t *HTTPTransport) authorize(req *http.Request) {
	if t.token == "" {
		return
	}
	req.Header.Set(sharedtoken.AuthorizationHeader, sharedtoken.Header(t.token))
}

func (t *HTTPTransport) Do(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		reader = strings.NewReader(string(payload))
	}

	req, err := http.NewRequestWithContext(ctx, method, t.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	t.authorize(req)

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("nore request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return statusErrorFrom(resp.StatusCode, data)
	}

	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// StatusError reports an HTTP response N.O.R.E. refused.
//
// It exists so a caller can tell *why* a request was rejected without parsing the
// message text. That distinction is load-bearing for cancellation: "there is no
// such execution" and "it already finished" call for completely different
// messages to an operator, and a client that cannot separate them either reports
// a false failure or hides a real one.
type StatusError struct {
	Code    int
	Message string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("nore returned HTTP %d: %s", e.Code, e.Message)
}

// statusErrorFrom builds the error for a rejected response, preserving the
// message format callers already surface.
func statusErrorFrom(code int, body []byte) error {
	return &StatusError{Code: code, Message: strings.TrimSpace(string(body))}
}

// StatusCode reports the HTTP status N.O.R.E. returned, or 0 when err did not
// come from an HTTP status.
func StatusCode(err error) int {
	var status *StatusError
	if errors.As(err, &status) {
		return status.Code
	}
	return 0
}

func (t *HTTPTransport) Stream(ctx context.Context, method, path string, body any, emit func([]byte) error) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		reader = strings.NewReader(string(payload))
	}

	req, err := http.NewRequestWithContext(ctx, method, t.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "text/event-stream")
	t.authorize(req)

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("nore request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return statusErrorFrom(resp.StatusCode, data)
	}

	// SSE line scanner: each event is "data: <json>\n\n"
	scanner := bufio.NewScanner(resp.Body)
	scanner.Split(bufio.ScanLines)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		// SSE data line
		if len(line) > 6 && string(line[:6]) == "data: " {
			if err := emit(line[6:]); err != nil {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("stream scan error: %w", err)
	}
	return nil
}

func (t *HTTPTransport) Close() error { return nil }

// OpenWebSocket establishes a WebSocket session to the given request path. The
// dial uses the same transport configuration as regular HTTP requests, so a
// Unix-socket-backed HTTPTransport dials the socket and an HTTP transport
// dials over the network.
func (t *HTTPTransport) OpenWebSocket(ctx context.Context, requestPath string) (*WebSocketStream, error) {
	wsURL, err := toWebSocketURL(t.baseURL, requestPath)
	if err != nil {
		return nil, err
	}

	// The upgrade request is authenticated the same way as any other request.
	// coder/websocket passes HTTPHeader onto the handshake, so the credential
	// stays in a header instead of the URL.
	header := http.Header{}
	if t.token != "" {
		header.Set(sharedtoken.AuthorizationHeader, sharedtoken.Header(t.token))
	}

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		Subprotocols: []string{"neuron.v1"},
		HTTPClient:   t.client,
		HTTPHeader:   header,
	})
	if err != nil {
		return nil, fmt.Errorf("websocket dial %s: %w", wsURL, err)
	}

	return &WebSocketStream{conn: conn}, nil
}
