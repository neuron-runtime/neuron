package websocket

import "errors"

var (
	// ErrRoomRequired is returned when a subscribe/unsubscribe message omits
	// the room field.
	ErrRoomRequired = errors.New("room is required")
	// ErrRoomInvalid is returned when a room name does not satisfy the room
	// naming contract enforced by validateRoom.
	ErrRoomInvalid = errors.New("invalid room name")
)
