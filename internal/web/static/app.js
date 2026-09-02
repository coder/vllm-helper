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
  searchForm: $("[data-search-form]"),
  searchInput: $("[data-search-input]"),
  searchBtn: $("[data-search-btn]"),
  searchError: $("[data-search-error]"),
  hits: $("[data-hits]"),
  sheet: $("[data-inspect]"),
  sheetTitle: $("[data-inspect-title]"),
  sheetLink: $("[data-inspect-link]"),
  sheetLoading: $("[data-inspect-loading]"),
  sheetBody: $("[data-inspect-body]"),
  sheetError: $("[data-inspect-error]"),
  verdict: $("[data-verdict]"),
  facts: $("[data-inspect-facts]"),
  flags: $("[data-inspect-flags]"),
  dropped: $("[data-inspect-dropped]"),
  pull: $("[data-pull]"),
  cancelInspect: $("[data-inspect-cancel]"),
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
// After a reload the URL no longer carries it either -- it was stripped from
// the address bar on the first visit -- so ask the server, which will answer
// a request the cookie already authenticated. Without this a reloaded tab
// could watch the machine but not touch it.
let cachedToken = tokenFromURL || "";
async function token() {
  if (!cachedToken) {
    const res = await fetch("/api/token");
    if (!res.ok) {
      throw new Error(
        "This tab is not authenticated. Reopen the URL printed by 'vllm-helper serve'.",
      );
    }
    cachedToken = (await res.json()).token;
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
  document
    .querySelectorAll("[data-remove]")
    .forEach((b) => (b.disabled = !canAct || b.dataset.remove === s.profile));
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
      '<li class="empty">No saved models yet. Find one under <strong>Add model</strong>.</li>';
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

      // Only inactive profiles get this. Removing the running one is
      // refused by the server anyway, and offering a button that cannot
      // work is worse than not offering it.
      const rm = document.createElement("button");
      rm.className = "m-remove";
      rm.textContent = "Remove";
      rm.dataset.remove = p.name;
      rm.addEventListener("click", () => removeProfile(p.name));
      li.append(rm);
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

// Polling cadence. Checking the status reaches the vLLM server, which writes
// a line to its own log for every request it answers, so a tab left open on a
// working machine must not poll at conversational speed -- it drowns the log
// someone is reading to debug something else. Poll quickly only while
// something is actually changing.
const FAST_MS = 2000; // a load or a crash loop: the screen changes second to second
const IDLE_MS = 30000; // steady state: nothing moves here unless someone asks it to

function intervalFor(phase) {
  return phase === "loading" || phase === "failing" ? FAST_MS : IDLE_MS;
}

let timer = null;
let activeProfile = null;

// Chained timeouts rather than an interval, so a slow poll delays the next
// one instead of stacking up behind it.
function schedule(ms) {
  clearTimeout(timer);
  // A hidden tab polls not at all; visibilitychange starts it again.
  if (document.hidden) return;
  timer = setTimeout(refresh, ms);
}

async function refresh() {
  // Assume steady state, so a failure to reach our own server backs off
  // rather than retrying hard against something that is down.
  let phase = "stopped";
  try {
    const [status, machine] = await Promise.all([
      api("/api/status"),
      api("/api/machine"),
    ]);
    phase = status.phase;
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
  schedule(intervalFor(phase));
}

// The log is on its own clock. Reading it costs a journalctl call and
// nothing else -- it never touches the vLLM server -- so it can stay live
// while someone is following it without adding a line to the log they are
// reading.
const LOG_FOLLOW_MS = 2000;
const LOG_IDLE_MS = 15000;

let logTimer = null;

function scheduleLog() {
  clearTimeout(logTimer);
  if (document.hidden) return;
  logTimer = setTimeout(
    logLoop,
    el.follow.checked ? LOG_FOLLOW_MS : LOG_IDLE_MS,
  );
}

async function logLoop() {
  await refreshLog();
  scheduleLog();
}

// Ticking the box should show the tail now, not in fifteen seconds.
el.follow.addEventListener("change", () => {
  if (el.follow.checked) logLoop();
});

// Coming back to the tab should show the truth immediately, not whatever was
// on screen when it was hidden.
document.addEventListener("visibilitychange", () => {
  if (document.hidden) {
    clearTimeout(timer);
    clearTimeout(logTimer);
  } else {
    refresh();
    logLoop();
  }
});

refresh();
logLoop();

// ---------- views ----------

// Two views, no router: the panel is one screen with a second job attached,
// and a URL per view would only mean the token dance had to happen twice.
function showView(name) {
  document
    .querySelectorAll("[data-panel]")
    .forEach((p) => (p.hidden = p.dataset.panel !== name));
  document
    .querySelectorAll(".tab")
    .forEach((t) =>
      t.dataset.view === name
        ? t.setAttribute("aria-current", "page")
        : t.removeAttribute("aria-current"),
    );
  if (name === "discover") el.searchInput.focus();
}

document
  .querySelectorAll(".tab")
  .forEach((t) => t.addEventListener("click", () => showView(t.dataset.view)));

// ---------- search ----------

function fmtCount(n) {
  if (n >= 1e6) return `${(n / 1e6).toFixed(1)}M`;
  if (n >= 1e3) return `${Math.round(n / 1e3)}k`;
  return String(n);
}

function renderHits(results) {
  el.hits.innerHTML = "";
  if (!results.length) {
    el.hits.innerHTML =
      '<li class="empty">No text-generation models matched. Try different terms.</li>';
    return;
  }
  for (const r of results) {
    const li = document.createElement("li");
    li.tabIndex = 0;
    li.setAttribute("role", "button");

    const main = document.createElement("div");
    main.className = "hit-main";
    const id = document.createElement("div");
    id.className = "hit-id";
    id.textContent = r.id;
    const meta = document.createElement("div");
    meta.className = "hit-meta";
    meta.append(
      `${fmtCount(r.downloads)} downloads · ${fmtCount(r.likes)} likes · ${r.license}`,
    );
    if (r.quant_tags?.length) {
      const q = document.createElement("span");
      q.className = "hit-quant";
      q.textContent = `  [${r.quant_tags.join(", ")}]`;
      meta.append(q);
    }
    main.append(id, meta);
    li.append(main);

    const open = () => inspect(r.id);
    li.addEventListener("click", open);
    li.addEventListener("keydown", (e) => {
      if (e.key === "Enter" || e.key === " ") {
        e.preventDefault();
        open();
      }
    });
    el.hits.append(li);
  }
}

el.searchForm.addEventListener("submit", async (e) => {
  e.preventDefault();
  const q = el.searchInput.value.trim();
  if (!q) return;
  el.searchError.hidden = true;
  el.searchBtn.disabled = true;
  el.searchBtn.textContent = "Searching…";
  el.hits.innerHTML = "";
  try {
    const { results } = await api(`/api/search?q=${encodeURIComponent(q)}`);
    renderHits(results);
  } catch (err) {
    el.searchError.textContent = err.message;
    el.searchError.hidden = false;
  } finally {
    el.searchBtn.disabled = false;
    el.searchBtn.textContent = "Search";
  }
});

// ---------- inspect ----------

let inspecting = null;

function fact(dl, term, value) {
  const dt = document.createElement("dt");
  dt.textContent = term;
  const dd = document.createElement("dd");
  dd.textContent = value;
  dl.append(dt, dd);
}

// The verdict repeats the CLI's wording on purpose. Someone who has read
// 'inspect' output in a terminal should not have to learn a second vocabulary
// for the same judgement, and the error direction matters: an over-report is
// an annoyance, an under-report gets a 100 GB download and an OOM.
function renderVerdict(d) {
  const of =
    d.variants > 1 ? `, smallest of ${d.variants} quants in this repo` : "";
  if (d.size_error) {
    el.verdict.dataset.fit = "unknown";
    el.verdict.textContent = `Size could not be measured: ${d.size_error}`;
  } else if (!d.size_gb) {
    el.verdict.dataset.fit = "unknown";
    el.verdict.textContent =
      "No servable weights found — an adapter or projector-only repo, or check the model card.";
  } else if (d.fits_budget === undefined) {
    el.verdict.dataset.fit = "unknown";
    el.verdict.textContent = `${d.size_gb.toFixed(1)} GB — no budget configured on this machine${of}.`;
  } else if (d.fits_budget) {
    el.verdict.dataset.fit = "yes";
    el.verdict.textContent = `${d.size_gb.toFixed(1)} GB — fits under the ~${Math.round(d.budget_gb)} GB budget${of}.`;
  } else {
    el.verdict.dataset.fit = "no";
    el.verdict.textContent = `${d.size_gb.toFixed(1)} GB — TOO BIG for this machine's ~${Math.round(d.budget_gb)} GB budget${of}. You can still pull it, but expect it not to load.`;
  }
}

async function inspect(repo) {
  inspecting = repo;
  el.sheetTitle.textContent = repo;
  el.sheetLink.href = `https://huggingface.co/${repo}`;
  el.sheetLoading.hidden = false;
  el.sheetBody.hidden = true;
  el.sheetError.hidden = true;
  el.pull.disabled = true;
  el.pull.textContent = "Pull and switch";
  el.sheet.showModal();

  try {
    const d = await api(`/api/inspect?repo=${encodeURIComponent(repo)}`);
    if (inspecting !== repo) return; // a newer pick won

    renderVerdict(d);

    el.facts.innerHTML = "";
    fact(el.facts, "license", d.license);
    fact(el.facts, "downloads", fmtCount(d.downloads));
    fact(
      el.facts,
      "quant",
      d.quant_tags?.length ? d.quant_tags.join(", ") : "none detected in tags",
    );
    if (d.detected_max_model_len) {
      fact(
        el.facts,
        "context",
        Number(d.detected_max_model_len).toLocaleString(),
      );
    }

    el.flags.hidden = !d.detected_flags;
    if (d.detected_flags) {
      el.flags.textContent = `Flags auto-detected from the model card, and accepted by your vLLM: ${d.detected_flags}`;
    }
    el.dropped.hidden = !d.dropped_flags?.length;
    if (d.dropped_flags?.length) {
      el.dropped.textContent = d.dropped_flags.join("\n");
    }

    // An existing profile means pulling would be a switch, so say that
    // rather than letting the button imply something new is downloaded.
    el.pull.textContent = d.existing_profile
      ? `Switch to saved '${d.existing_profile}'`
      : "Pull and switch";
    el.pull.disabled = false;
    el.sheetLoading.hidden = true;
    el.sheetBody.hidden = false;
  } catch (err) {
    if (inspecting !== repo) return;
    el.sheetLoading.hidden = true;
    el.sheetError.textContent = err.message;
    el.sheetError.hidden = false;
  }
}

el.cancelInspect.addEventListener("click", () => el.sheet.close());

el.pull.addEventListener("click", async () => {
  const repo = inspecting;
  if (!repo) return;
  if (
    !confirm(
      `Pull ${repo} and switch to it?\n\nThis restarts the server and unloads the current model. vLLM downloads the weights on start, so a large model can take a long time before it serves.`,
    )
  ) {
    return;
  }
  el.pull.disabled = true;
  el.pull.textContent = "Saving…";
  try {
    const res = await api(`/api/pull?repo=${encodeURIComponent(repo)}`, {
      method: "POST",
    });
    el.sheet.close();
    showView("machine");
    const notes = [...(res.notes || []), ...(res.warnings || [])];
    showError(notes.join("\n"));
    activeProfile = null; // force the model list to rebuild with the new one
    refresh();
  } catch (err) {
    el.sheetError.textContent = err.message;
    el.sheetError.hidden = false;
    el.pull.disabled = false;
    el.pull.textContent = "Pull and switch";
  }
});

// ---------- remove ----------

async function removeProfile(name) {
  if (
    !confirm(
      `Delete the saved profile '${name}'?\n\nThis removes its settings and its Coder model entry. The downloaded weights stay in your HuggingFace cache.`,
    )
  ) {
    return;
  }
  try {
    await api(`/api/profiles/${encodeURIComponent(name)}/remove`, {
      method: "POST",
    });
    activeProfile = null;
    refresh();
  } catch (err) {
    showError(err.message);
  }
}
