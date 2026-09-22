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
	}
}
