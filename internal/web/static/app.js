// Control panel for one machine. Polls rather than streams: the events worth
// seeing here are minutes apart, and a poll survives the server restarting
// underneath it without any reconnect logic.
"use strict";

const $ = (sel) => document.querySelector(sel);
const el = {
  host: $("[data-host]"),
  build: $("[data-build]"),
  phase: $("[data-phase-label]"),
  model: $("[data-model]"),
  profile: $("[data-profile]"),
  detail: $("[data-detail]"),
  failure: $("[data-failure]"),
  privilege: $("[data-privilege]"),
  error: $("[data-error]"),
  rail: $("[data-rail]"),
  railUsed: $("[data-rail-used]"),
  railBudget: $("[data-rail-budget]"),
  used: $("[data-used]"),
  budget: $("[data-budget]"),
  total: $("[data-total]"),
  gpu: $("[data-gpu]"),
  models: $("[data-models]"),
  modelCount: $("[data-model-count]"),
  log: $("[data-log]"),
  follow: $("[data-follow]"),
  restart: $("[data-restart]"),
  stop: $("[data-stop]"),
};

// The token arrives once in the URL and is exchanged for a cookie by the
// server. Strip it from the address bar so it isn't left in history or
// pasted into a chat along with a screenshot.
const tokenFromURL = new URLSearchParams(location.search).get("token");
if (tokenFromURL) {
  history.replaceState({}, "", location.pathname);
}

let busy = false;

async function api(path, options = {}) {
  const headers = { ...(options.headers || {}) };
  if (options.method && options.method !== "GET") {
    // Mutations must carry the token in a header. A cookie alone would let
    // any other site POST here with the browser attaching it.
    headers["X-VLLM-Helper-Token"] = await token();
  }
  const res = await fetch(path, { ...options, headers });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new Error(body.error || `${res.status} ${res.statusText}`);
  }
  return body;
}

// The cookie is HttpOnly, so the page cannot read the token back out of it.
// Keep the one from the URL for this session; a reload without it can still
// read (cookie auth) but cannot mutate, which is the safe direction.
let cachedToken = tokenFromURL || "";
async function token() {
  if (!cachedToken) {
    throw new Error(
      "This tab has no token. Reopen the URL printed by 'vllm-helper serve' to make changes.",
    );
  }
  return cachedToken;
}

const PHASES = {
  serving: "Serving",
  loading: "Loading model",
  failing: "Failing to start",
  stopped: "Stopped",
  idle: "No model selected",
};

function gb(mb) {
  return `${Math.round(mb / 1024)} GB`;
}

function showError(message) {
  el.error.textContent = message;
  el.error.hidden = !message;
}

function renderStatus(s) {
  el.host.textContent = s.host || "";
  el.build.textContent = s.build || "";

  el.phase.textContent = PHASES[s.phase] || s.phase;
  el.phase.dataset.state = s.phase;

  el.model.textContent = s.model && s.model !== "unknown" ? s.model : "—";
  el.profile.textContent = s.profile ? `profile ${s.profile}` : "";

  const bits = [`port ${s.port}`];
  if (s.phase === "serving") bits.push(`http ${s.http_status}`);
  if (s.restarts > 0) bits.push(`${s.restarts} restarts`);
  if (s.fail_reason) bits.push(s.fail_reason);
  el.detail.textContent = bits.join(" · ");

  // Only shown when it explains something. A stale "exit-code" on a unit
  // that is running again is noise.
  const failing = s.phase === "failing" && s.last_log;
  el.failure.hidden = !failing;
  if (failing) el.failure.textContent = s.last_log;

  el.privilege.hidden = s.privileged;
  if (!s.privileged) el.privilege.textContent = s.priv_reason;

  const canAct = s.privileged && !busy;
  el.restart.disabled = !canAct;
  el.stop.disabled = !canAct || s.phase === "stopped" || s.phase === "idle";
  document
    .querySelectorAll("[data-switch]")
    .forEach((b) => (b.disabled = !canAct || b.dataset.switch === s.profile));
}

function renderMachine(m) {
  if (!m.total_mem_gb) {
    el.rail.hidden = true;
    return;
  }
  el.rail.hidden = false;

  const totalMB = m.total_mem_gb * 1024;
  const usedMB = Math.max(0, totalMB - m.available_mem_mb);
  const pct = (v) => `${Math.min(100, Math.max(0, (v / totalMB) * 100))}%`;

  el.railUsed.style.width = pct(usedMB);
  el.railBudget.style.left = pct(m.budget_gb * 1024);

  el.used.textContent = gb(usedMB);
  el.budget.textContent = `${m.budget_gb} GB`;
  el.total.textContent = `${m.total_mem_gb} GB`;
  el.gpu.textContent = m.gpu_name || "none detected";
  el.gpu.title = m.budget_reason || "";
}

function renderProfiles(list, activeName) {
  el.modelCount.textContent = list.length ? `(${list.length})` : "";
  if (!list.length) {
    el.models.innerHTML =
      '<li class="empty">No saved models yet. Pull one with <code>vllm-helper new &lt;org/model&gt;</code>.</li>';
    return;
  }
  el.models.innerHTML = "";
  for (const p of list) {
    const li = document.createElement("li");
    li.dataset.active = String(p.active);

    const main = document.createElement("div");
    main.className = "m-main";
    const name = document.createElement("div");
    name.className = "m-name";
    name.textContent = p.model || p.name;
    const meta = document.createElement("div");
    meta.className = "m-meta";
    meta.textContent = [
      p.name,
      p.max_model_len
        ? `${Number(p.max_model_len).toLocaleString()} ctx`
        : null,
      p.gpu_memory_utilization ? `mem ${p.gpu_memory_utilization}` : null,
    ]
      .filter(Boolean)
      .join(" · ");
    main.append(name, meta);
    li.append(main);

    if (p.active) {
      const tag = document.createElement("span");
      tag.className = "tag";
      tag.textContent = "active";
      li.append(tag);
    } else {
      const btn = document.createElement("button");
      btn.className = "btn";
      btn.textContent = "Switch";
      btn.dataset.switch = p.name;
      btn.addEventListener("click", () => switchTo(p.name, p.model || p.name));
      li.append(btn);
    }
    el.models.append(li);
  }
}

async function switchTo(name, label) {
  if (
    !confirm(
      `Switch to ${label}?\n\nThis restarts the server and unloads the current model. Loading the new one can take several minutes.`,
    )
  ) {
    return;
  }
  busy = true;
  showError("");
  try {
    const res = await api(`/api/switch/${encodeURIComponent(name)}`, {
      method: "POST",
    });
    if (res.warnings?.length) showError(res.warnings.join("\n"));
  } catch (e) {
    showError(e.message);
  } finally {
    busy = false;
    refresh();
  }
}

async function act(path, question) {
  if (question && !confirm(question)) return;
  busy = true;
  showError("");
  try {
    await api(path, { method: "POST" });
  } catch (e) {
    showError(e.message);
  } finally {
    busy = false;
    refresh();
  }
}

el.restart.addEventListener("click", () =>
  act(
    "/api/restart",
    "Restart the server? The model reloads, which takes a few minutes.",
  ),
);
el.stop.addEventListener("click", () =>
  act(
    "/api/stop",
    "Stop the server? The endpoint goes down until you start it again.",
  ),
);

async function refreshLog() {
  try {
    const { logs, hint } = await api("/api/logs?n=300");
    const atBottom =
      el.log.scrollTop + el.log.clientHeight >= el.log.scrollHeight - 40;
    el.log.textContent = logs || hint || "No log output yet.";
    if (el.follow.checked && atBottom) el.log.scrollTop = el.log.scrollHeight;
  } catch (e) {
    el.log.textContent = e.message;
  }
}

let activeProfile = null;
async function refresh() {
  try {
    const [status, machine] = await Promise.all([
      api("/api/status"),
      api("/api/machine"),
    ]);
    // Rebuild the list only when the active model changed. Doing it on
    // every poll would fight the pointer mid-click.
    if (status.profile !== activeProfile) {
      activeProfile = status.profile;
      renderProfiles(await api("/api/profiles"), status.profile);
    }
    renderStatus(status);
    renderMachine(machine);
  } catch (e) {
    showError(e.message);
  }
  refreshLog();
}

refresh();
// Two seconds is fast enough to feel live while a model loads, and slow
// enough that a tab left open overnight costs the machine nothing.
setInterval(refresh, 2000);
