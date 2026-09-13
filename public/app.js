// Frontend bootstrap. For this scaffold it only connects to the signaling
// WebSocket and reflects connection state in the UI. Rooms, auth and WebRTC
// arrive in later sessions.

const statusEl = document.getElementById("conn-status");
const broadcastBtn = document.getElementById("broadcast-btn");

function setStatus(connected) {
  statusEl.textContent = connected ? "conectado" : "desconectado";
  statusEl.classList.toggle("status--on", connected);
  statusEl.classList.toggle("status--off", !connected);
}

function connectSignaling() {
  const proto = location.protocol === "https:" ? "wss" : "ws";
  const socket = new WebSocket(`${proto}://${location.host}/ws`);

  socket.addEventListener("open", () => setStatus(true));
  socket.addEventListener("close", () => setStatus(false));
  socket.addEventListener("error", () => setStatus(false));
  socket.addEventListener("message", (event) => {
    console.log("[ws] message:", event.data);
  });
}

broadcastBtn.addEventListener("click", () => {
  // Placeholder until screen capture lands in a later session.
  console.log("Transmitir: ainda não implementado.");
});

connectSignaling();
