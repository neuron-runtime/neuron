package websocket

import (
	"context"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// Read is a adapter for the wsjson, it serve as a wrapper around it.
func Read(ctx context.Context, conn *websocket.Conn, v any) error {

	if err := wsjson.Read(ctx, conn, v); err != nil {
		return err
	}
	return nil
}

func Write(ctx context.Context, conn *websocket.Conn, v any) error {

	if err := wsjson.Write(ctx, conn, v); err != nil {
		return err
	}
	return nil
}
