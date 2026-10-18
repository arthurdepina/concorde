// Package config loads runtime configuration from environment variables, with
// sane defaults. It also loads a .env file if present (git-ignored), so real
// secrets never live in the repo. See .env.example.
package config

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// Config holds all runtime settings.
type Config struct {
	Port int

	// The admin: the only account that gets the admin panel, and the only one
	// configured here rather than in the accounts file. Storing just the bcrypt
	// hash means the password is nowhere on disk — and keeping it in the
	// environment means a lost or corrupted accounts file can never lock the
	// admin out of the panel that manages it. Generate the hash with
	// `concorde hash`.
	AdminUser         string
	AdminPasswordHash string

	// AdminPasswordPlain is read only so the server can refuse to start when an
	// old ADMIN_PASSWORD is still lying around in the environment: falling back
	// to it would quietly undo the point of hashing.
	AdminPasswordPlain string

	// Where the broadcaster accounts live (JSON, created on first write). Keep
	// it out of the repo folder in production — it holds password hashes.
	AccountsFile string

	// A room with nobody broadcasting for this long gets closed.
	RoomTTLMinutes int

	// Broadcaster + viewers. P2P mesh stays cheap only with small rooms.
	MaxParticipants int

	// Hostnames allowed as the WebSocket Origin. Empty means same-origin only
	// (the safe default for local dev). Behind a reverse proxy or tunnel that may
	// not preserve the Host header, set this to the public host(s), e.g.
	// "share.example.com", so the browser origin is accepted.
	AllowedOrigins []string

	// Minutes between heartbeat log lines with the metrics snapshot. Zero or less
	// disables the heartbeat (the /admin/metrics endpoint still works).
	StatsIntervalMinutes int

	// Seconds between WebSocket keepalive pings. After the P2P media is flowing
	// the signaling socket goes quiet, and some proxies/tunnels close a WebSocket
	// idle for ~100s — a mid-session disconnect from the user's point of view. A
	// periodic ping keeps the connection warm. Zero or less disables pinging.
	WSPingSeconds int

	// How long a participant's seat in a room is held after their socket drops,
	// so the page can reconnect and carry on in the same room with the same role
	// and nobody else noticing. The broadcaster is a special case: their window
	// is the room's TTL, since the room lives for their stream (see
	// signaling.Hub.dropped). Zero or less disables resuming — a dropped socket
	// then leaves the room at once.
	ResumeGraceSeconds int

	// Wrong passwords allowed in a row per visitor (logins and room entries are
	// counted separately; a wrong room code is a miss too) before that visitor is
	// locked out for LoginLockoutMinutes — even the right password is refused
	// meanwhile, so neither passwords nor room codes can be brute-forced through
	// the public endpoints. Zero or less disables the limit.
	LoginMaxFailures    int
	LoginLockoutMinutes int
}

// Load reads configuration, applying defaults for anything unset.
func Load() Config {
	loadDotEnv(".env")

	return Config{
		Port:                 envInt("PORT", 3000),
		AdminUser:            envStr("ADMIN_USER", "admin"),
		AdminPasswordHash:    envStr("ADMIN_PASSWORD_HASH", ""),
		AdminPasswordPlain:   envStr("ADMIN_PASSWORD", ""),
		AccountsFile:         envStr("ACCOUNTS_FILE", "accounts.json"),
		RoomTTLMinutes:       envInt("ROOM_TTL_MINUTES", 10),
		MaxParticipants:      envInt("MAX_PARTICIPANTS", 3),
		AllowedOrigins:       envList("ALLOWED_ORIGINS"),
		StatsIntervalMinutes: envInt("STATS_INTERVAL_MINUTES", 15),
		WSPingSeconds:        envInt("WS_PING_SECONDS", 30),
		ResumeGraceSeconds:   envInt("RESUME_GRACE_SECONDS", 30),
		LoginMaxFailures:     envInt("LOGIN_MAX_FAILURES", 3),
		LoginLockoutMinutes:  envInt("LOGIN_LOCKOUT_MINUTES", 15),
	}
}

func envStr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return fallback
}

// envList reads a comma-separated environment variable into a slice, trimming
// spaces and dropping empty entries. An unset or blank value yields nil.
func envList(key string) []string {
	v, ok := os.LookupEnv(key)
	if !ok {
		return nil
	}
	var out []string
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// loadDotEnv reads simple KEY=VALUE lines from a .env file into the process
// environment. Existing environment variables are not overwritten. Missing file
// is not an error.
func loadDotEnv(path string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, value)
		}
	}
}
