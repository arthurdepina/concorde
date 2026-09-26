package signaling

import (
	"encoding/json"
	"testing"
)

// TestRTCReportCountsViewerOutcomes drives rtc-report messages over the socket:
// a viewer's three outcomes are counted, while reports from the broadcaster,
// from outside a room, or with an unknown outcome are ignored without an error
// reply. Reports get no response, so each step ends with a message that does
// (relayed signal / error); a client's messages are handled in order, so once
// that reply lands the reports before it have been processed.
func TestRTCReportCountsViewerOutcomes(t *testing.T) {
	url, hub := newTestServerHub(t)

	admin := dial(t, url)
	admin.write(map[string]any{"type": "create-room", "user": "admin", "password": "secret", "roomPassword": "pw"})
	created := admin.expect("room-created")
	roomID := created["roomId"].(string)
	adminID := created["selfId"].(string)

	friend := dial(t, url)
	friend.write(map[string]any{"type": "join-room", "roomId": roomID, "roomPassword": "pw"})
	friendID := friend.expect("joined")["selfId"].(string)
	admin.expect("peer-joined")

	payload := json.RawMessage(`{"k":"v"}`)

	// Viewer: one of each outcome, plus an unknown one.
	friend.write(map[string]any{"type": "rtc-report", "outcome": "connected", "localType": "srflx", "remoteType": "host", "protocol": "udp"})
	friend.write(map[string]any{"type": "rtc-report", "outcome": "failed"})
	friend.write(map[string]any{"type": "rtc-report", "outcome": "dropped"})
	friend.write(map[string]any{"type": "rtc-report", "outcome": "bogus"})
	friend.write(map[string]any{"type": "signal", "to": adminID, "payload": payload})
	admin.expect("signal")

	// Broadcaster: its reports are not counted (the viewer covers the pair).
	admin.write(map[string]any{"type": "rtc-report", "outcome": "failed"})
	admin.write(map[string]any{"type": "signal", "to": friendID, "payload": payload})
	friend.expect("signal")

	// Outside a room: ignored too.
	stranger := dial(t, url)
	stranger.write(map[string]any{"type": "rtc-report", "outcome": "failed"})
	stranger.write(map[string]any{"type": "join-room", "roomId": "NOROOM", "roomPassword": "x"})
	stranger.expect("error")

	s := hub.snapshotMetrics()
	if s.P2PConnected != 1 || s.P2PFailed != 1 || s.P2PDropped != 1 {
		t.Errorf("p2p connected/failed/dropped = %d/%d/%d, want 1/1/1", s.P2PConnected, s.P2PFailed, s.P2PDropped)
	}
	// Only the stranger's room-not-found; ignored reports send no error.
	if s.ErrorsSent != 1 {
		t.Errorf("errorsSent = %d, want 1", s.ErrorsSent)
	}
}

// TestRTCReportSanitizesPath makes sure client-supplied path fields are
// whitelisted before they reach the log.
func TestRTCReportSanitizesPath(t *testing.T) {
	for in, want := range map[string]string{"host": "host", "srflx": "srflx", "prflx": "prflx", "relay": "relay", "": "unknown", "evil\nline": "unknown"} {
		if got := candidateType(in); got != want {
			t.Errorf("candidateType(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"udp": "udp", "tcp": "tcp", "UDP": "unknown", "x": "unknown"} {
		if got := transportProtocol(in); got != want {
			t.Errorf("transportProtocol(%q) = %q, want %q", in, got, want)
		}
	}
}
