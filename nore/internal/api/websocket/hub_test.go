package websocket

import (
	"testing"

	shared "github.com/Muhammad-Jay/neuron/shared/protocol/websocket"
)

func newTestClient(hub *Hub) *Client {
	c := NewClient(nil, hub, nil)
	c.send = make(chan shared.Message, 64)
	hub.AddClient(c)
	return c
}

func awaitMessage(t *testing.T, c *Client) shared.Message {
	t.Helper()
	select {
	case msg := <-c.send:
		return msg
	default:
		t.Fatal("expected a message to be queued")
		return shared.Message{}
	}
}

func TestHubSubscribeAndPublish(t *testing.T) {
	hub := NewHub()
	client := newTestClient(hub)

	if err := hub.Subscribe(client, "exec:inst_1:exec_1"); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	hub.Publish("exec:inst_1:exec_1", "execution.started", map[string]any{"input": map[string]any{}})

	msg := awaitMessage(t, client)
	if msg.Type != shared.MessageEvent {
		t.Fatalf("expected event message, got %q", msg.Type)
	}
	if msg.Room != "exec:inst_1:exec_1" {
		t.Fatalf("unexpected room %q", msg.Room)
	}
	if msg.Event != "execution.started" {
		t.Fatalf("unexpected event name %q", msg.Event)
	}
}

// TestHubUnsubscribeKeepsOtherSubscribers guards against the historical
// unconditional room deletion that kicked other clients out of a room.
func TestHubUnsubscribeKeepsOtherSubscribers(t *testing.T) {
	hub := NewHub()
	a := newTestClient(hub)
	b := newTestClient(hub)

	room := "exec:inst_1:exec_1"
	if err := hub.Subscribe(a, room); err != nil {
		t.Fatalf("subscribe a: %v", err)
	}
	if err := hub.Subscribe(b, room); err != nil {
		t.Fatalf("subscribe b: %v", err)
	}

	if err := hub.Unsubscribe(a, room); err != nil {
		t.Fatalf("unsubscribe a: %v", err)
	}

	hub.Publish(room, "execution.completed", map[string]any{})

	// b must still receive the event; a must not.
	msg := awaitMessage(t, b)
	if msg.Type != shared.MessageEvent {
		t.Fatalf("b expected event, got %q", msg.Type)
	}
	select {
	case <-a.send:
		t.Fatal("unsubscribed client received an event")
	default:
	}
}

func TestHubUnsubscribeUnknownRoom(t *testing.T) {
	hub := NewHub()
	client := newTestClient(hub)
	if err := hub.Unsubscribe(client, "exec:inst_1:exec_none"); err != nil {
		t.Fatalf("unsubscribe unknown room should not error: %v", err)
	}
}

func TestHubRemoveClientCleansRooms(t *testing.T) {
	hub := NewHub()
	a := newTestClient(hub)
	b := newTestClient(hub)

	room := "exec:inst_1:exec_1"
	_ = hub.Subscribe(a, room)
	_ = hub.Subscribe(b, room)

	hub.RemoveClient(a)

	hub.Publish(room, "execution.completed", map[string]any{})
	msg := awaitMessage(t, b)
	if msg.Type != shared.MessageEvent {
		t.Fatalf("b expected event, got %q", msg.Type)
	}
}

func TestClientSendError(t *testing.T) {
	hub := NewHub()
	client := newTestClient(hub)

	client.sendError("req-1", ErrRoomInvalid)

	msg := awaitMessage(t, client)
	if msg.Type != shared.MessageError {
		t.Fatalf("expected error message, got %q", msg.Type)
	}
	if msg.RequestID != "req-1" {
		t.Fatalf("unexpected request id %q", msg.RequestID)
	}
	if msg.Error == nil || msg.Error.Code == "" || msg.Error.Message == "" {
		t.Fatal("expected populated error payload")
	}
}
