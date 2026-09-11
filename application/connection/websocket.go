package connection

import (
	"context"
	"errors"
	"fmt"
	"strings"

	shared "github.com/Muhammad-Jay/neuron/shared/protocol/websocket"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// ErrWebSocketUnavailable is returned when a connection cannot establish a
// WebSocket session (for example an in-memory or non-streaming transport).
var ErrWebSocketUnavailable = errors.New("websocket transport is not available")

// WebSocketStream is a client-side WebSocket session used for real-time event
// streaming from N.O.R.E. It speaks the neuron.v1 subprotocol with room-based
// subscriptions and structured JSON messages.
type WebSocketStream struct {
	conn *websocket.Conn
}

// Subscribe sends a subscribe message for the room and waits for the server
// acknowledgement. It returns an error if the server rejects the subscription.
func (s *WebSocketStream) Subscribe(ctx context.Context, room, requestID string) error {
	if err := wsjson.Write(ctx, s.conn, shared.Message{
		Type:      shared.MessageSubscribe,
		RequestID: requestID,
		Room:      room,
	}); err != nil {
		return fmt.Errorf("websocket subscribe: %w", err)
	}

	for {
		msg, err := s.Receive(ctx)
		if err != nil {
			return err
		}
		switch msg.Type {
		case shared.MessageSubscribed:
			if msg.RequestID == requestID {
				return nil
			}
		case shared.MessageError:
			if msg.RequestID == requestID && msg.Error != nil {
				return fmt.Errorf("websocket subscription rejected: %s: %s", msg.Error.Code, msg.Error.Message)
			}
		}
	}
}

// Unsubscribe sends an unsubscribe message for the room.
func (s *WebSocketStream) Unsubscribe(ctx context.Context, room, requestID string) error {
	if err := wsjson.Write(ctx, s.conn, shared.Message{
		Type:      shared.MessageUnsubscribe,
		RequestID: requestID,
		Room:      room,
	}); err != nil {
		return fmt.Errorf("websocket unsubscribe: %w", err)
	}
	return nil
}

// Receive reads the next message from the session.
func (s *WebSocketStream) Receive(ctx context.Context) (shared.Message, error) {
	var msg shared.Message
	if err := wsjson.Read(ctx, s.conn, &msg); err != nil {
		return shared.Message{}, err
	}
	return msg, nil
}

// Close terminates the WebSocket session with a normal closure.
func (s *WebSocketStream) Close() error {
	return s.conn.Close(websocket.StatusNormalClosure, "")
}

// toWebSocketURL maps an HTTP(S) endpoint and request path to its WebSocket
// equivalent (ws/wss), preserving the path.
func toWebSocketURL(baseURL, requestPath string) (string, error) {
	baseURL = strings.TrimRight(baseURL, "/")
	switch {
	case strings.HasPrefix(baseURL, "https://"):
		return "wss://" + strings.TrimPrefix(baseURL, "https://") + requestPath, nil
	case strings.HasPrefix(baseURL, "http://"):
		return "ws://" + strings.TrimPrefix(baseURL, "http://") + requestPath, nil
	default:
		return "", fmt.Errorf("unsupported endpoint scheme %q", baseURL)
	}
}
