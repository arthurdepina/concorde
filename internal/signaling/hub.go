// Package signaling is the WebRTC signaling core: it manages rooms, admin auth,
// the per-room participant limit, and relays SDP/ICE between peers. Media never
// flows through here — it goes browser-to-browser over WebRTC.
package signaling

import (
	"context"
	"log"
	"net/http"
	"runtime/debug"
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

	// startedAt anchors the uptime reported by the metrics. Set once in New.
	startedAt time.Time
	// metrics are lifetime counters for observability; see metrics.go.
	metrics Metrics

	mu    sync.Mutex
	rooms map[string]*Room
}

// New returns a ready-to-use Hub.
func New(cfg config.Config) *Hub {
	return &Hub{
		cfg:       cfg,
		startedAt: time.Now(),
		rooms:     make(map[string]*Room),
	}
}

// recoverPanic logs and records a panic that would otherwise crash the whole
// process from a background goroutine. net/http shields its own handler
// goroutines, but goroutines we start ourselves (writeLoop, the reaper, the
// stats logger) are not covered — an unrecovered panic there takes the entire
// server down. Defer this at the top of each such goroutine.
func (h *Hub) recoverPanic(where string) {
	if r := recover(); r != nil {
		h.metrics.panicsRecovered.Add(1)
		log.Printf("[panic] recovered in %s: %v\n%s", where, r, debug.Stack())
	}
}

// ServeWS upgrades an HTTP request to a WebSocket and runs the client until it
// disconnects. Wire it up as the /ws handler.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	// With no AllowedOrigins the library enforces same-origin (the safe local
	// default). Behind a tunnel/proxy that may not preserve the Host header, the
	// configured public hosts are accepted so browser WebSockets aren't rejected.
	var opts *websocket.AcceptOptions
	if len(h.cfg.AllowedOrigins) > 0 {
		opts = &websocket.AcceptOptions{OriginPatterns: h.cfg.AllowedOrigins}
	}
	conn, err := websocket.Accept(w, r, opts)
	if err != nil {
		h.metrics.acceptFailures.Add(1)
		log.Println("[ws] accept:", err)
		return
	}
	defer conn.CloseNow()

	h.metrics.connectionsOpened.Add(1)
	defer h.metrics.connectionsClosed.Add(1)
	connectedAt := time.Now()

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
	log.Printf("[ws] client %s disconnected after %s", client.id, time.Since(connectedAt).Round(time.Second))
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
	h.metrics.roomsCreated.Add(1)

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

// setBroadcasting records whether the room's broadcaster is actively sharing.
// Only the broadcaster may flip it; the reaper leaves an actively broadcasting
// room alone, and stopping restarts the idle clock (lastActive bumped).
func (h *Hub) setBroadcasting(c *Client, on bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if c.room == nil || c.role != roleBroadcaster {
		return
	}
	c.room.broadcasting = on
	c.room.lastActive = time.Now()

	// A deliberate stop gets relayed to the viewers so they can close their peer
	// connection right away, before it decays into "failed" and shows them a
	// connection-error toast. Starting needs no relay: the offer itself reaches
	// each viewer.
	if !on {
		for _, other := range c.room.clients {
			if other.id == c.id {
				continue
			}
			other.enqueue(broadcastStoppedMsg{Type: "broadcast-stopped"})
		}
	}
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
	h.metrics.signalsRelayed.Add(1)
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
		h.metrics.roomsClosed.Add(1)
		log.Printf("[room] %s closed (empty)", room.ID)
		return
	}

	room.lastActive = time.Now()
	for _, other := range room.clients {
		other.enqueue(peerLeftMsg{Type: "peer-left", PeerID: c.id})
	}
}

// closeRoomLocked shuts a room down: it tells every participant the room is gone,
// detaches them, and removes the room. Unlike removeLocked (one client leaving),
// this ends the whole session. Caller must hold h.mu.
func (h *Hub) closeRoomLocked(room *Room, reason string) {
	for _, c := range room.clients {
		c.enqueue(roomClosedMsg{Type: "room-closed", Reason: reason})
		c.room = nil
		c.role = ""
	}
	room.clients = nil
	delete(h.rooms, room.ID)
	h.metrics.roomsClosed.Add(1)
	log.Printf("[room] %s closed (%s)", room.ID, reason)
}

// StartReaper runs the idle-room sweep until ctx is cancelled. A TTL of zero or
// less disables it. The tick is TTL/4 (clamped) so a room lives at most ~1.25×
// TTL past going idle.
func (h *Hub) StartReaper(ctx context.Context) {
	ttl := time.Duration(h.cfg.RoomTTLMinutes) * time.Minute
	if ttl <= 0 {
		log.Println("[reaper] disabled (ROOM_TTL_MINUTES <= 0)")
		return
	}
	interval := ttl / 4
	if interval < 15*time.Second {
		interval = 15 * time.Second
	}

	go func() {
		defer h.recoverPanic("reaper")
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				h.reapIdle(now, ttl)
			}
		}
	}()
}

// StartStatsLogger logs a periodic heartbeat with the metrics snapshot until ctx
// is cancelled. An interval of zero or less disables it. The heartbeat is what
// makes a crash or a slow leak visible after the fact: scanning the log shows
// whether connections/rooms were climbing or a panic fired before the server
// went quiet, without needing to have hit /admin/metrics at the right moment.
func (h *Hub) StartStatsLogger(ctx context.Context) {
	interval := time.Duration(h.cfg.StatsIntervalMinutes) * time.Minute
	if interval <= 0 {
		log.Println("[stats] heartbeat disabled (STATS_INTERVAL_MINUTES <= 0)")
		return
	}

	go func() {
		defer h.recoverPanic("stats")
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s := h.snapshotMetrics()
				log.Printf("[stats] uptime=%ds conns=%d rooms=%d participants=%d | opened=%d closed=%d roomsCreated=%d roomsClosed=%d relayed=%d errors=%d dropped=%d acceptFail=%d panics=%d",
					s.UptimeSeconds, s.ActiveConnections, s.ActiveRooms, s.Participants,
					s.ConnectionsOpened, s.ConnectionsClosed, s.RoomsCreated, s.RoomsClosed,
					s.SignalsRelayed, s.ErrorsSent, s.DroppedMessages, s.AcceptFailures, s.PanicsRecovered)
			}
		}
	}()
}

// reapIdle closes every room that is not broadcasting and has been idle for at
// least ttl. Deleting from the map while ranging over it is safe in Go.
func (h *Hub) reapIdle(now time.Time, ttl time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for _, room := range h.rooms {
		if room.broadcasting {
			continue
		}
		if now.Sub(room.lastActive) >= ttl {
			h.closeRoomLocked(room, reasonIdle)
		}
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
