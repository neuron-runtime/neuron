package websocket

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestHandleWebSocketRejectsCrossOriginUpgrade pins the origin check.
//
// The daemon is reachable through the local socket, but a page in a user's
// browser can also reach an http:// origin on the same machine. If the upgrade
// accepted any Origin, a malicious page could open an authenticated session and
// subscribe to execution events. The default verification compares Origin to
// the request Host and must stay enabled.
func TestHandleWebSocketRejectsCrossOriginUpgrade(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(NewWebSocketHandler().HandleWebSocket))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	header := http.Header{}
	header.Set("Origin", "https://attacker.example")

	_, resp, err := websocket.Dial(ctx, toWSURL(srv.URL), &websocket.DialOptions{
		HTTPHeader: header,
	})
	if err == nil {
		t.Fatal("upgrade from a foreign origin was accepted")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		if resp == nil {
			t.Fatalf("expected a 403 response, got no response (err=%v)", err)
		}
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestHandleWebSocketAcceptsSameOriginUpgrade(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(NewWebSocketHandler().HandleWebSocket))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	header := http.Header{}
	header.Set("Origin", srv.URL)

	conn, _, err := websocket.Dial(ctx, toWSURL(srv.URL), &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("same-origin upgrade rejected: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
}

func toWSURL(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http")
}
