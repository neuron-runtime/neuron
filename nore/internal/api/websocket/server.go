package websocket

import (
	"context"
	"net/http"

	"github.com/Muhammad-Jay/neuron/shared/types/protocol"
	"github.com/coder/websocket"
)

// RoomProvider supplies ordered per-subscriber event streams for rooms that
// back real-time data (for example execution event rooms). A provider-backed
// room delivers history plus live events to each subscriber independently,
// mirroring the SSE endpoint. Providers return a nil channel when they do not
// recognize the room.
type RoomProvider interface {
	Stream(ctx context.Context, room string) (<-chan protocol.StreamEvent, error)
}

type WebSocketHandler struct {
	hub      *Hub
	provider RoomProvider
}

func NewWebSocketHandler() *WebSocketHandler {
	return &WebSocketHandler{
		hub: NewHub(),
	}
}

// SetRoomProvider registers the provider that backs streaming rooms. It must
// be called before the handler serves connections.
func (s *WebSocketHandler) SetRoomProvider(provider RoomProvider) {
	s.provider = provider
}

func (s *WebSocketHandler) Hub() *Hub {
	return s.hub
}

func (s *WebSocketHandler) HandleWebSocket(
	w http.ResponseWriter,
	r *http.Request,
) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols:       []string{"neuron.v1"},
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}

	conn.SetReadLimit(64 * 1024)

	client := NewClient(conn, s.hub, s.provider)

	s.hub.AddClient(client)

	defer func() {
		client.stopAllStreams()
		s.hub.RemoveClient(client)
		conn.Close(websocket.StatusNormalClosure, "")
	}()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	errCh := make(chan error, 2)

	go func() {
		errCh <- client.readLoop(ctx)
	}()

	go func() {
		errCh <- client.writeLoop(ctx)
	}()

	<-errCh
	cancel()
}
