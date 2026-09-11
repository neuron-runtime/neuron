package connection

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	shared "github.com/Muhammad-Jay/neuron/shared/protocol/websocket"
)

func TestToWebSocketURL(t *testing.T) {
	cases := []struct {
		baseURL string
		path    string
		want    string
	}{
		{"http://nore.local", "/v1/ws", "ws://nore.local/v1/ws"},
		{"https://api.nore.example.com", "/v1/ws", "wss://api.nore.example.com/v1/ws"},
		{"http://nore.local/", "/v1/ws", "ws://nore.local/v1/ws"},
		{"https://api.nore.example.com/", "/v1/ws", "wss://api.nore.example.com/v1/ws"},
	}
	for _, tc := range cases {
		got, err := toWebSocketURL(tc.baseURL, tc.path)
		if err != nil {
			t.Errorf("toWebSocketURL(%q, %q): %v", tc.baseURL, tc.path, err)
			continue
		}
		if got != tc.want {
			t.Errorf("toWebSocketURL(%q, %q) = %q, want %q", tc.baseURL, tc.path, got, tc.want)
		}
	}

	if _, err := toWebSocketURL("ftp://nore", "/v1/ws"); err == nil {
		t.Error("expected error for unsupported scheme")
	}
}

// mockWebSocketServer speaks the neuron.v1 protocol: it accepts a
// subscription, acknowledges it, and optionally publishes events.
type mockWebSocketServer struct {
	events []shared.Message
}

func newMockWebSocketServer(t *testing.T, publish []shared.Message) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			Subprotocols:       []string{"neuron.v1"},
			InsecureSkipVerify: true,
		})
		if err != nil {
			return
		}
		defer conn.CloseNow()

		for {
			var msg shared.Message
			if err := wsjson.Read(r.Context(), conn, &msg); err != nil {
				return
			}
			switch msg.Type {
			case shared.MessageSubscribe:
				_ = wsjson.Write(r.Context(), conn, shared.Message{
					Type:      shared.MessageSubscribed,
					RequestID: msg.RequestID,
					Room:      msg.Room,
				})
				// Follow with any pre-supplied events.
				for _, evt := range publish {
					if err := wsjson.Write(r.Context(), conn, evt); err != nil {
						return
					}
				}
				// Wait for the client to leave or the test to end.
				var waitMsg shared.Message
				if err := wsjson.Read(r.Context(), conn, &waitMsg); err != nil {
					return
				}
			case shared.MessageUnsubscribe:
				_ = wsjson.Write(r.Context(), conn, shared.Message{
					Type:      shared.MessageUnsubscribed,
					RequestID: msg.RequestID,
					Room:      msg.Room,
				})
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestWebSocketStreamSubscribeAndReceive(t *testing.T) {
	srv := newMockWebSocketServer(t, []shared.Message{
		{
			Type:  shared.MessageEvent,
			Room:  "exec:inst_1:exec_1",
			Event: "execution.completed",
			Data:  []byte(`{"id":"evt_1","type":"execution.completed"}`),
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	transport := NewHTTPTransport(nil, srv.URL)
	stream, err := transport.OpenWebSocket(ctx, "/v1/ws")
	if err != nil {
		t.Fatalf("open websocket: %v", err)
	}
	defer stream.Close()

	if err := stream.Subscribe(ctx, "exec:inst_1:exec_1", "req-1"); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	msg, err := stream.Receive(ctx)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if msg.Type != shared.MessageEvent || msg.Event != "execution.completed" {
		t.Fatalf("unexpected message: %+v", msg)
	}
}

func TestWebSocketStreamSubscribeRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			Subprotocols:       []string{"neuron.v1"},
			InsecureSkipVerify: true,
		})
		if err != nil {
			return
		}
		defer conn.CloseNow()

		var msg shared.Message
		if err := wsjson.Read(r.Context(), conn, &msg); err != nil {
			return
		}
		_ = wsjson.Write(r.Context(), conn, shared.Message{
			Type:      shared.MessageError,
			RequestID: msg.RequestID,
			Error: &shared.ErrorPayload{
				Code:    "not_found",
				Message: "execution not found",
			},
		})
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	transport := NewHTTPTransport(nil, srv.URL)
	stream, err := transport.OpenWebSocket(ctx, "/v1/ws")
	if err != nil {
		t.Fatalf("open websocket: %v", err)
	}
	defer stream.Close()

	err = stream.Subscribe(ctx, "exec:inst_1:exec_missing", "req-1")
	if err == nil {
		t.Fatal("expected subscription rejection error")
	}
}

// TestHTTPTransportOpenWebSocketConcurrency is a sanity test that the
// WebSocket session and regular HTTP transport coexist on one HTTPTransport.
func TestHTTPTransportOpenWebSocketConcurrency(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "" {
			conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
				Subprotocols:       []string{"neuron.v1"},
				InsecureSkipVerify: true,
			})
			if err != nil {
				return
			}
			conn.CloseNow()
			return
		}
		mu.Lock()
		calls++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	transport := NewHTTPTransport(nil, srv.URL)

	var out map[string]string
	if err := transport.Do(context.Background(), http.MethodGet, "/health", nil, &out); err != nil {
		t.Fatalf("do: %v", err)
	}
	if calls != 1 {
		t.Fatalf("http calls = %d, want 1", calls)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := transport.OpenWebSocket(ctx, "/v1/ws"); err != nil {
		t.Fatalf("open websocket: %v", err)
	}
}
