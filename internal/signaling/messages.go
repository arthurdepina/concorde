package signaling

import "encoding/json"

// Roles a client can have inside a room. The room creator broadcasts their
// screen; everyone who joins with the room password only watches.
const (
	roleBroadcaster = "broadcaster"
	roleViewer      = "viewer"
)

// Error codes sent to clients. Kept stable so the frontend can branch on them.
const (
	errBadMessage      = "bad-message"
	errBadCredentials  = "bad-credentials"
	errBadRoomPassword = "bad-room-password"
	errRoomNotFound    = "room-not-found"
	errRoomFull        = "room-full"
	errNotInRoom       = "not-in-room"
	errPeerNotFound    = "peer-not-found"
	errTooManyAttempts = "too-many-attempts"
	errResumeFailed    = "resume-failed"
)

// inbound is any message a client sends us. Only the fields relevant to Type
// are populated; the rest stay zero.
type inbound struct {
	Type string `json:"type"`

	// create-room: admin credentials + the password friends will use to join
	// (empty for an open room).
	User         string `json:"user"`
	Password     string `json:"password"`
	RoomPassword string `json:"roomPassword"`

	// join-room.
	RoomID string `json:"roomId"`

	// resume-session: the token that claims a seat back after a reconnect (see
	// resume.go). Issued when the client entered the room; never the password.
	Token string `json:"token"`

	// signal: relay a WebRTC payload (SDP/ICE) to another peer in the room.
	To      string          `json:"to"`
	Payload json.RawMessage `json:"payload"`

	// rtc-report: how a viewer's peer connection to the broadcaster turned out
	// (see Hub.rtcReport). The candidate types and protocol describe the path ICE
	// picked and only come with the "connected" outcome.
	Outcome    string `json:"outcome"`
	LocalType  string `json:"localType"`
	RemoteType string `json:"remoteType"`
	Protocol   string `json:"protocol"`
}

// Outcomes a viewer can report for its peer connection, in inbound.Outcome.
const (
	// ICE found a path and media can flow.
	rtcConnected = "connected"
	// ICE never found a path: with only STUN, this pair would need a TURN relay.
	rtcFailed = "failed"
	// Connected at first, then lost mid-stream (network change, dead link).
	rtcDropped = "dropped"
)

// peerInfo describes another participant to a client.
type peerInfo struct {
	ID   string `json:"id"`
	Role string `json:"role"`
}

type helloMsg struct {
	Type string `json:"type"`
	From string `json:"from"`
}

type errorMsg struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func newError(code, message string) errorMsg {
	return errorMsg{Type: "error", Code: code, Message: message}
}

// Entering a room — however it happens — always comes with the resume token for
// the seat just taken, so the page can claim it back if its socket drops. The
// token is empty when resuming is disabled (see Hub.resumeGrace).
type roomCreatedMsg struct {
	Type   string `json:"type"`
	RoomID string `json:"roomId"`
	SelfID string `json:"selfId"`
	Role   string `json:"role"`
	Token  string `json:"token"`
}

type joinedMsg struct {
	Type   string     `json:"type"`
	RoomID string     `json:"roomId"`
	SelfID string     `json:"selfId"`
	Role   string     `json:"role"`
	Token  string     `json:"token"`
	Peers  []peerInfo `json:"peers"`
}

// resumedMsg confirms a reconnecting page got its seat back, with the room state
// as it stands now — the page may have missed peers coming and going while its
// socket was down, so this list, not what it remembered, is the truth.
type resumedMsg struct {
	Type   string     `json:"type"`
	RoomID string     `json:"roomId"`
	SelfID string     `json:"selfId"`
	Role   string     `json:"role"`
	Token  string     `json:"token"`
	Peers  []peerInfo `json:"peers"`
}

type peerJoinedMsg struct {
	Type   string `json:"type"`
	PeerID string `json:"peerId"`
	Role   string `json:"role"`
}

type peerLeftMsg struct {
	Type   string `json:"type"`
	PeerID string `json:"peerId"`
}

// roomClosedMsg tells everyone in a room that it is gone: reaped for being idle
// too long, shut down from the admin panel, or given up by the broadcaster it
// existed for. The reason is a stable code the frontend can branch on.
type roomClosedMsg struct {
	Type   string `json:"type"`
	Reason string `json:"reason"`
}

// Reasons a room can close, sent in roomClosedMsg.
const (
	reasonIdle            = "idle"
	reasonByAdmin         = "closed-by-admin"
	reasonBroadcasterLeft = "broadcaster-left"
)

// signalMsg relays a WebRTC payload from one peer to another, tagged with the
// sender so the receiver knows who to answer.
type signalMsg struct {
	Type    string          `json:"type"`
	From    string          `json:"from"`
	Payload json.RawMessage `json:"payload"`
}

// broadcastStoppedMsg tells the viewers the broadcaster stopped sharing on
// purpose, so they can tear their peer connection down cleanly instead of
// waiting for it to decay into "failed" — which would otherwise look like a
// connection error for what is a normal stop.
type broadcastStoppedMsg struct {
	Type string `json:"type"`
}
