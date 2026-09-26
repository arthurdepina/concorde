package signaling

import (
	"sync/atomic"
	"time"
)

// Metrics holds cumulative, lock-free counters describing the server's lifetime
// activity. They exist to diagnose stability: connectionsOpened far above
// connectionsClosed means sockets are leaking; a climbing droppedMessages means
// a client's send buffer keeps overflowing (a stuck/slow peer); any
// panicsRecovered at all means a background goroutine hit a bug that would once
// have crashed the whole process. Read a live view via GET /admin/metrics or the
// periodic heartbeat log (see Hub.StartStatsLogger).
//
// Metrics must not be copied after first use (it embeds atomics); the Hub owns
// one and everything touches it through the *Hub.
type Metrics struct {
	connectionsOpened atomic.Int64
	connectionsClosed atomic.Int64
	roomsCreated      atomic.Int64
	roomsClosed       atomic.Int64
	signalsRelayed    atomic.Int64
	errorsSent        atomic.Int64
	droppedMessages   atomic.Int64
	acceptFailures    atomic.Int64
	panicsRecovered   atomic.Int64
	// loginLockouts counts visitors locked out for too many wrong passwords —
	// someone guessing, or a friend fumbling theirs.
	loginLockouts atomic.Int64
	// Peer connection outcomes reported by viewers (see Hub.rtcReport). Media
	// never passes through the server, so these are its only view of whether
	// P2P works: p2pFailed is how often STUN alone was not enough — the case
	// for adding TURN.
	p2pConnected atomic.Int64
	p2pFailed    atomic.Int64
	p2pDropped   atomic.Int64
}

// MetricsSnapshot is a point-in-time, serializable view of the counters plus the
// live room/connection counts and uptime. activeConnections is derived from the
// open/close counters; activeRooms/participants are read from live state.
type MetricsSnapshot struct {
	StartedAt         time.Time `json:"startedAt"`
	UptimeSeconds     int64     `json:"uptimeSeconds"`
	ActiveConnections int64     `json:"activeConnections"`
	ActiveRooms       int       `json:"activeRooms"`
	Participants      int       `json:"participants"`
	ConnectionsOpened int64     `json:"connectionsOpened"`
	ConnectionsClosed int64     `json:"connectionsClosed"`
	RoomsCreated      int64     `json:"roomsCreated"`
	RoomsClosed       int64     `json:"roomsClosed"`
	SignalsRelayed    int64     `json:"signalsRelayed"`
	ErrorsSent        int64     `json:"errorsSent"`
	DroppedMessages   int64     `json:"droppedMessages"`
	AcceptFailures    int64     `json:"acceptFailures"`
	PanicsRecovered   int64     `json:"panicsRecovered"`
	LoginLockouts     int64     `json:"loginLockouts"`
	P2PConnected      int64     `json:"p2pConnected"`
	P2PFailed         int64     `json:"p2pFailed"`
	P2PDropped        int64     `json:"p2pDropped"`
}

// snapshotMetrics builds a MetricsSnapshot. It takes h.mu only to count live
// rooms and participants; the cumulative counters are read atomically without
// the lock.
func (h *Hub) snapshotMetrics() MetricsSnapshot {
	now := time.Now()

	h.mu.Lock()
	activeRooms := len(h.rooms)
	participants := 0
	for _, room := range h.rooms {
		participants += len(room.clients)
	}
	h.mu.Unlock()

	m := &h.metrics
	return MetricsSnapshot{
		StartedAt:         h.startedAt,
		UptimeSeconds:     int64(now.Sub(h.startedAt).Seconds()),
		ActiveConnections: m.connectionsOpened.Load() - m.connectionsClosed.Load(),
		ActiveRooms:       activeRooms,
		Participants:      participants,
		ConnectionsOpened: m.connectionsOpened.Load(),
		ConnectionsClosed: m.connectionsClosed.Load(),
		RoomsCreated:      m.roomsCreated.Load(),
		RoomsClosed:       m.roomsClosed.Load(),
		SignalsRelayed:    m.signalsRelayed.Load(),
		ErrorsSent:        m.errorsSent.Load(),
		DroppedMessages:   m.droppedMessages.Load(),
		AcceptFailures:    m.acceptFailures.Load(),
		PanicsRecovered:   m.panicsRecovered.Load(),
		LoginLockouts:     m.loginLockouts.Load(),
		P2PConnected:      m.p2pConnected.Load(),
		P2PFailed:         m.p2pFailed.Load(),
		P2PDropped:        m.p2pDropped.Load(),
	}
}
