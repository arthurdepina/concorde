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

	// Only the admin can create rooms (for now).
	AdminUser     string
	AdminPassword string

	// A room with nobody broadcasting for this long gets closed.
	RoomTTLMinutes int

	// Broadcaster + viewers. P2P mesh stays cheap only with small rooms.
	MaxParticipants int
}

// Load reads configuration, applying defaults for anything unset.
func Load() Config {
	loadDotEnv(".env")

	return Config{
		Port:            envInt("PORT", 3000),
		AdminUser:       envStr("ADMIN_USER", "admin"),
		AdminPassword:   envStr("ADMIN_PASSWORD", "change-me"),
		RoomTTLMinutes:  envInt("ROOM_TTL_MINUTES", 10),
		MaxParticipants: envInt("MAX_PARTICIPANTS", 3),
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
