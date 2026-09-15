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
	url, _ := newTestServerHub(t)
	return url
}

// newTestServerHub is like newTestServer but also returns the hub behind the
// socket, so tests can exercise its admin HTTP handlers against live rooms.
func newTestServerHub(t *testing.T) (string, *Hub) {
	t.Helper()
	hub := New(config.Config{
		AdminUser:       "admin",
		AdminPassword:   "secret",
		MaxParticipants: 3,
	})
	srv := httptest.NewServer(http.HandlerFunc(hub.ServeWS))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http"), hub
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

// newHub builds a hub with fixed admin credentials for the unit-level tests that
// poke at rooms directly instead of over a socket.
func newHub() *Hub {
	return New(config.Config{AdminUser: "admin", AdminPassword: "secret", MaxParticipants: 3})
}

// TestReapIdleDirect drives the reaper against a hub it owns, so it can age a
// room and inject a fake broadcaster without a real socket.
func TestReapIdleDirect(t *testing.T) {
	hub := newHub()
	ttl := 10 * time.Minute

	// A stale, non-broadcasting room with one (fake) member.
	stale := &Room{
		ID:         "STALE1",
		clients:    map[string]*Client{"a": {id: "a", send: make(chan any, sendBuffer)}},
		createdAt:  time.Now().Add(-time.Hour),
		lastActive: time.Now().Add(-time.Hour),
	}
	staleMember := stale.clients["a"]
	staleMember.room = stale
	hub.rooms[stale.ID] = stale

	// A stale room that is actively broadcasting must be spared.
	live := &Room{
		ID:           "LIVE01",
		broadcasting: true,
		clients:      map[string]*Client{"b": {id: "b", send: make(chan any, sendBuffer)}},
		createdAt:    time.Now().Add(-time.Hour),
		lastActive:   time.Now().Add(-time.Hour),
	}
	live.clients["b"].room = live
	hub.rooms[live.ID] = live

	// A fresh room is well within its TTL.
	fresh := &Room{
		ID:         "FRESH1",
		clients:    map[string]*Client{"c": {id: "c", send: make(chan any, sendBuffer)}},
		createdAt:  time.Now(),
		lastActive: time.Now(),
	}
	hub.rooms[fresh.ID] = fresh

	hub.reapIdle(time.Now(), ttl)

	if _, ok := hub.rooms["STALE1"]; ok {
		t.Error("stale idle room should have been reaped")
	}
	if _, ok := hub.rooms["LIVE01"]; !ok {
		t.Error("broadcasting room should not be reaped")
	}
	if _, ok := hub.rooms["FRESH1"]; !ok {
		t.Error("fresh room should not be reaped")
	}

	// The evicted member is told the room closed, with the idle reason.
	select {
	case msg := <-staleMember.send:
		rc, ok := msg.(roomClosedMsg)
		if !ok || rc.Reason != reasonIdle {
			t.Fatalf("expected room-closed(idle), got %#v", msg)
		}
	default:
		t.Fatal("evicted member got no room-closed message")
	}
}

func TestReapDisabledWithZeroTTL(t *testing.T) {
	hub := New(config.Config{RoomTTLMinutes: 0})
	// Should not panic or start a goroutine that touches rooms.
	hub.StartReaper(context.Background())
}

func TestAdminRoomsRequiresAuth(t *testing.T) {
	hub := newHub()
	srv := httptest.NewServer(http.HandlerFunc(hub.ServeAdminRooms))
	defer srv.Close()

	res, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth, got %d", res.StatusCode)
	}
}

func TestAdminRoomsListsOpenRooms(t *testing.T) {
	hub := newHub()
	hub.rooms["ROOM01"] = &Room{
		ID:         "ROOM01",
		clients:    map[string]*Client{"a": {id: "a"}},
		createdAt:  time.Now(),
		lastActive: time.Now(),
	}
	srv := httptest.NewServer(http.HandlerFunc(hub.ServeAdminRooms))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	req.SetBasicAuth("admin", "secret")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}

	var body struct {
		Rooms []RoomInfo `json:"rooms"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Rooms) != 1 || body.Rooms[0].ID != "ROOM01" || body.Rooms[0].Participants != 1 {
		t.Fatalf("unexpected rooms payload: %#v", body.Rooms)
	}
}

func TestAdminCloseEvictsParticipants(t *testing.T) {
	url, hub := newTestServerHub(t)
	admin := dial(t, url)
	admin.write(map[string]any{"type": "create-room", "user": "admin", "password": "secret", "roomPassword": "pw"})
	created := admin.expect("room-created")
	roomID := created["roomId"].(string)

	friend := dial(t, url)
	friend.write(map[string]any{"type": "join-room", "roomId": roomID, "roomPassword": "pw"})
	friend.expect("joined")
	admin.expect("peer-joined")

	// Close it via the admin API on the same hub.
	closeReq := map[string]any{"roomId": roomID}
	body, _ := json.Marshal(closeReq)
	srv := httptest.NewServer(http.HandlerFunc(hub.ServeAdminClose))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(string(body)))
	req.SetBasicAuth("admin", "secret")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", res.StatusCode)
	}

	// Both participants are told the room closed by the admin.
	if got := admin.expect("room-closed")["reason"]; got != reasonByAdmin {
		t.Fatalf("admin expected room-closed(%s), got %v", reasonByAdmin, got)
	}
	if got := friend.expect("room-closed")["reason"]; got != reasonByAdmin {
		t.Fatalf("friend expected room-closed(%s), got %v", reasonByAdmin, got)
	}
}
