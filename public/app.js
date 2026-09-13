// Frontend client. Rooms (session 2) plus the WebRTC media path (session 3):
// the admin captures a browser tab with getDisplayMedia and streams it directly
// to each viewer over a peer connection. Media never touches the server — it
// only relays the SDP/ICE "signal" messages between browsers.
//
// Topology: only the broadcaster sends media, so this is a simple star, not a
// full mesh. The broadcaster always creates the offer; viewers only answer.
// That fixed asymmetry means there is no glare, so no perfect-negotiation dance.

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
  video: document.getElementById("video"),
  videoPlaceholder: document.getElementById("video-placeholder"),
  videoControls: document.getElementById("video-controls"),
  volume: document.getElementById("volume"),
};

const ROLE_LABEL = { broadcaster: "quem transmite", viewer: "quem assiste" };

// Public STUN only for now (see Plano.md: TURN is added later, if ever needed).
const ICE_SERVERS = [{ urls: "stun:stun.l.google.com:19302" }];

// getDisplayMedia hints. Target is Full HD / 24fps+ with tab audio. The browser
// treats these as ideals; audio quality knobs are off so anime/music passes
// through untouched instead of being processed as if it were a voice call.
const CAPTURE = {
  video: {
    width: { ideal: 1920 },
    height: { ideal: 1080 },
    frameRate: { ideal: 30 },
  },
  audio: {
    echoCancellation: false,
    noiseSuppression: false,
    autoGainControl: false,
  },
};

const state = {
  socket: null,
  selfId: null,
  roomId: null,
  role: null,
  peers: new Map(), // peerId -> role
  pcs: new Map(), // peerId -> RTCPeerConnection
  localStream: null, // broadcaster's captured tab
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
      // If we are already broadcasting, bring the newcomer into the stream.
      if (state.role === "broadcaster" && state.localStream) {
        offerTo(msg.peerId);
      }
      break;
    case "peer-left":
      state.peers.delete(msg.peerId);
      closePeer(msg.peerId);
      renderPeers();
      break;
    case "signal":
      handleSignal(msg.from, msg.payload);
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

// --- WebRTC ----------------------------------------------------------------

function sendSignal(to, payload) {
  send({ type: "signal", to, payload });
}

// createPeerConnection builds a connection to one peer and wires ICE trickling.
// ontrack is attached by the caller (only viewers receive tracks).
function createPeerConnection(peerId) {
  const pc = new RTCPeerConnection({ iceServers: ICE_SERVERS });
  pc.pendingCandidates = []; // ICE that arrives before the remote description

  pc.addEventListener("icecandidate", (e) => {
    if (e.candidate) {
      sendSignal(peerId, { kind: "ice", candidate: e.candidate });
    }
  });
  pc.addEventListener("connectionstatechange", () => {
    if (pc.connectionState === "failed") {
      console.warn("[rtc] connection to", peerId, "failed");
    }
  });

  state.pcs.set(peerId, pc);
  return pc;
}

// offerTo (broadcaster) opens a connection to a viewer, attaches the captured
// tracks, and sends the offer.
async function offerTo(peerId) {
  const pc = createPeerConnection(peerId);
  for (const track of state.localStream.getTracks()) {
    pc.addTrack(track, state.localStream);
  }
  try {
    const offer = await pc.createOffer();
    await pc.setLocalDescription(offer);
    sendSignal(peerId, { kind: "offer", sdp: pc.localDescription });
  } catch (err) {
    console.error("[rtc] offer failed:", err);
  }
}

async function handleSignal(from, payload) {
  if (!payload || !payload.kind) return;

  try {
    switch (payload.kind) {
      case "offer": {
        // Viewer side: answer the broadcaster and show the incoming stream.
        const pc = createPeerConnection(from);
        pc.addEventListener("track", (e) => showRemoteStream(e.streams[0]));
        await pc.setRemoteDescription(payload.sdp);
        await flushCandidates(pc);
        const answer = await pc.createAnswer();
        await pc.setLocalDescription(answer);
        sendSignal(from, { kind: "answer", sdp: pc.localDescription });
        break;
      }
      case "answer": {
        // Broadcaster side: the viewer accepted our offer.
        const pc = state.pcs.get(from);
        if (pc) {
          await pc.setRemoteDescription(payload.sdp);
          await flushCandidates(pc);
        }
        break;
      }
      case "ice": {
        const pc = state.pcs.get(from);
        if (!pc) break;
        // Candidates can outrun the remote description; buffer until it lands.
        if (pc.remoteDescription && pc.remoteDescription.type) {
          await pc.addIceCandidate(payload.candidate);
        } else {
          pc.pendingCandidates.push(payload.candidate);
        }
        break;
      }
    }
  } catch (err) {
    console.error("[rtc] signal handling failed:", err);
  }
}

async function flushCandidates(pc) {
  const pending = pc.pendingCandidates;
  pc.pendingCandidates = [];
  for (const candidate of pending) {
    try {
      await pc.addIceCandidate(candidate);
    } catch (err) {
      console.warn("[rtc] addIceCandidate:", err);
    }
  }
}

// startBroadcast captures a tab and streams it to every viewer in the room.
async function startBroadcast() {
  let stream;
  try {
    stream = await navigator.mediaDevices.getDisplayMedia(CAPTURE);
  } catch (err) {
    // Most often the user just cancelled the picker; nothing to report.
    if (err?.name !== "NotAllowedError") {
      console.error("[rtc] getDisplayMedia failed:", err);
    }
    return;
  }

  state.localStream = stream;
  // Stopping the share from the browser's own UI ends the broadcast too.
  stream.getVideoTracks()[0].addEventListener("ended", stopBroadcast);

  showLocalPreview();
  updateBroadcastButton();

  for (const peerId of state.peers.keys()) {
    offerTo(peerId);
  }
}

function stopBroadcast() {
  stopMedia();
  updateBroadcastButton();
}

// stopMedia tears down all media: local capture, every peer connection, and the
// video element. Safe to call for viewers too (no local stream to stop).
function stopMedia() {
  if (state.localStream) {
    state.localStream.getTracks().forEach((t) => t.stop());
    state.localStream = null;
  }
  for (const [, pc] of state.pcs) {
    pc.close();
  }
  state.pcs.clear();
  hideVideo();
}

function closePeer(peerId) {
  const pc = state.pcs.get(peerId);
  if (pc) {
    pc.close();
    state.pcs.delete(peerId);
  }
  // A viewer only ever connects to the broadcaster; if that is gone, so is the
  // stream on screen.
  if (state.role === "viewer" && state.pcs.size === 0) {
    hideVideo();
  }
}

// --- video display ---------------------------------------------------------

function showLocalPreview() {
  els.video.srcObject = state.localStream;
  els.video.muted = true; // don't echo our own tab back at us
  els.videoControls.hidden = true; // the broadcaster has no volume to adjust
  showVideo();
}

function showRemoteStream(stream) {
  els.video.srcObject = stream;
  els.video.muted = false;
  els.video.volume = els.volume.value / 100;
  els.videoControls.hidden = false;
  showVideo();
}

function showVideo() {
  els.video.hidden = false;
  els.videoPlaceholder.hidden = true;
  els.video.play().catch(() => {});
}

function hideVideo() {
  els.video.srcObject = null;
  els.video.hidden = true;
  els.videoControls.hidden = true;
  els.videoPlaceholder.hidden = false;
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
  updateBroadcastButton();

  els.lobby.hidden = true;
  els.roomView.hidden = false;
}

function leaveLocal() {
  stopMedia();

  state.roomId = null;
  state.selfId = null;
  state.role = null;
  state.peers.clear();

  updateBroadcastButton();
  els.roomView.hidden = true;
  els.lobby.hidden = false;
}

// The broadcast button only makes sense for the room's broadcaster.
function updateBroadcastButton() {
  const isBroadcaster = state.role === "broadcaster";
  els.broadcastBtn.hidden = !isBroadcaster;
  if (isBroadcaster) {
    els.broadcastBtn.textContent = state.localStream
      ? "Parar transmissão"
      : "Transmitir";
  }
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
  if (state.role !== "broadcaster") return;
  if (state.localStream) {
    stopBroadcast();
  } else {
    startBroadcast();
  }
});

els.volume.addEventListener("input", () => {
  els.video.volume = els.volume.value / 100;
});

// Hidden until we know we are a room's broadcaster.
els.broadcastBtn.hidden = true;

connectSignaling();
