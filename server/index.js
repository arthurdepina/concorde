// Concorde server: serves the static frontend and hosts the signaling
// WebSocket. Media itself never flows through here — it goes browser-to-browser
// over WebRTC. This process only does signaling, rooms, auth and TTL.

import { createServer } from "node:http";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

import express from "express";
import { WebSocketServer } from "ws";

// Load .env if present (Node's built-in loader; no external dependency).
try {
  process.loadEnvFile();
} catch {
  // No .env file — fall back to real environment / defaults.
}

const { config } = await import("./config.js");

const __dirname = dirname(fileURLToPath(import.meta.url));
const publicDir = join(__dirname, "..", "public");

const app = express();
app.use(express.static(publicDir));

app.get("/healthz", (_req, res) => {
  res.json({ ok: true });
});

const server = createServer(app);

// Signaling endpoint. Full room/auth/WebRTC logic comes in later sessions;
// for now this only proves the wiring end-to-end.
const wss = new WebSocketServer({ server, path: "/ws" });

wss.on("connection", (socket) => {
  console.log("[ws] client connected");
  socket.send(JSON.stringify({ type: "hello", from: "concorde" }));

  socket.on("message", (data) => {
    console.log("[ws] message:", data.toString());
  });

  socket.on("close", () => {
    console.log("[ws] client disconnected");
  });
});

server.listen(config.port, () => {
  console.log(`Concorde running at http://localhost:${config.port}`);
});
