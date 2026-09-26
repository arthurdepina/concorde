package signaling

import "log"

// rtcReport records how a viewer's peer connection to the broadcaster turned
// out. Media never passes through the server, so this report is its only view
// of whether P2P actually works: "failed" means ICE never found a path (with
// only STUN, that pair would have needed a TURN relay), "connected" comes with
// the candidate types of the path ICE picked, and "dropped" means a working
// connection was lost mid-stream. Only viewers report (one connection each, so
// nothing is counted twice). Reports are telemetry: anything malformed or from
// outside a room is ignored silently, with no error sent back.
func (h *Hub) rtcReport(c *Client, in inbound) {
	h.mu.Lock()
	var roomID string
	if c.room != nil && c.role == roleViewer {
		roomID = c.room.ID
	}
	h.mu.Unlock()
	if roomID == "" {
		return
	}

	switch in.Outcome {
	case rtcConnected:
		h.metrics.p2pConnected.Add(1)
		log.Printf("[rtc] room %s: viewer %s connected (%s→%s %s)", roomID, c.id,
			candidateType(in.LocalType), candidateType(in.RemoteType), transportProtocol(in.Protocol))
	case rtcFailed:
		h.metrics.p2pFailed.Add(1)
		log.Printf("[rtc] room %s: viewer %s failed to connect (no path without TURN)", roomID, c.id)
	case rtcDropped:
		h.metrics.p2pDropped.Add(1)
		log.Printf("[rtc] room %s: viewer %s dropped mid-stream", roomID, c.id)
	}
}

// candidateType whitelists an ICE candidate type from a client report, so
// arbitrary client text never reaches the log. host is a direct address (LAN or
// public IPv6), srflx/prflx an address learned through NAT (STUN), relay a TURN
// server.
func candidateType(t string) string {
	switch t {
	case "host", "srflx", "prflx", "relay":
		return t
	}
	return "unknown"
}

// transportProtocol whitelists the ICE transport protocol from a client report.
func transportProtocol(p string) string {
	switch p {
	case "udp", "tcp":
		return p
	}
	return "unknown"
}
