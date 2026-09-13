// Frontend signaling client. This session wires up rooms: an admin creates a
// room, friends join with the room password, and everyone sees who is present.
// Actual screen capture and the WebRTC media path arrive in a later session —
// the "signal" messages are already relayed by the server, ready for it.

const els = {
  status: document.getElementById("conn-status"),
  broadcastBtn: document.getElementById("broadcast-btn"),
  lobby: document.getElementById("lobby"),
  roomView: document.getElementById("room-view"),
  lobbyError: document.getElementById("lobby-error"),
  createForm: document.getElementById("create-form"),
  joinForm: document.getElementById("join-form"),
  adminUser: document.getElementById("admin-user"),
  adminPass: document.getElementById("admin-pass"),
  newRoomPass: document.getElementById("new-room-pass"),
  joinRoomId: document.getElementById("join-room-id"),
  joinRoomPass: document.getElementById("join-room-pass"),
  roomCode: document.getElementById("room-code"),
  roomRole: document.getElementById("room-role"),
  peerList: document.getElementById("peer-list"),
  leaveBtn: document.getElementById("leave-btn"),
};

const ROLE_LABEL = { broadcaster: "quem transmite", viewer: "quem assiste" };

const state = {
  socket: null,
  selfId: null,
  roomId: null,
  role: null,
  peers: new Map(), // peerId -> role
};

// --- connection ------------------------------------------------------------

function connectSignaling() {
  const proto = location.protocol === "https:" ? "wss" : "ws";
  const socket = new WebSocket(`${proto}://${location.host}/ws`);
  state.socket = socket;

  socket.addEventListener("open", () => setConnected(true));
  socket.addEventListener("close", () => {
    setConnected(false);
    leaveLocal();
  });
  socket.addEventListener("error", () => setConnected(false));
  socket.addEventListener("message", (event) => {
    let msg;
    try {
      msg = JSON.parse(event.data);
    } catch {
      return;
    }
    handleMessage(msg);
  });
}

function send(msg) {
  if (state.socket?.readyState === WebSocket.OPEN) {
    state.socket.send(JSON.stringify(msg));
  }
}

// --- protocol --------------------------------------------------------------

function handleMessage(msg) {
  switch (msg.type) {
    case "hello":
      break;
    case "room-created":
      enterRoom(msg.roomId, msg.selfId, msg.role, []);
      break;
    case "joined":
      enterRoom(msg.roomId, msg.selfId, msg.role, msg.peers);
      break;
    case "peer-joined":
      state.peers.set(msg.peerId, msg.role);
      renderPeers();
      break;
    case "peer-left":
      state.peers.delete(msg.peerId);
      renderPeers();
      break;
    case "signal":
      // Reserved for the WebRTC media path (next session).
      console.log("[signal] from", msg.from, msg.payload);
      break;
    case "error":
      showError(errorText(msg.code, msg.message));
      break;
    default:
      console.warn("[ws] unknown message:", msg);
  }
}

function errorText(code, fallback) {
  const map = {
    "bad-admin": "Usuário ou senha de admin incorretos.",
    "bad-room-password": "Senha da sala incorreta.",
    "room-not-found": "Sala não encontrada.",
    "room-full": "A sala está cheia.",
  };
  return map[code] || fallback || "Ocorreu um erro.";
}

// --- UI state --------------------------------------------------------------

function setConnected(connected) {
  els.status.textContent = connected ? "conectado" : "desconectado";
  els.status.classList.toggle("status--on", connected);
  els.status.classList.toggle("status--off", !connected);
}

function enterRoom(roomId, selfId, role, peers) {
  state.roomId = roomId;
  state.selfId = selfId;
  state.role = role;
  state.peers = new Map(peers.map((p) => [p.id, p.role]));

  els.roomCode.textContent = roomId;
  els.roomRole.textContent = ROLE_LABEL[role] || role;
  renderPeers();
  clearError();

  els.lobby.hidden = true;
  els.roomView.hidden = false;
}

function leaveLocal() {
  state.roomId = null;
  state.selfId = null;
  state.role = null;
  state.peers.clear();

  els.roomView.hidden = true;
  els.lobby.hidden = false;
}

function renderPeers() {
  els.peerList.innerHTML = "";
  if (state.peers.size === 0) {
    const li = document.createElement("li");
    li.textContent = "Ninguém mais na sala ainda.";
    els.peerList.appendChild(li);
    return;
  }
  for (const [, role] of state.peers) {
    const li = document.createElement("li");
    li.textContent = ROLE_LABEL[role] || role;
    els.peerList.appendChild(li);
  }
}

function showError(text) {
  els.lobbyError.textContent = text;
  els.lobbyError.hidden = false;
}

function clearError() {
  els.lobbyError.hidden = true;
}

// --- events ----------------------------------------------------------------

els.createForm.addEventListener("submit", (e) => {
  e.preventDefault();
  clearError();
  send({
    type: "create-room",
    user: els.adminUser.value.trim(),
    password: els.adminPass.value,
    roomPassword: els.newRoomPass.value,
  });
});

els.joinForm.addEventListener("submit", (e) => {
  e.preventDefault();
  clearError();
  send({
    type: "join-room",
    roomId: els.joinRoomId.value.trim().toUpperCase(),
    roomPassword: els.joinRoomPass.value,
  });
});

els.leaveBtn.addEventListener("click", () => {
  send({ type: "leave-room" });
  leaveLocal();
});

els.broadcastBtn.addEventListener("click", () => {
  // Screen capture lands in the next session.
  console.log("Transmitir: ainda não implementado.");
});

connectSignaling();
