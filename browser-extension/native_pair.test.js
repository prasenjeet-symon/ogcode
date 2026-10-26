#!/usr/bin/env node
// Throwaway self-contained harness for the native auto-pair logic in
// background.js (vanilla JS, no framework — run with: node native_pair.test.js).
//
// It evaluates background.js in a fresh vm context per scenario, stubbing the
// chrome.* surface (storage, runtime, native messaging port), the WebSocket,
// and timers. Scenarios drive the same entry points the real service worker
// runs: the boot IIFE (self-heal) and openSocket.
//
// Scenarios:
//   1. no stored credentials + host answers ok -> storage written with the
//      derived origin/token + bgConnected:true, then the ws socket opens.
//   2. host replies ok:false / dies with lastError / never answers (timeout)
//      -> manual fallback: status lands on unconfigured, NO storage writes,
//      no socket, exactly ONE native attempt, no retry after it.
//   3. stored credentials -> the native host is never contacted.
//   4. the chrome.alarms reconnect fallback: armed while a socket is missing
//      under wantConnected, cleared when one opens, handler re-enters connect.
//   5. a Disconnect during an in-flight pair voids it (no K_CONNECTED rewrite).
"use strict";

const fs = require("fs");
const path = require("path");
const vm = require("vm");

const SOURCE = fs.readFileSync(path.join(__dirname, "background.js"), "utf8");
// Read from manifest.json at run time: a hardcoded copy drifts (pipeline
// test.js already lagged the manifest by one release that way).
const MANIFEST_VERSION = JSON.parse(fs.readFileSync(path.join(__dirname, "manifest.json"), "utf8")).version;

let checks = 0;
let failures = 0;

function check(label, cond, actual) {
  checks++;
  if (cond) {
    console.log("ok   - " + label);
  } else {
    failures++;
    console.log("FAIL - " + label + (actual !== undefined ? "   got: " + JSON.stringify(actual) : ""));
  }
}

function deepEqual(a, b) {
  try { return JSON.stringify(a) === JSON.stringify(b); } catch (e) { return false; }
}

// --- one isolated world per scenario ---------------------------------------
function makeWorld(storage) {
  const world = {
    storage: Object.assign({}, storage || {}),
    storageSetCalls: [],
    nativeCalls: [],
    statusMessages: [],
    sockets: [],
    logs: [],
    alarms: { created: [], cleared: [], handlers: [] }, // chrome.alarms reconnect fallback
  };

  // fake native messaging host; the test drives it after the worker loaded
  const port = {
    posted: [],
    disconnected: false,
    _message: [],
    _disconnect: [],
    onMessage: { addListener(fn) { port._message.push(fn); } },
    onDisconnect: { addListener(fn) { port._disconnect.push(fn); } },
    postMessage(msg) { port.posted.push(msg); },
    disconnect() { port.disconnected = true; },
  };
  world.port = port;
  world.host = {
    respond(reply) { for (const fn of port._message) fn(reply); },
    die(message) { // Chrome sets runtime.lastError around onDisconnect
      chrome.runtime.lastError = { message: message || "host died" };
      try { for (const fn of port._disconnect) fn(); } finally {
        chrome.runtime.lastError = undefined;
      }
    },
  };

  // manual clock: background.js schedules the 5s native timeout via setTimeout
  let now = 0;
  let nextId = 1;
  const timers = new Map();
  world.clock = {
    setTimeout(fn, ms) { const id = nextId++; timers.set(id, { fn, at: now + ms }); return id; },
    clearTimeout(id) { timers.delete(id); },
    setInterval(fn) { return world.clock.setTimeout(fn, 1e9); },
    clearInterval(id) { timers.delete(id); },
    pending() { return timers.size; },
    advance(ms) { // fire everything due, in schedule order
      now += ms;
      const due = [...timers.entries()].filter(([, t]) => t.at <= now).sort((a, b) => a[1].at - b[1].at);
      for (const [id, t] of due) { timers.delete(id); t.fn(); }
    },
  };

  class WebSocket {
    constructor(url) {
      world.sockets.push(this);
      this.url = url;
      this.readyState = 0; // CONNECTING; never opens unless a test drives it
      this.onopen = null;
      this.onclose = null;
      this.onmessage = null;
      this.onerror = null;
    }
    addEventListener() {}
    send() {}
    close() {}
  }

  const chrome = {
    runtime: {
      lastError: undefined,
      getManifest() { return { version: MANIFEST_VERSION }; },
      onInstalled: { addListener() {} },
      onStartup: { addListener() {} },
      onMessage: { addListener() {} },
      connectNative(name) { world.nativeCalls.push(name); return port; },
      sendMessage(msg) { world.statusMessages.push(msg); },
    },
    sidePanel: { setPanelBehavior() {} },
    alarms: {
      onAlarm: { addListener(fn) { world.alarms.handlers.push(fn); } },
      create(name, info, cb) {
        world.alarms.created.push(Object.assign({ name: name }, info || {}));
        if (typeof cb === "function") cb();
      },
      clear(name, cb) {
        world.alarms.cleared.push(name);
        if (typeof cb === "function") cb();
      },
    },
    storage: {
      local: {
        get(keys, cb) {
          const out = {};
          for (const k of keys || []) out[k] = world.storage[k];
          cb(out);
        },
        set(obj, cb) {
          world.storageSetCalls.push(Object.assign({}, obj));
          Object.assign(world.storage, obj); // value visible to the next get
          cb();
        },
      },
      onChanged: { addListener() {} },
    },
    tabs: {
      onCreated: { addListener() {} },
      onUpdated: { addListener() {} },
      onRemoved: { addListener() {} },
    },
    debugger: {
      onEvent: { addListener() {} },
      onDetach: { addListener() {} },
    },
  };

  const sandbox = {
    chrome: chrome,
    WebSocket: WebSocket,
    URL: URL,
    console: {
      log() {},
      warn() { world.logs.push(Array.from(arguments).join(" ")); },
      error() { world.logs.push(Array.from(arguments).join(" ")); },
    },
    setTimeout: world.clock.setTimeout,
    clearTimeout: world.clock.clearTimeout,
    setInterval: world.clock.setInterval,
    clearInterval: world.clock.clearInterval,
  };
  world.context = vm.createContext(sandbox);
  vm.runInContext(SOURCE, world.context, { filename: "background.js" });
  return world;
}

// Flush the microtask chains (storage callbacks, promise continuations).
async function settle() {
  for (let i = 0; i < 10; i++) {
    await new Promise((r) => setImmediate(r));
  }
}

function lastStatus(world) {
  return world.statusMessages[world.statusMessages.length - 1];
}

// ---------------------------------------------------------------------------
async function main() {
  const serverEntry = {
    origin: "http://127.0.0.1:7801",
    url: "ws://127.0.0.1:7801/api/browser/bridge?token=tok-123",
    token: "tok-123",
    port: 7801,
    project: "/abs/project/dir",
  };

  // ------------------------------------------------------------------ 1 ---
  // Absent credentials + a good native reply: storage written, connect starts.
  {
    const w = makeWorld({});
    await settle();
    check("boot with no credentials asks the native host once",
      w.nativeCalls.length === 1 && w.nativeCalls[0] === "com.ogcode", w.nativeCalls);
    check("the pair request is {type:\"pair\"}", deepEqual(w.port.posted, [{ type: "pair" }]));
    check("in-flight status is 'connecting' via the ogcode app",
      w.statusMessages.length >= 1
      && w.statusMessages[0].state === "connecting"
      && w.statusMessages[0].detail === "via the ogcode app", w.statusMessages);

    w.host.respond({ ok: true, version: "0.51.0", servers: [serverEntry] });
    await settle();

    check("storage gets exactly the manual-Connect keys {serverOrigin, pairToken, bgConnected:true}",
      w.storageSetCalls.length === 1
      && deepEqual(w.storageSetCalls[0], { serverOrigin: "http://127.0.0.1:7801", pairToken: "tok-123", bgConnected: true }),
      w.storageSetCalls);
    check("origin is derived from the ws url (scheme swapped, path+query dropped, same stored shape)",
      w.storage.serverOrigin === "http://127.0.0.1:7801" && w.storage.pairToken === "tok-123" && w.storage.bgConnected === true,
      w.storage);
    check("connect starts: the ws socket opens to the paired bridge",
      w.sockets.length === 1
      && w.sockets[0].url === "ws://127.0.0.1:7801/api/browser/bridge?token=tok-123",
      w.sockets.map((s) => s.url));
    check("the native port is disconnected after the reply", w.port.disconnected === true);
    check("exactly one native attempt (no retry)", w.nativeCalls.length === 1);
  }

  // ------------------------------------------------------------------ 2 ---
  // Host replies ok:false -> manual fallback, no storage writes, no socket.
  {
    const w = makeWorld({});
    await settle();
    w.host.respond({ ok: false, error: "no running ogcode server" });
    await settle();
    check("ok:false falls back to the manual flow (unconfigured status)",
      lastStatus(w).state === "unconfigured"
      && lastStatus(w).detail === "no ogcode app — paste token to pair", lastStatus(w));
    check("ok:false writes NO storage", w.storageSetCalls.length === 0, w.storageSetCalls);
    check("ok:false opens no socket", w.sockets.length === 0, w.sockets.length);
    check("ok:false disconnects the port and never retries the host",
      w.port.disconnected === true && w.nativeCalls.length === 1);
  }

  // Host dies with lastError (the real "host not installed" shape).
  {
    const w = makeWorld({});
    await settle();
    w.host.die("Specified native messaging host not found");
    await settle();
    check("host-missing lands on the manual paste fallback",
      lastStatus(w).state === "unconfigured"
      && lastStatus(w).detail === "no ogcode app — paste token to pair", lastStatus(w));
    check("host-missing writes nothing and opens no socket",
      w.storageSetCalls.length === 0 && w.sockets.length === 0);
    check("the failure reason is logged, not hidden",
      w.logs.some((line) => line.indexOf("native auto-pair") !== -1), w.logs);
  }

  // Hung host -> the 5s budget gives up; nothing blocks, nothing retries.
  {
    const w = makeWorld({});
    await settle();
    check("timeout: the native timer is armed for 5s", w.clock.pending() === 1);
    w.clock.advance(6000);
    await settle();
    check("timeout falls back to the manual flow (unconfigured status)",
      lastStatus(w).state === "unconfigured"
      && lastStatus(w).detail === "no ogcode app — paste token to pair", lastStatus(w));
    check("timeout writes nothing and opens no socket",
      w.storageSetCalls.length === 0 && w.sockets.length === 0);
    check("timeout disconnects the port", w.port.disconnected === true);
    w.clock.advance(60000);
    await settle();
    check("long after a timeout still no retry, no store, no socket",
      w.nativeCalls.length === 1 && w.storageSetCalls.length === 0 && w.sockets.length === 0);
  }

  // A second trigger in the same worker life reuses the memoised failure.
  {
    const w = makeWorld({});
    await settle();
    w.host.die("host gone");
    await settle();
    w.context.connect(); // re-enters openSocket -> absent credentials again
    await settle();
    check("a second trigger does not re-launch the host (one attempt per worker life)",
      w.nativeCalls.length === 1, w.nativeCalls);
    check("the second trigger still falls back to the manual flow",
      lastStatus(w).state === "unconfigured"
      && lastStatus(w).detail === "no ogcode app — paste token to pair", lastStatus(w));
  }

  // A trigger that overlaps the live attempt shares it: no second native
  // launch, no second in-flight status, one shared resolution.
  {
    const w = makeWorld({});
    await settle();
    w.context.autoPairThenConnect(); // boot attempt already in flight
    w.host.respond({ ok: true, version: "0.51.0", servers: [serverEntry] });
    await settle();
    check("an overlapping trigger does not launch the host twice",
      w.nativeCalls.length === 1, w.nativeCalls);
    check("an overlapping trigger adds no second in-flight status",
      w.statusMessages.filter((m) => m.state === "connecting"
        && m.detail === "via the ogcode app").length === 1, w.statusMessages);
    check("the shared attempt still lands the pair + the socket",
      deepEqual(w.storageSetCalls[0], { serverOrigin: "http://127.0.0.1:7801", pairToken: "tok-123", bgConnected: true })
      && w.sockets.length === 1
      && w.sockets[0].url === "ws://127.0.0.1:7801/api/browser/bridge?token=tok-123",
      { set: w.storageSetCalls, sockets: w.sockets.map((s) => s.url) });
  }

  // ------------------------------------------------------------------ 3 ---
  // Stored credentials: the native host is never contacted.
  {
    const w = makeWorld({ serverOrigin: "http://localhost:7801", pairToken: "tok-old", bgConnected: true });
    await settle();
    check("stored credentials: the native host is never contacted", w.nativeCalls.length === 0, w.nativeCalls);
    check("stored credentials: nothing is rewritten", w.storageSetCalls.length === 0, w.storageSetCalls);
    check("stored credentials: the regular connect takes over",
      w.sockets.length === 1
      && w.sockets[0].url === "ws://localhost:7801/api/browser/bridge?token=tok-old",
      w.sockets.map((s) => s.url));
  }

  // Stored credentials without bgConnected (manual connect intent).
  {
    const w = makeWorld({ serverOrigin: "http://localhost:7801", pairToken: "tok-old" });
    await settle();
    w.context.connect();
    await settle();
    check("a stored-origin connect never touches the native host",
      w.nativeCalls.length === 0 && w.sockets.length === 1
      && w.sockets[0].url === "ws://localhost:7801/api/browser/bridge?token=tok-old",
      w.sockets.map((s) => s.url));
  }

  // Half-paired on boot (origin only): one auto-pair attempt; the stored
  // origin is never overwritten and the fallback is manual.
  {
    const w = makeWorld({ serverOrigin: "http://localhost:9999" });
    await settle();
    w.host.respond({ ok: false, error: "no running ogcode server" });
    await settle();
    check("half-paired boot: the native host is asked once",
      w.nativeCalls.length === 1, w.nativeCalls);
    check("half-paired boot falls back without overwriting the stored origin",
      w.storageSetCalls.length === 0 && w.storage.serverOrigin === "http://localhost:9999", w.storage);
  }

  // ------------------------------------------------------------------ 4 ---
  // The chrome.alarms reconnect fallback (SW-suspend resilience).
  {
    const w = makeWorld({ serverOrigin: "http://localhost:7801", pairToken: "tok-old", bgConnected: true });
    await settle();
    check("stored creds: boot opens one socket", w.sockets.length === 1, w.sockets.length);
    check("no alarm is armed while a connect attempt is holding a socket",
      w.alarms.created.length === 0, w.alarms.created);
    const sock = w.sockets[0];
    sock.readyState = 1; // CONNECTING -> open
    sock.onopen();
    await settle();
    check("socket open: the alarm is cleared (the open socket keeps the SW alive)",
      w.alarms.created.length === 0 && w.alarms.cleared.length === 1,
      { created: w.alarms.created, cleared: w.alarms.cleared });
    sock.readyState = 3; // CLOSED
    sock.onclose();
    await settle();
    check("socket lost with pending intent: one repeating ~60s alarm is armed under the single name",
      w.alarms.created.length === 1
      && w.alarms.created[0].name === "ogweb-reconnect"
      && w.alarms.created[0].periodInMinutes === 1, w.alarms.created);
    w.alarms.handlers.forEach((fn) => fn({ name: "ogweb-reconnect" }));
    await settle();
    check("the alarm handler re-enters the connect path (one more socket attempt)",
      w.sockets.length === 2, w.sockets.length);
    const sock2 = w.sockets[1];
    sock2.readyState = 1;
    sock2.onopen();
    await settle();
    w.alarms.handlers.forEach((fn) => fn({ name: "ogweb-reconnect" }));
    await settle();
    check("an alarm firing while a socket is open constructs nothing (socket guard)",
      w.sockets.length === 2, w.sockets.length);
    w.alarms.handlers.forEach((fn) => fn({ name: "something-else" }));
    await settle();
    check("a foreign alarm name is ignored", w.sockets.length === 2, w.sockets.length);
  }

  // ------------------------------------------------------------------ 5 ---
  // A Disconnect while the pair attempt is in flight voids it.
  {
    const w = makeWorld({});
    await settle();
    check("the pair attempt is in flight", w.nativeCalls.length === 1 && w.sockets.length === 0);
    w.context.disconnect(); // the user disconnects while the pair runs
    w.host.respond({ ok: true, version: "0.51.0", servers: [serverEntry] });
    await settle();
    check("the completed pair writes NOTHING (no bgConnected:true rewrite)",
      w.storageSetCalls.length === 0, w.storageSetCalls);
    check("the completed pair opens no socket", w.sockets.length === 0, w.sockets.length);
    check("status stays where the Disconnect left it (disconnected, not unconfigured)",
      lastStatus(w).state === "disconnected", lastStatus(w));
  }

  // ------------------------------------------------------------ unit bits ---
  {
    const w = makeWorld({});
    const f = w.context.originFromWsUrl;
    check("originFromWsUrl: ws -> http, host kept, path+query dropped",
      f("ws://127.0.0.1:7801/api/browser/bridge?token=x") === "http://127.0.0.1:7801");
    check("originFromWsUrl: wss -> https",
      f("wss://bridge.example:8443/api/browser/bridge?token=y") === "https://bridge.example:8443");
    check("originFromWsUrl: junk is refused",
      f("") === null && f("http://127.0.0.1:7801/api") === null, f("http://127.0.0.1:7801/api"));
  }

  console.log("\n" + (checks - failures) + "/" + checks + " checks passed" + (failures ? ", " + failures + " FAILED" : " — all green"));
  process.exit(failures ? 1 : 0);
}

main().catch((e) => { console.error("harness crashed:", e); process.exit(1); });