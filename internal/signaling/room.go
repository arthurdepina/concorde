package signaling

import "time"

// Room is a single broadcast session: one broadcaster plus a few viewers, all
// swapping WebRTC signaling through the hub. Media never touches this struct —
// it only tracks who is present. All fields are guarded by Hub.mu.
type Room struct {
	ID       string
	Password string

	clients map[string]*Client

	createdAt time.Time
	// lastActive is bumped whenever membership changes. Session 4 uses it to
	// close idle rooms (TTL); for now it is only recorded.
	lastActive time.Time
}
