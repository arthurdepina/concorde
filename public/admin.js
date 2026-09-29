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
  accounts: document.getElementById("accounts"),
  accountForm: document.getElementById("account-form"),
  accountName: document.getElementById("account-name"),
  accountsBody: document.getElementById("accounts-body"),
  accountSecret: document.getElementById("account-secret"),
  accountsError: document.getElementById("accounts-error"),
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
  { key: "loginLockouts", label: "Bloqueios de login", alarm: true },
  // P2P outcomes reported by viewers: "sem conexão" is a pair STUN alone could
  // not connect — each one is a case for adding TURN.
  { key: "p2pConnected", label: "P2P conectadas" },
  { key: "p2pFailed", label: "P2P sem conexão (TURN)", alarm: true },
  { key: "p2pDropped", label: "P2P caídas", alarm: true },
  // Sockets that dropped with their seat held, and how that ended. "Retomadas"
  // well below "Quedas de socket" means pages are not making it back — a
  // reconnect that is failing, or a grace period too short.
  { key: "sessionsAway", label: "Quedas de socket" },
  { key: "sessionsResumed", label: "Sessões retomadas" },
  { key: "sessionsExpired", label: "Sessões perdidas" },
  { key: "resumesRejected", label: "Retomadas recusadas" },
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
    if (res.status === 401 || res.status === 429) {
      authHeader = null;
      els.loginError.textContent =
        res.status === 429
          ? "Muitas senhas erradas. Espere alguns minutos e tente de novo."
          : "Usuário ou senha de admin incorretos.";
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
  els.accounts.hidden = false;
  els.dashboard.hidden = false;
}

// --- accounts --------------------------------------------------------------

// Who may open a room. The admin itself comes from the server's environment, so
// it is listed but cannot be edited or deleted here — losing the login that
// manages the accounts would be a bad way to find that out.

els.accountForm.addEventListener("submit", async (e) => {
  e.preventDefault();
  const name = els.accountName.value.trim();
  if (!name) return;
  await accountRequest("POST", { name }, `Conta de ${name} criada.`);
  els.accountName.value = "";
});

async function refreshAccounts() {
  els.accountsError.hidden = true;
  try {
    const res = await authFetch("/admin/accounts");
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    renderAccounts((await res.json()).accounts || []);
  } catch (err) {
    els.accountsError.textContent = "Não foi possível carregar as contas.";
    els.accountsError.hidden = false;
  }
}

function renderAccounts(list) {
  els.accountsBody.innerHTML = "";
  for (const account of list) {
    const tr = document.createElement("tr");

    const name = document.createElement("td");
    name.className = "admin__code";
    name.textContent = account.name;

    const role = document.createElement("td");
    role.textContent = account.builtin
      ? "admin (do .env)"
      : account.role === "admin"
        ? "admin"
        : "transmite";

    const actions = document.createElement("td");
    if (!account.builtin) {
      actions.append(
        accountButton("Nova senha", () =>
          accountRequest("PUT", { name: account.name }, `Nova senha de ${account.name}.`),
        ),
        accountButton("Remover", () => removeAccount(account.name), true),
      );
    }

    tr.append(name, role, actions);
    els.accountsBody.appendChild(tr);
  }
}

function accountButton(label, onClick, danger = false) {
  const btn = document.createElement("button");
  btn.className = danger ? "btn-secondary btn-danger" : "btn-secondary";
  btn.type = "button";
  btn.textContent = label;
  btn.addEventListener("click", () => onClick(btn));
  return btn;
}

function removeAccount(name) {
  if (!confirm(`Remover a conta de ${name}? Ela não poderá mais abrir salas.`)) return;
  return accountRequest("DELETE", { name }, `Conta de ${name} removida.`);
}

// accountRequest runs one change and refreshes the list. A password in the
// answer is shown once — the server keeps only its hash, so this is the only
// chance anyone has to copy it.
async function accountRequest(method, body, done) {
  els.accountsError.hidden = true;
  els.accountSecret.hidden = true;
  try {
    const res = await authFetch("/admin/accounts", {
      method,
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    if (!res.ok) throw new Error(await accountErrorText(res));
    if (res.status !== 204) {
      const data = await res.json();
      if (data.password) showAccountSecret(data.name, data.password, done);
    }
    await refreshAccounts();
  } catch (err) {
    els.accountsError.textContent = err.message;
    els.accountsError.hidden = false;
  }
}

// The server answers a refused change with a stable code; the wording the admin
// reads lives here, like every other message on the site.
const ACCOUNT_ERRORS = {
  "account-not-found": "Essa conta não existe mais.",
  "account-exists": "Já existe uma conta com esse usuário.",
  "account-builtin": "A conta do admin é configurada no servidor (.env), não aqui.",
  "bad-name": "Use de 1 a 32 caracteres: letras sem acento, números, ponto, traço ou _.",
  "short-password": "A senha precisa ter pelo menos 10 caracteres.",
  "long-password": "Essa senha é longa demais (máximo 72 bytes).",
  "save-failed": "Não foi possível salvar o arquivo de contas.",
};

async function accountErrorText(res) {
  try {
    const { code } = await res.json();
    if (ACCOUNT_ERRORS[code]) return ACCOUNT_ERRORS[code];
  } catch {
    // Not our JSON (a proxy error page, say): fall back to the status.
  }
  return `Não foi possível concluir (HTTP ${res.status}).`;
}

function showAccountSecret(name, password, done) {
  els.accountSecret.replaceChildren();
  els.accountSecret.append(
    `${done} Senha (anote agora, não dá para ver de novo): `,
    Object.assign(document.createElement("code"), { textContent: password }),
  );
  els.accountSecret.hidden = false;
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
  await Promise.all([refreshRooms(), refreshMetrics(), refreshAccounts()]);
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

    // Someone whose socket dropped still holds their seat, so say so — a room
    // that looks busy may be a room everyone has been disconnected from.
    const owner = document.createElement("td");
    owner.textContent = room.owner || "—";

    const people = document.createElement("td");
    people.textContent = room.away > 0
      ? `${room.participants} (${room.away} ausente${room.away > 1 ? "s" : ""})`
      : String(room.participants);

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

    tr.append(code, owner, people, live, idle, action);
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
