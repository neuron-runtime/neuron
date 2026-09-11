package websocket

import "strings"

// validateRoom enforces the room naming contract used across the API. Rooms
// are `:`-separated segments (for example `inst:inst_123` or
// `exec:exec_456`); each segment must be non-empty and drawn from the DNS-like
// character set so room names are safe to log, persist, and match without
// canonicalization.
func validateRoom(room string) error {
	if room == "" {
		return ErrRoomInvalid
	}
	for _, segment := range strings.Split(room, ":") {
		if segment == "" {
			return ErrRoomInvalid
		}
		for _, r := range segment {
			ok := r == '-' || r == '_' ||
				(r >= '0' && r <= '9') ||
				(r >= 'a' && r <= 'z') ||
				(r >= 'A' && r <= 'Z')
			if !ok {
				return ErrRoomInvalid
			}
		}
	}
	return nil
}
