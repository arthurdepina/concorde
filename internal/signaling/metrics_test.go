package signaling

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestMetricsCountLifecycle drives a real create/join/relay/close flow over the
// socket and asserts the counters moved as expected, so the wiring in the hub
// stays honest.
func TestMetricsCountLifecycle(t *testing.T) {
	url, hub := newTestServerHub(t)

	admin := dial(t, url)
	admin.write(map[string]any{"type": "create-room", "user": "admin", "password": "secret", "roomPassword": "pw"})
	created := admin.expect("room-created")
	roomID := created["roomId"].(string)
	adminID := created["selfId"].(string)

	friend := dial(t, url)
	friend.write(map[string]any{"type": "join-room", "roomId": roomID, "roomPassword": "pw"})
	friend.expect("joined")
	admin.expect("peer-joined")

	// One relayed signal, and one error (unknown message type).
	friend.write(map[string]any{"type": "signal", "to": adminID, "payload": json.RawMessage(`{"k":"v"}`)})
	admin.expect("signal")
	friend.write(map[string]any{"type": "bogus"})
	if friend.expect("error")["code"] != errBadMessage {
		t.Fatal("expected bad-message error")
	}

	s := hub.snapshotMetrics()
	if s.RoomsCreated != 1 {
		t.Errorf("roomsCreated = %d, want 1", s.RoomsCreated)
	}
	if s.ConnectionsOpened != 2 {
		t.Errorf("connectionsOpened = %d, want 2", s.ConnectionsOpened)
	}
	if s.ActiveConnections != 2 {
		t.Errorf("activeConnections = %d, want 2", s.ActiveConnections)
	}
	if s.ActiveRooms != 1 || s.Participants != 2 {
		t.Errorf("activeRooms/participants = %d/%d, want 1/2", s.ActiveRooms, s.Participants)
	}
	if s.SignalsRelayed != 1 {
		t.Errorf("signalsRelayed = %d, want 1", s.SignalsRelayed)
	}
	if s.ErrorsSent != 1 {
		t.Errorf("errorsSent = %d, want 1", s.ErrorsSent)
	}
	if s.UptimeSeconds < 0 {
		t.Errorf("uptimeSeconds = %d, want >= 0", s.UptimeSeconds)
	}
}

func TestAdminMetricsRequiresAuth(t *testing.T) {
	hub := newHub()
	srv := httptest.NewServer(http.HandlerFunc(hub.ServeAdminMetrics))
	defer srv.Close()

	res, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth, got %d", res.StatusCode)
	}
}

func TestAdminMetricsReturnsSnapshot(t *testing.T) {
	hub := newHub()
	hub.metrics.roomsCreated.Add(3)
	hub.rooms["ROOM01"] = &Room{ID: "ROOM01", clients: map[string]*Client{"a": {id: "a"}}}

	srv := httptest.NewServer(http.HandlerFunc(hub.ServeAdminMetrics))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	req.SetBasicAuth("admin", "secret")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}

	var s MetricsSnapshot
	if err := json.NewDecoder(res.Body).Decode(&s); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if s.RoomsCreated != 3 {
		t.Errorf("roomsCreated = %d, want 3", s.RoomsCreated)
	}
	if s.ActiveRooms != 1 || s.Participants != 1 {
		t.Errorf("activeRooms/participants = %d/%d, want 1/1", s.ActiveRooms, s.Participants)
	}
}

// TestRecoverPanic confirms a panic in a wrapped goroutine is swallowed and
// counted rather than taking the process down.
func TestRecoverPanic(t *testing.T) {
	hub := newHub()

	func() {
		defer hub.recoverPanic("test")
		panic("boom")
	}()

	if got := hub.metrics.panicsRecovered.Load(); got != 1 {
		t.Fatalf("panicsRecovered = %d, want 1", got)
	}
}
