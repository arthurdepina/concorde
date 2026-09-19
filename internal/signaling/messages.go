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
	errBadAdmin        = "bad-admin"
	errBadRoomPassword = "bad-room-password"
	errRoomNotFound    = "room-not-found"
	errRoomFull        = "room-full"
	errNotInRoom       = "not-in-room"
	errPeerNotFound    = "peer-not-found"
)

// inbound is any message a client sends us. Only the fields relevant to Type
// are populated; the rest stay zero.
type inbound struct {
	Type string `json:"type"`

	// create-room: admin credentials + the password friends will use to join.
	User         string `json:"user"`
	Password     string `json:"password"`
	RoomPassword string `json:"roomPassword"`

	// join-room.
	RoomID string `json:"roomId"`

	// signal: relay a WebRTC payload (SDP/ICE) to another peer in the room.
	To      string          `json:"to"`
	Payload json.RawMessage `json:"payload"`
}

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

type roomCreatedMsg struct {
	Type   string `json:"type"`
	RoomID string `json:"roomId"`
	SelfID string `json:"selfId"`
	Role   string `json:"role"`
}

type joinedMsg struct {
	Type   string     `json:"type"`
	RoomID string     `json:"roomId"`
	SelfID string     `json:"selfId"`
	Role   string     `json:"role"`
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

// roomClosedMsg tells everyone in a room that it is gone: either reaped for being
// idle too long or shut down from the admin panel. The reason is a stable code
// the frontend can branch on ("idle" | "closed-by-admin").
type roomClosedMsg struct {
	Type   string `json:"type"`
	Reason string `json:"reason"`
}

// Reasons a room can close, sent in roomClosedMsg.
const (
	reasonIdle    = "idle"
	reasonByAdmin = "closed-by-admin"
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
