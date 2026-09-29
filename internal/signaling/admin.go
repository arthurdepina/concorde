package signaling

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/arthurdepina/concorde/internal/accounts"
)

// RoomInfo is the admin panel's view of one open room. It carries no passwords —
// only what the admin needs to decide whether to close a room.
type RoomInfo struct {
	ID string `json:"id"`
	// Owner is the account that opened the room — with friends able to open
	// their own, the panel should say whose room this is.
	Owner        string `json:"owner"`
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

// verifyLogin checks a name and password against the accounts, applying the
// per-visitor lockout. A lockout is reported apart from a plain wrong password
// because the two deserve different answers (429 vs 401, and a different message
// in the app). Callers must not hold h.mu: bcrypt is slow on purpose.
func (h *Hub) verifyLogin(ip, user, password string) (acc accounts.Account, ok bool, lockedFor time.Duration) {
	key := loginKindAccount + ip
	now := time.Now()
	if wait := h.logins.lockedFor(key, now); wait > 0 {
		return accounts.Account{}, false, wait
	}
	acc, ok = h.accounts.Verify(user, password)
	if !ok {
		h.loginFailed(key, now)
		return accounts.Account{}, false, 0
	}
	h.logins.succeed(key)
	return acc, true, 0
}

// authorizeAdmin validates the request's HTTP Basic Auth and reports whether the
// handler may proceed. Otherwise it has already answered: 429 while this visitor
// is locked out for too many wrong passwords, 401 for missing or wrong
// credentials, 403 for a valid login that is not the admin — a broadcaster
// account may open rooms, never run the panel. Only a wrong password counts
// toward the lockout: a request carrying no credentials is not a guess, and a
// correct password is not one either.
func (h *Hub) authorizeAdmin(w http.ResponseWriter, r *http.Request) bool {
	user, pass, ok := r.BasicAuth()
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	acc, ok, wait := h.verifyLogin(clientIP(r), user, pass)
	if wait > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int((wait+time.Second-1)/time.Second)))
		http.Error(w, "too many failed attempts, try again later", http.StatusTooManyRequests)
		return false
	}
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	if acc.Role != accounts.RoleAdmin {
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
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

// accountView is the panel's view of one account. It exists so the hash cannot
// reach a client by accident: this struct simply has nowhere to put it.
type accountView struct {
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"createdAt"`
	// Builtin marks the admin from the environment: the panel shows it but
	// cannot edit or delete it.
	Builtin bool `json:"builtin"`
}

// ServeAdminAccounts manages who may open a room, at /admin/accounts:
//
//	GET                          list the accounts
//	POST   {name, password?}     create one (password generated when omitted)
//	PUT    {name, password?}     change its password
//	DELETE {name}                delete it
//
// A created or reset password comes back in the response because it is the only
// time anyone can read it — what is kept is the bcrypt hash.
func (h *Hub) ServeAdminAccounts(w http.ResponseWriter, r *http.Request) {
	if !h.authorizeAdmin(w, r) {
		return
	}

	if r.Method == http.MethodGet {
		list := h.accounts.List()
		out := make([]accountView, 0, len(list))
		for _, acc := range list {
			out = append(out, accountView{
				Name:      acc.Name,
				Role:      acc.Role,
				CreatedAt: acc.CreatedAt,
				Builtin:   acc.Builtin,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"accounts": out})
		return
	}

	var body struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(body.Name)

	switch r.Method {
	case http.MethodPost, http.MethodPut:
		password := body.Password
		generated := password == ""
		if generated {
			password = accounts.GeneratePassword()
		}
		var err error
		if r.Method == http.MethodPost {
			err = h.accounts.Add(name, password)
		} else {
			err = h.accounts.SetPassword(name, password)
		}
		if err != nil {
			writeAccountError(w, err)
			return
		}
		verb := "created"
		if r.Method == http.MethodPut {
			verb = "password changed for"
		}
		log.Printf("[auth] admin %s account %q", verb, name)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"name": name, "password": password, "generated": generated})

	case http.MethodDelete:
		if err := h.accounts.Remove(name); err != nil {
			writeAccountError(w, err)
			return
		}
		log.Printf("[auth] admin deleted account %q", name)
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// writeAccountError answers with a status and a stable code. The code is what
// the panel branches on to show the reason in Portuguese — the message here is
// only for logs and curl.
func writeAccountError(w http.ResponseWriter, err error) {
	status, code := http.StatusInternalServerError, "save-failed"
	switch {
	case errors.Is(err, accounts.ErrNotFound):
		status, code = http.StatusNotFound, "account-not-found"
	case errors.Is(err, accounts.ErrExists):
		status, code = http.StatusConflict, "account-exists"
	case errors.Is(err, accounts.ErrBuiltin):
		status, code = http.StatusForbidden, "account-builtin"
	case errors.Is(err, accounts.ErrBadName):
		status, code = http.StatusBadRequest, "bad-name"
	case errors.Is(err, accounts.ErrShortPassword):
		status, code = http.StatusBadRequest, "short-password"
	case errors.Is(err, accounts.ErrLongPassword):
		status, code = http.StatusBadRequest, "long-password"
	default:
		log.Printf("[auth] account change failed: %v", err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": err.Error()})
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
			Owner:        room.Owner,
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
