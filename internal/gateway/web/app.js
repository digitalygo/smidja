const state = {
  csrf: "",
  since: -1,
  lastID: "",
  current: null,
  sessions: [],
  workspaces: [],
  events: null,
};

const els = {
  login: document.getElementById("login"),
  loginForm: document.getElementById("loginForm"),
  loginToken: document.getElementById("loginToken"),
  loginBtn: document.getElementById("loginBtn"),
  app: document.getElementById("app"),
  sessionList: document.getElementById("sessionList"),
  transcript: document.getElementById("transcript"),
  workspace: document.getElementById("workspace"),
  text: document.getElementById("text"),
  sendBtn: document.getElementById("sendBtn"),
  cancelBtn: document.getElementById("cancelBtn"),
  status: document.getElementById("status"),
};

async function fetchJSON(path, opts) {
  const res = await fetch(path, Object.assign({ credentials: "same-origin" }, opts));
  const body = await res.json().catch(() => ({}));
  if (!res.ok) {
    const err = new Error(body.error || ("http " + res.status));
    err.status = res.status;
    throw err;
  }
  return body;
}

function clear(el) {
  while (el.firstChild) {
    el.removeChild(el.firstChild);
  }
}

function setStatus(msg, isErr) {
  els.status.textContent = msg;
  els.status.className = isErr ? "status err" : "status";
}

function truncate(s, n) {
  return s.length > n ? s.slice(0, n) + "..." : s;
}

async function boot() {
  try {
    const r = await fetchJSON("/api/csrf");
    state.csrf = r.csrf || "";
    showApp();
  } catch (e) {
    if (e.status === 401) {
      showLogin();
    }
  }
}

function showLogin() {
  els.login.hidden = false;
  els.app.hidden = true;
}

function showApp() {
  els.login.hidden = true;
  els.app.hidden = false;
  refreshSessions();
  connect();
}

async function login() {
  const token = els.loginToken.value.trim();
  if (!token) {
    return;
  }
  try {
    const r = await fetchJSON("/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ token }),
    });
    state.csrf = r.csrf;
    showApp();
  } catch (e) {
    setStatus("login failed", true);
  }
}

async function refreshSessions() {
  try {
    const r = await fetchJSON("/api/sessions");
    state.sessions = r.sessions || [];
    state.workspaces = r.workspaces || [];
    renderSessions();
    renderWorkspaces();
    const cur = state.sessions.find((s) => s.workspace === state.current?.workspace);
    if (!cur && state.sessions.length > 0) {
      openSession(state.sessions[0]);
    } else if (cur && cur.sessionID) {
      openTranscript(cur.sessionID);
    } else if (!cur) {
      clearTranscript();
    }
  } catch (e) {
    setStatus("failed to load sessions", true);
  }
}

function renderSessions() {
  clear(els.sessionList);
  for (const s of state.sessions) {
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "session";
    btn.textContent = s.workspace + " " + new Date(s.createdAt).toLocaleString();
    btn.addEventListener("click", () => openSession(s));
    els.sessionList.appendChild(btn);
  }
}

function renderWorkspaces() {
  clear(els.workspace);
  for (const name of state.workspaces) {
    const opt = document.createElement("option");
    opt.value = name;
    opt.textContent = name;
    els.workspace.appendChild(opt);
  }
  if (state.current && state.workspaces.includes(state.current.workspace)) {
    els.workspace.value = state.current.workspace;
  }
}

function clearTranscript() {
  clear(els.transcript);
}

async function openSession(rec) {
  state.current = { workspace: rec.workspace, sessionID: rec.sessionID || null };
  els.workspace.value = rec.workspace;
  clearTranscript();
  if (rec.sessionID) {
    await openTranscript(rec.sessionID);
  } else {
    setStatus("no transcript yet for " + rec.workspace);
  }
}

async function openTranscript(id) {
  try {
    const t = await fetchJSON("/api/transcript?id=" + encodeURIComponent(id));
    renderTranscript(t.messages || []);
    state.current = { workspace: state.current?.workspace, sessionID: id };
    setStatus("transcript loaded");
  } catch (e) {
    setStatus("failed to load transcript", true);
  }
}

function renderTranscript(messages) {
  clear(els.transcript);
  for (const m of messages) {
    const row = document.createElement("div");
    row.className = "message " + m.role;
    const who = document.createElement("div");
    who.className = "who";
    who.textContent = m.role;
    const body = document.createElement("div");
    body.className = "body";
    body.textContent = m.text || "";
    row.appendChild(who);
    row.appendChild(body);
    els.transcript.appendChild(row);
  }
}

async function send() {
  const text = els.text.value.trim();
  const workspace = els.workspace.value;
  if (!text || !workspace) {
    return;
  }
  try {
    const r = await fetchJSON("/api/send", {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-CSRF": state.csrf },
      body: JSON.stringify({ workspace, text }),
    });
    state.lastID = r.id;
    els.text.value = "";
    setStatus("queued " + truncate(text, 40));
    refreshSessions();
  } catch (e) {
    setStatus("send failed: " + e.message, true);
  }
}

async function cancel() {
  try {
    await fetchJSON("/api/cancel", {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-CSRF": state.csrf },
      body: JSON.stringify({ id: state.lastID }),
    });
    setStatus("cancel requested");
  } catch (e) {
    setStatus("cancel failed", true);
  }
}

function connect() {
  if (state.events) {
    state.events.close();
  }
  const es = new EventSource("/api/events?since=" + state.since);
  state.events = es;
  es.addEventListener("hello", (e) => {
    const data = JSON.parse(e.data);
    state.since = data.latest || 0;
    setStatus("connected");
  });
  es.addEventListener("delivery", (e) => {
    const ev = JSON.parse(e.data);
    state.since = ev.seq;
    if (ev.sessionID) {
      openTranscript(ev.sessionID);
    }
    if (ev.error) {
      setStatus("delivery failed: " + ev.error, true);
    } else if (ev.result) {
      setStatus("reply: " + truncate(ev.result, 80));
    } else {
      setStatus("delivery " + ev.id);
    }
    refreshSessions();
  });
  es.addEventListener("gap", () => {
    state.since = 0;
    refreshSessions();
  });
  es.onerror = () => {
    setStatus("disconnected, reconnecting");
  };
}

els.loginForm.addEventListener("submit", (e) => e.preventDefault());
els.loginBtn.addEventListener("click", login);
els.loginToken.addEventListener("keydown", (e) => {
  if (e.key === "Enter") {
    login();
  }
});
els.sendBtn.addEventListener("click", send);
els.cancelBtn.addEventListener("click", cancel);
els.text.addEventListener("keydown", (e) => {
  if (e.key === "Enter" && !e.shiftKey) {
    e.preventDefault();
    send();
  }
});

boot();
