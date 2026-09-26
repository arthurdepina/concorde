package signaling

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/arthurdepina/concorde/internal/config"
)

// newResumeServer builds a server with seat-resuming switched on. The shared
// helper leaves the grace at zero — the pre-session-18 behavior, where a closed
// socket vacates its seat immediately — so these tests bring their own.
func newResumeServer(t *testing.T, graceSeconds int) (string, *Hub) {
	t.Helper()
	hub := New(config.Config{
		AdminUser:          "admin",
		AdminPassword:      "secret",
		MaxParticipants:    3,
		ResumeGraceSeconds: graceSeconds,
	})
	srv := httptest.NewServer(http.HandlerFunc(hub.ServeWS))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http"), hub
}

// createRoom opens a room over the socket and returns its id, the creator's
// client id and its resume token.
func createRoom(t *testing.T, c *testConn) (roomID, selfID, token string) {
	t.Helper()
	c.write(map[string]any{"type": "create-room", "user": "admin", "password": "secret", "roomPassword": "pw"})
	m := c.expect("room-created")
	return str(t, m, "roomId"), str(t, m, "selfId"), str(t, m, "token")
}

// joinRoom puts a viewer in the room and drains the peer-joined the others get.
func joinRoom(t *testing.T, c *testConn, roomID string, others ...*testConn) (selfID, token string) {
	t.Helper()
	c.write(map[string]any{"type": "join-room", "roomId": roomID, "roomPassword": "pw"})
	m := c.expect("joined")
	for _, other := range others {
		other.expect("peer-joined")
	}
	return str(t, m, "selfId"), str(t, m, "token")
}

func str(t *testing.T, m map[string]any, key string) string {
	t.Helper()
	v, _ := m[key].(string)
	if v == "" {
		t.Fatalf("missing %q in %v", key, m)
	}
	return v
}

// waitFor polls until cond holds, so tests never race the server's own cleanup.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// seatIsAway reports whether the room still holds this client's seat with its
// socket gone — the state a reconnecting page is meant to find.
func seatIsAway(h *Hub, roomID, clientID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	room := h.rooms[roomID]
	if room == nil {
		return false
	}
	c := room.clients[clientID]
	return c != nil && c.away.Load()
}

func roomExists(h *Hub, roomID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.rooms[roomID]
	return ok
}

func roomIsBroadcasting(h *Hub, roomID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	room := h.rooms[roomID]
	return room != nil && room.broadcasting
}

// TestResumeKeepsSeatAndNobodyElseNotices is the heart of session 18: a viewer's
// socket dies, the page comes back with its token, and it lands in the same room
// with the same id and role — while the broadcaster's next message is the one
// the resumed viewer sent, proving no peer-left/peer-joined went out in between.
func TestResumeKeepsSeatAndNobodyElseNotices(t *testing.T) {
	url, hub := newResumeServer(t, 30)
	admin := dial(t, url)
	roomID, adminID, _ := createRoom(t, admin)

	friend := dial(t, url)
	friendID, token := joinRoom(t, friend, roomID, admin)

	// The socket dies without a close handshake, as a dropped link would.
	friend.conn.CloseNow()
	waitFor(t, "seat held for the dropped viewer", func() bool { return seatIsAway(hub, roomID, friendID) })

	back := dial(t, url)
	back.write(map[string]any{"type": "resume-session", "token": token})
	resumed := back.expect("resumed")
	if got := str(t, resumed, "selfId"); got != friendID {
		t.Fatalf("resumed with a different id: want %s, got %s", friendID, got)
	}
	if got := str(t, resumed, "roomId"); got != roomID {
		t.Fatalf("resumed into the wrong room: want %s, got %s", roomID, got)
	}
	if got := str(t, resumed, "role"); got != roleViewer {
		t.Fatalf("resumed with the wrong role: %s", got)
	}
	peers, _ := resumed["peers"].([]any)
	if len(peers) != 1 {
		t.Fatalf("expected the broadcaster in peers, got %v", resumed["peers"])
	}
	if seatIsAway(hub, roomID, friendID) {
		t.Fatal("seat still marked away after resume")
	}

	// Signaling works again under the same id, and it is the first thing the
	// broadcaster hears: no peer-left, no peer-joined.
	back.write(map[string]any{"type": "signal", "to": adminID, "payload": map[string]any{"kind": "need-offer"}})
	if got := admin.expect("signal")["from"]; got != friendID {
		t.Fatalf("expected signal from the resumed viewer %s, got %v", friendID, got)
	}
	if got := hub.metrics.sessionsResumed.Load(); got != 1 {
		t.Fatalf("sessionsResumed = %d, want 1", got)
	}
}

// A token is good for one resume: the next one comes with the resumed message.
func TestResumeTokenRotates(t *testing.T) {
	url, hub := newResumeServer(t, 30)
	admin := dial(t, url)
	roomID, _, _ := createRoom(t, admin)

	friend := dial(t, url)
	friendID, token := joinRoom(t, friend, roomID, admin)
	friend.conn.CloseNow()
	waitFor(t, "seat held", func() bool { return seatIsAway(hub, roomID, friendID) })

	back := dial(t, url)
	back.write(map[string]any{"type": "resume-session", "token": token})
	fresh := str(t, back.expect("resumed"), "token")
	if fresh == token {
		t.Fatal("resume handed back the same token")
	}

	// The spent token no longer opens the seat.
	back.conn.CloseNow()
	waitFor(t, "seat held again", func() bool { return seatIsAway(hub, roomID, friendID) })
	again := dial(t, url)
	again.write(map[string]any{"type": "resume-session", "token": token})
	if got := again.expect("error")["code"]; got != errResumeFailed {
		t.Fatalf("expected %q for the spent token, got %v", errResumeFailed, got)
	}
}

func TestResumeRejectsUnknownToken(t *testing.T) {
	url, hub := newResumeServer(t, 30)
	c := dial(t, url)
	c.write(map[string]any{"type": "resume-session", "token": "NOSUCHTOKEN"})
	if got := c.expect("error")["code"]; got != errResumeFailed {
		t.Fatalf("expected %q, got %v", errResumeFailed, got)
	}
	if got := hub.metrics.resumesRejected.Load(); got != 1 {
		t.Fatalf("resumesRejected = %d, want 1", got)
	}
}

// A page that reconnects before the server has buried the old socket must still
// get its seat; the stale connection is the one that loses.
func TestResumeTakesOverAStillOpenSocket(t *testing.T) {
	url, hub := newResumeServer(t, 30)
	admin := dial(t, url)
	roomID, _, _ := createRoom(t, admin)

	friend := dial(t, url)
	friendID, token := joinRoom(t, friend, roomID, admin)

	// No CloseNow: as far as the server knows, friend is still connected.
	back := dial(t, url)
	back.write(map[string]any{"type": "resume-session", "token": token})
	if got := str(t, back.expect("resumed"), "selfId"); got != friendID {
		t.Fatalf("takeover landed on the wrong seat: %s", got)
	}
	waitFor(t, "the stale socket to be closed", func() bool {
		return hub.metrics.connectionsClosed.Load() >= 1
	})
	if !roomExists(hub, roomID) {
		t.Fatal("room disappeared during takeover")
	}
	if seatIsAway(hub, roomID, friendID) {
		t.Fatal("seat marked away after a takeover")
	}
}

// Nobody comes back: after the grace the seat is released and the others are
// told, just as they would have been before resuming existed.
func TestSeatExpiresWhenNobodyComesBack(t *testing.T) {
	url, hub := newResumeServer(t, 1)
	admin := dial(t, url)
	roomID, _, _ := createRoom(t, admin)

	friend := dial(t, url)
	friendID, _ := joinRoom(t, friend, roomID, admin)
	friend.conn.CloseNow()

	if got := admin.expect("peer-left")["peerId"]; got != friendID {
		t.Fatalf("expected peer-left for %s, got %v", friendID, got)
	}
	if got := hub.metrics.sessionsExpired.Load(); got != 1 {
		t.Fatalf("sessionsExpired = %d, want 1", got)
	}
}

// A dropped broadcaster keeps the room — and the role — until the TTL reaper
// gives up on them, so a blip does not cost the room its code. The viewers hear
// nothing: their P2P stream does not run on this socket.
func TestBroadcasterDropKeepsRoomForTheirReturn(t *testing.T) {
	url, hub := newResumeServer(t, 30)
	admin := dial(t, url)
	roomID, adminID, token := createRoom(t, admin)
	admin.write(map[string]any{"type": "broadcast-start"})
	waitFor(t, "the room to go live", func() bool { return roomIsBroadcasting(hub, roomID) })

	viewer := dial(t, url)
	viewerID, _ := joinRoom(t, viewer, roomID, admin)

	admin.conn.CloseNow()
	waitFor(t, "the broadcaster's seat to be held", func() bool { return seatIsAway(hub, roomID, adminID) })
	if !roomExists(hub, roomID) {
		t.Fatal("room closed when the broadcaster dropped")
	}
	if roomIsBroadcasting(hub, roomID) {
		t.Fatal("room still counts as broadcasting with the broadcaster gone")
	}

	back := dial(t, url)
	back.write(map[string]any{"type": "resume-session", "token": token})
	if got := str(t, back.expect("resumed"), "role"); got != roleBroadcaster {
		t.Fatalf("the broadcaster came back as %s", got)
	}

	// The viewer's first message is the resumed broadcaster's signal: it was
	// never told the broadcaster left or stopped.
	back.write(map[string]any{"type": "signal", "to": viewerID, "payload": map[string]any{"kind": "offer"}})
	if got := viewer.expect("signal")["from"]; got != adminID {
		t.Fatalf("expected a signal from %s, got %v", adminID, got)
	}
}

// Leaving on purpose is not a drop: the room the broadcaster's stream existed
// for ends right there, instead of lingering with nobody able to broadcast.
func TestBroadcasterLeaveClosesTheRoom(t *testing.T) {
	url, hub := newResumeServer(t, 30)
	admin := dial(t, url)
	roomID, _, token := createRoom(t, admin)

	viewer := dial(t, url)
	joinRoom(t, viewer, roomID, admin)

	admin.write(map[string]any{"type": "leave-room"})
	if got := viewer.expect("room-closed")["reason"]; got != reasonBroadcasterLeft {
		t.Fatalf("expected reason %q, got %v", reasonBroadcasterLeft, got)
	}
	waitFor(t, "the room to be gone", func() bool { return !roomExists(hub, roomID) })

	// The token died with the room.
	back := dial(t, url)
	back.write(map[string]any{"type": "resume-session", "token": token})
	if got := back.expect("error")["code"]; got != errResumeFailed {
		t.Fatalf("expected %q after the room closed, got %v", errResumeFailed, got)
	}
}

// A viewer leaving is still just a viewer leaving: the room carries on.
func TestViewerLeaveKeepsTheRoom(t *testing.T) {
	url, hub := newResumeServer(t, 30)
	admin := dial(t, url)
	roomID, _, _ := createRoom(t, admin)

	viewer := dial(t, url)
	viewerID, token := joinRoom(t, viewer, roomID, admin)

	viewer.write(map[string]any{"type": "leave-room"})
	if got := admin.expect("peer-left")["peerId"]; got != viewerID {
		t.Fatalf("expected peer-left for %s, got %v", viewerID, got)
	}
	if !roomExists(hub, roomID) {
		t.Fatal("a viewer leaving closed the room")
	}
	back := dial(t, url)
	back.write(map[string]any{"type": "resume-session", "token": token})
	if got := back.expect("error")["code"]; got != errResumeFailed {
		t.Fatalf("a deliberate leave should revoke the token, got %v", got)
	}
}

// The zombie room, finally buried: a broadcaster who drops and never comes back
// leaves a room that counts as idle, so the TTL reaper closes it and the viewers
// are let go instead of sitting in a room nobody can ever broadcast in.
func TestRoomWithAwayBroadcasterIsReaped(t *testing.T) {
	url, hub := newResumeServer(t, 30)
	admin := dial(t, url)
	roomID, adminID, _ := createRoom(t, admin)
	admin.write(map[string]any{"type": "broadcast-start"})
	waitFor(t, "the room to go live", func() bool { return roomIsBroadcasting(hub, roomID) })

	viewer := dial(t, url)
	joinRoom(t, viewer, roomID, admin)

	admin.conn.CloseNow()
	waitFor(t, "the broadcaster's seat to be held", func() bool { return seatIsAway(hub, roomID, adminID) })

	// Well past the TTL with nobody broadcasting.
	ttl := 10 * time.Minute
	hub.reapIdle(time.Now().Add(2*ttl), ttl)

	if got := viewer.expect("room-closed")["reason"]; got != reasonIdle {
		t.Fatalf("expected the viewer to be released with reason %q, got %v", reasonIdle, got)
	}
	if roomExists(hub, roomID) {
		t.Fatal("room with an away broadcaster outlived its TTL")
	}
}

// With the grace at zero the old behavior stands: no tokens, and a closed socket
// leaves the room at once.
func TestResumeDisabledByZeroGrace(t *testing.T) {
	url, hub := newResumeServer(t, 0)
	admin := dial(t, url)
	admin.write(map[string]any{"type": "create-room", "user": "admin", "password": "secret", "roomPassword": "pw"})
	created := admin.expect("room-created")
	if got, _ := created["token"].(string); got != "" {
		t.Fatalf("expected no token with resuming disabled, got %q", got)
	}
	roomID := str(t, created, "roomId")

	friend := dial(t, url)
	friend.write(map[string]any{"type": "join-room", "roomId": roomID, "roomPassword": "pw"})
	if got, _ := friend.expect("joined")["token"].(string); got != "" {
		t.Fatalf("expected no token with resuming disabled, got %q", got)
	}
	admin.expect("peer-joined")

	friend.conn.CloseNow()
	admin.expect("peer-left")
	if got := hub.metrics.sessionsAway.Load(); got != 0 {
		t.Fatalf("sessionsAway = %d with resuming disabled, want 0", got)
	}
}
