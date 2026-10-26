// ogweb — MV3 service worker.
//
// Connects to the ogcode server's browser bridge over WebSocket and executes
// takeover ops (navigate / snapshot / content / click / type / key / scroll /
// screenshot) on tabs the user has explicitly granted in the side panel.
//
// Wire contract: PROTOCOL.md — this file and the Go server mirror agree on it.
// Defensive posture: nothing thrown here may kill the ws loop; every op is
// answered with a result frame, always.
"use strict";

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------
const PROTOCOL_VERSION = 1;
const EXT_VERSION = chrome.runtime.getManifest().version;

const HEARTBEAT_MS = 15000;        // {"type":"ping"} cadence while connected
const BACKOFF_MIN_MS = 1000;       // reconnect backoff start
const BACKOFF_MAX_MS = 30000;      // reconnect backoff cap
const ALARM_RECONNECT = "ogweb-reconnect"; // SW-suspend fallback during backoff (single stable name)
const ALARM_RECONNECT_MINUTES = 1; // repeating reconnect-alarm cadence (~60s)
const HELLO_DEBOUNCE_MS = 300;     // coalesce tabs-changed hellos
const LOAD_TIMEOUT_MS = 12000;     // how long navigate waits for the load
const TEXT_CAP = 20000;            // content: body.innerText cap
const ELEMENT_TEXT_CAP = 120;      // snapshot: per-element text cap
const ELEMENT_CAP = 300;           // snapshot: max elements per page
const SELECTOR_CAP = 200;          // snapshot: selector string cap
const SNAPSHOT_JSON_CAP = 50000;   // snapshot: target size for the JSON payload
const SCREENSHOT_EDGE_MAX = 1024;  // screenshot: delivered image long edge
const SCREENSHOT_JPEG_QUALITY = 0.8; // screenshot: JPEG encoding quality
const CAPTURE_MAX = 300;           // passive capture: entries kept per tab, per ring
const NET_URL_CAP = 500;           // passive capture: stored request url cap
const CONSOLE_TEXT_CAP = 2000;     // passive capture: console/exception text cap
const BODY_TEXT_CAP = 65536;       // network body fetch: returned character cap
const STALE_MSG = "snapshot stale — run snapshot again";
const NOT_GRANTED_MSG = "is not granted for takeover";

// chrome.storage.local keys (documented in README.md)
const K_ORIGIN = "serverOrigin";   // e.g. "http://localhost:7801"
const K_TOKEN = "pairToken";       // hex token from the ogcode pair URL
const K_GRANTED = "grantedTabs";   // array of granted tab ids
const K_CONNECTED = "bgConnected"; // true after the user pressed Connect

const NATIVE_HOST_NAME = "com.ogcode"; // native messaging host installed with the ogcode app
const NATIVE_PAIR_TIMEOUT_MS = 5000;   // native pair attempt: one try, at most this long

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------
let ws = null;                     // active WebSocket or null
let wantConnected = false;         // user intent (survives transient drops)
let backoffMs = BACKOFF_MIN_MS;
let reconnectTimer = null;
let connecting = false;  // in-flight connect construction (openSocket guard)
let connectEpoch = 0;    // bumped by disconnect(): async pair attempts from older intents are void
let pingTimer = null;
let helloTimer = null;
let status = { state: "disconnected", detail: "" };

let grantedIds = new Set();        // tab ids the user allow-listed
const tabsCache = new Map();       // tabId -> {url, title} for hello bookkeeping
const snapshots = new Map();       // tabId -> {refToSel: Map, refMeta: Map, url, at}
const tabLocks = new Map();        // tabId -> promise tail, serialises debugger ops
const watchedTabs = new Set();     // tabIds holding a persistent capture debugger session
const attachAttempts = new Map();  // tabId -> unsettled attaches WE started (disambiguates "already attached": ours vs DevTools)
const netRings = new Map();        // tabId -> capped network request entries (see capture)
const consoleRings = new Map();    // tabId -> capped console entries (see capture)

// ---------------------------------------------------------------------------
// Small utilities
// ---------------------------------------------------------------------------
function asMsg(e) {
  if (!e) return "unknown error";
  if (typeof e === "string") return e;
  if (e.message) return String(e.message);
  return String(e);
}

function safeSnippet(s, n = 120) {
  try {
    const t = String(s).replace(/\s+/g, " ").trim();
    return t.length > n ? t.slice(0, n) + "…" : t;
  } catch (e) { return "?"; }
}

function toIdSet(raw) {
  const out = new Set();
  if (Array.isArray(raw)) {
    for (const v of raw) {
      const n = Number(v);
      if (Number.isInteger(n) && n > 0) out.add(n);
    }
  }
  return out;
}

// ---------------------------------------------------------------------------
// chrome.* promise wrappers (explicit callbacks so runtime.lastError is read
// exactly once — promise support for chrome.debugger is not assumed)
// ---------------------------------------------------------------------------
function storageGet(keys) {
  return new Promise((resolve) => chrome.storage.local.get(keys, (rows) => resolve(rows || {})));
}

function tabsQuery(query) {
  return new Promise((resolve, reject) => {
    chrome.tabs.query(query || {}, (tabs) => {
      if (chrome.runtime.lastError) reject(new Error(chrome.runtime.lastError.message));
      else resolve(tabs || []);
    });
  });
}

function tabsGet(tabId) {
  return new Promise((resolve, reject) => {
    chrome.tabs.get(tabId, (tab) => {
      const err = chrome.runtime.lastError;
      if (err || !tab) reject(new Error("tab " + tabId + " no longer exists"));
      else resolve(tab);
    });
  });
}

function tabsCreate(props) {
  return new Promise((resolve, reject) => {
    chrome.tabs.create(props, (tab) => {
      const err = chrome.runtime.lastError;
      if (err || !tab) reject(new Error("could not create tab: " + (err ? err.message : "no result")));
      else resolve(tab);
    });
  });
}

function tabsUpdate(tabId, props) {
  return new Promise((resolve, reject) => {
    chrome.tabs.update(tabId, props, (tab) => {
      const err = chrome.runtime.lastError;
      if (err) reject(new Error("could not update tab " + tabId + ": " + err.message));
      else resolve(tab);
    });
  });
}

// ---------------------------------------------------------------------------
// Status + side panel messaging
// ---------------------------------------------------------------------------
function setStatus(state, detail) {
  status = { state: state, detail: detail || "" };
  let msg = null;
  try { msg = { type: "status", state: state, detail: status.detail, version: EXT_VERSION }; } catch (e) { msg = null; }
  if (!msg) return;
  try {
    const p = chrome.runtime.sendMessage(msg);
    if (p && typeof p.catch === "function") p.catch(() => {}); // no receiver open: fine
  } catch (e) { /* side panel closed: fine */ }
}

// ---------------------------------------------------------------------------
// Native auto-pair (paste-free fallback). When no credentials are stored, the
// extension asks the ogcode native messaging host (com.ogcode, installed with
// the ogcode app) for a fresh pair token once, stores it exactly like the
// manual paste path does, and lets the regular connect machinery take over.
// Any failure (host missing, error reply, no reachable server, timeout) falls
// back to the manual paste flow: no retry, no loop, and stored settings are
// never overwritten.
// ---------------------------------------------------------------------------

// ws://127.0.0.1:7801/api/browser/bridge?token=x -> http://127.0.0.1:7801 —
// the same shape the manual paste path stores: scheme swapped, host kept as-is.
function originFromWsUrl(url) {
  const m = /^(wss?):\/\/([^/?#]+)/i.exec(String(url || ""));
  if (!m) return null;
  return (m[1].toLowerCase() === "wss" ? "https:" : "http:") + "//" + m[2];
}

// One native messaging round trip: connect, send, await the first reply, then
// disconnect from the port. Bounded by NATIVE_PAIR_TIMEOUT_MS so a hung host
// cannot block the connect loop; chrome.runtime.lastError is read exactly
// once (onDisconnect), like every other chrome.* wrapper above.
function nativePairRequest(request) {
  return new Promise((resolve, reject) => {
    let port = null;
    let timer = null;
    let done = false;
    const finish = (err, reply) => {
      if (done) return;
      done = true;
      if (timer) { clearTimeout(timer); timer = null; }
      if (port) { try { port.disconnect(); } catch (e) { /* already gone */ } port = null; }
      if (err) reject(err); else resolve(reply);
    };
    timer = setTimeout(() => {
      finish(new Error("ogcode native host did not answer within " + Math.round(NATIVE_PAIR_TIMEOUT_MS / 1000) + "s"));
    }, NATIVE_PAIR_TIMEOUT_MS);
    try { port = chrome.runtime.connectNative(NATIVE_HOST_NAME); } catch (e) {
      finish(new Error("ogcode native host is not available: " + asMsg(e)));
      return;
    }
    port.onMessage.addListener((reply) => finish(null, reply));
    port.onDisconnect.addListener(() => {
      const err = chrome.runtime.lastError;
      finish(new Error("ogcode native host closed without a reply" + (err ? ": " + err.message : "")));
    });
    try { port.postMessage(request); } catch (e) {
      finish(new Error("could not send the pair request: " + asMsg(e)));
    }
  });
}

// The host is asked at most once per service-worker life: the attempt is
// memoised as a promise, so later triggers reuse the settled result and ones
// overlapping the live attempt share it instead of launching the host again.
let nativePairPromise = null;

function autoPairViaNativeHost() {
  if (nativePairPromise) return nativePairPromise;
  nativePairPromise = (async () => {
    let reply;
    try {
      reply = await nativePairRequest({ type: "pair" });
    } catch (e) {
      return { ok: false, error: asMsg(e) };
    }
    if (!reply || typeof reply !== "object" || typeof reply.ok !== "boolean") {
      return { ok: false, error: "ogcode native host sent an unrecognised pair reply" };
    }
    if (reply.ok !== true) {
      return { ok: false, error: reply.error ? safeSnippet(reply.error) : "ogcode native host refused the pair request" };
    }
    const servers = Array.isArray(reply.servers) ? reply.servers : [];
    if (servers.length === 0) {
      return { ok: false, error: "no running ogcode server found" };
    }
    const server = servers[0];
    if (!server || typeof server.token !== "string" || !server.token) {
      return { ok: false, error: "ogcode native host offered no pair token" };
    }
    const origin = originFromWsUrl(server.url);
    if (!origin) {
      return { ok: false, error: "ogcode native host sent an unusable bridge url" };
    }
    return { ok: true, origin: origin, token: server.token };
  })();
  return nativePairPromise;
}

// Paste-free pairing driver: used at worker start (nothing stored) and from
// openSocket when a connect intent finds the stored credentials missing. On
// success it writes the same keys a manual Connect press writes and re-enters
// connect(); on any failure it settles into the manual paste flow untouched.
async function autoPairThenConnect() {
  const epoch = connectEpoch; // a Disconnect after this point must void the attempt
  const first = nativePairPromise === null;
  if (first) {
    setStatus("connecting", "via the ogcode app"); // the one native attempt is in flight
  }
  const pair = await autoPairViaNativeHost();
  if (connectEpoch !== epoch) {
    // The user disconnected while the pair attempt was in flight: the
    // completed attempt must not rewrite K_CONNECTED or reopen a socket.
    return;
  }
  if (!pair.ok) {
    console.warn("[ogcode] native auto-pair unavailable:", pair.error);
    wantConnected = false;
    setStatus("unconfigured", "no ogcode app — paste token to pair");
    return;
  }
  chrome.storage.local.set({ [K_ORIGIN]: pair.origin, [K_TOKEN]: pair.token, [K_CONNECTED]: true }, () => {
    void chrome.runtime.lastError;
    if (connectEpoch !== epoch) return; // a Disconnect landed during the write
    connect(); // like a manual Connect press: settings land, socket opens
  });
}

// ---------------------------------------------------------------------------
// WebSocket lifecycle
// ---------------------------------------------------------------------------
function buildWsUrl(origin, token) {
  let u;
  try { u = new URL(origin); } catch (e) { return null; }
  if (u.protocol !== "http:" && u.protocol !== "https:") return null;
  // host kept as-is (spec): the substring between the scheme and the first /?#.
  // URL.parse canonicalises it (dropping :443), so copy it verbatim instead.
  const m = /^[a-z][a-z\d+.-]*:\/\/([^/?#]+)/i.exec(String(origin));
  const host = m && m[1] ? m[1] : u.host;
  if (!host) return null;
  const scheme = u.protocol === "https:" ? "wss:" : "ws:";
  const query = token ? "?token=" + encodeURIComponent(token) : "";
  return scheme + "//" + host + "/api/browser/bridge" + query;
}

function connect() {
  wantConnected = true;
  if (ws && (ws.readyState === 0 || ws.readyState === 1)) return;
  clearTimeout(reconnectTimer);
  reconnectTimer = null;
  void openSocket();
}

function disconnect() {
  wantConnected = false;
  connectEpoch++; // async pair attempts begun before this must not resurrect the bridge
  syncConnectAlarm(); // nothing is pending on the connect front any more
  clearTimeout(reconnectTimer);
  reconnectTimer = null;
  const sock = ws;
  ws = null;
  stopHeartbeat();
  if (sock && (sock.readyState === 0 || sock.readyState === 1)) {
    try { sock.close(); } catch (e) { /* already gone */ }
  }
  if (status.state !== "unconfigured") setStatus("disconnected", "");
}

// Auto (re)connect after a service worker restart if the user had connected;
// with nothing stored, offer a paste-free pair via the native messaging host.
async function selfHealConnect() {
  try {
    const rows = await storageGet([K_ORIGIN, K_TOKEN, K_CONNECTED]);
    if (rows[K_ORIGIN] && rows[K_TOKEN] && rows[K_CONNECTED]) {
      if (!ws || (ws.readyState !== 0 && ws.readyState !== 1)) connect();
    } else if (!rows[K_ORIGIN] || !rows[K_TOKEN]) {
      void autoPairThenConnect();
    }
  } catch (e) { /* settings not readable: stay put */ }
}

async function openSocket() {
  if (ws && (ws.readyState === 0 || ws.readyState === 1)) return;
  if (!wantConnected) return;
  if (connecting) {
    // Another construction is already in flight (boot, onStartup, a racing
    // storage change, the reconnect timer): it owns the socket — building a
    // second one would have this call's socket overwrite `ws` while the
    // first leaks open.
    return;
  }
  connecting = true;
  try {
    const rows = await storageGet([K_ORIGIN, K_TOKEN]);
    // A concurrent connect (e.g. the storage-change listener and the panel's
    // connect message) resolves its settings read first; lose that race quietly
    // instead of opening a second socket that would leak. The same read window
    // can also carry the user's Disconnect: intent is re-checked after every
    // await on this path.
    if (ws && (ws.readyState === 0 || ws.readyState === 1)) return;
    if (!wantConnected) return;
    const origin = rows[K_ORIGIN] || "";
    const token = rows[K_TOKEN] || "";
    if (!origin || !token) {
      // No (complete) stored credentials: one native auto-pair attempt, then the
      // manual paste flow — autoPairThenConnect settles the status either way.
      await autoPairThenConnect();
      return;
    }
    const url = buildWsUrl(origin, token);
    if (!url) {
      wantConnected = false;
      setStatus("error", "invalid server origin: " + origin);
      return;
    }

    setStatus("connecting", origin);
    let sock;
    try { sock = new WebSocket(url); } catch (e) {
      scheduleReconnect("socket open failed: " + asMsg(e));
      return;
    }
    ws = sock;

    sock.onopen = () => {
      if (ws !== sock || !wantConnected) {
        // A Disconnect was requested while the open was in flight (or another
        // construction replaced this socket): never mark connected over it.
        if (ws === sock) ws = null;
        try { sock.close(); } catch (closeErr) { /* already going */ }
        syncConnectAlarm();
        return;
      }
      backoffMs = BACKOFF_MIN_MS;
      startHeartbeat();
      setStatus("connected", origin);
      syncConnectAlarm(); // the open socket keeps the worker alive: drop the alarm
      void sendHelloNow();
    };
    sock.onmessage = (ev) => onWsMessage(ev);
    sock.onerror = () => { /* onclose follows */ };
    sock.onclose = () => {
      if (ws === sock) ws = null;
      stopHeartbeat();
      if (wantConnected) scheduleReconnect("connection lost");
      else if (status.state !== "unconfigured") setStatus("disconnected", "");
    };
  } finally {
    connecting = false;
  }
}

function scheduleReconnect(reason) {
  if (!wantConnected) return;
  if (reconnectTimer) return;
  const delay = computeBackoff(backoffMs);
  backoffMs = Math.min(backoffMs * 2, BACKOFF_MAX_MS);
  setStatus("reconnecting", reason + " — retrying in " + Math.round(delay / 1000) + "s");
  reconnectTimer = setTimeout(() => { reconnectTimer = null; void openSocket(); }, delay);
  syncConnectAlarm(); // no open socket keeps this worker alive: the alarm takes over
}

// Exponential backoff capped at 30s with ±20% jitter.
function computeBackoff(base) {
  const j = base * (0.8 + Math.random() * 0.4);
  return Math.min(Math.max(Math.round(j), 250), BACKOFF_MAX_MS);
}

// ---------------------------------------------------------------------------
// chrome.alarms fallback for the suspended service worker
// ---------------------------------------------------------------------------
// During backoff there is no open socket to keep the worker alive, and the
// escalated backoff (30s cap + jitter -> up to ~36s quiet) meets the ~30s idle
// window Chrome uses to suspend the worker: the pending reconnect timer dies
// with it and reconnect silently stops until an unrelated event wakes the
// worker. A repeating alarm bridges exactly that gap while a socket is missing
// under wantConnected; its handler re-enters the same openSocket() path the
// backoff timer uses. The listener is registered TOP-LEVEL: MV3 delivers
// events into a freshly evaluated worker after each suspend. One stable name —
// re-creating replaces, never stacks.
function syncConnectAlarm() {
  const pending = wantConnected && !(ws && (ws.readyState === 0 || ws.readyState === 1));
  try {
    if (pending) {
      chrome.alarms.create(ALARM_RECONNECT,
        { delayInMinutes: ALARM_RECONNECT_MINUTES, periodInMinutes: ALARM_RECONNECT_MINUTES },
        () => { void chrome.runtime.lastError; });
    } else {
      chrome.alarms.clear(ALARM_RECONNECT, () => { void chrome.runtime.lastError; });
    }
  } catch (e) { /* no alarms API: the reconnect timers alone still drive the loop */ }
}

chrome.alarms.onAlarm.addListener((alarm) => {
  if (!alarm || alarm.name !== ALARM_RECONNECT) return;
  try {
    if (ws && (ws.readyState === 0 || ws.readyState === 1)) return; // a socket is already up or coming up
    // A suspended worker that just woke starts with fresh module state
    // (wantConnected false): selfHealConnect re-reads the persisted intent
    // and funnels into the same openSocket() the backoff timer uses.
    void selfHealConnect();
  } catch (e) {
    console.warn("[ogcode] reconnect alarm handling error:", asMsg(e));
  }
});

function startHeartbeat() {
  stopHeartbeat();
  pingTimer = setInterval(() => { sendJson({ type: "ping" }); }, HEARTBEAT_MS);
}

function stopHeartbeat() {
  if (pingTimer) { clearInterval(pingTimer); pingTimer = null; }
}

function sendJson(obj) {
  const sock = ws;
  if (!sock || sock.readyState !== 1) return false;
  try {
    sock.send(JSON.stringify(obj));
    return true;
  } catch (e) {
    try { sock.close(); } catch (e2) { /* already closing */ }
    return false;
  }
}

// ---------------------------------------------------------------------------
// Server messages
// ---------------------------------------------------------------------------
function onWsMessage(ev) {
  if (typeof ev.data !== "string") return; // binary frames are ignored
  let msg;
  try { msg = JSON.parse(ev.data); } catch (e) {
    console.warn("[ogcode] bridge sent a non-JSON frame; ignored:", safeSnippet(ev.data));
    return;
  }
  try {
    if (msg && msg.type === "pong") return; // heartbeat ack — nothing to do
    if (msg && typeof msg.id === "string" && typeof msg.op === "string") {
      void runOp(msg);
      return;
    }
    console.warn("[ogcode] unrecognized bridge frame; ignored:", safeSnippet(ev.data));
  } catch (e) {
    console.warn("[ogcode] message handling error:", asMsg(e));
  }
}

async function runOp(op) {
  const id = op.id;
  try {
    const data = await executeOp(op);
    sendResult(id, true, data);
  } catch (e) {
    sendResult(id, false, undefined, asMsg(e));
  }
}

function sendResult(id, ok, data, error) {
  const msg = ok ? { type: "result", id: id, ok: true } : { type: "result", id: id, ok: false, error: error || "unknown error" };
  if (ok && data !== undefined) msg.data = data;
  if (!sendJson(msg)) {
    console.warn("[ogcode] could not deliver result for", id, "— socket not open");
  }
}

// ---------------------------------------------------------------------------
// Op gate + dispatch. executeOp is the only entry: validate -> dispatch a
// tab-creating open -> grant check -> tab-exists check -> serialise per tab
// -> run. A navigate with newTab creates its own tab, so it needs no granted
// tab up front — the extension grants the tab it creates (grantTab).
// ---------------------------------------------------------------------------
async function executeOp(op) {
  if (!op || typeof op !== "object") throw new Error("malformed op frame");
  const args = op.args && typeof op.args === "object" ? op.args : {};
  const run = OP_HANDLERS[op.op];
  if (!run) throw new Error("unknown op: " + JSON.stringify(op.op));
  const tab = op.tab;
  if (op.op === "navigate" && args.newTab === true && typeof tab !== "number") {
    return run(0, args, null); // acts on no existing tab
  }
  if (typeof tab !== "number" || !Number.isInteger(tab) || tab <= 0) {
    throw new Error("op is missing a numeric tab id");
  }
  if (!grantedIds.has(tab)) {
    throw new Error("tab " + tab + " " + NOT_GRANTED_MSG);
  }
  const tabInfo = await tabsGet(tab); // re-verify the tab still exists
  await ensureWatch(tab); // passive capture: arm the watch on a granted tab's first op
  return withTabLock(tab, () => run(tab, args, tabInfo));
}

function withTabLock(tabId, fn) {
  const tail = tabLocks.get(tabId) || Promise.resolve();
  const next = tail.then(fn, fn); // run even if the previous op rejected
  tabLocks.set(tabId, next.catch(() => {}));
  return next;
}

// ---------------------------------------------------------------------------
// chrome.debugger helpers
// ---------------------------------------------------------------------------
function dbgTarget(tabId) { return { tabId: tabId }; }

function dbgErrText(tabId, raw) {
  const msg = String(raw || "unknown debugger error");
  if (/another debugger is already attached/i.test(msg)) {
    return "cannot attach debugger to tab " + tabId + ": another debugger is already attached " +
      "(close DevTools on that tab or detach the other debugger, then retry)";
  }
  if (/not attached|detach/i.test(msg)) {
    return "debugger detached from tab " + tabId + " (the debugging banner was dismissed) — retry the op";
  }
  return "debugger error on tab " + tabId + ": " + msg;
}

function dbgAttach(tabId) {
  return new Promise((resolve, reject) => {
    chrome.debugger.attach(dbgTarget(tabId), "1.3", () => {
      const err = chrome.runtime.lastError;
      if (err) reject(new Error(dbgErrText(tabId, err.message)));
      else resolve();
    });
  });
}

function dbgDetach(tabId) {
  return new Promise((resolve) => {
    chrome.debugger.detach(dbgTarget(tabId), () => {
      void chrome.runtime.lastError; // best effort
      resolve();
    });
  });
}

function dbgSend(tabId, method, params) {
  return new Promise((resolve, reject) => {
    chrome.debugger.sendCommand(dbgTarget(tabId), method, params || {}, (res) => {
      const err = chrome.runtime.lastError;
      if (err) reject(new Error(dbgErrText(tabId, err.message)));
      else resolve(res);
    });
  });
}

async function withDebugger(tabId, fn) {
  if (watchedTabs.has(tabId)) return await fn(); // reuse the persistent capture session
  await dbgAttach(tabId);
  try {
    return await fn();
  } finally {
    await dbgDetach(tabId);
  }
}

// ---------------------------------------------------------------------------
// Passive capture. The first op on a granted tab arms one debugger session
// that STAYS attached (Network.enable + Runtime.enable + Log.enable) and its
// traffic collects into per-tab rings the `network` and `console` ops read
// back. Only granted tabs collect; an ungrant, a closed tab, or a dismissed
// debug banner drops the session, and a later op re-arms it via ensureWatch.
// ---------------------------------------------------------------------------
async function ensureWatch(tabId) {
  if (watchedTabs.has(tabId)) return;
  let attached = false;
  const attemptsOf = () => attachAttempts.get(tabId) || 0;
  attachAttempts.set(tabId, attemptsOf() + 1); // marker: this attach attempt is ours
  try {
    await dbgAttach(tabId);
    attached = true;
    await dbgSend(tabId, "Network.enable", {});
    await dbgSend(tabId, "Runtime.enable", {});
    await dbgSend(tabId, "Log.enable", {});
    watchedTabs.add(tabId);
  } catch (e) {
    // "already attached" is ambiguous — an open DevTools errors the same way.
    // Take the "leftover from us" branch only while ANOTHER attach attempt of
    // ours is in flight (racing ensureWatch calls: the winner owns the
    // session) — then it is genuinely ours and good enough to collect.
    if (/already attached/i.test(asMsg(e)) && attemptsOf() > 1) {
      watchedTabs.add(tabId);
      return;
    }
    // Any other "already attached" is a foreign client (DevTools): we must not
    // track the tab — the op's next real command fails on it and surfaces the
    // dbgErrText remedy (close DevTools / detach the other debugger).
    if (attached) void dbgDetach(tabId); // half-armed session is worse than none
    // capture could not start: leave unwatched — the driving op carries on
  } finally {
    // the marker lives for this attach attempt only: clear it as it settles
    const left = attemptsOf() - 1;
    if (left > 0) attachAttempts.set(tabId, left);
    else attachAttempts.delete(tabId);
  }
}

// Drop a tab's capture state and detach the persistent session (best effort).
function dropWatch(tabId) {
  watchedTabs.delete(tabId);
  attachAttempts.delete(tabId); // attempts cannot outlive a watch they belong to
  netRings.delete(tabId);
  consoleRings.delete(tabId);
  void dbgDetach(tabId);
}

// Release every watch whose tab left the granted set (side-panel tick on the
// K_GRANTED storage write). Re-granting re-arms naturally via ensureWatch.
function pruneWatches() {
  for (const tabId of [...watchedTabs]) {
    if (!grantedIds.has(tabId)) dropWatch(tabId);
  }
}

// CDP timestamps are epoch seconds — *1000 keeps the ISO output on real dates.
// Network entries record the wall clock instead (startedAt).
function cdpIso(ts) {
  const n = typeof ts === "number" && isFinite(ts) ? Math.round(ts * 1000) : Date.now();
  return new Date(n).toISOString();
}

// RemoteObject -> text for the console ring. Primitives stringify; objects
// prefer their description, then the preview properties. Never throws.
function dbgValueText(v) {
  try {
    if (v === null || v === undefined) return String(v);
    const t = typeof v;
    if (t === "string" || t === "number" || t === "boolean") return String(v);
    // RemoteObjects wrap primitives: console.log("hi") arrives as {type:"string",value:"hi"}
    if (typeof v.value === "string" || typeof v.value === "number" || typeof v.value === "boolean") return String(v.value);
    if (typeof v.description === "string" && v.description) return v.description;
    if (v.preview && Array.isArray(v.preview.objectProperties)) {
      return v.preview.objectProperties.map((p) => {
        try {
          const name = p && p.name !== undefined ? String(p.name) : "?";
          const value = p && p.value !== undefined ? String(p.value) : "";
          return name + ": " + value;
        } catch (e2) { return "?"; }
      }).join(", ");
    }
    return String(v);
  } catch (e) { return "?"; }
}

function dbgArgsText(args) {
  try {
    const list = Array.isArray(args) ? args : [];
    return list.map(dbgValueText).join(" ");
  } catch (e) { return "?"; }
}

function netRingFor(tabId) {
  let ring = netRings.get(tabId);
  if (!ring) { ring = []; netRings.set(tabId, ring); }
  return ring;
}

function netEntryById(tabId, requestId) {
  const ring = netRings.get(tabId);
  if (!ring) return null;
  for (let i = ring.length - 1; i >= 0; i--) {
    if (ring[i].id === requestId) return ring[i];
  }
  return null;
}

function pushConsoleEntry(tabId, entry) {
  let ring = consoleRings.get(tabId);
  if (!ring) { ring = []; consoleRings.set(tabId, ring); }
  ring.push(entry);
  while (ring.length > CAPTURE_MAX) ring.shift();
}

// One CDP event -> one ring update. Reachable only for watched, granted tabs.
function captureEvent(tabId, method, params) {
  if (method === "Network.requestWillBeSent") {
    const ring = netRingFor(tabId);
    let entry = netEntryById(tabId, params.requestId);
    if (!entry) {
      entry = { id: params.requestId };
      ring.push(entry);
      while (ring.length > CAPTURE_MAX) ring.shift();
    }
    if (params.redirectResponse && entry) {
      // the same requestId rides each redirect hop
      const rr = params.redirectResponse;
      if (rr.status !== undefined) entry.status = rr.status;
      if (rr.statusText !== undefined) entry.statusText = rr.statusText;
      if (rr.mimeType !== undefined) entry.mimeType = rr.mimeType;
    }
    entry.url = String(params.url || "").slice(0, NET_URL_CAP);
    entry.method = params.request && params.request.method;
    entry.type = params.type;
    entry.startedAt = new Date().toISOString();
    entry._t0 = params.timestamp;
    return;
  }
  if (method === "Network.responseReceived" || method === "Network.loadingFinished" ||
      method === "Network.loadingFailed") {
    const entry = netEntryById(tabId, params.requestId);
    if (!entry) return;
    if (method === "Network.responseReceived") {
      const r = params.response || {};
      if (r.status !== undefined) entry.status = r.status;
      if (r.statusText !== undefined) entry.statusText = r.statusText;
      if (r.mimeType !== undefined) entry.mimeType = r.mimeType;
      if (r.resourceSize !== undefined) entry.resourceSize = r.resourceSize;
      if (r.fromDiskCache !== undefined) entry.fromCache = r.fromDiskCache;
      return;
    }
    if (typeof entry._t0 === "number") {
      entry.durationMs = Math.max(0, Math.round((params.timestamp - entry._t0) * 1000));
    }
    if (method === "Network.loadingFinished") {
      if (params.encodedDataLength !== undefined) entry.encodedSize = params.encodedDataLength;
      return;
    }
    entry.failed = String(params.errorText || "") + (params.canceled ? " (canceled)" : "");
    return;
  }
  if (method === "Runtime.consoleAPICalled") {
    const cf = params.stackTrace && params.stackTrace.callFrames && params.stackTrace.callFrames[0];
    pushConsoleEntry(tabId, {
      at: cdpIso(params.timestamp),
      level: params.type,
      source: "console",
      text: dbgArgsText(params.args).slice(0, CONSOLE_TEXT_CAP),
      url: cf && cf.url,
      line: cf && cf.lineNumber,
    });
    return;
  }
  if (method === "Runtime.exceptionThrown") {
    const d = params.exceptionDetails || {};
    const ex = d.exception;
    const top = d.stackTrace && d.stackTrace.callFrames && d.stackTrace.callFrames[0];
    pushConsoleEntry(tabId, {
      at: cdpIso(params.timestamp),
      level: "error",
      source: "exception",
      text: String((ex && ex.description) || d.text || "").slice(0, CONSOLE_TEXT_CAP),
      url: d.url !== undefined ? d.url : top && top.url,
      line: d.lineNumber !== undefined ? d.lineNumber : top && top.lineNumber,
    });
    return;
  }
  if (method === "Log.entryAdded") {
    const le = params.entry || {};
    pushConsoleEntry(tabId, {
      at: new Date().toISOString(),
      level: le.level,
      source: "browser-log",
      text: String((le.source || "") + ": " + (le.text || "")).slice(0, CONSOLE_TEXT_CAP),
      url: le.url,
      line: le.lineNumber,
    });
    return;
  }
}

// Event collector: capture errors never throw.
chrome.debugger.onEvent.addListener((source, method, params) => {
  try {
    if (!source || typeof source.tabId !== "number") return;
    const tabId = source.tabId;
    if (!watchedTabs.has(tabId) || !grantedIds.has(tabId)) return;
    captureEvent(tabId, String(method || ""), params || {});
  } catch (e) { /* capture errors never throw */ }
});

// The debug banner was dismissed (or the session died another way): the watch
// is gone — drop the state so the next op re-attaches fresh instead of
// reusing a dead session.
chrome.debugger.onDetach.addListener((source) => {
  try {
    if (!source || typeof source.tabId !== "number") return;
    if (watchedTabs.has(source.tabId)) dropWatch(source.tabId);
  } catch (e) { /* capture errors never throw */ }
});

async function evalIn(tabId, expression) {
  const res = await dbgSend(tabId, "Runtime.evaluate", {
    expression: expression,
    returnByValue: true,
    awaitPromise: true,
    userGesture: true,
  });
  if (res && res.exceptionDetails) {
    const d = res.exceptionDetails;
    const where = d.exception && (d.exception.description || d.exception.value);
    throw new Error("page evaluation failed: " + String(where || d.text || "exception"));
  }
  const v = res && res.result && res.result.value;
  return v === undefined ? null : v;
}

async function mouseClick(tabId, x, y) {
  await dbgSend(tabId, "Input.dispatchMouseEvent", { type: "mousePressed", x: x, y: y, button: "left", clickCount: 1 });
  await dbgSend(tabId, "Input.dispatchMouseEvent", { type: "mouseReleased", x: x, y: y, button: "left", clickCount: 1 });
}

const KEY_DEFS = {
  "Enter":     { key: "Enter",     code: "Enter",     vk: 13, text: "\r" },
  "Tab":       { key: "Tab",       code: "Tab",       vk: 9 },
  "Escape":    { key: "Escape",    code: "Escape",    vk: 27 },
  "ArrowDown": { key: "ArrowDown", code: "ArrowDown", vk: 40 },
  "ArrowUp":   { key: "ArrowUp",   code: "ArrowUp",   vk: 38 },
  "Backspace": { key: "Backspace", code: "Backspace", vk: 8,  text: "\b" },
};

async function pressKey(tabId, named) {
  const def = KEY_DEFS[named];
  if (!def) {
    throw new Error("unsupported key: " + named +
      " (supported: Enter, Tab, Escape, ArrowDown, ArrowUp, Backspace)");
  }
  const down = {
    type: def.text ? "keyDown" : "rawKeyDown",
    key: def.key,
    code: def.code,
    modifiers: 0,
    windowsVirtualKeyCode: def.vk,
    nativeVirtualKeyCode: def.vk,
  };
  if (def.text) down.text = def.text;
  await dbgSend(tabId, "Input.dispatchKeyEvent", down);
  await dbgSend(tabId, "Input.dispatchKeyEvent", {
    type: "keyUp",
    key: def.key,
    code: def.code,
    modifiers: 0,
    windowsVirtualKeyCode: def.vk,
    nativeVirtualKeyCode: def.vk,
  });
}

// ---------------------------------------------------------------------------
// In-page expressions (run via Runtime.evaluate, returnByValue)
//
// Snapshot returns a JSON STRING so the size cap can be enforced in-page.
// Selectors are embedded via JSON.stringify(sel) — always a valid JS literal.
//
// Q_SOURCE is the shared in-page selector resolver: it supports " >> "
// (space-angle-angle-space) chains that cross open shadow roots — the first
// segment is queried in the document, each later segment inside the previous
// element's shadowRoot; a missing (e.g. closed) shadowRoot means not found.
// ---------------------------------------------------------------------------
const Q_SOURCE =
  "function q(sel) {" +
  "  const segs = String(sel).split(' >> ');" +
  "  let node = document.querySelector(segs[0]);" +
  "  for (let i = 1; node && i < segs.length; i++) {" +
  "    const sr = node.shadowRoot;" +
  "    if (!sr) return null;" +
  "    node = sr.querySelector(segs[i]);" +
  "  }" +
  "  return node || null;" +
  "}";

function snapshotExpression(query, limit) {
  return "(() => {" +
    "const MAXEL = " + ELEMENT_CAP + ", MAXTEXT = " + ELEMENT_TEXT_CAP + ", SELCAP = " + SELECTOR_CAP + ", JSONCAP = " + SNAPSHOT_JSON_CAP + ";" +
    // QN: lowercase query filter ('' = no filter); LIM: max elements after
    // filtering (0 = no limit) — both applied IN-PAGE so the wire payload is
    // genuinely smaller. refs keep their unfiltered DOM-order numbering.
    "const QN = " + JSON.stringify(typeof query === "string" ? query.toLowerCase() : "") + ";" +
    "const LIM = " + (Number.isInteger(limit) && limit > 0 ? limit : 0) + ";" +
    Q_SOURCE +
    "const SNAP_Q = 'a[href],button,input,select,textarea,[onclick]," +
      "[tabindex]:not([tabindex=\"-1\]),[aria-labelledby]," +
      "[role=\"button\"],[role=\"link\"],[role=\"tab\"],[role=\"checkbox\"],[role=\"radio\"]," +
      "[contenteditable=\"true\"],[contenteditable=\"\"]';" +
    "const OUT = { url: String(location.href), title: document.title || ''," +
    "  viewport: { w: window.innerWidth, h: window.innerHeight }, elements: [], truncated: false };" +
    "if (QN) OUT.totalMatching = 0;" +
    "let refN = 0, matched = 0, droppedByQuery = false, droppedByLimit = false;" +
    "function matchesQuery(item) {" +
    "  const fields = [item.tag, item.text, item.placeholder, item.href, item.value];" +
    "  for (let i = 0; i < fields.length; i++) {" +
    "    const f = fields[i];" +
    "    if (f && String(f).toLowerCase().indexOf(QN) !== -1) return true;" +
    "  }" +
    "  return QN === (item.checked ? 'checked' : 'unchecked');" +
    "}" +
    "function cap(s, n) { if (s === null || s === undefined) return '';" +
    "  s = String(s); return s.length > n ? s.slice(0, n) : s; }" +
    "function norm(s) { s = s === null || s === undefined ? '' : String(s);" +
    "  return s.trim().replace(/\\s+/g, ' '); }" +
    "function chainIn(n) {" +
    "  const segs = [];" +
    "  let cur = n;" +
    "  for (let i = 0; cur && cur.nodeType === 1 && i < 15; i++) {" +
    "    if (cur.id) { try { const s = '#' + CSS.escape(cur.id);" +
    "      if (q(s) === cur) { segs.unshift(s); return segs.join(' > '); } } catch (e) {} }" +
    "    let name = cur.tagName.toLowerCase();" +
    "    if (name === 'html' || name === 'body') { segs.unshift(name); return segs.join(' > '); }" +
    "    const p = cur.parentElement;" +
    "    if (p) {" +
    "      let idx = 1, count = 0;" +
    "      for (let j = 0; j < p.children.length; j++) {" +
    "        if (p.children[j].tagName === cur.tagName) { count++; if (p.children[j] === cur) idx = count; }" +
    "      }" +
    "      if (count > 1) name += ':nth-of-type(' + idx + ')';" +
    "    }" +
    "    segs.unshift(name);" +
    "    if (!p) return segs.join(' > ');" +
    "    cur = p;" +
    "  }" +
    "  return segs.join(' > ');" +
    "}" +
    "function selOf(el) {" +
    "  if (el.id) { try { const s = '#' + CSS.escape(el.id);" +
    "    if (q(s) === el) return s.slice(0, SELCAP); } catch (e) {} }" +
    "  const segs = [];" +
    "  let node = el;" +
    "  while (node && node.nodeType === 1) {" +
    "    segs.unshift(chainIn(node));" +
    "    const root = node.getRootNode ? node.getRootNode() : null;" +
    "    const host = root && root.host ? root.host : null;" +
    "    if (!host) break;" +
    "    node = host;" +
    "  }" +
    "  return segs.join(' >> ').slice(0, SELCAP);" +
    "}" +
    "function buildItem(el) {" +
    "  let r = null; try { r = el.getBoundingClientRect(); } catch (e) { return null; }" +
    "  if (!r || (r.width <= 0 && r.height <= 0)) return null;" +
    "  let st = null; try { st = getComputedStyle(el); } catch (e) {}" +
    "  if (st && (st.display === 'none' || st.visibility === 'hidden')) return null;" +
    "  const tagName = el.tagName.toLowerCase();" +
    "  const role = (el.getAttribute && el.getAttribute('role')) || '';" +
    "  const editable = !!el.isContentEditable;" +
    "  const baseTags = ['a', 'button', 'input', 'select', 'textarea'];" +
    "  const tag = baseTags.indexOf(tagName) !== -1 ? tagName : (role || (editable ? 'contenteditable' : tagName));" +
    "  const aria = (el.getAttribute && el.getAttribute('aria-label')) || '';" +
    "  let text = '';" +
    "  if (tagName === 'input') text = aria || ((el.labels && el.labels[0] && el.labels[0].innerText) || '');" +
    "  else text = (el.innerText || aria || '');" +
    "  text = norm(text);" +
    "  if (!text) {" +
    "    const lb = (el.getAttribute && el.getAttribute('aria-labelledby')) || '';" +
    "    if (lb) {" +
    "      let lab = '';" +
    "      for (const idv of lb.split(/\\s+/)) {" +
    "        if (!idv) continue;" +
    "        const lbl = document.getElementById(idv);" +
    "        if (lbl) lab += (lab ? ' ' : '') + norm(lbl.textContent);" +
    "      }" +
    "      if (lab) text = lab;" +
    "    }" +
    "  }" +
    "  const item = { ref: 0, tag: tag, tn: tagName, text: cap(text, MAXTEXT), selector: '', checked: false };" +
    "  if (typeof el.checked === 'boolean') item.checked = el.checked;" +
    "  else if (role === 'checkbox' || role === 'radio' || role === 'switch') item.checked = String(el.getAttribute('aria-checked')) === 'true';" +
    "  if (tagName === 'a') item.href = cap(el.href, 300);" +
    "  if (tagName === 'textarea' || editable) item.value = cap(editable ? (el.innerText || '') : el.value, 300);" +
    "  else if (tagName === 'input' && el.type !== 'checkbox' && el.type !== 'radio') item.value = cap(el.value, 300);" +
    "  const ph = (el.getAttribute && el.getAttribute('placeholder')) || '';" +
    "  if (ph) item.placeholder = cap(ph, 300);" +
    "  if (el.disabled === true || (el.getAttribute && el.getAttribute('aria-disabled')) === 'true') item.disabled = true;" +
    "  return item;" +
    "}" +
    "function collect(root, depth) {" +
    "  let list;" +
    "  try { list = root.querySelectorAll(SNAP_Q); } catch (e) { return; }" +
    "  for (const el of list) {" +
    "    const item = buildItem(el);" +
    "    if (!item) continue;" +
    "    item.ref = ++refN;" +
    "    if (QN) {" +
    "      if (!matchesQuery(item)) { droppedByQuery = true; continue; }" +
    "      matched++;" +
    "      OUT.totalMatching = matched;" +
    "    }" +
    "    if (LIM && OUT.elements.length >= LIM) { droppedByLimit = true; continue; }" +
    "    if (OUT.elements.length >= MAXEL) { OUT.truncated = true; return; }" +
    "    OUT.elements.push(item);" +
    "    if (depth < 6 && el.shadowRoot) collect(el.shadowRoot, depth + 1);" +
    "  }" +
    "}" +
    "collect(document, 0);" +
    "if (droppedByQuery || droppedByLimit) OUT.truncated = true;" +
    "let s = JSON.stringify(OUT);" +
    // Over the cap: binary-search the keep-count. The old trim popped one
    // element and re-serialized the WHOLE payload per iteration (worst case
    // ~290 serializations of a ~50KB string). Probes serialize only the
    // candidate keep-count; the delivered shape is serialized once. Output
    // contract unchanged: valid JSON, within JSONCAP (never trimmed below 10
    // elements even when the floor itself overflows), truncated when any
    // element was dropped.
    "if (s.length > JSONCAP && OUT.elements.length > 10) {" +
    "  OUT.truncated = true;" + /* the final shape always drops elements: set before probing so probe sizes match the delivered one */
    "  let lo = 10, hi = OUT.elements.length;" +
    "  while (hi - lo > 1) {" +
    "    const mid = (lo + hi) >> 1;" +
    "    const probe = Object.assign({}, OUT);" +
    "    probe.elements = OUT.elements.slice(0, mid);" +
    "    if (JSON.stringify(probe).length <= JSONCAP) { lo = mid; } else { hi = mid; }" +
    "  }" +
    "  OUT.elements.length = lo;" +
    "  s = JSON.stringify(OUT);" +
    "}" +
    "return s;" +
  "})()";
}

function contentExpression() {
  return "(() => {" +
    "const T = " + TEXT_CAP + ";" +
    "const body = document.body;" +
    "const text = body ? String(body.innerText || '') : '';" +
    "return { url: String(location.href), title: document.title || '', text: text.length > T ? text.slice(0, T) : text };" +
  "})()";
}

// ONE evaluation per click/type: resolve (shadow-aware), drift-gate against
// the snapshot's ref metadata, scroll into view, clamp the dispatch point into
// the element's own box and then the viewport, and hit-test that point.
// expectTag/expectText are the ref metadata stored with the snapshot; empty
// strings (direct-selector ops) skip the gate. Returns the dispatch point and
// the URL/title/text baseline taken before the click.
function aimExpression(sel, expectTag, expectText) {
  return "(() => {" +
    Q_SOURCE +
    "function norm(s) { s = s === null || s === undefined ? '' : String(s);" +
    "  return s.trim().replace(/\\s+/g, ' '); }" +
    "function cap8(s) { return norm(s).slice(0, 80); }" +
    "const el = q(" + JSON.stringify(sel) + ");" +
    "if (!el) return { found: false };" +
    "const etag = " + JSON.stringify(expectTag || "") + ";" +
    "const etext = " + JSON.stringify(expectText || "") + ";" +
    "if (etag && el.tagName.toLowerCase() !== etag) return {" +
    "  found: false, drift: true, tag: el.tagName.toLowerCase()," +
    "  text: cap8(el.innerText || el.textContent || '') };" +
    "try { el.scrollIntoView({ block: 'center', inline: 'nearest', behavior: 'instant' }); } catch (e) {}" +
    "const r = el.getBoundingClientRect();" +
    "if (!r || (r.width <= 0 && r.height <= 0)) return { found: false };" +
    "if (r.right < 0 || r.bottom < 0 || r.left > window.innerWidth || r.top > window.innerHeight) return { found: false, offscreen: true };" +
    "let x = r.left + r.width / 2;" +
    "let y = r.top + r.height / 2;" +
    "x = Math.min(Math.max(x, r.left), r.right);" +
    "y = Math.min(Math.max(y, r.top), r.bottom);" +
    "x = Math.max(1, Math.min(window.innerWidth - 1, x));" +
    "y = Math.max(1, Math.min(window.innerHeight - 1, y));" +
    "x = Math.round(x);" +
    "y = Math.round(y);" +
    "const hit = document.elementFromPoint(x, y);" +
    "if (hit && hit !== el && !el.contains(hit) && !hit.contains(el)) return {" +
    "  found: true, x: x, y: y, blocked: true," +
    "  hitTag: hit.tagName ? hit.tagName.toLowerCase() : ''," +
    "  hitText: cap8(hit.innerText || '') };" +
    "return { found: true, x: x, y: y," +
    "  url: String(location.href), title: document.title || ''," +
    "  tag: el.tagName.toLowerCase(), text: cap8(el.innerText || el.textContent || '') };" +
  "})()";
}

// Post-click read-back: URL/title after the click. In-page try/catch because
// the click itself may trigger a navigation that tears the context down.
function postClickExpression() {
  return "(() => { try { return { url: String(location.href), title: document.title || '' }; } catch (e) { return null; } })()";
}

// Type read-back: what the field holds after Input.insertText (value for
// input/textarea, innerText for contenteditable), capped 300.
function readBackExpression(sel) {
  return "(() => {" +
    Q_SOURCE +
    "const el = q(" + JSON.stringify(sel) + ");" +
    "if (!el) return { value: '' };" +
    "let v = '';" +
    "try {" +
    "  if (el.isContentEditable) v = el.innerText || '';" +
    "  else if (el.tagName === 'TEXTAREA' || el.tagName === 'INPUT') v = el.value || '';" +
    "  else v = el.innerText || '';" +
    "} catch (e) {}" +
    "v = String(v);" +
    "return { value: v.length > 300 ? v.slice(0, 300) : v };" +
  "})()";
}

function prepareTypeExpression(sel) {
  return "(() => {" +
    Q_SOURCE +
    "const el = q(" + JSON.stringify(sel) + ");" +
    "if (!el) return { found: false };" +
    "const editable = !!el.isContentEditable;" +
    "if (!editable && (el.disabled || el.readOnly)) return { found: false, disabled: true };" +
    "const isTextarea = el.tagName === 'TEXTAREA';" +
    "const isInput = el.tagName === 'INPUT';" +
    "let cleared = false;" +
    "if ((isInput || isTextarea) && !editable && !el.disabled && !el.readOnly && el.value) {" +
    "  const proto = isTextarea ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;" +
    "  const d = Object.getOwnPropertyDescriptor(proto, 'value');" +
    "  if (d && d.set) d.set.call(el, ''); else el.value = '';" +
    "  el.dispatchEvent(new Event('input', { bubbles: true }));" +
    "  cleared = true;" +
    "}" +
    "el.focus();" +
    "return { found: true, tag: el.tagName.toLowerCase(), editable: editable, cleared: cleared };" +
  "})()";
}

function viewportCenterExpression() {
  return "(() => ({ x: Math.round(window.innerWidth / 2), y: Math.round(window.innerHeight / 2) }))()";
}

// ---------------------------------------------------------------------------
// Op implementations. Each receives (tabId, args, tabInfo) and returns the
// result `data` object, or throws an Error whose message becomes `error`.
// ---------------------------------------------------------------------------
// grantTab marks a tab the extension itself created as agent-controlled: the
// same bookkeeping as a side-panel tick, persisted so the grant survives a
// service-worker restart, with a hello scheduled so the server's status
// shows the tab. The storage.onChanged listener below re-derives grantedIds
// from the written value; this context receives that event too, so the set
// stays identical.
function grantTab(tabId) {
  grantedIds.add(tabId);
  chrome.storage.local.set(
    { [K_GRANTED]: [...grantedIds].sort((a, b) => a - b) },
    () => { void chrome.runtime.lastError; }
  );
  scheduleHello(); // granted flags changed -> tell the server
}

async function opNavigate(tab, args) {
  const url = requireString(args.url, "navigate requires args.url");
  let parsed;
  try { parsed = new URL(url); } catch (e) { throw new Error("invalid url: " + url); }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
    throw new Error("only http(s) navigation is allowed");
  }
  if (args.newTab === true) {
    if (typeof tab === "number" && tab > 0) {
      throw new Error("specify tab or newTab, not both");
    }
    const created = await tabsCreate({ url: url });
    grantTab(created.id); // the tab ogcode opened is ogcode's to drive
    snapshots.delete(created.id);
    await ensureWatch(created.id); // capture starts with the created tab's first load
    await waitForTabLoaded(created.id, LOAD_TIMEOUT_MS);
    const fin = await tabsGet(created.id);
    return { tabId: created.id, url: (fin && fin.url) || url, title: (fin && fin.title) || "" };
  }
  await ensureWatch(tab); // armed before the navigation so this load is captured
  await tabsUpdate(tab, { url: url });
  snapshots.delete(tab);
  await waitForTabLoaded(tab, LOAD_TIMEOUT_MS);
  const fin = await tabsGet(tab);
  return { tabId: tab, url: (fin && fin.url) || url, title: (fin && fin.title) || "" };
}

async function opSnapshot(tab, args) {
  const a = args || {};
  const query = typeof a.query === "string" ? a.query : "";
  const limit = Number.isInteger(a.limit) && a.limit > 0 ? a.limit : 0;
  return withDebugger(tab, async () => {
    const raw = await evalIn(tab, snapshotExpression(query, limit));
    let parsed;
    if (typeof raw === "string") {
      try { parsed = JSON.parse(raw); } catch (e) { throw new Error("snapshot script returned invalid JSON"); }
    } else if (raw && typeof raw === "object") {
      parsed = raw; // defensive: object result works too
    } else {
      throw new Error("snapshot script returned no data");
    }
    const refToSel = new Map();
    const refMeta = new Map();
    for (const el of (parsed.elements || [])) {
      if (el && typeof el.ref === "number" && typeof el.selector === "string" && el.selector) {
        refToSel.set(el.ref, el.selector);
        refMeta.set(el.ref, {
          tag: typeof el.tn === "string" ? el.tn : "",
          text: typeof el.text === "string" ? String(el.text).slice(0, 80) : "",
        });
      }
    }
    snapshots.set(tab, { refToSel: refToSel, refMeta: refMeta, url: parsed.url || "", at: Date.now() });
    return parsed;
  });
}

async function opContent(tab) {
  return withDebugger(tab, async () => {
    const v = await evalIn(tab, contentExpression());
    return {
      url: (v && v.url) || "",
      title: (v && v.title) || "",
      text: typeof (v && v.text) === "string" ? v.text : "",
    };
  });
}

// args {ref} | {selector} -> selector string, plus `meta` for refs (the
// snapshot's stored tag/text for that ref; null for direct selectors) so ops
// can drift-gate against what the snapshot saw. Throws STALE_MSG when a ref
// cannot be resolved to an element.
function resolveSelector(tabId, args) {
  const direct = typeof args.selector === "string" && args.selector ? args.selector : null;
  if (direct) return { selector: direct, fromRef: false, meta: null };
  const ref = args.ref;
  if (typeof ref !== "number" && !(typeof ref === "string" && /^\d+$/.test(ref))) {
    throw new Error("op args must include ref or selector");
  }
  const snap = snapshots.get(tabId);
  if (!snap || !snap.refToSel) throw new Error(STALE_MSG);
  const num = typeof ref === "string" ? Number(ref) : ref;
  const sel = snap.refToSel.get(num);
  if (!sel) throw new Error(STALE_MSG);
  const meta = (snap.refMeta && snap.refMeta.get(num)) || null;
  return { selector: sel, fromRef: true, meta: meta };
}

// When the op used a ref from a snapshot, refuse if the page has navigated
// since (the ref map may point somewhere else now).
async function assertSnapshotFresh(tabId, fromRef) {
  if (!fromRef) return;
  const snap = snapshots.get(tabId);
  if (!snap || !snap.url) throw new Error(STALE_MSG);
  const t = await tabsGet(tabId);
  if (t && t.url && t.url !== snap.url) throw new Error(STALE_MSG);
}

function resolveMiss(resolution) {
  return resolution.fromRef ? STALE_MSG : "no element matched selector: " + resolution.selector;
}

// One aim eval per click/type op: resolve + drift-gate + dispatch point +
// hit-test. Throws on ref drift (stale), an unmatched selector, a fully
// off-viewport element, or an occluded point; returns the aim payload.
async function aimPoint(tab, resolution) {
  const expectTag = resolution.meta && resolution.meta.tag ? resolution.meta.tag : "";
  const expectText = resolution.meta && resolution.meta.text ? resolution.meta.text : "";
  const aim = await evalIn(tab, aimExpression(resolution.selector, expectTag, expectText));
  if (!aim || aim.found !== true) {
    if (aim && aim.drift) {
      throw new Error(STALE_MSG + " (ref now points at " + String(aim.tag || "?") +
        " \"" + String(aim.text || "").slice(0, 60) + "\")");
    }
    if (aim && aim.offscreen) {
      throw new Error("element is completely outside the viewport: " + resolution.selector);
    }
    throw new Error(resolveMiss(resolution));
  }
  if (aim.blocked) {
    throw new Error("click point blocked by overlay: " + String(aim.hitTag || "?") +
      " \"" + String(aim.hitText || "") + "\"");
  }
  return aim;
}

async function opClick(tab, args) {
  const r = resolveSelector(tab, args);
  await assertSnapshotFresh(tab, r.fromRef);
  return withDebugger(tab, async () => {
    const aim = await aimPoint(tab, r);
    await mouseClick(tab, aim.x, aim.y);
    let post = null;
    try { post = await evalIn(tab, postClickExpression()); } catch (e) { post = null; }
    const preUrl = aim.url || "";
    const preTitle = aim.title || "";
    return {
      tabId: tab,
      x: aim.x,
      y: aim.y,
      url: (post && post.url) || preUrl,
      title: (post && post.title) || preTitle,
      changed: !post || (post.url || "") !== preUrl || (post.title || "") !== preTitle,
    };
  });
}

async function opType(tab, args) {
  const text = args.text === null || args.text === undefined ? "" : String(args.text);
  const submit = args.submit === true;
  const r = resolveSelector(tab, args);
  await assertSnapshotFresh(tab, r.fromRef);
  return withDebugger(tab, async () => {
    const aim = await aimPoint(tab, r);
    await mouseClick(tab, aim.x, aim.y); // focus the element (spec)
    const prep = await evalIn(tab, prepareTypeExpression(r.selector));
    if (!prep || prep.found !== true) {
      if (prep && prep.disabled) {
        throw new Error("element is disabled or read-only: " + r.selector);
      }
      throw new Error(resolveMiss(r));
    }
    if (text) await dbgSend(tab, "Input.insertText", { text: text });
    const rb = await evalIn(tab, readBackExpression(r.selector));
    const value = rb && typeof rb.value === "string" ? rb.value : "";
    if (submit) await pressKey(tab, "Enter");
    return { tabId: tab, typed: text.length, submit: submit, cleared: prep.cleared === true, value: value };
  });
}

async function opKey(tab, args) {
  const key = requireString(args.key, "key requires args.key");
  return withDebugger(tab, async () => {
    await pressKey(tab, key);
    return { tabId: tab, key: key };
  });
}

async function opScroll(tab, args) {
  let direction = typeof args.direction === "string" ? args.direction.toLowerCase() : "down";
  if (direction !== "up" && direction !== "down") direction = "down";
  let amount = typeof args.amount === "number" && isFinite(args.amount) && args.amount > 0 ? args.amount : 600;
  amount = Math.min(Math.round(amount), 30000);
  return withDebugger(tab, async () => {
    const center = await evalIn(tab, viewportCenterExpression());
    const x = (center && center.x) || 300;
    const y = (center && center.y) || 300;
    await dbgSend(tab, "Input.dispatchMouseEvent", {
      type: "mouseWheel",
      x: x,
      y: y,
      deltaX: 0,
      deltaY: direction === "up" ? -amount : amount,
    });
    return { tabId: tab, direction: direction, amount: amount };
  });
}

// Re-encode a CDP PNG capture for the model: downscale so the long edge is
// SCREENSHOT_EDGE_MAX (keep 1:1 when already within it) and encode JPEG at
// SCREENSHOT_JPEG_QUALITY. Runs on worker-only APIs (Blob, createImageBitmap,
// OffscreenCanvas) — the PNG capture is only ever an intermediate.
async function encodeScreenshotJpeg(pngB64) {
  const bin = atob(pngB64);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  const bmp = await createImageBitmap(new Blob([bytes], { type: "image/png" }));
  try {
    const long = Math.max(bmp.width, bmp.height);
    const scale = long > SCREENSHOT_EDGE_MAX ? SCREENSHOT_EDGE_MAX / long : 1;
    const w = Math.max(1, Math.round(bmp.width * scale));
    const h = Math.max(1, Math.round(bmp.height * scale));
    const canvas = new OffscreenCanvas(w, h);
    canvas.getContext("2d").drawImage(bmp, 0, 0, w, h);
    const blob = await canvas.convertToBlob({ type: "image/jpeg", quality: SCREENSHOT_JPEG_QUALITY });
    const out = new Uint8Array(await blob.arrayBuffer());
    const parts = [];
    for (let i = 0; i < out.length; i += 0x8000) {
      parts.push(String.fromCharCode.apply(null, out.subarray(i, Math.min(out.length, i + 0x8000))));
    }
    return { base64: btoa(parts.join("")), width: w, height: h };
  } finally {
    if (bmp.close) bmp.close();
  }
}

async function opScreenshot(tab, args) {
  const format = typeof args.format === "string" && args.format ? args.format : "png";
  if (format !== "png") throw new Error("unsupported screenshot format: " + format + " (only png)");
  return withDebugger(tab, async () => {
    const res = await dbgSend(tab, "Page.captureScreenshot", { format: "png" });
    const b64 = res && typeof res.data === "string" ? res.data : "";
    if (!b64) throw new Error("screenshot returned no image data");
    const jpeg = await encodeScreenshotJpeg(b64);
    return { base64: jpeg.base64, format: "image/jpeg", width: jpeg.width, height: jpeg.height };
  });
}

// ---------------------------------------------------------------------------
// Capture read-back ops: read-only over the rings, except that a requestId
// body fetch goes over the debugger (serialised by executeOp's tab lock like
// every other op).
// ---------------------------------------------------------------------------
const NET_ENTRY_FIELDS = ["id", "url", "method", "type", "startedAt", "status", "statusText",
  "mimeType", "resourceSize", "encodedSize", "durationMs", "fromCache", "failed"];
const CONSOLE_ENTRY_FIELDS = ["at", "level", "source", "text", "url", "line"];

function entryOut(fields, e) {
  const out = {};
  for (const k of fields) {
    if (e[k] !== undefined) out[k] = e[k];
  }
  return out;
}

// default 100, hard clamp 1..CAPTURE_MAX
function boundedMax(raw) {
  const n = Number(raw);
  const v = Number.isFinite(n) ? Math.round(n) : 100;
  return Math.min(CAPTURE_MAX, Math.max(1, v));
}

async function opNetwork(tab, args) {
  const max = boundedMax(args.max);
  const filter = typeof args.url === "string" && args.url ? args.url.toLowerCase() : null;
  if (args.requestId !== undefined && args.requestId !== null && args.requestId !== "") {
    const reqId = String(args.requestId);
    const entry = netEntryById(tab, reqId);
    if (!entry) {
      throw new Error("no captured request " + reqId + " in tab " + tab +
        " — capture may not have been running");
    }
    const res = await dbgSend(tab, "Network.getResponseBody", { requestId: reqId });
    const body = res && typeof res.body === "string" ? res.body : "";
    const truncated = body.length > BODY_TEXT_CAP;
    return {
      watching: watchedTabs.has(tab),
      entry: entryOut(NET_ENTRY_FIELDS, entry),
      body: truncated ? body.slice(0, BODY_TEXT_CAP) : body,
      base64Encoded: !!(res && res.base64Encoded === true),
      bodyTruncated: truncated,
    };
  }
  const ring = netRings.get(tab) || [];
  const matched = filter
    ? ring.filter((e) => typeof e.url === "string" && e.url.toLowerCase().includes(filter))
    : ring;
  return { watching: watchedTabs.has(tab), entries: matched.slice(-max).map((e) => entryOut(NET_ENTRY_FIELDS, e)) };
}

async function opConsole(tab, args) {
  const max = boundedMax(args.max);
  const level = typeof args.level === "string" && args.level ? args.level : null;
  const source = typeof args.source === "string" && args.source ? args.source : null;
  let matched = consoleRings.get(tab) || [];
  if (level) matched = matched.filter((e) => e.level === level);
  if (source) matched = matched.filter((e) => e.source === source);
  return { watching: watchedTabs.has(tab), entries: matched.slice(-max).map((e) => entryOut(CONSOLE_ENTRY_FIELDS, e)) };
}

const OP_HANDLERS = {
  navigate: opNavigate,
  snapshot: opSnapshot,
  content: opContent,
  read_text: opContent, // server may send either name; same handler
  click: opClick,
  type: opType,
  key: opKey,
  scroll: opScroll,
  screenshot: opScreenshot,
  network: opNetwork,
  console: opConsole,
};

function requireString(v, label) {
  if (typeof v !== "string" || !v) throw new Error(label);
  return v;
}

// Wait until the tab reports status "complete", or the timeout elapses.
// Resolves with the tab (or null) rather than rejecting so a slow page does
// not fail the op.
function waitForTabLoaded(tabId, ms) {
  return new Promise((resolve) => {
    let done = false;
    const finish = () => {
      if (done) return;
      done = true;
      chrome.tabs.onUpdated.removeListener(listener);
      clearTimeout(timer);
      chrome.tabs.get(tabId, (t) => {
        void chrome.runtime.lastError;
        resolve(t || null);
      });
    };
    const listener = (id, changeInfo) => {
      if (id === tabId && changeInfo && changeInfo.status === "complete") finish();
    };
    const timer = setTimeout(finish, ms);
    chrome.tabs.onUpdated.addListener(listener);
    chrome.tabs.get(tabId, (t) => {
      if (!chrome.runtime.lastError && t && t.status === "complete") finish();
    });
  });
}

// ---------------------------------------------------------------------------
// hello: full tab inventory. Sent on open and whenever tabs change (debounced).
// ---------------------------------------------------------------------------
async function listTabs() {
  const raw = await tabsQuery({});
  return raw
    .filter((t) => typeof t.id === "number")
    .map((t) => ({ id: t.id, url: t.url || "", title: t.title || "", granted: grantedIds.has(t.id) }));
}

async function sendHelloNow() {
  try {
    const tabs = await listTabs();
    tabsCache.clear();
    for (const t of tabs) tabsCache.set(t.id, { url: t.url, title: t.title });
    sendJson({ type: "hello", version: PROTOCOL_VERSION, tabs: tabs });
  } catch (e) {
    console.warn("[ogcode] hello failed:", asMsg(e));
  }
}

function scheduleHello() {
  if (!wantConnected || !(ws && ws.readyState === 1)) return;
  if (helloTimer) return;
  helloTimer = setTimeout(() => {
    helloTimer = null;
    void sendHelloNow();
  }, HELLO_DEBOUNCE_MS);
}

// ---------------------------------------------------------------------------
// chrome.tabs + chrome.storage event wiring
// ---------------------------------------------------------------------------
chrome.tabs.onCreated.addListener((tab) => {
  if (tab && typeof tab.id === "number") tabsCache.set(tab.id, { url: tab.url || "", title: tab.title || "" });
  scheduleHello();
});

chrome.tabs.onUpdated.addListener((tabId, changeInfo) => {
  const c = tabsCache.get(tabId);
  if (c) {
    if (changeInfo.url) c.url = changeInfo.url;
    if (changeInfo.title) c.title = changeInfo.title;
  }
  if (changeInfo && (changeInfo.url || changeInfo.title || changeInfo.status)) scheduleHello();
});

chrome.tabs.onRemoved.addListener((tabId) => {
  tabsCache.delete(tabId);
  snapshots.delete(tabId);
  tabLocks.delete(tabId); // nothing queues behind a gone tab's lock tail
  if (watchedTabs.has(tabId)) dropWatch(tabId);
  // A grant for a dead tab id is meaningless — and Chrome reuses ids: evict it
  // from the persisted allowlist, or a fresh tab could inherit it.
  if (grantedIds.has(tabId)) {
    grantedIds.delete(tabId);
    chrome.storage.local.set(
      { [K_GRANTED]: [...grantedIds].sort((a, b) => a - b) },
      () => { void chrome.runtime.lastError; }
    );
  }
  scheduleHello();
});

chrome.storage.onChanged.addListener((changes, area) => {
  if (area !== "local") return;
  if (changes[K_GRANTED]) {
    grantedIds = toIdSet(changes[K_GRANTED].newValue);
    pruneWatches(); // a tab that left the grant set: detach its capture watch
    scheduleHello(); // granted flags changed -> tell the server
  }
  if (changes[K_ORIGIN] || changes[K_TOKEN]) {
    // pairing settings changed (Connect pressed): if connected, drop the
    // socket so the reconnect picks the new settings up.
    const sock = ws;
    if (sock && (sock.readyState === 0 || sock.readyState === 1) && wantConnected) {
      try { sock.close(); } catch (e) { /* fine */ }
    }
  }
  if (changes[K_CONNECTED]) {
    const on = changes[K_CONNECTED].newValue === true;
    if (on) connect();
    else if (!wantConnected) { /* already down */ }
    else disconnect();
  }
});

// ---------------------------------------------------------------------------
// Messages from the side panel
// ---------------------------------------------------------------------------
chrome.runtime.onMessage.addListener((msg, _sender, sendResponse) => {
  try {
    if (!msg || typeof msg !== "object") return false;
    if (msg.type === "connect") {
      connect();
      sendResponse({ type: "status", state: status.state, detail: status.detail, version: EXT_VERSION });
      return true;
    }
    if (msg.type === "disconnect") {
      disconnect();
      sendResponse({ type: "status", state: status.state, detail: status.detail, version: EXT_VERSION });
      return true;
    }
    if (msg.type === "get-status") {
      sendResponse({ type: "status", state: status.state, detail: status.detail, version: EXT_VERSION });
      return true;
    }
  } catch (e) {
    console.warn("[ogcode] panel message handling error:", asMsg(e));
  }
  return false;
});

// ---------------------------------------------------------------------------
// Startup: seed the allowlist, offer click-to-open side panel, self-heal.
// ---------------------------------------------------------------------------
// The action-click fallback that opens the side panel. Idempotent, so it runs
// on EVERY worker start, not just after an install — a reset worker must not
// leave the toolbar icon without a working fallback.
function enablePanelFallback() {
  try {
    const p = chrome.sidePanel.setPanelBehavior({ openPanelOnActionClick: true });
    if (p && typeof p.catch === "function") p.catch(() => {});
  } catch (e) { /* older Chrome: set via toolbar context menu */ }
}

// Grants must NOT survive a browser restart: Chrome renumbers tab ids from
// low numbers on every start, so a persisted grant would let an arbitrary
// fresh tab inherit last session's allowlist tick and be driven without the
// user ever granting it. Cleared at browser start; mid-session worker
// restarts keep the grants (those ids still name the same live tabs), and a
// boot-time seed that resolves after the clear is voided by the flag below.
let grantsCleared = false;
populateGrantsOnBoot();
async function populateGrantsOnBoot() {
  try {
    const rows = await storageGet([K_GRANTED]);
    grantedIds = grantsCleared ? new Set() : toIdSet(rows[K_GRANTED]);
  } catch (e) {
    grantedIds = new Set();
  }
  void selfHealConnect();
}

enablePanelFallback(); // every SW start

chrome.runtime.onInstalled.addListener(() => {
  enablePanelFallback();
  void selfHealConnect();
});

chrome.runtime.onStartup.addListener(() => {
  grantsCleared = true; // a boot-time seed that lands after this is void
  grantedIds = new Set();
  try {
    chrome.storage.local.set({ [K_GRANTED]: [] }, () => { void chrome.runtime.lastError; });
  } catch (e) { /* storage unavailable: the next grant write still lands */ }
  tabsCache.clear();
  snapshots.clear();
  tabLocks.clear();
  watchedTabs.clear();
  netRings.clear();
  consoleRings.clear();
  void selfHealConnect();
});