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

	"github.com/neuron-runtime/neuron/application/connection"
	shared "github.com/neuron-runtime/neuron/shared/protocol/websocket"
	"github.com/neuron-runtime/neuron/shared/types/core"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
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

func TestSignalExecution(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody protocol.SignalDeliveryRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(protocol.Response{
			Status:  http.StatusOK,
			Message: "execution signaled",
			Data: protocol.SignalDeliveryResponse{
				DeliveryID: "del_1",
				SignalName: "approved",
				Status:     "accepted",
				Replay:     false,
			},
		})
	}))
	t.Cleanup(srv.Close)

	c := NewEndpoint(srv.URL)
	resp, err := c.SignalExecution(context.Background(), "exec_1", "approved", protocol.SignalDeliveryRequest{
		DeliveryID:     "del_1",
		Payload:        map[string]any{"status": "approved"},
		CorrelationKey: "cap-key",
	})
	if err != nil {
		t.Fatalf("SignalExecution: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Fatalf("method = %s, want POST", gotMethod)
	}
	if gotPath != protocol.ExecutionSignal("exec_1", "approved") {
		t.Fatalf("path = %s, want %s", gotPath, protocol.ExecutionSignal("exec_1", "approved"))
	}
	if gotBody.DeliveryID != "del_1" || gotBody.CorrelationKey != "cap-key" {
		t.Fatalf("body = %+v", gotBody)
	}
	if gotBody.Payload["status"] != "approved" {
		t.Fatalf("payload = %+v", gotBody.Payload)
	}
	if resp.DeliveryID != "del_1" || resp.SignalName != "approved" || resp.Replay || resp.Status != "accepted" {
		t.Fatalf("response = %+v", resp)
	}
}

func TestSignalExecutionPropagatesStatusErrorCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(protocol.Response{
			Status:  http.StatusConflict,
			Message: "ambiguous wait",
			Code:    protocol.SignalCodeAmbiguousWait,
		})
	}))
	t.Cleanup(srv.Close)

	c := NewEndpoint(srv.URL)
	_, err := c.SignalExecution(context.Background(), "exec_1", "approved", protocol.SignalDeliveryRequest{DeliveryID: "del_1"})
	if err == nil {
		t.Fatal("expected error")
	}
	if connection.StatusCode(err) != http.StatusConflict {
		t.Fatalf("status = %d, want 409", connection.StatusCode(err))
	}
	if connection.ResponseCode(err) != protocol.SignalCodeAmbiguousWait {
		t.Fatalf("response code = %q, want %q", connection.ResponseCode(err), protocol.SignalCodeAmbiguousWait)
	}
}

func TestListInstanceExecutionsWithStatusQuery(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		deadline := int64(123)
		_ = json.NewEncoder(w).Encode(protocol.Response{
			Status:  http.StatusOK,
			Message: "executions",
			Data: []protocol.ExecutionItem{
				{
					ID:     "exec_1",
					Status: "waiting",
					Waits: []protocol.WaitItem{
						{
							CapabilityID:   "cap_1",
							WaitType:       "signal",
							ExpectedSignal: "approved",
							WaitReason:     "approval required",
							CorrelationKey: "k1",
							Deadline:       &deadline,
						},
					},
				},
			},
		})
	}))
	t.Cleanup(srv.Close)

	c := NewEndpoint(srv.URL)
	items, err := c.ListInstanceExecutions(context.Background(), "inst_1", "?status=waiting")
	if err != nil {
		t.Fatalf("ListInstanceExecutions: %v", err)
	}
	if gotPath != protocol.InstanceExecutions("inst_1") {
		t.Fatalf("path = %s", gotPath)
	}
	if gotQuery != "status=waiting" {
		t.Fatalf("query = %q", gotQuery)
	}
	if len(items) != 1 || items[0].Status != "waiting" || len(items[0].Waits) != 1 {
		t.Fatalf("items = %+v", items)
	}
	wait := items[0].Waits[0]
	if wait.ExpectedSignal != "approved" || wait.CorrelationKey != "k1" || wait.WaitReason != "approval required" || wait.Deadline == nil || *wait.Deadline != 123 {
		t.Fatalf("wait = %+v", wait)
	}
}
