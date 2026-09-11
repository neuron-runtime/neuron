package websocket

import (
	"encoding/json"
	"sync"

	shared "github.com/Muhammad-Jay/neuron/shared/protocol/websocket"
)

type Hub struct {
	mu sync.RWMutex

	clients map[*Client]struct{}
	rooms   map[string]map[*Client]struct{}
}

func NewHub() *Hub {
	return &Hub{
		mu:      sync.RWMutex{},
		clients: make(map[*Client]struct{}),
		rooms:   make(map[string]map[*Client]struct{}),
	}
}

func (h *Hub) AddClient(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.clients[client] = struct{}{}
}

func (h *Hub) RemoveClient(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	delete(h.clients, client)

	for room := range h.rooms {
		delete(h.rooms[room], client)

		if len(h.rooms[room]) == 0 {
			delete(h.rooms, room)
		}
	}
}

func (h *Hub) Subscribe(client *Client, room string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.rooms[room]; !ok {
		h.rooms[room] = make(map[*Client]struct{})
	}

	h.rooms[room][client] = struct{}{}
	h.clients[client] = struct{}{}

	return nil
}

func (h *Hub) Unsubscribe(client *Client, room string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	subscriber, ok := h.rooms[room]
	if !ok {
		return nil
	}

	delete(subscriber, client)

	if len(subscriber) == 0 {
		delete(h.rooms, room)
	}

	return nil
}

func (h *Hub) Publish(
	room string,
	event string,
	data any,
) {
	payload, err := json.Marshal(data)
	if err != nil {
		return
	}

	msg := shared.Message{
		Type:  shared.MessageEvent,
		Room:  room,
		Event: event,
		Data:  payload,
	}

	h.mu.RLock()
	clients := make([]*Client, 0, len(h.rooms[room]))

	for client := range h.rooms[room] {
		clients = append(clients, client)
	}

	h.mu.RUnlock()

	for _, client := range clients {
		client.sendMessage(msg)
	}
}
