// Concorde server: serves the static frontend and hosts the signaling
// WebSocket. Media itself never flows through here — it goes browser-to-browser
// over WebRTC. This process only does signaling, rooms, auth and TTL.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/arthurdepina/concorde/internal/config"
)

func main() {
	cfg := config.Load()

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir("public")))
	mux.HandleFunc("/healthz", handleHealth)
	mux.HandleFunc("/ws", handleWS)

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

// handleWS is the signaling endpoint. Full room/auth/WebRTC logic comes in
// later sessions; for now this only proves the wiring end-to-end.
func handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		log.Println("[ws] accept:", err)
		return
	}
	defer conn.CloseNow()

	log.Println("[ws] client connected")
	ctx := r.Context()

	if err := wsjson.Write(ctx, conn, map[string]string{"type": "hello", "from": "concorde"}); err != nil {
		return
	}

	for {
		var msg json.RawMessage
		if err := wsjson.Read(ctx, conn, &msg); err != nil {
			log.Println("[ws] client disconnected")
			return
		}
		log.Printf("[ws] message: %s", msg)
	}
}
