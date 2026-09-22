package signaling

import (
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
)

// RoomInfo is the admin panel's view of one open room. It carries no passwords —
// only what the admin needs to decide whether to close a room.
type RoomInfo struct {
	ID           string    `json:"id"`
	Participants int       `json:"participants"`
	Broadcasting bool      `json:"broadcasting"`
	CreatedAt    time.Time `json:"createdAt"`
	LastActive   time.Time `json:"lastActive"`
	IdleSeconds  int       `json:"idleSeconds"`
}

// checkAdmin validates HTTP Basic Auth against the admin credentials. The
// comparison is constant-time so it does not leak the password by timing.
func (h *Hub) checkAdmin(r *http.Request) bool {
	user, pass, ok := r.BasicAuth()
	if !ok {
		return false
	}
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(h.cfg.AdminUser)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(pass), []byte(h.cfg.AdminPassword)) == 1
	return userOK && passOK
}

// ServeAdminRooms answers GET /admin/rooms with a JSON snapshot of open rooms.
func (h *Hub) ServeAdminRooms(w http.ResponseWriter, r *http.Request) {
	if !h.checkAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
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
	if !h.checkAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
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
	if !h.checkAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
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
		out = append(out, RoomInfo{
			ID:           room.ID,
			Participants: len(room.clients),
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
