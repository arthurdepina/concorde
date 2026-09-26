package signaling

import (
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// RoomInfo is the admin panel's view of one open room. It carries no passwords —
// only what the admin needs to decide whether to close a room.
type RoomInfo struct {
	ID           string `json:"id"`
	Participants int    `json:"participants"`
	// Away counts participants whose socket dropped and whose seat is being held
	// for a reconnect (see resume.go) — they are in Participants too. A room
	// showing people but all of them away is one nobody is actually looking at.
	Away         int       `json:"away"`
	Broadcasting bool      `json:"broadcasting"`
	CreatedAt    time.Time `json:"createdAt"`
	LastActive   time.Time `json:"lastActive"`
	IdleSeconds  int       `json:"idleSeconds"`
}

// adminCredentialsOK checks user/pass against the admin credentials. The
// comparison is constant-time so it does not leak the password by timing.
func (h *Hub) adminCredentialsOK(user, pass string) bool {
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(h.cfg.AdminUser)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(pass), []byte(h.cfg.AdminPassword)) == 1
	return userOK && passOK
}

// authorizeAdmin validates the request's HTTP Basic Auth against the admin
// credentials and reports whether the handler may proceed. Otherwise it has
// already answered: 429 while this visitor is locked out for too many wrong
// passwords, 401 for missing or wrong credentials. Only a wrong password counts
// toward the lockout — a request carrying no credentials is not a guess.
func (h *Hub) authorizeAdmin(w http.ResponseWriter, r *http.Request) bool {
	key := loginKindAdmin + clientIP(r)
	now := time.Now()
	if wait := h.logins.lockedFor(key, now); wait > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int((wait+time.Second-1)/time.Second)))
		http.Error(w, "too many failed attempts, try again later", http.StatusTooManyRequests)
		return false
	}
	user, pass, ok := r.BasicAuth()
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	if !h.adminCredentialsOK(user, pass) {
		h.loginFailed(key, now)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	h.logins.succeed(key)
	return true
}

// ServeAdminRooms answers GET /admin/rooms with a JSON snapshot of open rooms.
func (h *Hub) ServeAdminRooms(w http.ResponseWriter, r *http.Request) {
	if !h.authorizeAdmin(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	rooms := h.snapshotRooms()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"rooms": rooms})
}

// ServeAdminMetrics answers GET /admin/metrics with the lifetime counters and
// live room/connection counts. Admin-only, like the other admin endpoints, so
// the numbers (and the fact that the server is up) are not exposed publicly.
func (h *Hub) ServeAdminMetrics(w http.ResponseWriter, r *http.Request) {
	if !h.authorizeAdmin(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(h.snapshotMetrics())
}

// ServeAdminClose answers POST /admin/rooms/close ({"roomId": "..."}) by closing
// that room and evicting everyone in it.
func (h *Hub) ServeAdminClose(w http.ResponseWriter, r *http.Request) {
	if !h.authorizeAdmin(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		RoomID string `json:"roomId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	roomID := strings.ToUpper(strings.TrimSpace(body.RoomID))

	if !h.CloseRoom(roomID, reasonByAdmin) {
		http.Error(w, "room not found", http.StatusNotFound)
		return
	}
	log.Printf("[admin] closed room %s", roomID)
	w.WriteHeader(http.StatusNoContent)
}

// snapshotRooms returns the current open rooms, newest first, safe to serialize
// outside the lock.
func (h *Hub) snapshotRooms() []RoomInfo {
	now := time.Now()

	h.mu.Lock()
	defer h.mu.Unlock()

	out := make([]RoomInfo, 0, len(h.rooms))
	for _, room := range h.rooms {
		away := 0
		for _, c := range room.clients {
			if c.away.Load() {
				away++
			}
		}
		out = append(out, RoomInfo{
			ID:           room.ID,
			Participants: len(room.clients),
			Away:         away,
			Broadcasting: room.broadcasting,
			CreatedAt:    room.createdAt,
			LastActive:   room.lastActive,
			IdleSeconds:  int(now.Sub(room.lastActive).Seconds()),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

// CloseRoom shuts down the room with the given id, notifying and evicting its
// participants. Returns false if no such room exists.
func (h *Hub) CloseRoom(id, reason string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	room, ok := h.rooms[id]
	if !ok {
		return false
	}
	h.closeRoomLocked(room, reason)
	return true
}
