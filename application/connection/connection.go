package connection

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Muhammad-Jay/neuron/shared/types/protocol"
	"github.com/coder/websocket"
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

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("nore request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return fmt.Errorf("nore returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
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

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("nore request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return fmt.Errorf("nore returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
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

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		Subprotocols: []string{"neuron.v1"},
		HTTPClient:   t.client,
	})
	if err != nil {
		return nil, fmt.Errorf("websocket dial %s: %w", wsURL, err)
	}

	return &WebSocketStream{conn: conn}, nil
}
