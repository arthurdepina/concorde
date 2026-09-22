// Admin panel. Lists open rooms and lets the admin close them. Auth is HTTP
// Basic against the admin credentials, sent on every request; nothing is stored
// beyond this tab's lifetime (kept in memory only). The rooms API never returns
// passwords — only what is needed to decide whether to close a room.

const els = {
  loginForm: document.getElementById("login-form"),
  adminUser: document.getElementById("admin-user"),
  adminPass: document.getElementById("admin-pass"),
  loginError: document.getElementById("login-error"),
  dashboard: document.getElementById("dashboard"),
  refreshBtn: document.getElementById("refresh-btn"),
  table: document.getElementById("rooms-table"),
  body: document.getElementById("rooms-body"),
  empty: document.getElementById("empty"),
  dashError: document.getElementById("dash-error"),
  refreshStatus: document.getElementById("refresh-status"),
  metrics: document.getElementById("metrics"),
  uptime: document.getElementById("uptime"),
  statGrid: document.getElementById("stat-grid"),
  metricsError: document.getElementById("metrics-error"),
};

// The metrics tiles, in display order. `alarm` marks counters that should stay
// at zero in a healthy server — any positive value turns the tile red.
const STAT_FIELDS = [
  { key: "activeConnections", label: "Conexões ativas" },
  { key: "activeRooms", label: "Salas ativas" },
  { key: "participants", label: "Participantes" },
  { key: "connectionsOpened", label: "Conexões (total)" },
  { key: "roomsCreated", label: "Salas criadas" },
  { key: "signalsRelayed", label: "Sinais repassados" },
  { key: "errorsSent", label: "Erros enviados" },
  { key: "droppedMessages", label: "Msgs descartadas", alarm: true },
  { key: "acceptFailures", label: "Falhas de conexão", alarm: true },
  { key: "panicsRecovered", label: "Panics contidos", alarm: true },
];

// How often the dashboard re-fetches while open.
const REFRESH_MS = 5000;

let authHeader = null; // "Basic ..." once logged in
let refreshTimer = null;

function authFetch(path, options = {}) {
  return fetch(path, {
    ...options,
    headers: { ...(options.headers || {}), Authorization: authHeader },
  });
}

els.loginForm.addEventListener("submit", async (e) => {
  e.preventDefault();
  els.loginError.hidden = true;

  const user = els.adminUser.value.trim();
  const pass = els.adminPass.value;
  authHeader = "Basic " + btoa(`${user}:${pass}`);

  // A probe request validates the credentials before showing the dashboard.
  try {
    const res = await authFetch("/admin/rooms");
    if (res.status === 401) {
      authHeader = null;
      els.loginError.textContent = "Usuário ou senha de admin incorretos.";
      els.loginError.hidden = false;
      return;
    }
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    showDashboard();
    await refresh();
    startAutoRefresh();
  } catch (err) {
    authHeader = null;
    els.loginError.textContent = "Falha ao conectar ao servidor.";
    els.loginError.hidden = false;
  }
});

els.refreshBtn.addEventListener("click", refresh);

function showDashboard() {
  els.loginForm.hidden = true;
  els.metrics.hidden = false;
  els.dashboard.hidden = false;
}

function startAutoRefresh() {
  clearInterval(refreshTimer);
  refreshTimer = setInterval(refresh, REFRESH_MS);
}

async function refresh() {
  if (!authHeader) return;
  // Rooms and metrics are independent panels; one failing shouldn't blank the
  // other. Each reports its own error and only the rooms fetch drives the
  // shared status pill.
  await Promise.all([refreshRooms(), refreshMetrics()]);
}

async function refreshRooms() {
  els.dashError.hidden = true;
  try {
    const res = await authFetch("/admin/rooms");
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const data = await res.json();
    renderRooms(data.rooms || []);
    setStatus(true);
  } catch (err) {
    setStatus(false);
    els.dashError.textContent = "Não foi possível atualizar as salas.";
    els.dashError.hidden = false;
  }
}

async function refreshMetrics() {
  els.metricsError.hidden = true;
  try {
    const res = await authFetch("/admin/metrics");
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    renderMetrics(await res.json());
  } catch (err) {
    els.metricsError.textContent = "Não foi possível carregar as métricas.";
    els.metricsError.hidden = false;
  }
}

function renderRooms(rooms) {
  els.body.innerHTML = "";
  const has = rooms.length > 0;
  els.table.hidden = !has;
  els.empty.hidden = has;

  for (const room of rooms) {
    const tr = document.createElement("tr");

    const code = document.createElement("td");
    code.className = "admin__code";
    code.textContent = room.id;

    const people = document.createElement("td");
    people.textContent = String(room.participants);

    const live = document.createElement("td");
    live.textContent = room.broadcasting ? "🔴 sim" : "não";

    const idle = document.createElement("td");
    idle.textContent = room.broadcasting ? "—" : formatIdle(room.idleSeconds);

    const action = document.createElement("td");
    const btn = document.createElement("button");
    btn.className = "btn-secondary btn-danger";
    btn.type = "button";
    btn.textContent = "Fechar";
    btn.addEventListener("click", () => closeRoom(room.id, btn));
    action.appendChild(btn);

    tr.append(code, people, live, idle, action);
    els.body.appendChild(tr);
  }
}

async function closeRoom(roomId, btn) {
  btn.disabled = true;
  try {
    const res = await authFetch("/admin/rooms/close", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ roomId }),
    });
    // 404 means it was already gone; either way, refresh to reflect reality.
    if (!res.ok && res.status !== 404) throw new Error(`HTTP ${res.status}`);
    await refresh();
  } catch (err) {
    btn.disabled = false;
    els.dashError.textContent = "Não foi possível fechar a sala.";
    els.dashError.hidden = false;
  }
}

function renderMetrics(snapshot) {
  els.uptime.textContent = "no ar há " + formatUptime(snapshot.uptimeSeconds || 0);

  els.statGrid.innerHTML = "";
  for (const field of STAT_FIELDS) {
    const value = snapshot[field.key] || 0;

    const tile = document.createElement("div");
    tile.className = "stat";
    if (field.alarm && value > 0) tile.classList.add("stat--alarm");

    const label = document.createElement("div");
    label.className = "stat__label";
    label.textContent = field.label;

    const num = document.createElement("div");
    num.className = "stat__value";
    num.textContent = String(value);

    tile.append(label, num);
    els.statGrid.appendChild(tile);
  }
}

function formatUptime(seconds) {
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = seconds % 60;
  if (d > 0) return `${d}d ${h}h ${m}m`;
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m ${s}s`;
  return `${s}s`;
}

function formatIdle(seconds) {
  if (seconds < 60) return `${seconds}s`;
  const m = Math.floor(seconds / 60);
  const s = seconds % 60;
  return `${m}m ${s}s`;
}

function setStatus(ok) {
  els.refreshStatus.textContent = ok ? "atualizado" : "erro";
  els.refreshStatus.classList.toggle("status--on", ok);
  els.refreshStatus.classList.toggle("status--off", !ok);
}
