package signaling

import "time"

// Room is a single broadcast session: one broadcaster plus a few viewers, all
// swapping WebRTC signaling through the hub. Media never touches this struct —
// it only tracks who is present. All fields are guarded by Hub.mu.
type Room struct {
	ID       string
	Password string
	// Owner is the account that created the room. Kept for the logs and the
	// admin panel: with friends able to open rooms too, "who is this room?" is
	// a question worth being able to answer.
	Owner string

	clients map[string]*Client

	// broadcasting is true while the broadcaster is actively sharing a tab. An
	// actively broadcasting room is never reaped, no matter how old lastActive is.
	broadcasting bool

	createdAt time.Time
	// lastActive is bumped whenever membership changes or the broadcast is
	// started/stopped. The TTL reaper closes a room that has gone this long
	// without anyone broadcasting (see Hub.reapIdle).
	lastActive time.Time
}
