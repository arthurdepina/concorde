// Concorde server: serves the static frontend and hosts the signaling
// WebSocket. Media itself never flows through here — it goes browser-to-browser
// over WebRTC. This process only does signaling, rooms, auth and TTL.
package main

import (
	"log"
	"net/http"
	"strconv"

	"github.com/arthurdepina/concorde/internal/config"
	"github.com/arthurdepina/concorde/internal/signaling"
)

func main() {
	cfg := config.Load()
	hub := signaling.New(cfg)

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir("public")))
	mux.HandleFunc("/healthz", handleHealth)
	mux.HandleFunc("/ws", hub.ServeWS)

	addr := ":" + strconv.Itoa(cfg.Port)
	log.Printf("Concorde running at http://localhost%s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}
