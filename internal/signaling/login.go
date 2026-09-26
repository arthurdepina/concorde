package signaling

import (
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Prefixes for loginLimiter keys. Admin and room passwords get separate streaks,
// so a friend fumbling a room password never locks the admin out (or vice versa).
const (
	loginKindAdmin = "admin|"
	loginKindRoom  = "room|"
)

// loginLimiter caps wrong-password attempts per visitor. After maxFailures misses
// in a row the key is locked out for lockout — the right password is refused
// too, meanwhile — so passwords can't be brute-forced through the public
// endpoints. A correct password clears the record, and a streak whose last miss
// is older than lockout is forgotten. maxFailures <= 0 disables the limit.
//
// It has its own mutex, independent of Hub.mu. Callers may hold Hub.mu while
// calling in, never the other way around.
type loginLimiter struct {
	maxFailures int
	lockout     time.Duration

	mu        sync.Mutex
	records   map[string]*loginRecord
	lastPrune time.Time
}

type loginRecord struct {
	failures    int
	lastFailure time.Time
	lockedUntil time.Time
}

func newLoginLimiter(maxFailures int, lockout time.Duration) *loginLimiter {
	return &loginLimiter{
		maxFailures: maxFailures,
		lockout:     lockout,
		records:     make(map[string]*loginRecord),
	}
}

// lockedFor reports how much longer key stays locked out; zero means it may try.
func (l *loginLimiter) lockedFor(key string, now time.Time) time.Duration {
	if l.maxFailures <= 0 {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if rec, ok := l.records[key]; ok && now.Before(rec.lockedUntil) {
		return rec.lockedUntil.Sub(now)
	}
	return 0
}

// fail records a wrong password from key and reports whether this miss locked
// it out.
func (l *loginLimiter) fail(key string, now time.Time) bool {
	if l.maxFailures <= 0 {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(now)

	rec, ok := l.records[key]
	if !ok || now.Sub(rec.lastFailure) >= l.lockout {
		// First miss, or the previous streak is old enough to forget.
		rec = &loginRecord{}
		l.records[key] = rec
	}
	rec.failures++
	rec.lastFailure = now
	if rec.failures < l.maxFailures {
		return false
	}
	rec.failures = 0
	rec.lockedUntil = now.Add(l.lockout)
	return true
}

// succeed clears key's record after a correct password.
func (l *loginLimiter) succeed(key string) {
	if l.maxFailures <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.records, key)
}

// pruneLocked drops records that are neither locked out nor mid-streak, at most
// once a minute, so the map can't grow without bound. Caller must hold l.mu.
func (l *loginLimiter) pruneLocked(now time.Time) {
	if now.Sub(l.lastPrune) < time.Minute {
		return
	}
	l.lastPrune = now
	for key, rec := range l.records {
		if !now.Before(rec.lockedUntil) && now.Sub(rec.lastFailure) >= l.lockout {
			delete(l.records, key)
		}
	}
}

// loginFailed records a wrong password for key and, when that locks it out,
// logs and counts the lockout so it shows up in the admin metrics.
func (h *Hub) loginFailed(key string, now time.Time) {
	if !h.logins.fail(key, now) {
		return
	}
	h.metrics.loginLockouts.Add(1)
	log.Printf("[auth] %s locked out for %s after %d wrong passwords", key, h.logins.lockout, h.logins.maxFailures)
}

// clientIP is the address a login attempt is charged to. Behind the Cloudflare
// Tunnel every request reaches us from the local cloudflared, so the TCP peer is
// loopback for everyone and the real visitor is in CF-Connecting-IP. That header
// is trusted only from loopback: a client reaching the server directly could
// otherwise forge it to dodge the limit.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if cf := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); cf != "" {
			return cf
		}
	}
	return host
}
