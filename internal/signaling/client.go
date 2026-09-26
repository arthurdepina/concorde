package signaling

import (
	"context"
	"log"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// pingTimeout is how long a single keepalive ping waits for its pong before the
// connection is treated as dead.
const pingTimeout = 10 * time.Second

// sendBuffer is how many outbound messages we queue per client before treating
// it as too slow. Signaling traffic is tiny and bursty, so this is generous;
// three-person rooms never come close.
const sendBuffer = 32

// Client is one WebSocket connection. The read loop parses inbound messages and
// hands them to the hub; a separate write loop drains send so that other
// goroutines (peers, the hub) can notify this client without blocking.
//
// A Client outlives its socket: when the connection drops, the client keeps its
// seat in the room for a grace period so the page can reconnect and claim it
// back (see resume.go). id, room, role, token and expiry are owned by the hub
// and only touched while holding Hub.mu.
type Client struct {
	id string
	// ip is the visitor's address (see clientIP), charged for wrong passwords.
	ip   string
	conn *websocket.Conn
	hub  *Hub
	send chan any

	room *Room
	role string

	// token claims this seat back after a reconnect; empty outside a room.
	token string
	// expiry releases the seat once the grace period runs out, unless a resume
	// stops it first. Only set while away.
	expiry *time.Timer
	// away is set while the socket is gone but the seat is still held. It is
	// atomic because enqueue reads it from any goroutine, with or without the
	// hub lock: there is no write loop left to drain the buffer, so messages for
	// an away client are dropped on the floor instead of filling it up. Nothing
	// is lost by that — a resume answers with the whole room state.
	away atomic.Bool
}

// enqueue queues a message for the write loop. It never blocks: if the buffer is
// full the client is too slow and we drop the message rather than stall the hub
// (which would freeze every room, since the hub holds a single lock).
func (c *Client) enqueue(msg any) {
	if c.away.Load() {
		return // socket gone, seat held: see Client.away
	}
	if _, ok := msg.(errorMsg); ok && c.hub != nil {
		c.hub.metrics.errorsSent.Add(1)
	}
	select {
	case c.send <- msg:
	default:
		if c.hub != nil {
			c.hub.metrics.droppedMessages.Add(1)
		}
		log.Printf("[ws] client %s send buffer full, dropping %T", c.id, msg)
	}
}

// writeLoop serializes all writes to the connection until the context is
// cancelled (connection closing) or a write fails.
func (c *Client) writeLoop(ctx context.Context) {
	defer c.hub.recoverPanic("writeLoop")
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-c.send:
			if err := wsjson.Write(ctx, c.conn, msg); err != nil {
				return
			}
		}
	}
}

// pingLoop keeps the connection warm by sending a WebSocket ping every interval.
// After the P2P media is flowing the signaling socket carries almost nothing, and
// some proxies/tunnels (Cloudflare included) close a WebSocket that has been idle
// for ~100s — which looks to the user like a mid-session disconnect. The ping is
// traffic that resets that idle timer; waiting for the pong also detects a truly
// dead peer promptly. A failed ping tears the connection down so readLoop
// unblocks and the normal cleanup runs. Clients always have readLoop running, so
// the peer's library auto-answers our pings with pongs.
func (c *Client) pingLoop(ctx context.Context, interval time.Duration) {
	defer c.hub.recoverPanic("pingLoop")
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pctx, cancel := context.WithTimeout(ctx, pingTimeout)
			err := c.conn.Ping(pctx)
			cancel()
			if err != nil {
				// Dead or unresponsive peer: close now so readLoop returns.
				c.conn.CloseNow()
				return
			}
		}
	}
}

// readLoop reads and dispatches messages until the client disconnects. On
// return the caller cleans up (leaves the room, closes the socket).
func (c *Client) readLoop(ctx context.Context) {
	for {
		var msg inbound
		if err := wsjson.Read(ctx, c.conn, &msg); err != nil {
			return
		}
		switch msg.Type {
		case "create-room":
			c.hub.createRoom(c, msg)
		case "join-room":
			c.hub.joinRoom(c, msg)
		case "signal":
			c.hub.relay(c, msg)
		case "broadcast-start":
			c.hub.setBroadcasting(c, true)
		case "broadcast-stop":
			c.hub.setBroadcasting(c, false)
		case "resume-session":
			c.hub.resumeSession(c, msg)
		case "rtc-report":
			c.hub.rtcReport(c, msg)
		case "leave-room":
			c.hub.leaveRoom(c)
		default:
			c.enqueue(newError(errBadMessage, "unknown message type: "+msg.Type))
		}
	}
}
