package signaling

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/arthurdepina/concorde/internal/config"
)

// TestKeepAliveSurvivesIdle guards against the pinger breaking a healthy
// connection: with keepalive on, a socket that sits idle across a ping interval
// must still be alive and usable afterwards. It does not (and cannot easily)
// prove the tunnel idle timeout is defeated — that is validated in production via
// the per-connection duration logs — but it locks in that the ping/pong loop and
// normal traffic coexist.
func TestKeepAliveSurvivesIdle(t *testing.T) {
	hub := New(config.Config{
		AdminUser:       "admin",
		AdminPassword:   "secret",
		MaxParticipants: 3,
		WSPingSeconds:   1,
	})
	srv := httptest.NewServer(http.HandlerFunc(hub.ServeWS))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	ctx := context.Background()
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// A reader goroutine keeps the connection processing frames so the library
	// auto-answers the server's pings with pongs (a real client's readLoop does
	// the same). Decoded messages are forwarded so the test can assert on them.
	msgs := make(chan map[string]any, 8)
	go func() {
		for {
			var m map[string]any
			if err := wsjson.Read(ctx, conn, &m); err != nil {
				close(msgs)
				return
			}
			msgs <- m
		}
	}()

	if got := (<-msgs)["type"]; got != "hello" {
		t.Fatalf("expected hello first, got %v", got)
	}

	// Sit idle across ~2 ping intervals; without a live pong the server would
	// close the connection here.
	time.Sleep(2200 * time.Millisecond)

	// The connection must still work: create a room and get the confirmation.
	if err := wsjson.Write(ctx, conn, map[string]any{
		"type": "create-room", "user": "admin", "password": "secret", "roomPassword": "pw",
	}); err != nil {
		t.Fatalf("write after idle (connection likely dropped): %v", err)
	}

	select {
	case m, ok := <-msgs:
		if !ok {
			t.Fatal("connection closed during idle — keepalive dropped a healthy socket")
		}
		if m["type"] != "room-created" {
			t.Fatalf("expected room-created after idle, got %v", m["type"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no room-created after idle — connection likely dropped")
	}
}
