// ogweb — side panel logic.
//
// Owns: pairing inputs (persisted to chrome.storage.local as serverOrigin /
// pairToken), the connect/disconnect button (talks to background.js over
// runtime messages), the live status line, and the per-tab allowlist
// (persisted as grantedTabs: [tabId, ...]).
"use strict";

const K_ORIGIN = "serverOrigin";
const K_TOKEN = "pairToken";
const K_GRANTED = "grantedTabs";

const $origin = document.getElementById("origin");
const $token = document.getElementById("token");
const $connect = document.getElementById("connectBtn");
const $dot = document.getElementById("dot");
const $statusText = document.getElementById("statusText");
const $tabs = document.getElementById("tabs");
const $tabsEmpty = document.getElementById("tabsEmpty");
const $ver = document.getElementById("ver");

let granted = new Set();
let statusState = "disconnected";
let statusDetail = "";

const CONNECTING_STATES = new Set(["connected", "connecting", "reconnecting"]);

const RENDER_TABS_DEBOUNCE_MS = 150; // coalesce tabs-event rebuilds (see renderTabs)
let renderTabsTimer = null;

init();

function init() {
  $ver.textContent = chrome.runtime.getManifest().version;

  chrome.storage.local.get([K_ORIGIN, K_TOKEN, K_GRANTED], (rows) => {
    if (chrome.runtime.lastError) return;
    if (rows[K_ORIGIN]) $origin.value = String(rows[K_ORIGIN]);
    if (rows[K_TOKEN]) $token.value = String(rows[K_TOKEN]);
    granted = toIdSet(rows[K_GRANTED]);
    renderTabs();
  });

  chrome.runtime.sendMessage({ type: "get-status" }, (resp) => {
    if (chrome.runtime.lastError || !resp) return;
    applyStatus(resp.state, resp.detail);
  });

  chrome.runtime.onMessage.addListener((msg) => {
    if (msg && msg.type === "status" && msg.state) {
      applyStatus(msg.state, msg.detail);
    }
    // tabs events come straight from the chrome.tabs listeners below
    return false;
  });

  chrome.storage.onChanged.addListener((changes, area) => {
    if (area !== "local") return;
    if (changes[K_GRANTED]) {
      granted = toIdSet(changes[K_GRANTED].newValue);
      renderTabs();
    }
  });

  // live tab list
  chrome.tabs.onCreated.addListener(() => renderTabs());
  chrome.tabs.onRemoved.addListener(() => renderTabs());
  chrome.tabs.onUpdated.addListener((tabId, changeInfo) => {
    if (!changeInfo || changeInfo.url || changeInfo.title || changeInfo.status) renderTabs();
  });

  $connect.addEventListener("click", onConnectClick);
  renderTabs();
}

function onConnectClick() {
  if (CONNECTING_STATES.has(statusState)) {
    chrome.storage.local.set({ bgConnected: false }, () => {
      void chrome.runtime.lastError;
      sendMessage({ type: "disconnect" });
    });
    applyStatus("disconnected", "");
    return;
  }
  const origin = $origin.value.trim().replace(/\/+$/, "");
  const token = $token.value.trim();
  if (!/^https?:\/\//.test(origin)) {
    applyStatus("error", "origin must start with http:// or https://");
    return;
  }
  if (!token) {
    applyStatus("error", "pair token is required");
    return;
  }
  applyStatus("connecting", origin);
  chrome.storage.local.set({ [K_ORIGIN]: origin, [K_TOKEN]: token, bgConnected: true }, () => {
    void chrome.runtime.lastError;
    sendMessage({ type: "connect" });
  });
}

function renderTabs() {
  if (renderTabsTimer) clearTimeout(renderTabsTimer);
  renderTabsTimer = setTimeout(() => {
    renderTabsTimer = null;
    renderTabsNow();
  }, RENDER_TABS_DEBOUNCE_MS);
}

function renderTabsNow() {
  chrome.tabs.query({}, (tabs) => {
    if (chrome.runtime.lastError) return;
    $tabs.textContent = "";
    const open = (tabs || [])
      .filter((t) => typeof t.id === "number")
      .sort((a, b) => (a.windowId - b.windowId) || (a.index - b.index));
    for (const tab of open) $tabs.appendChild(tabRow(tab));
    $tabsEmpty.hidden = open.length > 0;
  });
}

function tabRow(tab) {
  const isGranted = granted.has(tab.id);

  const li = document.createElement("li");
  if (isGranted) li.className = "granted";

  const check = document.createElement("label");
  check.className = "check";
  const box = document.createElement("input");
  box.type = "checkbox";
  box.checked = isGranted;
  box.title = "Allow ogcode to view and drive this tab";
  box.addEventListener("change", () => {
    if (box.checked) granted.add(tab.id);
    else granted.delete(tab.id);
    chrome.storage.local.set({ [K_GRANTED]: [...granted].sort((a, b) => a - b) }, () => {
      void chrome.runtime.lastError;
      renderTabs();
    });
  });
  check.appendChild(box);
  check.appendChild(document.createTextNode(" allow"));

  const meta = document.createElement("div");
  meta.className = "meta";
  const title = document.createElement("div");
  title.className = "title";
  title.textContent = tab.title || tab.url || "(untitled)";
  title.title = title.textContent;
  const url = document.createElement("div");
  url.className = "url";
  url.textContent = tab.url || "";
  url.title = url.textContent;
  meta.appendChild(title);
  meta.appendChild(url);

  li.appendChild(check);
  li.appendChild(meta);
  return li;
}

function applyStatus(state, detail) {
  statusState = String(state || "disconnected");
  statusDetail = String(detail || "");
  $dot.className = "state-" + statusState;
  let text;
  switch (statusState) {
    case "connected": text = "Connected" + (statusDetail ? " — " + statusDetail : ""); break;
    case "connecting": text = "Connecting…" + (statusDetail ? " (" + statusDetail + ")" : ""); break;
    case "reconnecting": text = "Reconnecting" + (statusDetail ? " — " + statusDetail : ""); break;
    case "unconfigured": text = "Enter server origin + pair token" + (statusDetail ? " — " + statusDetail : ""); break;
    case "error": text = "Error: " + (statusDetail || "unknown"); break;
    default: text = "Disconnected"; break;
  }
  $statusText.textContent = text;
  $statusText.title = text;
  $connect.textContent = CONNECTING_STATES.has(statusState) ? "Disconnect" : "Connect";
}

function sendMessage(msg) {
  try {
    chrome.runtime.sendMessage(msg, () => void chrome.runtime.lastError);
  } catch (e) {
    // background worker may be asleep; it will pick settings up on wake
  }
}

function toIdSet(raw) {
  const s = new Set();
  if (Array.isArray(raw)) {
    for (const v of raw) {
      const n = Number(v);
      if (Number.isInteger(n) && n > 0) s.add(n);
    }
  }
  return s;
}