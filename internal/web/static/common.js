// Shared by every page: talking to the API, and the chrome that is the same
// everywhere. Page-specific behaviour lives in machine.js / models.js.
"use strict";

const $ = (sel) => document.querySelector(sel);

// The token arrives once in the URL and is exchanged for a cookie by the
// server. Strip it from the address bar so it isn't left in history or
// pasted into a chat along with a screenshot.
const tokenFromURL = new URLSearchParams(location.search).get("token");
if (tokenFromURL) {
  history.replaceState({}, "", location.pathname);
}

// The cookie is HttpOnly, so the page cannot read the token back out of it.
// After a reload -- or a navigation to another page, which is why this
// matters more now -- the URL no longer carries it either, so ask the server,
// which will answer a request the cookie already authenticated.
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

function gb(mb) {
  return `${Math.round(mb / 1024)} GB`;
}

function fmtCount(n) {
  if (n >= 1e6) return `${(n / 1e6).toFixed(1)}M`;
  if (n >= 1e3) return `${Math.round(n / 1e3)}k`;
  return String(n);
}

// Both pages carry the machine's identity in the bar, so it is filled in from
// one place. It is also the cheapest possible check that the page is talking
// to the server it thinks it is.
async function fillChrome() {
  try {
    const s = await api("/api/status");
    const host = $("[data-host]");
    const build = $("[data-build]");
    if (host) host.textContent = s.host || "";
    if (build) build.textContent = s.build || "";
    return s;
  } catch {
    return null;
  }
}
