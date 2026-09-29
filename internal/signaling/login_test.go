package signaling

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/arthurdepina/concorde/internal/config"
)

func TestLoginLimiterLocksAfterMaxFailures(t *testing.T) {
	l := newLoginLimiter(3, 15*time.Minute)
	t0 := time.Now()

	for i := 0; i < 2; i++ {
		if l.fail("k", t0) {
			t.Fatalf("miss %d should not lock yet", i+1)
		}
	}
	if !l.fail("k", t0) {
		t.Fatal("third miss should lock")
	}
	if l.lockedFor("k", t0.Add(14*time.Minute)) == 0 {
		t.Fatal("should stay locked until the lockout ends")
	}
	if l.lockedFor("other", t0) != 0 {
		t.Fatal("lockout must be per key")
	}
	if got := l.lockedFor("k", t0.Add(15*time.Minute)); got != 0 {
		t.Fatalf("should unlock once the lockout ends, still locked for %s", got)
	}
	// After the lockout a fresh streak starts: one miss doesn't re-lock.
	if l.fail("k", t0.Add(16*time.Minute)) {
		t.Fatal("first miss after the lockout should not lock")
	}
}

func TestLoginLimiterForgetsStreaks(t *testing.T) {
	t0 := time.Now()

	l := newLoginLimiter(3, 15*time.Minute)
	l.fail("k", t0)
	l.fail("k", t0)
	l.succeed("k")
	if l.fail("k", t0) {
		t.Fatal("a correct password should reset the streak")
	}

	// Misses spaced further apart than the lockout never add up.
	spaced := newLoginLimiter(3, 15*time.Minute)
	for i := 0; i < 5; i++ {
		if spaced.fail("k", t0.Add(time.Duration(i)*20*time.Minute)) {
			t.Fatalf("spaced-out miss %d should not lock", i+1)
		}
	}
}

func TestLoginLimiterDisabled(t *testing.T) {
	l := newLoginLimiter(0, 15*time.Minute)
	for i := 0; i < 10; i++ {
		if l.fail("k", time.Now()) {
			t.Fatal("a disabled limiter must never lock")
		}
	}
	if l.lockedFor("k", time.Now()) != 0 {
		t.Fatal("a disabled limiter must never lock")
	}
}

func TestClientIP(t *testing.T) {
	cases := []struct{ remote, cf, want string }{
		{"127.0.0.1:5000", "203.0.113.7", "203.0.113.7"},     // through the local tunnel
		{"[::1]:5000", "203.0.113.7", "203.0.113.7"},         // same, over IPv6 loopback
		{"127.0.0.1:5000", "", "127.0.0.1"},                  // local dev, no tunnel
		{"192.168.0.20:5000", "203.0.113.7", "192.168.0.20"}, // a direct client can't forge it
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = c.remote
		if c.cf != "" {
			r.Header.Set("CF-Connecting-IP", c.cf)
		}
		if got := clientIP(r); got != c.want {
			t.Errorf("clientIP(%s, cf=%q) = %q, want %q", c.remote, c.cf, got, c.want)
		}
	}
}

// newLockoutServer is newTestServerHub with the login limit on (3 misses).
func newLockoutServer(t *testing.T) (string, *Hub) {
	t.Helper()
	hub := testHub(config.Config{
		AdminUser:           "admin",
		MaxParticipants:     3,
		LoginMaxFailures:    3,
		LoginLockoutMinutes: 15,
	})
	srv := httptest.NewServer(http.HandlerFunc(hub.ServeWS))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http"), hub
}

func TestCreateRoomLocksOutAfterWrongPasswords(t *testing.T) {
	url, hub := newLockoutServer(t)
	c := dial(t, url)

	for i := 0; i < 3; i++ {
		c.write(map[string]any{"type": "create-room", "user": "admin", "password": "wrong", "roomPassword": "pw"})
		if got := c.expect("error")["code"]; got != errBadCredentials {
			t.Fatalf("miss %d: expected %q, got %v", i+1, errBadCredentials, got)
		}
	}
	// Locked out now: even the right password is refused.
	c.write(map[string]any{"type": "create-room", "user": "admin", "password": "secret", "roomPassword": "pw"})
	if got := c.expect("error")["code"]; got != errTooManyAttempts {
		t.Fatalf("expected %q, got %v", errTooManyAttempts, got)
	}
	if got := hub.snapshotMetrics().LoginLockouts; got != 1 {
		t.Fatalf("expected 1 lockout counted, got %d", got)
	}

	// Room passwords keep their own count, so joining isn't blocked.
	c.write(map[string]any{"type": "join-room", "roomId": "NOROOM", "roomPassword": "x"})
	if got := c.expect("error")["code"]; got != errRoomNotFound {
		t.Fatalf("expected %q, got %v", errRoomNotFound, got)
	}
}

func TestJoinRoomLocksOutAfterWrongPasswords(t *testing.T) {
	url, _ := newLockoutServer(t)
	admin := dial(t, url)
	admin.write(map[string]any{"type": "create-room", "user": "admin", "password": "secret", "roomPassword": "pw"})
	roomID := admin.expect("room-created")["roomId"].(string)

	friend := dial(t, url)
	for i := 0; i < 3; i++ {
		friend.write(map[string]any{"type": "join-room", "roomId": roomID, "roomPassword": "nope"})
		if got := friend.expect("error")["code"]; got != errBadRoomPassword {
			t.Fatalf("miss %d: expected %q, got %v", i+1, errBadRoomPassword, got)
		}
	}
	friend.write(map[string]any{"type": "join-room", "roomId": roomID, "roomPassword": "pw"})
	if got := friend.expect("error")["code"]; got != errTooManyAttempts {
		t.Fatalf("expected %q, got %v", errTooManyAttempts, got)
	}
}

func TestAdminHTTPLocksOutAfterWrongPasswords(t *testing.T) {
	hub := testHub(config.Config{
		AdminUser:           "admin",
		LoginMaxFailures:    3,
		LoginLockoutMinutes: 15,
	})
	srv := httptest.NewServer(http.HandlerFunc(hub.ServeAdminRooms))
	defer srv.Close()

	get := func(withAuth bool, pass string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
		if withAuth {
			req.SetBasicAuth("admin", pass)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do: %v", err)
		}
		res.Body.Close()
		return res
	}

	// Requests without credentials aren't guesses and don't count.
	for i := 0; i < 5; i++ {
		if res := get(false, ""); res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401 without auth, got %d", res.StatusCode)
		}
	}
	for i := 0; i < 3; i++ {
		if res := get(true, "wrong"); res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("miss %d: expected 401, got %d", i+1, res.StatusCode)
		}
	}
	res := get(true, "secret")
	if res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429 while locked out, got %d", res.StatusCode)
	}
	if res.Header.Get("Retry-After") == "" {
		t.Fatal("a 429 should carry Retry-After")
	}
}
