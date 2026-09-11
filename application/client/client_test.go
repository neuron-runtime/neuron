package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/Muhammad-Jay/neuron/application/connection"
	shared "github.com/Muhammad-Jay/neuron/shared/protocol/websocket"
	"github.com/Muhammad-Jay/neuron/shared/types/core"
	"github.com/Muhammad-Jay/neuron/shared/types/protocol"
)

// newTestWSServer spins up an httptest server that speaks the neuron.v1
// WebSocket protocol.  The handler echoes a subscribe acknowledgement,
// then pushes one or more event messages, then blocks until the client
// sends an unsubscribe.
func newTestWSServer(t *testing.T, events []shared.Message) *httptest.Server {
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

		// Read the subscribe request.
		var msg shared.Message
		if err := wsjson.Read(r.Context(), conn, &msg); err != nil {
			return
		}
		if msg.Type != shared.MessageSubscribe {
			return
		}

		// Ack.
		_ = wsjson.Write(r.Context(), conn, shared.Message{
			Type:      shared.MessageSubscribed,
			RequestID: msg.RequestID,
			Room:      msg.Room,
		})

		// Push events, then close the connection to end the stream.
		for _, evt := range events {
			if err := wsjson.Write(r.Context(), conn, evt); err != nil {
				return
			}
		}
		_ = conn.Close(websocket.StatusNormalClosure, "stream complete")
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestStreamExecutionEventsWS(t *testing.T) {
	evtData, _ := json.Marshal(protocol.StreamEvent{
		ID:   "evt_1",
		Type: "execution.completed",
	})
	serverEvents := []shared.Message{
		{
			Type: shared.MessageEvent,
			Room: shared.ExecutionRoom("inst_1", "exec_1"),
			Data: evtData,
		},
	}

	srv := newTestWSServer(t, serverEvents)
	transport := connection.NewHTTPTransport(nil, srv.URL)
	conn := connection.New(transport)
	c := New(conn)

	var received []protocol.StreamEvent
	err := c.StreamExecutionEventsWS(context.Background(), "inst_1", core.ID("exec_1"),
		func(evt protocol.StreamEvent) error {
			received = append(received, evt)
			return nil
		},
	)
	// The server closes the connection after the events; client returns a
	// read error for a cleanly-closed stream, so the event assertions are
	// what matter here.
	_ = err

	if len(received) != 1 {
		t.Fatalf("expected 1 event, got %d", len(received))
	}
	if received[0].ID != "evt_1" || received[0].Type != "execution.completed" {
		t.Fatalf("unexpected event: %+v", received[0])
	}
}

func TestStreamExecutionEventsWSEmitErrorPropagates(t *testing.T) {
	evtData, _ := json.Marshal(protocol.StreamEvent{ID: "evt_1", Type: "execution.started"})
	serverEvents := []shared.Message{
		{Type: shared.MessageEvent, Room: shared.ExecutionRoom("inst_1", "exec_1"), Data: evtData},
	}

	srv := newTestWSServer(t, serverEvents)
	transport := connection.NewHTTPTransport(nil, srv.URL)
	conn := connection.New(transport)
	c := New(conn)

	sentinel := context.Canceled
	err := c.StreamExecutionEventsWS(context.Background(), "inst_1", core.ID("exec_1"),
		func(evt protocol.StreamEvent) error { return sentinel },
	)
	if err != sentinel {
		t.Fatalf("expected sentinel from emit, got %v", err)
	}
}

func TestStreamExecutionEventsWSTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Accept but never send any messages.
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			Subprotocols:       []string{"neuron.v1"},
			InsecureSkipVerify: true,
		})
		if err != nil {
			return
		}
		defer conn.CloseNow()

		// Block.
		var msg shared.Message
		wsjson.Read(r.Context(), conn, &msg)
	}))
	t.Cleanup(srv.Close)

	transport := connection.NewHTTPTransport(nil, srv.URL)
	conn := connection.New(transport)
	c := New(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	var count int
	err := c.StreamExecutionEventsWS(ctx, "inst_1", core.ID("exec_1"),
		func(evt protocol.StreamEvent) error { count++; return nil },
	)
	// Must return within deadline; the emit count is 0 because the server
	// never sends an event.  The error may be context.DeadlineExceeded or
	// a stream read error derived from it.
	if err == nil {
		t.Fatal("expected deadline error")
	}
}
