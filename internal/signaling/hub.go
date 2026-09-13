// Package signaling is the WebRTC signaling core: it manages rooms, admin auth,
// the per-room participant limit, and relays SDP/ICE between peers. Media never
// flows through here — it goes browser-to-browser over WebRTC.
package signaling

import (
	"context"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/arthurdepina/concorde/internal/config"
)

// Hub owns every room and connected client. A single mutex guards all mutable
// state; at this scale (a handful of tiny rooms) that is simpler and plenty
// fast. Handlers must never block while holding mu — hence non-blocking sends.
type Hub struct {
	cfg config.Config

	mu    sync.Mutex
	rooms map[string]*Room
}

// New returns a ready-to-use Hub.
func New(cfg config.Config) *Hub {
	return &Hub{
		cfg:   cfg,
		rooms: make(map[string]*Room),
	}
}

// ServeWS upgrades an HTTP request to a WebSocket and runs the client until it
// disconnects. Wire it up as the /ws handler.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		log.Println("[ws] accept:", err)
		return
	}
	defer conn.CloseNow()

	client := &Client{
		id:   randomCode(10),
		conn: conn,
		hub:  h,
		send: make(chan any, sendBuffer),
	}
	log.Printf("[ws] client %s connected", client.id)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	go client.writeLoop(ctx)
	client.enqueue(helloMsg{Type: "hello", From: "concorde"})

	client.readLoop(ctx)

	h.leaveRoom(client)
	log.Printf("[ws] client %s disconnected", client.id)
}

// createRoom validates admin credentials and opens a new room with the caller as
// broadcaster.
func (h *Hub) createRoom(c *Client, in inbound) {
	if in.User != h.cfg.AdminUser || in.Password != h.cfg.AdminPassword {
		c.enqueue(newError(errBadAdmin, "invalid admin credentials"))
		return
	}
	if strings.TrimSpace(in.RoomPassword) == "" {
		c.enqueue(newError(errBadRoomPassword, "a room password is required"))
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	// Moving between rooms is fine; drop the old membership first.
	h.removeLocked(c)

	now := time.Now()
	room := &Room{
		ID:         h.uniqueRoomIDLocked(),
		Password:   in.RoomPassword,
		clients:    map[string]*Client{c.id: c},
		createdAt:  now,
		lastActive: now,
	}
	h.rooms[room.ID] = room
	c.room = room
	c.role = roleBroadcaster

	log.Printf("[room] %s created by %s", room.ID, c.id)
	c.enqueue(roomCreatedMsg{Type: "room-created", RoomID: room.ID, SelfID: c.id, Role: c.role})
}

// joinRoom lets a friend into an existing room, given the right password and
// room capacity, and notifies everyone already inside.
func (h *Hub) joinRoom(c *Client, in inbound) {
	roomID := strings.ToUpper(strings.TrimSpace(in.RoomID))

	h.mu.Lock()
	defer h.mu.Unlock()

	room, ok := h.rooms[roomID]
	if !ok {
		c.enqueue(newError(errRoomNotFound, "no room with that code"))
		return
	}
	if room.Password != in.RoomPassword {
		c.enqueue(newError(errBadRoomPassword, "wrong room password"))
		return
	}
	if len(room.clients) >= h.cfg.MaxParticipants {
		c.enqueue(newError(errRoomFull, "room is full"))
		return
	}

	h.removeLocked(c)

	peers := make([]peerInfo, 0, len(room.clients))
	for _, other := range room.clients {
		peers = append(peers, peerInfo{ID: other.id, Role: other.role})
	}

	room.clients[c.id] = c
	room.lastActive = time.Now()
	c.room = room
	c.role = roleViewer

	log.Printf("[room] %s joined by %s (%d/%d)", room.ID, c.id, len(room.clients), h.cfg.MaxParticipants)
	c.enqueue(joinedMsg{Type: "joined", RoomID: room.ID, SelfID: c.id, Role: c.role, Peers: peers})

	for _, other := range room.clients {
		if other.id == c.id {
			continue
		}
		other.enqueue(peerJoinedMsg{Type: "peer-joined", PeerID: c.id, Role: c.role})
	}
}

// leaveRoom removes the client from whatever room it is in, if any.
func (h *Hub) leaveRoom(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.removeLocked(c)
}

// relay forwards a WebRTC signaling payload to another peer in the same room.
func (h *Hub) relay(c *Client, in inbound) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if c.room == nil {
		c.enqueue(newError(errNotInRoom, "not in a room"))
		return
	}
	target, ok := c.room.clients[in.To]
	if !ok {
		c.enqueue(newError(errPeerNotFound, "peer not in room"))
		return
	}
	target.enqueue(signalMsg{Type: "signal", From: c.id, Payload: in.Payload})
}

// removeLocked detaches c from its room and notifies the survivors, deleting the
// room if it becomes empty. Caller must hold h.mu.
func (h *Hub) removeLocked(c *Client) {
	room := c.room
	if room == nil {
		return
	}
	delete(room.clients, c.id)
	c.room = nil
	c.role = ""

	if len(room.clients) == 0 {
		delete(h.rooms, room.ID)
		log.Printf("[room] %s closed (empty)", room.ID)
		return
	}

	room.lastActive = time.Now()
	for _, other := range room.clients {
		other.enqueue(peerLeftMsg{Type: "peer-left", PeerID: c.id})
	}
}

// uniqueRoomIDLocked returns a room code not currently in use. Caller must hold
// h.mu.
func (h *Hub) uniqueRoomIDLocked() string {
	for {
		id := randomCode(6)
		if _, exists := h.rooms[id]; !exists {
			return id
		}
	}
}
