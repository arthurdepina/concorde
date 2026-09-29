// Concorde server: serves the static frontend and hosts the signaling
// WebSocket. Media itself never flows through here — it goes browser-to-browser
// over WebRTC. This process only does signaling, rooms, auth and TTL.
//
// Run with no arguments to start the server; see `concorde help` for the
// account and password commands.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/arthurdepina/concorde/internal/accounts"
	"github.com/arthurdepina/concorde/internal/config"
	"github.com/arthurdepina/concorde/internal/signaling"
)

func main() {
	cfg := config.Load()

	if handled, err := runCLI(os.Args[1:], cfg); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, "concorde:", err)
			os.Exit(1)
		}
		return
	}

	hub, err := startup(cfg)
	if err != nil {
		log.Fatal("concorde: ", err)
	}
	hub.StartReaper(context.Background())
	hub.StartStatsLogger(context.Background())

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir("public")))
	mux.HandleFunc("/healthz", handleHealth)
	mux.HandleFunc("/ws", hub.ServeWS)
	mux.HandleFunc("/admin/rooms", hub.ServeAdminRooms)
	mux.HandleFunc("/admin/rooms/close", hub.ServeAdminClose)
	mux.HandleFunc("/admin/metrics", hub.ServeAdminMetrics)
	mux.HandleFunc("/admin/accounts", hub.ServeAdminAccounts)

	addr := ":" + strconv.Itoa(cfg.Port)
	log.Printf("Concorde running at http://localhost%s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

// startup resolves the admin credential and loads the accounts before anything
// is served. Every failure here is fatal on purpose: a server that starts with
// nobody able to log in, or with a credential it could not verify, is worse than
// one that refuses to start and says why.
func startup(cfg config.Config) (*signaling.Hub, error) {
	adminHash, announce, err := resolveAdmin(cfg)
	if err != nil {
		return nil, err
	}

	store, err := accounts.Open(cfg.AccountsFile)
	if err != nil {
		return nil, err
	}
	store.SetAdmin(cfg.AdminUser, adminHash)

	if announce != "" {
		log.Printf("No ADMIN_PASSWORD_HASH set — this run only, the admin password is: %s", announce)
		log.Printf("To keep one across restarts, run `concorde hash` and put the line in your .env.")
	}
	log.Printf("[auth] admin %q; %d broadcaster account(s) from %s",
		cfg.AdminUser, len(store.List())-1, store.Path())

	return signaling.New(cfg, store), nil
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}
