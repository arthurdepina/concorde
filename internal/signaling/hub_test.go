package signaling

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/arthurdepina/concorde/internal/config"
)

// testConn is a thin WebSocket client that skips the initial "hello" and reads
// messages as generic maps, so tests can assert on message type and fields.
type testConn struct {
	t    *testing.T
	conn *websocket.Conn
	ctx  context.Context
}

func dial(t *testing.T, url string) *testConn {
	t.Helper()
	ctx := context.Background()
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close(websocket.StatusNormalClosure, "") })

	c := &testConn{t: t, conn: conn, ctx: ctx}
	if got := c.read()["type"]; got != "hello" {
		t.Fatalf("expected hello first, got %v", got)
	}
	return c
}

func (c *testConn) write(v any) {
	c.t.Helper()
	if err := wsjson.Write(c.ctx, c.conn, v); err != nil {
		c.t.Fatalf("write: %v", err)
	}
}

func (c *testConn) read() map[string]any {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(c.ctx, 2*time.Second)
	defer cancel()
	var m map[string]any
	if err := wsjson.Read(ctx, c.conn, &m); err != nil {
		c.t.Fatalf("read: %v", err)
	}
	return m
}

// expect reads one message and asserts its type.
func (c *testConn) expect(msgType string) map[string]any {
	c.t.Helper()
	m := c.read()
	if m["type"] != msgType {
		c.t.Fatalf("expected %q, got %v (%v)", msgType, m["type"], m)
	}
	return m
}

func newTestServer(t *testing.T) string {
	t.Helper()
	hub := New(config.Config{
		AdminUser:       "admin",
		AdminPassword:   "secret",
		MaxParticipants: 3,
	})
	srv := httptest.NewServer(http.HandlerFunc(hub.ServeWS))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func TestCreateRoomRejectsBadAdmin(t *testing.T) {
	url := newTestServer(t)
	c := dial(t, url)
	c.write(map[string]any{"type": "create-room", "user": "admin", "password": "wrong", "roomPassword": "pw"})
	if got := c.expect("error")["code"]; got != errBadAdmin {
		t.Fatalf("expected %q, got %v", errBadAdmin, got)
	}
}

func TestCreateRoomRequiresRoomPassword(t *testing.T) {
	url := newTestServer(t)
	c := dial(t, url)
	c.write(map[string]any{"type": "create-room", "user": "admin", "password": "secret", "roomPassword": "  "})
	if got := c.expect("error")["code"]; got != errBadRoomPassword {
		t.Fatalf("expected %q, got %v", errBadRoomPassword, got)
	}
}

func TestJoinFlowAndPeerNotifications(t *testing.T) {
	url := newTestServer(t)

	admin := dial(t, url)
	admin.write(map[string]any{"type": "create-room", "user": "admin", "password": "secret", "roomPassword": "pw"})
	created := admin.expect("room-created")
	roomID, _ := created["roomId"].(string)
	if roomID == "" {
		t.Fatal("no roomId in room-created")
	}
	if created["role"] != roleBroadcaster {
		t.Fatalf("creator should be broadcaster, got %v", created["role"])
	}

	// A friend joins with the right password.
	friend := dial(t, url)
	friend.write(map[string]any{"type": "join-room", "roomId": roomID, "roomPassword": "pw"})
	joined := friend.expect("joined")
	if joined["role"] != roleViewer {
		t.Fatalf("joiner should be viewer, got %v", joined["role"])
	}
	peers, _ := joined["peers"].([]any)
	if len(peers) != 1 {
		t.Fatalf("expected 1 existing peer, got %v", joined["peers"])
	}

	// Admin is told a peer joined.
	pj := admin.expect("peer-joined")
	if pj["role"] != roleViewer {
		t.Fatalf("expected viewer peer-joined, got %v", pj)
	}
}

func TestWrongRoomPassword(t *testing.T) {
	url := newTestServer(t)
	admin := dial(t, url)
	admin.write(map[string]any{"type": "create-room", "user": "admin", "password": "secret", "roomPassword": "pw"})
	roomID := admin.expect("room-created")["roomId"].(string)

	friend := dial(t, url)
	friend.write(map[string]any{"type": "join-room", "roomId": roomID, "roomPassword": "nope"})
	if got := friend.expect("error")["code"]; got != errBadRoomPassword {
		t.Fatalf("expected %q, got %v", errBadRoomPassword, got)
	}
}

func TestRoomFull(t *testing.T) {
	url := newTestServer(t)
	admin := dial(t, url)
	admin.write(map[string]any{"type": "create-room", "user": "admin", "password": "secret", "roomPassword": "pw"})
	roomID := admin.expect("room-created")["roomId"].(string)

	// Two friends fill the room (3 max, admin included).
	for i := 0; i < 2; i++ {
		f := dial(t, url)
		f.write(map[string]any{"type": "join-room", "roomId": roomID, "roomPassword": "pw"})
		f.expect("joined")
		admin.expect("peer-joined")
	}

	// The fourth is rejected.
	extra := dial(t, url)
	extra.write(map[string]any{"type": "join-room", "roomId": roomID, "roomPassword": "pw"})
	if got := extra.expect("error")["code"]; got != errRoomFull {
		t.Fatalf("expected %q, got %v", errRoomFull, got)
	}
}

func TestSignalRelay(t *testing.T) {
	url := newTestServer(t)
	admin := dial(t, url)
	admin.write(map[string]any{"type": "create-room", "user": "admin", "password": "secret", "roomPassword": "pw"})
	created := admin.expect("room-created")
	roomID := created["roomId"].(string)
	adminID := created["selfId"].(string)

	friend := dial(t, url)
	friend.write(map[string]any{"type": "join-room", "roomId": roomID, "roomPassword": "pw"})
	joined := friend.expect("joined")
	friendID := joined["selfId"].(string)
	admin.expect("peer-joined")

	// Friend relays a payload to the admin.
	payload := json.RawMessage(`{"sdp":"fake-offer"}`)
	friend.write(map[string]any{"type": "signal", "to": adminID, "payload": payload})

	got := admin.expect("signal")
	if got["from"] != friendID {
		t.Fatalf("expected signal from %s, got %v", friendID, got["from"])
	}
}

func TestPeerLeftOnDisconnect(t *testing.T) {
	url := newTestServer(t)
	admin := dial(t, url)
	admin.write(map[string]any{"type": "create-room", "user": "admin", "password": "secret", "roomPassword": "pw"})
	roomID := admin.expect("room-created")["roomId"].(string)

	friend := dial(t, url)
	friend.write(map[string]any{"type": "join-room", "roomId": roomID, "roomPassword": "pw"})
	friend.expect("joined")
	admin.expect("peer-joined")

	// Friend leaves explicitly.
	friend.write(map[string]any{"type": "leave-room"})
	if got := admin.expect("peer-left")["type"]; got != "peer-left" {
		t.Fatalf("expected peer-left, got %v", got)
	}
}
