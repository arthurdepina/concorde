package signaling

import (
	"context"
	"log"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// sendBuffer is how many outbound messages we queue per client before treating
// it as too slow. Signaling traffic is tiny and bursty, so this is generous;
// three-person rooms never come close.
const sendBuffer = 32

// Client is one WebSocket connection. The read loop parses inbound messages and
// hands them to the hub; a separate write loop drains send so that other
// goroutines (peers, the hub) can notify this client without blocking.
//
// room and role are owned by the hub and only touched while holding Hub.mu.
type Client struct {
	id   string
	conn *websocket.Conn
	hub  *Hub
	send chan any

	room *Room
	role string
}

// enqueue queues a message for the write loop. It never blocks: if the buffer is
// full the client is too slow and we drop the message rather than stall the hub
// (which would freeze every room, since the hub holds a single lock).
func (c *Client) enqueue(msg any) {
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
		case "leave-room":
			c.hub.leaveRoom(c)
		default:
			c.enqueue(newError(errBadMessage, "unknown message type: "+msg.Type))
		}
	}
}
