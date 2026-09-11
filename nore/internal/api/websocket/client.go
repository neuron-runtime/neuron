package websocket

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/Muhammad-Jay/neuron/shared/types/protocol"
	"github.com/coder/websocket"

	shared "github.com/Muhammad-Jay/neuron/shared/protocol/websocket"
)

type Client struct {
	conn *websocket.Conn
	hub  *Hub

	provider RoomProvider

	send chan shared.Message

	mu      sync.Mutex
	streams map[string]context.CancelFunc
}

func NewClient(conn *websocket.Conn, hub *Hub, provider RoomProvider) *Client {
	return &Client{
		conn:     conn,
		hub:      hub,
		provider: provider,
		send:     make(chan shared.Message, 64),
		streams:  make(map[string]context.CancelFunc),
	}
}

func (c *Client) writeLoop(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-c.send:
			if !ok {
				return nil
			}

			if err := Write(ctx, c.conn, msg); err != nil {
				return err
			}
		}
	}
}

func (c *Client) readLoop(ctx context.Context) error {
	for {
		var msg shared.Message

		if err := Read(ctx, c.conn, &msg); err != nil {
			return err
		}

		if err := c.handleMessage(ctx, msg); err != nil {
			c.sendError(msg.RequestID, err)
		}
	}
}

func (c *Client) handleMessage(ctx context.Context, msg shared.Message) error {
	switch msg.Type {
	case shared.MessageSubscribe:
		return c.handleSubscribe(ctx, msg)
	case shared.MessageUnsubscribe:
		return c.handleUnsubscribe(msg)

	default:
		return fmt.Errorf("unsupported message type %q", msg.Type)
	}
}

func (c *Client) handleSubscribe(ctx context.Context, msg shared.Message) error {
	if msg.Room == "" {
		return ErrRoomRequired
	}

	if err := validateRoom(msg.Room); err != nil {
		return err
	}

	var events <-chan protocol.StreamEvent
	if c.provider != nil {
		stream, err := c.provider.Stream(ctx, msg.Room)
		if err != nil {
			return err
		}
		events = stream
	}

	if err := c.hub.Subscribe(c, msg.Room); err != nil {
		return err
	}

	c.sendMessage(shared.Message{
		Type:      shared.MessageSubscribed,
		RequestID: msg.RequestID,
		Room:      msg.Room,
	})

	if events != nil {
		streamCtx, cancel := context.WithCancel(ctx)
		c.trackStream(msg.Room, cancel)
		go c.forwardStream(streamCtx, msg.Room, events)
	}

	return nil
}

func (c *Client) handleUnsubscribe(msg shared.Message) error {
	if msg.Room == "" {
		return ErrRoomRequired
	}

	if err := validateRoom(msg.Room); err != nil {
		return err
	}

	c.stopStream(msg.Room)

	if err := c.hub.Unsubscribe(c, msg.Room); err != nil {
		return err
	}

	c.sendMessage(shared.Message{
		Type:      shared.MessageUnsubscribed,
		RequestID: msg.RequestID,
		Room:      msg.Room,
	})

	return nil
}

// forwardStream relays provider events for a subscribed room to the client as
// WebSocket event messages. It stops when the stream closes or the room's
// context is cancelled.
func (c *Client) forwardStream(ctx context.Context, room string, events <-chan protocol.StreamEvent) {
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-events:
			if !ok {
				return
			}
			data, err := json.Marshal(evt)
			if err != nil {
				continue
			}
			c.sendMessage(shared.Message{
				Type:  shared.MessageEvent,
				Room:  room,
				Event: evt.Type,
				Data:  data,
			})
		}
	}
}

func (c *Client) trackStream(room string, cancel context.CancelFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if prior, ok := c.streams[room]; ok {
		prior()
	}
	c.streams[room] = cancel
}

func (c *Client) stopStream(room string) {
	c.mu.Lock()
	cancel, ok := c.streams[room]
	if ok {
		delete(c.streams, room)
	}
	c.mu.Unlock()
	if ok {
		cancel()
	}
}

func (c *Client) stopAllStreams() {
	c.mu.Lock()
	streams := c.streams
	c.streams = make(map[string]context.CancelFunc)
	c.mu.Unlock()
	for _, cancel := range streams {
		cancel()
	}
}

func (c *Client) sendMessage(msg shared.Message) {
	select {
	case c.send <- msg:
	default:
		// Client is too slow. Drop the message rather than blocking the
		// publisher; a client that cannot keep up will observe a partial
		// stream and can reconnect to resume from the persisted history.
	}
}

func (c *Client) sendError(requestID string, err error) {
	c.sendMessage(shared.Message{
		Type:      shared.MessageError,
		RequestID: requestID,
		Error: &shared.ErrorPayload{
			Code:    "internal_error",
			Message: err.Error(),
		},
	})
}
