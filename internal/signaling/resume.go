package signaling

import (
	"log"
	"strings"
	"time"
)

// A seat in a room outlives the WebSocket that holds it. A dropped socket — a
// tunnel closing an idle connection, a laptop waking up, a phone changing
// networks — used to evict the participant at once: viewers lost the room, and
// the broadcaster left behind a room nobody could broadcast in. Instead the seat
// is kept "away" and the page, which now reconnects on its own, claims it back
// with a token issued when it entered. Same client id, same room, same role, and
// no peer-joined/peer-left for anyone else — from the room's point of view
// nobody ever left, so the media (which never needed the server) keeps flowing.
//
// The token is a bearer credential for one seat: it lives in the page's
// sessionStorage, never the room password, is revoked the moment the seat is
// given up, and dies with the room.

// resumeTokenLength is how many alphabet characters a resume token has. 24 of
// them carry ~120 bits, so there is nothing to brute-force and a failed resume
// is not counted against the login lockout.
const resumeTokenLength = 24

// resumeGrace is how long a seat is held for a client whose socket dropped.
// Zero or less turns resuming off entirely: sockets then vacate their seat the
// moment they close, as they did before.
func (h *Hub) resumeGrace() time.Duration {
	return time.Duration(h.cfg.ResumeGraceSeconds) * time.Second
}

// issueTokenLocked hands c a fresh token for the seat it just took, replacing
// any older one. Caller must hold h.mu.
func (h *Hub) issueTokenLocked(c *Client) string {
	if h.resumeGrace() <= 0 {
		return ""
	}
	h.revokeTokenLocked(c)
	c.token = randomCode(resumeTokenLength)
	h.resumable[c.token] = c
	return c.token
}

// revokeTokenLocked makes c's token unusable. Caller must hold h.mu.
func (h *Hub) revokeTokenLocked(c *Client) {
	if c.token == "" {
		return
	}
	delete(h.resumable, c.token)
	c.token = ""
}

// forgetSeatLocked drops everything that would let c's seat be claimed back: its
// token and any pending grace timer. Caller must hold h.mu.
func (h *Hub) forgetSeatLocked(c *Client) {
	h.revokeTokenLocked(c)
	if c.expiry != nil {
		c.expiry.Stop()
		c.expiry = nil
	}
	c.away.Store(false)
}

// dropped runs when a client's socket closes, in place of leaving the room. A
// client holding a seat is marked away and kept in the room for a while instead
// of being evicted, so a reconnect can pick that seat up again.
func (h *Hub) dropped(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// No seat, or a newer connection already claimed it: nothing to hold.
	if c.room == nil || c.room.clients[c.id] != c {
		return
	}
	grace := h.resumeGrace()
	if grace <= 0 {
		h.removeLocked(c)
		return
	}

	room := c.room
	c.away.Store(true)
	h.metrics.sessionsAway.Add(1)

	if c.role == roleBroadcaster {
		// The room stops counting as live, which starts the TTL clock — and that
		// clock doubles as the window for the broadcaster to come back and pick
		// the role up again. If they never do, the reaper closes the room, so a
		// room nobody can broadcast in no longer lingers. The viewers are told
		// nothing: their P2P stream does not depend on this socket and may well
		// still be playing.
		room.broadcasting = false
		room.lastActive = time.Now()
		log.Printf("[room] %s: broadcaster %s away (room idle from now)", room.ID, c.id)
		return
	}

	c.expiry = time.AfterFunc(grace, func() { h.expireAway(c) })
	log.Printf("[room] %s: viewer %s away (%s to come back)", room.ID, c.id, grace)
}

// expireAway vacates a seat whose grace ran out: the client never came back, so
// the others finally hear that it left.
func (h *Hub) expireAway(c *Client) {
	defer h.recoverPanic("expireAway")

	h.mu.Lock()
	defer h.mu.Unlock()

	// Came back in time, or the seat is gone already (room closed, evicted).
	if !c.away.Load() || c.room == nil || c.room.clients[c.id] != c {
		return
	}
	h.metrics.sessionsExpired.Add(1)
	log.Printf("[room] %s: %s never came back, seat released", c.room.ID, c.id)
	h.removeLocked(c)
}

// resumeSession gives a reconnecting page its seat back: same id, room and role,
// silently as far as the other participants are concerned.
//
// The takeover is unconditional, even when the old socket still looks alive: a
// browser notices a dead link long before our keepalive does, so the page is
// often back before the server has buried the previous connection. The stale
// socket is closed here rather than left to fight over the seat.
func (h *Hub) resumeSession(c *Client, in inbound) {
	h.mu.Lock()

	old, ok := h.resumable[strings.TrimSpace(in.Token)]
	if !ok || old.room == nil || old.room.clients[old.id] != old {
		h.mu.Unlock()
		h.metrics.resumesRejected.Add(1)
		c.enqueue(newError(errResumeFailed, "session expired"))
		return
	}

	// This connection gives up any seat of its own before taking the old one.
	h.removeLocked(c)

	room, role, stale := old.room, old.role, old.conn
	h.forgetSeatLocked(old)
	old.room = nil
	old.role = ""
	// Nothing more may be queued to a socket we are about to close.
	old.away.Store(true)

	c.id = old.id
	c.role = role
	c.room = room
	room.clients[c.id] = c
	room.lastActive = time.Now()
	token := h.issueTokenLocked(c) // the old token is spent; hand out the next

	peers := make([]peerInfo, 0, len(room.clients))
	for _, other := range room.clients {
		if other.id != c.id {
			peers = append(peers, peerInfo{ID: other.id, Role: other.role})
		}
	}
	roomID := room.ID
	h.mu.Unlock()

	// Outside the lock: closing a socket can block, and the hub must not.
	stale.CloseNow()

	h.metrics.sessionsResumed.Add(1)
	log.Printf("[room] %s: %s resumed as %s", roomID, c.id, role)
	c.enqueue(resumedMsg{
		Type:   "resumed",
		RoomID: roomID,
		SelfID: c.id,
		Role:   role,
		Token:  token,
		Peers:  peers,
	})
}
