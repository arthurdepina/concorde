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
  createError: document.getElementById("create-error"),
  joinError: document.getElementById("join-error"),
  createForm: document.getElementById("create-form"),
  joinForm: document.getElementById("join-form"),
  adminUser: document.getElementById("admin-user"),
  adminPass: document.getElementById("admin-pass"),
  newRoomPass: document.getElementById("new-room-pass"),
  joinRoomId: document.getElementById("join-room-id"),
  joinRoomPass: document.getElementById("join-room-pass"),
  roomCode: document.getElementById("room-code"),
  copyCodeBtn: document.getElementById("copy-code"),
  roomRole: document.getElementById("room-role"),
  peerList: document.getElementById("peer-list"),
  leaveBtn: document.getElementById("leave-btn"),
  video: document.getElementById("video"),
  videoWindow: document.getElementById("video-window"),
  videoPlaceholder: document.getElementById("video-placeholder"),
  videoControls: document.getElementById("video-controls"),
  volume: document.getElementById("volume"),
  volumeControl: document.getElementById("volume-control"),
  fullscreenBtn: document.getElementById("fullscreen-btn"),
  statsBtn: document.getElementById("stats-btn"),
  statsOverlay: document.getElementById("stats-overlay"),
  browserWarning: document.getElementById("browser-warning"),
  browserWarningClose: document.getElementById("browser-warning-close"),
  toasts: document.getElementById("toasts"),
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
    channelCount: { ideal: 2 },
  },
};

// Encoding targets (numbers measured in Chrome, see Changelog, session 17).
// Chrome caps video at 2 Mb/s by default, which held anime-like 1080p content
// to ~540p; the cap is a ceiling, and congestion control still backs off below
// it on a link that can't take it. Chrome also starts its bandwidth estimate at
// ~300 kb/s and took ~45 s to climb from 360p to 1080p; starting it higher gets
// there in ~20 s. Opus defaults to ~32 kb/s mono — voice-call quality — so tab
// audio gets stereo at a music-grade bitrate.
const VIDEO_MAX_BITRATE = 8_000_000; // bits/s, per viewer
const VIDEO_START_BITRATE = 3_000_000; // bits/s, per viewer
const AUDIO_BITRATE = 128_000; // bits/s, Opus stereo

const state = {
  socket: null,
  connected: false,
  selfId: null,
  roomId: null,
  role: null,
  peers: new Map(), // peerId -> role
  pcs: new Map(), // peerId -> RTCPeerConnection
  localStream: null, // broadcaster's captured tab
  resume: null, // { token, roomId } to claim our seat back after a drop
  pendingForm: "join", // which lobby form a server error belongs to
};

// --- connection ------------------------------------------------------------

// The signaling socket is not the stream: the media runs peer-to-peer and does
// not care that this socket died, so a drop no longer tears the room down. The
// page reconnects on its own, backing off so an outage is not answered with a
// flood, and claims its seat back with the token the server issued (see
// "session" below) — same room, same role, nobody else the wiser.
const RECONNECT_MIN_MS = 1000;
const RECONNECT_MAX_MS = 20000;

let reconnectAttempts = 0;
let reconnectTimer = null;

function connectSignaling() {
  clearTimeout(reconnectTimer);
  reconnectTimer = null;

  const proto = location.protocol === "https:" ? "wss" : "ws";
  const socket = new WebSocket(`${proto}://${location.host}/ws`);
  state.socket = socket;

  socket.addEventListener("open", () => {
    reconnectAttempts = 0;
    setConnected(true);
    // Back from a drop (or a page reload, which keeps the token in
    // sessionStorage): ask for the seat we left behind.
    if (state.resume) send({ type: "resume-session", token: state.resume.token });
  });
  socket.addEventListener("close", () => {
    if (socket !== state.socket) return; // a socket we already replaced
    setConnected(false);
    scheduleReconnect();
  });
  // No "error" handler: a failed connection always fires close right after, and
  // that is where the retry lives.
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

// scheduleReconnect waits a little longer after each failure (1s, 2s, 4s… up to
// RECONNECT_MAX_MS), with jitter so several pages coming back from the same
// outage do not knock on the door in lockstep.
function scheduleReconnect() {
  if (reconnectTimer) return;
  const backoff = Math.min(RECONNECT_MIN_MS * 2 ** reconnectAttempts, RECONNECT_MAX_MS);
  reconnectAttempts += 1;
  reconnectTimer = setTimeout(connectSignaling, Math.round(backoff * (0.8 + Math.random() * 0.4)));
}

// reconnectNow skips the wait. The browser telling us the network is back, or
// the tab coming to the front, is better news than any timer.
function reconnectNow() {
  const readyState = state.socket?.readyState;
  if (readyState === WebSocket.OPEN || readyState === WebSocket.CONNECTING) return;
  reconnectAttempts = 0;
  connectSignaling();
}

function send(msg) {
  if (state.socket?.readyState === WebSocket.OPEN) {
    state.socket.send(JSON.stringify(msg));
  }
}

// --- session ---------------------------------------------------------------

// The seat's resume token. It is kept in memory (so an in-page reconnect works
// even where storage is blocked) and mirrored into sessionStorage, which carries
// it across a page reload and dies with the tab. The room password is never
// stored — the token only vouches for the seat we already had.
const SESSION_KEY = "concorde.session";

function rememberSession(token, roomId) {
  state.resume = token ? { token, roomId } : null;
  try {
    if (state.resume) sessionStorage.setItem(SESSION_KEY, JSON.stringify(state.resume));
    else sessionStorage.removeItem(SESSION_KEY);
  } catch {
    // Storage blocked (private window, etc.): resuming still works until reload.
  }
}

function forgetSession() {
  rememberSession(null, null);
}

// restoreSession reads back what a previous page load left, so opening the room
// again — or just hitting reload — walks straight back in.
function restoreSession() {
  try {
    const saved = JSON.parse(sessionStorage.getItem(SESSION_KEY) || "null");
    if (saved?.token) state.resume = saved;
  } catch {
    state.resume = null;
  }
}

// --- protocol --------------------------------------------------------------

function handleMessage(msg) {
  switch (msg.type) {
    case "hello":
      break;
    case "room-created":
      enterRoom(msg.roomId, msg.selfId, msg.role, [], msg.token);
      break;
    case "joined":
      enterRoom(msg.roomId, msg.selfId, msg.role, msg.peers, msg.token);
      break;
    case "resumed":
      enterRoom(msg.roomId, msg.selfId, msg.role, msg.peers, msg.token);
      recoverMedia();
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
    case "broadcast-stopped":
      handleBroadcastStopped();
      break;
    case "room-closed":
      // We may have left already (the broadcaster's own "Sair" closes the room);
      // no need to announce the end of something we just ended ourselves.
      if (state.roomId === null) break;
      forgetSession();
      leaveLocal();
      showToast(roomClosedText(msg.reason), "info");
      break;
    case "error":
      if (msg.code === "resume-failed") {
        handleResumeFailed();
        break;
      }
      showError(errorText(msg.code, msg.message));
      break;
    default:
      console.warn("[ws] unknown message:", msg);
  }
}

function errorText(code, fallback) {
  const map = {
    "bad-credentials": "Usuário ou senha incorretos.",
    "bad-room-password": "Senha da sala incorreta.",
    "room-not-found": "Sala não encontrada.",
    "room-full": "A sala está cheia.",
    "too-many-attempts": "Muitas senhas erradas. Espere alguns minutos e tente de novo.",
  };
  return map[code] || fallback || "Ocorreu um erro.";
}

function roomClosedText(reason) {
  if (reason === "closed-by-admin") return "A sala foi fechada pelo admin.";
  if (reason === "idle") return "A sala foi encerrada por inatividade.";
  if (reason === "broadcaster-left") return "Quem transmite saiu da sala.";
  return "A sala foi encerrada.";
}

// The seat is gone: we were away too long, or the room ended while we were. Back
// to the lobby — but the room code stays in the address bar, so if the room is
// still up, rejoining is just typing the password again.
function handleResumeFailed() {
  const wasInRoom = state.roomId !== null;
  forgetSession();
  leaveLocal({ keepLink: true });
  showToast(
    wasInRoom ? "A conexão caiu por tempo demais. Entre na sala de novo." : "Sua sessão expirou.",
    "info",
  );
  applyHashToJoinForm(); // code still in the bar: pre-fill it and go to the password
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
  // Viewers report how the connection turned out, so the server can count how
  // often STUN alone is not enough (see reportPath). Each viewer has exactly one
  // connection, so every pair is counted once.
  let everConnected = false;
  pc.addEventListener("connectionstatechange", () => {
    if (pc.connectionState === "connected" && !everConnected) {
      everConnected = true;
      if (state.role === "viewer") reportPath(pc);
    }
    if (pc.connectionState === "failed" && state.role === "viewer") {
      console.warn("[rtc] connection to", peerId, "failed");
      // Never connected: no path without TURN. Connected before: lost midway.
      send({ type: "rtc-report", outcome: everConnected ? "dropped" : "failed" });
      // A viewer has a single connection (to the broadcaster); if it fails there
      // is nothing left on screen, so it is worth surfacing — unless signaling
      // is down too, in which case the same outage took both, we are already on
      // our way back, and the resume will ask for a fresh offer. (For the
      // broadcaster, one viewer dropping is never worth a popup.)
      if (state.connected) showToast("Falha na conexão com quem transmite.");
    }
  });

  state.pcs.set(peerId, pc);
  return pc;
}

// offerTo (broadcaster) opens a connection to a viewer, attaches the captured
// tracks, and sends the offer.
async function offerTo(peerId) {
  // Any earlier connection to this peer is superseded by the offer we are about
  // to make (a resumed session, a restarted share), so drop it first.
  dropPeerConnection(peerId);
  const pc = createPeerConnection(peerId);
  const stream = state.localStream;
  for (const track of stream.getTracks()) {
    // Send-only (viewers never send media back). The video bitrate ceiling goes
    // in at creation, so it applies from the first frame without a later
    // setParameters round.
    const init = { direction: "sendonly", streams: [stream] };
    if (track.kind === "video") {
      init.sendEncodings = [{ maxBitrate: VIDEO_MAX_BITRATE }];
    }
    pc.addTransceiver(track, init);
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
        // A fresh offer supersedes any earlier connection to this peer (e.g. the
        // broadcaster stopped and started again), so drop the stale one first
        // instead of leaking it under the same peer id.
        dropPeerConnection(from);
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
          await pc.setRemoteDescription({
            type: payload.sdp.type,
            sdp: tuneAnswer(payload.sdp.sdp),
          });
          await flushCandidates(pc);
        }
        break;
      }
      case "need-offer": {
        // A viewer came back from a drop with a dead connection and is asking
        // for a fresh one. Only the broadcaster answers, and only while sharing.
        if (state.role === "broadcaster" && state.localStream) offerTo(from);
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

const VIDEO_CODECS = new Set(["vp8", "vp9", "h264", "h265", "av1"]);

// tuneAnswer edits a viewer's answer before the broadcaster applies it. The
// sender's encoders follow the receiver's preferences in the answer's fmtp
// lines, and no browser API sets these — hence the SDP edit. Opus gets stereo at
// AUDIO_BITRATE; each video codec gets Chrome's start-bitrate hint (kb/s, see
// VIDEO_START_BITRATE). Only those fmtp lines are touched (retransmission/FEC
// entries are left alone).
function tuneAnswer(sdp) {
  const opus = { stereo: "1", maxaveragebitrate: String(AUDIO_BITRATE) };
  const video = { "x-google-start-bitrate": String(VIDEO_START_BITRATE / 1000) };
  for (const [, pt, codec] of sdp.matchAll(/^a=rtpmap:(\d+) ([\w-]+)\//gm)) {
    const name = codec.toLowerCase();
    if (name === "opus") sdp = setFmtp(sdp, pt, opus);
    else if (VIDEO_CODECS.has(name)) sdp = setFmtp(sdp, pt, video);
  }
  return sdp;
}

// setFmtp merges `wanted` into the fmtp line of payload type pt, adding the
// line right after its rtpmap if there is none yet.
function setFmtp(sdp, pt, wanted) {
  const fmtp = new RegExp(`^a=fmtp:${pt} (.*)$`, "m");
  if (fmtp.test(sdp)) {
    return sdp.replace(fmtp, (_, params) => `a=fmtp:${pt} ${mergeFmtp(params, wanted)}`);
  }
  const rtpmap = new RegExp(`^a=rtpmap:${pt} .*$`, "m");
  return sdp.replace(rtpmap, (line) => `${line}\r\na=fmtp:${pt} ${mergeFmtp("", wanted)}`);
}

// mergeFmtp sets `wanted` on a "k=v;k=v" fmtp parameter list, keeping the rest.
function mergeFmtp(params, wanted) {
  const merged = new Map();
  for (const part of params.split(";")) {
    const [key, value] = part.split("=").map((s) => s.trim());
    if (key) merged.set(key, value);
  }
  for (const [key, value] of Object.entries(wanted)) merged.set(key, value);
  return [...merged].map(([k, v]) => (v === undefined ? k : `${k}=${v}`)).join(";");
}

// startBroadcast captures a tab and streams it to every viewer in the room.
async function startBroadcast() {
  // getDisplayMedia is missing on insecure origins (non-HTTPS, non-localhost)
  // and on browsers that do not support screen capture at all.
  if (!navigator.mediaDevices?.getDisplayMedia) {
    showToast("Captura de tela indisponível aqui (precisa de HTTPS ou localhost).");
    return;
  }

  let stream;
  try {
    stream = await navigator.mediaDevices.getDisplayMedia(CAPTURE);
  } catch (err) {
    // NotAllowedError is almost always the user cancelling the picker; stay quiet
    // in that case and only report genuine failures.
    if (err?.name === "NotAllowedError") return;
    console.error("[rtc] getDisplayMedia failed:", err);
    showToast("Não foi possível iniciar a captura de tela.");
    return;
  }

  state.localStream = stream;
  const [video] = stream.getVideoTracks();
  // Screen capture defaults to "detail" (static text: keep resolution, drop
  // frames). Anime is moving video, so tell the encoder to favour smoothness.
  video.contentHint = "motion";
  for (const audio of stream.getAudioTracks()) audio.contentHint = "music";
  // Stopping the share from the browser's own UI ends the broadcast too.
  video.addEventListener("ended", stopBroadcast);

  showLocalPreview();
  updateBroadcastButton();
  // Tell the server we are live, so the TTL reaper leaves this room alone.
  send({ type: "broadcast-start" });

  for (const peerId of state.peers.keys()) {
    offerTo(peerId);
  }
}

function stopBroadcast() {
  stopMedia();
  updateBroadcastButton();
  // Back to idle: this restarts the room's TTL clock on the server.
  send({ type: "broadcast-stop" });
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

// dropPeerConnection closes and forgets the connection to a peer, if any. Unlike
// closePeer it leaves the video element alone: the caller is replacing the
// connection, not ending the stream, so the picture must not flicker.
function dropPeerConnection(peerId) {
  const pc = state.pcs.get(peerId);
  if (!pc) return;
  pc.close();
  state.pcs.delete(peerId);
}

function closePeer(peerId) {
  dropPeerConnection(peerId);
  // A viewer only ever connects to the broadcaster; if that is gone, so is the
  // stream on screen.
  if (state.role === "viewer" && state.pcs.size === 0) {
    hideVideo();
  }
}

// recoverMedia runs after a resumed session. The peer connections never ran on
// the signaling socket, so most of the time they sailed through the drop and
// there is nothing to do here. This is for when the same outage took the media
// with it: the broadcaster re-offers to whoever it can no longer reach, and a
// viewer asks the broadcaster for a fresh offer.
function recoverMedia() {
  // People may have come and gone while we were away, and the resumed peer list
  // is the truth — so let go of anyone no longer in the room.
  for (const peerId of state.pcs.keys()) {
    if (!state.peers.has(peerId)) closePeer(peerId);
  }

  if (state.role === "broadcaster") {
    if (!state.localStream) return; // not sharing: nothing to restore
    // The room stopped counting as live the moment we dropped; take it back, or
    // the TTL reaper would close a room that is in fact streaming.
    send({ type: "broadcast-start" });
    for (const peerId of state.peers.keys()) {
      if (!isLiveConnection(state.pcs.get(peerId))) offerTo(peerId);
    }
    return;
  }

  for (const [peerId, role] of state.peers) {
    if (role === "broadcaster" && !isLiveConnection(state.pcs.get(peerId))) {
      sendSignal(peerId, { kind: "need-offer" });
    }
  }
}

// A connection that is up, or still has a fair chance of getting there on its
// own. "disconnected" does not count: it can recover by itself, but a fresh
// offer is cheaper than waiting to find out.
function isLiveConnection(pc) {
  return Boolean(pc) && ["new", "connecting", "connected"].includes(pc.connectionState);
}

// The broadcaster stopped sharing on purpose (server relayed broadcast-stopped).
// Close our side right away — a connection we close reports "closed", not
// "failed", so this sidesteps the connection-lost toast an idle-then-failed
// connection would raise. The broadcaster stays in the room, so a later restart
// just brings a fresh offer.
function handleBroadcastStopped() {
  if (state.role !== "viewer") return;
  stopMedia();
  showToast("A transmissão foi encerrada.", "info");
}

// --- video display ---------------------------------------------------------

// The broadcaster's preview keeps only the stats toggle: there is no volume to
// adjust (the preview is muted) and no point in fullscreening your own tab.
function showLocalPreview() {
  els.video.srcObject = state.localStream;
  els.video.muted = true; // don't echo our own tab back at us
  setControls({ viewer: false });
  showVideo();
}

function showRemoteStream(stream) {
  els.video.srcObject = stream;
  els.video.muted = false;
  els.video.volume = els.volume.value / 100;
  setControls({ viewer: true });
  showVideo();
}

function setControls({ viewer }) {
  els.videoControls.hidden = false;
  els.volumeControl.hidden = !viewer;
  els.fullscreenBtn.hidden = !viewer;
}

function showVideo() {
  els.video.hidden = false;
  els.videoPlaceholder.hidden = true;
  els.video.play().catch(() => {});
  syncStats();
}

function hideVideo() {
  els.video.srcObject = null;
  els.video.hidden = true;
  els.videoControls.hidden = true;
  els.videoPlaceholder.hidden = false;
  syncStats();
}

// --- stream stats ----------------------------------------------------------

// An overlay (toggled from the video controls, remembered per browser) with the
// stream's real numbers from getStats, refreshed every second: what a viewer is
// actually receiving or, for the broadcaster, what goes out to each viewer and
// what — if anything — holds the encoder back. It only polls while shown.
const STATS_INTERVAL_MS = 1000;
const STATS_PREF_KEY = "concorde.stats";

let statsOn = loadStatsPref();
let statsTimer = null;
let statsBusy = false; // a slow getStats round must not overlap the next tick

function loadStatsPref() {
  try {
    return localStorage.getItem(STATS_PREF_KEY) === "1";
  } catch {
    return false;
  }
}

function toggleStats() {
  statsOn = !statsOn;
  try {
    localStorage.setItem(STATS_PREF_KEY, statsOn ? "1" : "0");
  } catch {
    // Storage blocked (private window, etc.): the toggle still works this visit.
  }
  syncStats();
}

// syncStats shows the overlay while it is toggled on and a video is up, and
// polls only then.
function syncStats() {
  const visible = statsOn && !els.video.hidden;
  els.statsBtn.setAttribute("aria-pressed", String(statsOn));
  els.statsOverlay.hidden = !visible;
  if (visible && !statsTimer) {
    renderStats(["medindo…"]);
    statsTimer = setInterval(updateStats, STATS_INTERVAL_MS);
    updateStats();
  } else if (!visible && statsTimer) {
    clearInterval(statsTimer);
    statsTimer = null;
  }
}

async function updateStats() {
  if (statsBusy) return;
  statsBusy = true;
  try {
    const samples = await Promise.all(
      [...state.pcs.values()].map((pc) => readStats(pc).catch(() => null)),
    );
    if (!statsTimer) return; // hidden while we were reading
    renderStats(
      state.role === "broadcaster" ? broadcasterLines(samples) : viewerLines(samples[0]),
    );
  } finally {
    statsBusy = false;
  }
}

function renderStats(lines) {
  els.statsOverlay.replaceChildren(
    ...lines.map((text) => {
      const line = document.createElement("div");
      line.textContent = text;
      return line;
    }),
  );
}

// readStats condenses one getStats() report into what the overlay shows.
// Bitrates and loss are rates over the last interval, so each call keeps its
// report on the connection for the next call to diff against.
async function readStats(pc) {
  const report = await pc.getStats();
  const prev = pc.lastStats;
  pc.lastStats = report;

  const sample = { video: null, audio: null, rttMs: null, lossPct: null, path: null };
  const pair = selectedPair(report);
  if (pair) {
    sample.path = pathOf(report, pair);
    if (pair.currentRoundTripTime != null) sample.rttMs = pair.currentRoundTripTime * 1000;
  }

  for (const s of report.values()) {
    if (s.type === "inbound-rtp" || s.type === "outbound-rtp") {
      const inbound = s.type === "inbound-rtp";
      const before = prev?.get(s.id);
      const media = {
        kbps: rateKbps(before, s, inbound ? "bytesReceived" : "bytesSent"),
        codec: report.get(s.codecId)?.mimeType?.split("/")[1],
      };
      if (s.kind === "video") {
        media.width = s.frameWidth;
        media.height = s.frameHeight;
        media.fps = s.framesPerSecond;
        media.limit = s.qualityLimitationReason; // sender side only
        if (inbound) sample.lossPct = lossPct(before, s);
        sample.video = media;
      } else if (s.kind === "audio") {
        sample.audio = media;
      }
    } else if (s.type === "remote-inbound-rtp" && s.kind === "video" && s.fractionLost != null) {
      // Broadcaster side: the viewer's receiver reports its loss back over RTCP.
      sample.lossPct = s.fractionLost * 100;
    }
  }
  return sample;
}

function rateKbps(before, now, field) {
  if (before?.[field] == null || now[field] == null) return null;
  const secs = (now.timestamp - before.timestamp) / 1000;
  return secs > 0 ? ((now[field] - before[field]) * 8) / secs / 1000 : null;
}

function lossPct(before, now) {
  if (!before) return null;
  const lost = Math.max(0, now.packetsLost - before.packetsLost);
  const got = now.packetsReceived - before.packetsReceived;
  return lost + got > 0 ? (lost / (lost + got)) * 100 : 0;
}

// selectedPair returns the candidate pair ICE settled on, if any.
function selectedPair(report) {
  for (const s of report.values()) {
    if (s.type === "transport" && s.selectedCandidatePairId) {
      return report.get(s.selectedCandidatePairId);
    }
  }
  // Firefox has no transport stats; it flags the pair itself.
  for (const s of report.values()) {
    if (s.type === "candidate-pair" && s.selected) return s;
  }
  return null;
}

function pathOf(report, pair) {
  if (!pair) return null;
  const local = report.get(pair.localCandidateId);
  const remote = report.get(pair.remoteCandidateId);
  return {
    local: local?.candidateType,
    remote: remote?.candidateType,
    protocol: local?.protocol,
  };
}

// reportPath sends the server the "connected" outcome of the viewer's
// connection, with the path ICE picked (see createPeerConnection).
async function reportPath(pc) {
  let path = null;
  try {
    const report = await pc.getStats();
    path = pathOf(report, selectedPair(report));
  } catch {
    // Still report the outcome, just without the path.
  }
  send({
    type: "rtc-report",
    outcome: "connected",
    localType: path?.local,
    remoteType: path?.remote,
    protocol: path?.protocol,
  });
}

// pathLabel names how the connection got through: straight between the two
// addresses (same network or public IPv6), across NAT with an address learned
// through STUN, or relayed by a TURN server.
function pathLabel(path) {
  if (!path) return "conectando…";
  const types = [path.local, path.remote];
  if (types.includes("relay")) return "via TURN (relay)";
  if (types.every((t) => t === "host")) return "P2P direto";
  return "P2P via NAT (STUN)";
}

const decimal = new Intl.NumberFormat("pt-BR", { maximumFractionDigits: 1 });

const LIMIT_LABEL = { none: "nenhum", bandwidth: "banda", cpu: "CPU", other: "outro" };

function fmtBitrate(kbps) {
  if (kbps == null) return "—";
  return kbps >= 1000 ? `${decimal.format(kbps / 1000)} Mb/s` : `${Math.round(kbps)} kb/s`;
}

function fmtFrames(video) {
  if (!video?.width) return "aguardando vídeo…";
  return `${video.width}×${video.height} · ${Math.round(video.fps ?? 0)} fps`;
}

function fmtVideoRate(video) {
  const codec = video?.codec ? ` (${video.codec})` : "";
  return `vídeo ${fmtBitrate(video?.kbps)}${codec}`;
}

function fmtNet(sample) {
  const parts = [];
  if (sample.rttMs != null) parts.push(`${Math.round(sample.rttMs)} ms`);
  if (sample.lossPct != null) parts.push(`perda ${decimal.format(sample.lossPct)}%`);
  return parts.join(" · ");
}

function joinParts(...parts) {
  return parts.filter(Boolean).join(" · ");
}

// A viewer sees what it receives from the broadcaster.
function viewerLines(sample) {
  if (!sample) return ["sem conexão"];
  return [
    fmtFrames(sample.video),
    joinParts(fmtVideoRate(sample.video), `áudio ${fmtBitrate(sample.audio?.kbps)}`),
    joinParts(pathLabel(sample.path), fmtNet(sample)),
  ];
}

// The broadcaster sees what goes out to each viewer — each has its own encoder
// — and the encoder's bottleneck ("banda" = upload/network, "CPU" = this
// machine), which says what to fix when quality drops.
function broadcasterLines(samples) {
  if (samples.length === 0) return ["ninguém assistindo ainda"];
  const lines = [];
  samples.forEach((sample, i) => {
    if (i > 0) lines.push("");
    lines.push(joinParts(`espectador ${i + 1}`, sample && pathLabel(sample.path)));
    if (!sample) {
      lines.push("sem dados");
      return;
    }
    const limit = sample.video?.limit;
    lines.push(joinParts(fmtFrames(sample.video), fmtVideoRate(sample.video)));
    lines.push(
      joinParts(
        `áudio ${fmtBitrate(sample.audio?.kbps)}`,
        fmtNet(sample),
        limit && `gargalo: ${LIMIT_LABEL[limit] || limit}`,
      ),
    );
  });
  return lines;
}

// --- UI state --------------------------------------------------------------

// Three states, because a dropped socket is no longer the end of anything: with
// a seat to go back to we are on our way back ("reconectando…"), and the stream
// on screen keeps playing throughout.
function setConnected(connected) {
  state.connected = connected;
  const reconnecting = !connected && Boolean(state.resume);
  els.status.textContent = connected ? "conectado" : reconnecting ? "reconectando…" : "desconectado";
  els.status.classList.toggle("status--on", connected);
  els.status.classList.toggle("status--wait", reconnecting);
  els.status.classList.toggle("status--off", !connected && !reconnecting);
  updateBroadcastButton();
}

// enterRoom shows the room, whether we just created it, just joined it, or just
// got our seat back — in which case peers and token are the server's current
// truth, not what this page remembered.
function enterRoom(roomId, selfId, role, peers, token) {
  state.roomId = roomId;
  state.selfId = selfId;
  state.role = role;
  state.peers = new Map((peers || []).map((p) => [p.id, p.role]));
  rememberSession(token, roomId);

  els.roomCode.textContent = roomId;
  els.roomRole.textContent = ROLE_LABEL[role] || role;
  setRoomHash(roomId);
  renderPeers();
  clearError();
  updateBroadcastButton();

  els.lobby.hidden = true;
  els.roomView.hidden = false;
}

// leaveLocal drops back to the lobby. keepLink leaves the room code in the
// address bar for the cases where the room may well still be there and only our
// seat is gone — the friend just types the password again.
function leaveLocal({ keepLink = false } = {}) {
  stopMedia();
  if (!keepLink) clearRoomHash();

  state.roomId = null;
  state.selfId = null;
  state.role = null;
  state.peers.clear();

  updateBroadcastButton();
  els.roomView.hidden = true;
  els.lobby.hidden = false;
}

// The broadcast button only makes sense for the room's broadcaster — and only
// while the server can hear us, since starting a share is a conversation with
// every viewer.
function updateBroadcastButton() {
  const isBroadcaster = state.role === "broadcaster";
  const live = Boolean(state.localStream);
  els.broadcastBtn.hidden = !isBroadcaster;
  // Stopping must always be possible: that is local, and a blip should not trap
  // anyone into sharing their tab.
  els.broadcastBtn.disabled = !state.connected && !live;
  els.broadcastBtn.classList.toggle("is-live", live);
  if (isBroadcaster) {
    els.broadcastBtn.textContent = live ? "Parar transmissão" : "Transmitir";
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

// Server errors are shown right under the form the user just submitted (tracked
// in state.pendingForm), so a wrong password / missing room lands where they were
// looking instead of in a shared line below both forms.
function showError(text) {
  const el = state.pendingForm === "create" ? els.createError : els.joinError;
  clearError();
  el.textContent = text;
  el.hidden = false;
}

function clearError() {
  els.createError.hidden = true;
  els.joinError.hidden = true;
}

// --- toasts ----------------------------------------------------------------

// Transient, view-independent notices (capture failures, dropped connection, a
// room closing). Form-field problems stay inline in the lobby via showError.
function showToast(text, kind = "error") {
  const el = document.createElement("div");
  el.className = `toast toast--${kind}`;
  el.setAttribute("role", kind === "error" ? "alert" : "status");
  el.textContent = text;
  els.toasts.appendChild(el);
  setTimeout(() => el.remove(), 6000);
}

// requireConnection guards the actions that are nothing but a request to the
// server. Without it a click during a reconnect would quietly do nothing, since
// send() drops what it cannot deliver.
function requireConnection() {
  if (state.connected) return true;
  showToast("Sem conexão com o servidor. Tentando reconectar…");
  reconnectNow();
  return false;
}

// --- browser support -------------------------------------------------------

// We only verify tab audio on Chrome/Edge (see Plano.md). Everything else gets a
// non-blocking warning. Opera is Chromium-based but still flagged, so we match
// the Chrome/Edge brands specifically and exclude Opera.
function isSupportedBrowser() {
  const brands = navigator.userAgentData?.brands;
  if (brands?.length) {
    const names = brands.map((b) => b.brand);
    if (names.includes("Opera")) return false;
    return names.includes("Google Chrome") || names.includes("Microsoft Edge");
  }
  // Fallback for browsers without UA Client Hints (e.g. Firefox).
  const ua = navigator.userAgent;
  if (/OPR\//.test(ua) || /Firefox\//.test(ua)) return false;
  return /Edg\//.test(ua) || /Chrome\//.test(ua);
}

function maybeWarnBrowser() {
  if (!isSupportedBrowser()) {
    els.browserWarning.hidden = false;
  }
}

// --- fullscreen ------------------------------------------------------------

// Viewers can blow the stream up to the whole screen. We fullscreen the whole
// video window (not just the <video>) so the volume/fullscreen controls stay
// reachable while in fullscreen.
function toggleFullscreen() {
  if (document.fullscreenElement) {
    document.exitFullscreen().catch(() => {});
  } else {
    els.videoWindow.requestFullscreen?.().catch(() => {});
  }
}

function syncFullscreenButton() {
  const on = document.fullscreenElement === els.videoWindow;
  els.fullscreenBtn.setAttribute(
    "aria-label",
    on ? "Sair da tela cheia" : "Tela cheia",
  );
  els.fullscreenBtn.title = on ? "Sair da tela cheia" : "Tela cheia";
}

// While in fullscreen, the controls (and cursor) hide after a couple of seconds
// of no mouse movement and reappear on the next move (CSS handles the fade via
// the .controls-hidden class). The listener is only attached while fullscreen.
let hideControlsTimer = null;

function revealControls() {
  els.videoWindow.classList.remove("controls-hidden");
  clearTimeout(hideControlsTimer);
  hideControlsTimer = setTimeout(() => {
    if (document.fullscreenElement === els.videoWindow) {
      els.videoWindow.classList.add("controls-hidden");
    }
  }, 2500);
}

function onFullscreenChange() {
  syncFullscreenButton();
  if (document.fullscreenElement === els.videoWindow) {
    els.videoWindow.addEventListener("mousemove", revealControls);
    revealControls();
  } else {
    els.videoWindow.removeEventListener("mousemove", revealControls);
    clearTimeout(hideControlsTimer);
    els.videoWindow.classList.remove("controls-hidden");
  }
}

// --- room URL (hash) -------------------------------------------------------

// The room's code lives in the URL fragment (#CODE) so a room has a shareable
// link. The password is never in the URL — the friend always types it. The
// fragment also stays entirely client-side (browsers never send it to the
// server), so the code leaks into no access log. A link opened in the lobby
// pre-fills the join form; while in a room the bar mirrors the current code.
const NON_CODE = /[^A-Z0-9]/g;

function codeFromHash() {
  return decodeURIComponent(location.hash.replace(/^#/, ""))
    .trim()
    .toUpperCase()
    .replace(NON_CODE, "");
}

function roomLink() {
  return `${location.origin}/#${state.roomId}`;
}

// Reflect the room in the address bar without adding a history entry — and,
// because replaceState (unlike assigning location.hash) does not fire
// hashchange, our own updates never loop back into applyHashToJoinForm.
function setRoomHash(code) {
  history.replaceState(null, "", `#${code}`);
}

function clearRoomHash() {
  history.replaceState(null, "", location.pathname + location.search);
}

// A link opened while in the lobby pre-fills the code and jumps to the password
// so the friend just types the password and hits Enter.
function applyHashToJoinForm() {
  if (state.roomId !== null) return; // already in a room; leave the bar alone
  const code = codeFromHash();
  if (!code) return;
  els.joinRoomId.value = code;
  els.joinRoomPass.focus();
}

// --- events ----------------------------------------------------------------

els.createForm.addEventListener("submit", (e) => {
  e.preventDefault();
  if (!requireConnection()) return;
  state.pendingForm = "create";
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
  if (!requireConnection()) return;
  state.pendingForm = "join";
  clearError();
  send({
    type: "join-room",
    roomId: els.joinRoomId.value.trim().toUpperCase(),
    roomPassword: els.joinRoomPass.value,
  });
});

// Click-to-copy a shareable link to the room (#CODE, no password), with brief
// inline confirmation on the button.
els.copyCodeBtn.addEventListener("click", async () => {
  if (!state.roomId) return;
  try {
    await navigator.clipboard.writeText(roomLink());
    els.copyCodeBtn.textContent = "copiado!";
    els.copyCodeBtn.classList.add("is-copied");
    setTimeout(() => {
      els.copyCodeBtn.textContent = "copiar link";
      els.copyCodeBtn.classList.remove("is-copied");
    }, 1500);
  } catch {
    showToast("Não foi possível copiar o link.", "error");
  }
});

// Leaving is the one deliberate goodbye: the seat is given up, the token with
// it, and the room code leaves the address bar. For a broadcaster the server
// closes the whole room — it only existed for their stream.
els.leaveBtn.addEventListener("click", () => {
  send({ type: "leave-room" });
  forgetSession();
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

els.fullscreenBtn.addEventListener("click", toggleFullscreen);
document.addEventListener("fullscreenchange", onFullscreenChange);
els.statsBtn.addEventListener("click", toggleStats);

els.browserWarningClose.addEventListener("click", () => {
  els.browserWarning.hidden = true;
});

els.volume.addEventListener("input", () => {
  els.video.volume = els.volume.value / 100;
});

// A link pasted into the bar while in the lobby (or the one this page opened
// with) pre-fills the join form.
window.addEventListener("hashchange", applyHashToJoinForm);

// The browser knows before any timer does that there is a point in trying again:
// the network came back, or the tab did (phones and laptops freeze background
// tabs, so the socket is often already dead by the time we are looked at again).
window.addEventListener("online", reconnectNow);
document.addEventListener("visibilitychange", () => {
  if (document.visibilityState === "visible") reconnectNow();
});

// Hidden until we know we are a room's broadcaster.
els.broadcastBtn.hidden = true;

maybeWarnBrowser();
restoreSession(); // a reload can walk straight back into the room
applyHashToJoinForm();
syncStats(); // restore the remembered stats toggle state on the button
connectSignaling();
