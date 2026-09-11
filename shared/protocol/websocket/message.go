package websocket

import "encoding/json"

type MessageType string

const (
	MessageSubscribe    MessageType = "subscribe"
	MessageUnsubscribe  MessageType = "unsubscribe"
	MessageSubscribed   MessageType = "subscribed"
	MessageUnsubscribed MessageType = "unsubscribed"

	MessageEvent MessageType = "event"
	MessageError MessageType = "error"
)

// roomPrefixes are the namespaces for the rooms a client may subscribe to.
const (
	RoomPrefixExecution = "exec"
)

// ExecutionRoom returns the WebSocket room address for an execution. Both the
// server (event bridge) and the client (streaming CLI) construct rooms with
// this single authority so the contract cannot drift.
func ExecutionRoom(instanceID, executionID string) string {
	return RoomPrefixExecution + ":" + instanceID + ":" + executionID
}

type Message struct {
	Type      MessageType     `json:"type"`
	RequestID string          `json:"requestId,omitempty"`
	Room      string          `json:"room,omitempty"`
	Event     string          `json:"event,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
	Error     *ErrorPayload   `json:"error,omitempty"`
}

type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
