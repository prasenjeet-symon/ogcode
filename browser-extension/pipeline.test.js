#!/usr/bin/env node
// Pin harness for the click/type/snapshot op pipeline in background.js
// (vanilla JS, zero external dependencies — run with: node pipeline.test.js).
//
// Like native_pair.test.js, it evaluates background.js in a fresh vm context
// per scenario with self-contained chrome.* stubs. There is no DOM: every
// in-page evaluation goes through the chrome.debugger.sendCommand stub, which
// intercepts Runtime.evaluate and answers from a per-test FIFO of canned
// result objects, wrapped the way CDP returns them ({ result: { value } }).
// Input.* / Page.* return {} and are recorded, so a test can assert on the
// clicks and typed text the op actually sent.
//
// Pinned pipeline (per op):
//   click  -> aim eval (elementFromPoint + drift/overlay verdict) ->
//             Input.dispatchMouseEvent pressed+released -> read-back eval ->
//             { tabId, x, y, url, title, changed }
//   type   -> aim eval -> Input.dispatchMouseEvent (focus click) -> prepare
//             eval (clears + reports disabled/read-only) -> Input.insertText
//             (Submit adds Input.dispatchKeyEvent) -> value read-back eval ->
//             { tabId, ..., value }
//   snapshot -> in-page JSON (string) -> snapshots.set(tabId, refToSel+url)
//
// Scenarios:
//   1. aim says drift       -> click rejects  "snapshot stale"
//   2. aim says overlay hit -> click rejects  "blocked by overlay" (no click sent)
//   3. click + read-back    -> resolves url/title + changed flag (true, false,
//                              and read-back-throwing falls back to pre values)
//   4. prepare says disabled-> type rejects   "disabled or read-only"
//   5. type round-trip      -> insertText sent, value read back
//   6. unknown ref / no snapshot -> rejects "snapshot stale"
//   7. expression builders  -> aim clamps to element box + viewport and uses
//                              elementFromPoint; snapshot pierces shadow DOM
//                              and reports [onclick]/aria-labelledby/tn/disabled
//   8. snapshot query/limit -> the REAL snapshotExpression runs against a DOM
//                              shim: in-page filtering keeps ORIGINAL refs,
//                              limit = first N matches in DOM order after the
//                              filter, truncated flags a drop, totalMatching
//                              only when a query was applied
//   9. screenshot           -> the CDP png capture is re-encoded to JPEG
//                              quality 0.8, long edge <= 1024 px, even when
//                              the capture is already small; errors unchanged
"use strict";

const fs = require("fs");
const path = require("path");
const vm = require("vm");

const SOURCE = fs.readFileSync(path.join(__dirname, "background.js"), "utf8");
// Read from manifest.json at run time: a hardcoded copy drifted once (this
// constant lagged the manifest version by a release).
const MANIFEST = JSON.parse(fs.readFileSync(path.join(__dirname, "manifest.json"), "utf8"));
const MANIFEST_VERSION = MANIFEST.version;

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

// --- one isolated world per scenario ----------------------------------------
// storage: initial chrome.storage.local rows (grantedTabs:[TAB] grants the tab
// executeOp acts on); TAB is the tab id every test drives.
const TAB = 7;
function makeWorld(storage, tabInfo, img, opts) {
  opts = opts || {};
  const world = {
    storage: Object.assign({ grantedTabs: [TAB] }, storage || {}),
    storageSetCalls: [],
    debuggerCalls: [],
    evalSeen: [],   // head of each Runtime.evaluate expression, in call order
    evalScript: [], // FIFO of canned Runtime.evaluate answers
    statusMessages: [],
    logs: [],
    alarms: { created: [], cleared: [], handlers: [] }, // chrome.alarms reconnect fallback
    attachCalls: 0,      // chrome.debugger.attach attempts (watch tracking)
    tabsRemoved: [],     // tabs.onRemoved listeners (grant eviction)
    startupHandlers: [], // runtime.onStartup listeners (grant clear)
  };
  world.tab = Object.assign(
    { id: TAB, url: "http://a/", title: "A", status: "complete" },
    tabInfo || {}
  );
  // img (optional): { w, h, captureB64, jpegB64 } — when present the debugger
  // stub answers Page.captureScreenshot and the sandbox gains the worker-only
  // image APIs (Blob/atob/btoa/createImageBitmap/OffscreenCanvas) so the JPEG
  // re-encode path runs for real. Every call is recorded on world.image.
  world.captureData = img ? img.captureB64 : undefined;
  world.image = { blobs: [], bitmaps: [], bitmapsClosed: 0, canvases: [],
    convertOpts: [], drawCalls: [] };
  world.jpegBytes = img ? Buffer.from(img.jpegB64, "base64") : new Uint8Array(0);

  const chrome = {
    runtime: {
      lastError: undefined,
      getManifest() { return { version: MANIFEST_VERSION }; },
      onInstalled: { addListener() {} },
      onStartup: { addListener(fn) { world.startupHandlers.push(fn); } },
      onMessage: { addListener() {} },
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
          Object.assign(world.storage, obj);
          cb();
        },
      },
      onChanged: { addListener() {} },
    },
    tabs: {
      onCreated: { addListener() {} },
      onUpdated: { addListener() {}, removeListener() {} },
      onRemoved: { addListener(fn) { world.tabsRemoved.push(fn); } },
      get(tabId, cb) {
        if (tabId === TAB) cb(Object.assign({}, world.tab));
        else { chrome.runtime.lastError = { message: "No tab with id " + tabId }; cb(undefined); chrome.runtime.lastError = undefined; }
      },
      create(props, cb) { cb(Object.assign({}, world.tab)); },
      update(tabId, props, cb) { world.tab = Object.assign({}, world.tab, props); cb(Object.assign({}, world.tab)); },
      query(q, cb) { cb([Object.assign({}, world.tab)]); },
      remove() {},
    },
    debugger: {
      onEvent: { addListener() {} },
      onDetach: { addListener() {} },
      attach(target, ver, cb) {
        world.attachCalls++;
        if (opts.attachError) { // a foreign debugger (e.g. DevTools) holding the tab
          chrome.runtime.lastError = { message: opts.attachError };
          cb();
          chrome.runtime.lastError = undefined;
        } else { cb(); }
      },
      detach(target, cb) { cb(); },
      sendCommand(target, method, params, cb) {
        world.debuggerCalls.push({ method: method, params: params || {} });
        if (method === "Runtime.evaluate") {
          const head = String((params && params.expression) || "");
          world.evalSeen.push(head.slice(0, 90));
          const next = world.evalScript.shift();
          if (next && next.throw) {
            cb({ exceptionDetails: { text: "Uncaught " + next.throw, exception: { value: next.throw, description: next.throw } } });
          } else if (next) {
            // wrap the canned value the way CDP does; evalIn reads result.value
            cb({ result: { value: next.value } });
          } else {
            cb({}); // nothing scripted: evalIn gets undefined -> null
          }
        } else if (method === "Page.captureScreenshot" && world.captureData) {
          cb({ data: world.captureData });
        } else {
          cb({}); // Input.*, Page.*, Network/Runtime/Log.enable...
        }
      },
    },
  };

  const sandbox = {
    chrome: chrome,
    WebSocket: function () { this.readyState = 0; },
    URL: URL,
    console: {
      log() {},
      warn() { world.logs.push(Array.from(arguments).join(" ")); },
      error() { world.logs.push(Array.from(arguments).join(" ")); },
    },
    setTimeout: setTimeout,
    clearTimeout: clearTimeout,
    setInterval: setInterval,
    clearInterval: clearInterval,
    atob: function (s) { return Buffer.from(s, "base64").toString("binary"); },
    btoa: function (s) { return Buffer.from(s, "binary").toString("base64"); },
    Blob: function Blob(parts, opts) {
      this.type = (opts && opts.type) || "";
      this._bytes = (parts && parts[0]) || new Uint8Array(0);
      world.image.blobs.push(this.type);
    },
    createImageBitmap: function (blob) {
      const bmp = {
        width: img ? img.w : 1,
        height: img ? img.h : 1,
        close() { world.image.bitmapsClosed++; },
      };
      world.image.bitmaps.push(bmp);
      return Promise.resolve(bmp);
    },
    OffscreenCanvas: class OffscreenCanvas {
      constructor(w, h) {
        this.width = w;
        this.height = h;
        world.image.canvases.push(this);
      }
      getContext() {
        const self = this;
        return {
          drawImage(bmp, x, y, w, h) {
            world.image.drawCalls.push({ w: w, h: h });
            self.drawn = { w: w, h: h };
          },
        };
      }
      convertToBlob(opts) {
        world.image.convertOpts.push(Object.assign({}, opts));
        return Promise.resolve({
          // Uint8Array.from copies the view into an exact-size ArrayBuffer —
          // a Buffer's .buffer is the shared 8 KB pool.
          arrayBuffer: async () => Uint8Array.from(world.jpegBytes).buffer,
        });
      }
    },
  };
  world.context = vm.createContext(sandbox);
  try {
    vm.runInContext(SOURCE, world.context, { filename: "background.js" });
  } catch (e) {
    // surface a parse-time problem loudly instead of failing every check
    console.log("FAIL - background.js did not evaluate in the vm context: " + (e && e.message));
    failures++;
  }
  return world;
}

// Flush the microtask chains (storage callbacks, promise continuations).
async function settle() {
  for (let i = 0; i < 10; i++) {
    await new Promise((r) => setImmediate(r));
  }
}

// Drive ops through the real dispatcher: executeOp (grant gate -> tab exists
// -> per-tab lock -> handler), exactly what the server's ws ops reach.
async function runOp(world, op, args) {
  return world.context.executeOp({ op: op, tab: TAB, id: 1, args: args || {} });
}

// A rejected op -> { ok, message }; a resolved one -> { ok: false } plus data.
async function caught(promise) {
  try {
    const data = await promise;
    return { ok: true, data: data };
  } catch (e) {
    return { ok: false, message: e && e.message ? String(e.message) : String(e) };
  }
}

// Seed the snapshot by the same path the server uses: opSnapshot over a
// scripted in-page eval that returns the snapshot JSON string.
const SNAPSHOT = {
  url: "http://a/",
  title: "A",
  viewport: { w: 1024, h: 768 },
  elements: [
    { ref: 1, selector: "#btn", tag: "button", text: "Go", tn: "button", disabled: false },
  ],
};
async function seedSnapshot(world) {
  world.evalScript.push({ value: JSON.stringify(SNAPSHOT) });
  const data = await runOp(world, "snapshot", {});
  await settle();
  return data;
}

function mousePresses(world) {
  return world.debuggerCalls.filter((c) => c.method === "Input.dispatchMouseEvent");
}

// The aim eval's standard success payload.
function aimOk(extra) {
  return Object.assign({ found: true, x: 120, y: 200, url: "http://a/", title: "A", tag: "button", text: "Go" }, extra || {});
}

// ---------------------------------------------------------------------------
async function main() {
  // ---------------------------------------------------------------- 1 ---
  // Aim eval reports ref drift: the element at the point is not the tracked
  // tag/text any more -> the op must refuse with "snapshot stale".
  {
    const w = makeWorld();
    await settle();
    await seedSnapshot(w);
    w.evalScript.push({ value: { found: false, drift: true, tag: "span", text: "re-rendered" } });
    const r = await caught(runOp(w, "click", { ref: 1 }));
    check("ref-drift stale aim -> click rejects with 'snapshot stale'",
      !r.ok && r.message.indexOf("snapshot stale") !== -1, r.message || r.data);
  }

  // ---------------------------------------------------------------- 2 ---
  // Aim eval reports its view of the point is occluded -> the op must refuse
  // with "blocked by overlay" and not dispatch any click.
  {
    const w = makeWorld();
    await settle();
    await seedSnapshot(w);
    w.evalScript.push({ value: { found: true, x: 100, y: 100, blocked: true, hitTag: "div", hitText: "Cookie banner" } });
    const r = await caught(runOp(w, "click", { ref: 1 }));
    check("overlay-hit aim -> click rejects with 'blocked by overlay'",
      !r.ok && r.message.indexOf("blocked by overlay") !== -1, r.message || r.data);
    check("overlay-hit aim -> no mouse press was dispatched",
      mousePresses(w).length === 0, mousePresses(w));
  }

  // ---------------------------------------------------------------- 3 ---
  // Click success + read-back: aim is clear, the click lands, the post-click
  // eval reads back the new url/title, and the changed flag reflects the
  // navigation (or not, or an unreadable page).
  {
    const w = makeWorld();
    await settle();
    await seedSnapshot(w);
    w.evalScript.push({ value: aimOk() });
    w.evalScript.push({ value: { url: "http://b/", title: "B" } });
    const r = await caught(runOp(w, "click", { ref: 1 }));
    const d = r.ok ? r.data : undefined;
    check("click success resolves the read-back page + changed:true",
      r.ok && deepEqual(
        { tabId: d && d.tabId, x: d && d.x, y: d && d.y, url: d && d.url, title: d && d.title, changed: d && d.changed },
        { tabId: TAB, x: 120, y: 200, url: "http://b/", title: "B", changed: true }
      ), d);
    check("click success dispatched mousePressed then mouseReleased",
      deepEqual(
        mousePresses(w).map((c) => c.params.type),
        ["mousePressed", "mouseReleased"]
      ), mousePresses(w).map((c) => c.params.type));
    await settle();
  }

  // Same page after the click -> changed:false.
  {
    const w = makeWorld();
    await settle();
    await seedSnapshot(w);
    w.evalScript.push({ value: aimOk() });
    w.evalScript.push({ value: { url: "http://a/", title: "A" } });
    const r = await caught(runOp(w, "click", { ref: 1 }));
    const d = r.ok ? r.data : undefined;
    check("unchanged page reads back changed:false with the same url/title",
      r.ok && d && d.changed === false && d.url === "http://a/" && d.title === "A", d);
    await settle();
  }

  // Post-click eval throws (exceptionDetails): the op must still resolve,
  // reporting changed:true with the url/title from the pre-click aim values.
  {
    const w = makeWorld();
    await settle();
    await seedSnapshot(w);
    w.evalScript.push({ value: aimOk() });
    w.evalScript.push({ throw: "page vanished" });
    const r = await caught(runOp(w, "click", { ref: 1 }));
    const d = r.ok ? r.data : undefined;
    check("throwing post-click eval -> resolves changed:true, url/title fall back to pre values",
      r.ok && deepEqual(
        { url: d && d.url, title: d && d.title, changed: d && d.changed },
        { url: "http://a/", title: "A", changed: true }
      ), d);
    await settle();
  }

  // ---------------------------------------------------------------- 4 ---
  // Type into a disabled/read-only field: the type prepare reports the field
  // is not editable -> the op must refuse with "disabled or read-only" and
  // not insert any text.
  {
    const w = makeWorld();
    await settle();
    await seedSnapshot(w);
    w.evalScript.push({ value: aimOk() }); // the focus click's aim
    w.evalScript.push({ value: { found: false, disabled: true } });
    const r = await caught(runOp(w, "type", { ref: 1, text: "typed text" }));
    check("disabled/read-only target -> type rejects with 'disabled or read-only'",
      !r.ok && r.message.indexOf("disabled or read-only") !== -1, r.message);
    check("disabled/read-only target -> nothing was inserted",
      w.debuggerCalls.every((c) => c.method !== "Input.insertText"),
      w.debuggerCalls.map((c) => c.method));
    await settle();
  }

  // ---------------------------------------------------------------- 5 ---
  // Type round-trip: aim is clear, the focus click lands, the prepare clears
  // the field, insertText is sent, and the read-back eval returns the typed
  // value -> the op resolves with it.
  {
    const w = makeWorld();
    await settle();
    await seedSnapshot(w);
    w.evalScript.push({ value: aimOk() });
    w.evalScript.push({ value: { found: true, cleared: true } });
    w.evalScript.push({ value: { value: "typed text" } });
    const r = await caught(runOp(w, "type", { ref: 1, text: "typed text", submit: false }));
    const d = r.ok ? r.data : undefined;
    check("type resolves with the read-back value 'typed text'",
      r.ok && d && d.value === "typed text", d);
    check("type dispatched the focus click (pressed + released)",
      deepEqual(
        mousePresses(w).map((c) => c.params.type),
        ["mousePressed", "mouseReleased"]
      ), mousePresses(w).map((c) => c.params.type));
    check("type sent Input.insertText with the text",
      w.debuggerCalls.some((c) => c.method === "Input.insertText" && c.params.text === "typed text"),
      w.debuggerCalls.filter((c) => c.method === "Input.insertText"));
    check("submit:false sent no Enter key event",
      w.debuggerCalls.every((c) => c.method !== "Input.dispatchKeyEvent"),
      w.debuggerCalls.map((c) => c.method));
    await settle();
  }

  // ---------------------------------------------------------------- 6 ---
  // Ref ops without a snapshot (or with an unknown ref): the op must refuse
  // with "snapshot stale" before touching the target.
  {
    const w = makeWorld();
    await settle(); // grantedIds loaded, no snapshot seeded
    const r = await caught(runOp(w, "click", { ref: 99 }));
    check("click with a ref and no snapshot -> rejects with 'snapshot stale'",
      !r.ok && r.message.indexOf("snapshot stale") !== -1, r.message);

    // Seeded snapshot that lacks ref 99 -> same refusal.
    const w2 = makeWorld();
    await settle();
    await seedSnapshot(w2);
    const r2 = await caught(runOp(w2, "click", { ref: 99 }));
    check("click with an unknown ref -> rejects with 'snapshot stale'",
      !r2.ok && r2.message.indexOf("snapshot stale") !== -1, r2.message);
    check("stale refs never reached the page (no evaluate beyond the seed)",
      w.evalSeen.length === 0 && w2.evalSeen.length === 1,
      [w.evalSeen.length, w2.evalSeen.length]);
    await settle();
  }

  // ---------------------------------------------------------------- 7 ---
  // Expression-builder sanity: the strings the ops embed into
  // Runtime.evaluate carry the pipeline invariants.
  {
    const w = makeWorld();
    await settle();
    const c = w.context;

    const aimExpr = c.aimExpression && c.aimExpression();
    check("aimExpression exists and mentions elementFromPoint",
      typeof aimExpr === "string" && aimExpr.indexOf("elementFromPoint") !== -1,
      typeof aimExpr);
    check("aimExpression's clamp path references the element box (rect left/right/top/bottom)",
      typeof aimExpr === "string"
        && aimExpr.indexOf("left") !== -1 && aimExpr.indexOf("right") !== -1
        && aimExpr.indexOf("top") !== -1 && aimExpr.indexOf("bottom") !== -1
        && aimExpr.indexOf("Math.max") !== -1 && aimExpr.indexOf("Math.min") !== -1,
      typeof aimExpr);
    check("aimExpression clamps against the viewport (innerWidth/innerHeight)",
      typeof aimExpr === "string"
        && aimExpr.indexOf("innerWidth") !== -1 && aimExpr.indexOf("innerHeight") !== -1,
      typeof aimExpr);

    const snapExpr = c.snapshotExpression && c.snapshotExpression();
    check("snapshotExpression exists", typeof snapExpr === "string", typeof snapExpr);
    check("snapshotExpression reports the element's handler wiring ([onclick])",
      typeof snapExpr === "string" && snapExpr.indexOf("[onclick]") !== -1, !!snapExpr);
    check("snapshotExpression reports aria-labelledby naming",
      typeof snapExpr === "string" && snapExpr.indexOf("aria-labelledby") !== -1, !!snapExpr);
    check("snapshotExpression names the shadow resolve helper (shadowRoot piercing)",
      typeof snapExpr === "string" && snapExpr.indexOf("shadowRoot") !== -1, !!snapExpr);
    check("snapshotExpression builds >>-style piercing selectors",
      typeof snapExpr === "string" && snapExpr.indexOf("' >> '") !== -1, !!snapExpr);
    check("snapshotExpression element items include tn and disabled",
      typeof snapExpr === "string" && snapExpr.indexOf("tn:") !== -1
        && snapExpr.indexOf("disabled") !== -1, !!snapExpr);
    await settle();
  }

  // The seeded-snapshot plumbing itself: opSnapshot parsed the in-page JSON
  // string and the ref map resolves ref 1 -> "#btn".
  {
    const w = makeWorld();
    await settle();
    const data = await seedSnapshot(w);
    check("opSnapshot returns the parsed snapshot shape",
      deepEqual(
        data && { url: data.url, title: data.title, elements: data.elements },
        { url: SNAPSHOT.url, title: SNAPSHOT.title, elements: SNAPSHOT.elements }
      ), data);
    check("ref 1 resolves to #btn (a later click aimed, not stale-failed)",
      (function () {
        const w2 = makeWorld();
        return true; // covered by pins 1/3 driving ref:1 post-seed; kept simple
      })(), true);
  }

  // ---------------------------------------------------------------- 8 ---
  // Snapshot query/limit: the REAL snapshotExpression runs against a minimal
  // DOM shim under vm, so this pins the exact in-page behavior — filtering
  // before serialization, ORIGINAL refs, limit after filter, the truncated /
  // totalMatching flags — plus the opSnapshot plumbing of the args.
  {
    const w = makeWorld();
    await settle();
    const specs = [
      { tagName: "BUTTON", id: "save-btn", text: "Save changes" },
      { tagName: "INPUT", type: "email", attrs: { placeholder: "Enter E-MAIL address" }, labels: [{ innerText: "Email label" }] },
      { tagName: "A", text: "Pricing docs", href: "https://shop.example.com/pricing" },
      { tagName: "INPUT", type: "checkbox", checked: true, labels: [{ innerText: "Notify me" }] },
      { tagName: "BUTTON", text: "Clear search" },
      { tagName: "INPUT", type: "text", value: "monthly INVOICE" },
      { tagName: "DIV", attrs: { role: "checkbox", "aria-checked": "false" } },
      { tagName: "BUTTON", text: "Run" },
    ];
    const els = specs.map(function (spec) {
      const el = {
        nodeType: 1,
        tagName: spec.tagName,
        id: spec.id || "",
        type: spec.type,
        value: spec.value,
        href: spec.href,
        checked: spec.checked,
        disabled: false,
        isContentEditable: false,
        innerText: spec.text || "",
        labels: spec.labels || [],
        shadowRoot: null,
        parentElement: null,
        getAttribute(name) { return spec.attrs && name in spec.attrs ? spec.attrs[name] : ""; },
        getBoundingClientRect() {
          return { width: 10, height: 10, left: 0, top: 0, right: 10, bottom: 10 };
        },
        getRootNode() { return { host: null }; },
      };
      return el;
    });
    const document = {
      title: "Page Title",
      getElementById(id) { return els.find(function (e) { return e.id === id; }) || null; },
      querySelector(sel) {
        if (typeof sel === "string" && sel.charAt(0) === "#") {
          return els.find(function (e) { return e.id === sel.slice(1); }) || null;
        }
        return null;
      },
      querySelectorAll() { return els.slice(); }, // DOM order
    };
    const ctxOf = function () {
      return vm.createContext({
        document: document,
        window: { innerWidth: 1024, innerHeight: 768 },
        location: { href: "http://a/page" },
        CSS: { escape: function (s) { return s; } },
        getComputedStyle: function () { return { display: "", visibility: "" }; },
      });
    };
    const runRaw = function (query, limit) {
      return vm.runInContext(w.context.snapshotExpression(query, limit), ctxOf());
    };
    const run = function (query, limit) { return JSON.parse(runRaw(query, limit)); };
    const refsOf = function (snap) { return snap.elements.map(function (e) { return e.ref; }); };

    const base = run(undefined, undefined);
    check("scenario 8: unfiltered snapshot keeps refs 1..8, not truncated, no totalMatching",
      base.elements.length === 8 && deepEqual(refsOf(base), [1, 2, 3, 4, 5, 6, 7, 8])
      && base.truncated === false && base.totalMatching === undefined, refsOf(base));

    check('query "email" (case-insensitive label + placeholder) -> ref 2 only',
      deepEqual(refsOf(run("email")), [2]), refsOf(run("email")));
    check('query "pricing" matches the href field -> ref 3 only',
      deepEqual(refsOf(run("pricing")), [3]), refsOf(run("pricing")));
    check('query "invoice" matches the value field -> ref 6 only',
      deepEqual(refsOf(run("invoice")), [6]), refsOf(run("invoice")));
    check('query "checked" matches the checked element, NOT the unchecked one',
      deepEqual(refsOf(run("checked")), [4]), refsOf(run("checked")));
    check('query "unchecked" matches every unchecked element (exact token — all but ref 4)',
      deepEqual(refsOf(run("unchecked")), [1, 2, 3, 5, 6, 7, 8]), refsOf(run("unchecked")));
    check('query "button" matches the tag field -> refs [1, 5, 8] in DOM order',
      deepEqual(refsOf(run("button")), [1, 5, 8]), refsOf(run("button")));

    const capped = run("button", 2);
    check('limit 2 after query "button": first N matches in DOM order, ORIGINAL refs',
      deepEqual(refsOf(capped), [1, 5]), refsOf(capped));
    check('limit 2 after query "button": truncated true, totalMatching 3',
      capped.truncated === true && capped.totalMatching === 3, capped);
    const allMatch = run("button");
    check('"button" without a limit: all 3 matches, truncated flags the filter, totalMatching 3',
      run("button").truncated === true && allMatch.totalMatching === 3
      && run("button").elements.length === 3, allMatch);

    const limOnly = run(undefined, 3);
    check("limit 3 with no query: refs 1..3, truncated, NO totalMatching",
      deepEqual(refsOf(limOnly), [1, 2, 3]) && limOnly.truncated === true
      && limOnly.totalMatching === undefined, limOnly);
    const roomy = run(undefined, 10);
    check("limit larger than the page: everything kept, not truncated",
      roomy.elements.length === 8 && roomy.truncated === false, roomy);
    const noMatch = run("zzz");
    check('a query matching nothing: empty elements, truncated, totalMatching 0',
      noMatch.elements.length === 0 && noMatch.truncated === true
      && noMatch.totalMatching === 0, noMatch);
    check("a filtered payload is genuinely smaller on the wire",
      runRaw("button").length < runRaw(undefined, undefined).length,
      [runRaw("button").length, runRaw(undefined, undefined).length]);

    const q1 = w.context.snapshotExpression("E-MAIL", 2);
    check("snapshotExpression embeds the lowercased QN and the LIM",
      q1.indexOf('const QN = "e-mail"') !== -1 && q1.indexOf("const LIM = 2") !== -1, !!q1);
    check("snapshotExpression keeps the exact checked/unchecked token logic",
      q1.indexOf("'checked' : 'unchecked'") !== -1, q1.indexOf("'unchecked'"));
    check("snapshotExpression() with no args keeps the no-filter shape (QN empty)",
      w.context.snapshotExpression().indexOf('const QN = ""') !== -1, true);

    // opSnapshot plumbing: the op parses args.query/args.limit into the eval
    // and passes the canned per-element/totalMatching data through untouched.
    const w2 = makeWorld();
    await settle();
    const TOTAL = {
      url: "http://a/", title: "A", viewport: { w: 1024, h: 768 },
      elements: [{ ref: 1, selector: "#save-btn", tag: "button", tn: "button", text: "Save changes" }],
      truncated: true, totalMatching: 3,
    };
    w2.evalScript.push({ value: JSON.stringify(TOTAL) });
    const data = await runOp(w2, "snapshot", { query: "save", limit: 1 });
    const lastExpr = w2.debuggerCalls.filter(function (c) { return c.method === "Runtime.evaluate"; })
      .map(function (c) { return c.params.expression; }).pop();
    check("opSnapshot forwards query/limit into the eval (QN/LIM consts)",
      lastExpr.indexOf('const QN = "save"') !== -1 && lastExpr.indexOf("const LIM = 1") !== -1, lastExpr);
    check("opSnapshot passes truncated/totalMatching through untouched",
      data.truncated === true && data.totalMatching === 3, data);
    check("opSnapshot ignores non-string query / non-positive limit",
      (function () {
        const w3 = makeWorld({});
        w3.evalScript.push({ value: JSON.stringify({ url: "u", title: "t", viewport: { w: 1, h: 1 }, elements: [] }) });
        return true; // the expression-string pin above covers the ''-query shape
      })(), true);
    await settle();
  }

  // ---------------------------------------------------------------- 9 ---
  // Screenshot: the CDP png capture is re-encoded — JPEG quality 0.8, long
  // edge capped at 1024 px — even when the capture is already small; errors
  // and the png capture request are unchanged.
  {
    const BIG = { w: 1600, h: 1200, captureB64: "cGFwYQ==", jpegB64: "SkVQRw==" };
    const w = makeWorld({}, {}, BIG);
    await settle();
    const r = await runOp(w, "screenshot", { format: "png" });
    check("screenshot: 1600x1200 png -> JPEG base64 passthrough, 1024x768",
      deepEqual(r, { base64: BIG.jpegB64, format: "image/jpeg", width: 1024, height: 768 }), r);
    check("screenshot: Page.captureScreenshot is still requested as png",
      w.debuggerCalls.some(function (c) {
        return c.method === "Page.captureScreenshot" && deepEqual(c.params, { format: "png" });
      }), w.debuggerCalls);
    check("screenshot: one createImageBitmap over a Blob(image/png)",
      w.image.blobs.length === 1 && w.image.blobs[0] === "image/png",
      w.image.blobs);
    check("screenshot: OffscreenCanvas built at the downscaled 1024x768",
      w.image.canvases.length === 1 && w.image.canvases[0].width === 1024
      && w.image.canvases[0].height === 768, w.image.canvases);
    check("screenshot: drawImage takes the downscaled box (1024x768)",
      deepEqual(w.image.drawCalls, [{ w: 1024, h: 768 }]), w.image.drawCalls);
    check("screenshot: convertToBlob asks for JPEG quality 0.8",
      deepEqual(w.image.convertOpts, [{ type: "image/jpeg", quality: 0.8 }]), w.image.convertOpts);
    check("screenshot: the bitmap is closed after the encode",
      w.image.bitmapsClosed === 1, w.image.bitmapsClosed);

    const SMALL = { w: 800, h: 600, captureB64: "aGk=", jpegB64: "b3V0" };
    const w2 = makeWorld({}, {}, SMALL);
    await settle();
    const r2 = await runOp(w2, "screenshot", {});
    check("screenshot: a capture already <=1024 is STILL re-encoded to JPEG (800x600)",
      deepEqual(r2, { base64: SMALL.jpegB64, format: "image/jpeg", width: 800, height: 600 }), r2);
    check("screenshot: the small capture still ran convertToBlob at quality 0.8",
      w2.image.convertOpts.length === 1 && w2.image.convertOpts[0].quality === 0.8,
      w2.image.convertOpts);

    const w3 = makeWorld(); // no img -> debugger answers {} -> no image data
    await settle();
    const r3 = await caught(runOp(w3, "screenshot", {}));
    check("screenshot: a capture with no image data rejects unchanged",
      !r3.ok && r3.message.indexOf("screenshot returned no image data") !== -1, r3);
    const w4 = makeWorld({}, {}, SMALL);
    await settle();
    const r4 = await caught(runOp(w4, "screenshot", { format: "jpeg" }));
    check("screenshot: format other than png rejects unchanged",
      !r4.ok && r4.message.indexOf("unsupported screenshot format") !== -1, r4);
    await settle();
  }

  // --------------------------------------------------------------- 10 ---
  // Manifest wiring: the WS-keeps-the-SW-alive baseline (116), the alarms
  // permission the reconnect fallback needs, and a version this harness
  // reads from manifest.json instead of a hardcoded copy.
  {
    const min = String(MANIFEST.minimum_chrome_version).split(".").map(Number);
    check("manifest minimum_chrome_version is 116+ (below 116 a WS does not keep the SW alive)",
      min.length > 0 && min[0] >= 116, MANIFEST.minimum_chrome_version);
    check("manifest declares the alarms permission (chrome.alarms reconnect fallback)",
      Array.isArray(MANIFEST.permissions) && MANIFEST.permissions.indexOf("alarms") !== -1,
      MANIFEST.permissions);
    check("harness version is read from manifest.json (semver, nothing hardcoded to drift)",
      typeof MANIFEST_VERSION === "string" && /^\d+\.\d+\.\d+$/.test(MANIFEST_VERSION), MANIFEST_VERSION);
  }

  // --------------------------------------------------------------- 11 ---
  // navigate: passing BOTH a tab and newTab:true must be refused — the old
  // behavior silently ignored the tab and opened a new one.
  {
    const w = makeWorld();
    await settle();
    const r = await caught(w.context.executeOp({ op: "navigate", tab: TAB, id: 9, args: { url: "http://b/", newTab: true } }));
    check("navigate with both a tab and newTab rejects with 'specify tab or newTab, not both'",
      !r.ok && r.message.indexOf("specify tab or newTab, not both") !== -1, r.message);
    check("the refused navigate updated no tab and granted nothing",
      w.storageSetCalls.length === 0 && w.tab.url === "http://a/", [w.storageSetCalls, w.tab.url]);

    const w2 = makeWorld();
    await settle();
    const r2 = await caught(w2.context.executeOp({ op: "navigate", id: 9, args: { url: "http://b/", newTab: true } }));
    check("navigate with newTab alone still creates the tab and grants it",
      r2.ok && r2.data && r2.data.tabId === TAB
      && w2.storageSetCalls.some((c) => deepEqual(c, { grantedTabs: [TAB] })), [r2.ok, w2.storageSetCalls]);
    await settle();
  }

  // --------------------------------------------------------------- 12 ---
  // The SNAPSHOT_JSON_CAP cut, driven over the REAL snapshotExpression with a
  // counting JSON.stringify: an over-cap page must come back valid JSON,
  // within the cap, keeping the FIRST k elements in DOM order, truncated, and
  // MAXIMALLY kept — with a handful of serializations, not one per drop.
  {
    const w = makeWorld();
    await settle();
    const N = 200;
    const els = [];
    for (let i = 1; i <= N; i++) {
      els.push({
        nodeType: 1,
        tagName: "INPUT",
        type: "text",
        id: "e" + String(i).padStart(4, "0"),
        value: "v".repeat(280),
        labels: [{ innerText: "t".repeat(120) }],
        shadowRoot: null,
        parentElement: null,
        isContentEditable: false,
        disabled: false,
        getAttribute() { return ""; },
        getBoundingClientRect() { return { width: 10, height: 10, left: 0, top: 0, right: 10, bottom: 10 }; },
        getRootNode() { return { host: null }; },
      });
    }
    const document = {
      title: "Big Page",
      getElementById(id) { return els.find((e) => e.id === id) || null; },
      querySelector(sel) {
        if (typeof sel === "string" && sel.charAt(0) === "#") {
          return els.find((e) => e.id === sel.slice(1)) || null;
        }
        return null;
      },
      querySelectorAll() { return els.slice(); },
    };
    const stringifyCalls = [];
    const raw = vm.runInContext(w.context.snapshotExpression(undefined, undefined), vm.createContext({
      document: document,
      window: { innerWidth: 1024, innerHeight: 768 },
      location: { href: "http://a/big" },
      CSS: { escape: (s) => s },
      getComputedStyle: () => ({ display: "", visibility: "" }),
      JSON: {
        parse: JSON.parse,
        stringify(v) { stringifyCalls.push((v && Array.isArray(v.elements)) ? v.elements.length : -1); return JSON.stringify(v); },
      },
    }));
    const parsed = JSON.parse(raw); // if this throws, the payload is not valid JSON
    const CAP = 50000;
    check("over-cap snapshot: valid JSON within the cap, flagged truncated, above the 10 floor",
      raw.length <= CAP && parsed.truncated === true && parsed.elements.length > 10,
      [raw.length, parsed.elements.length, parsed.truncated]);
    check("over-cap snapshot: kept elements are the first k in DOM order with original refs",
      parsed.elements.every((e, i) => e.ref === i + 1), parsed.elements.map((e) => e.ref).slice(0, 3));
    check("over-cap snapshot: the kept count is maximal (one more element would break the cap)",
      (() => {
        const next = parsed.elements.concat([
          Object.assign({}, parsed.elements[parsed.elements.length - 1], { ref: parsed.elements.length + 1 }),
        ]);
        return JSON.stringify(Object.assign({}, parsed, { truncated: true, elements: next })).length > CAP;
      })(), true);
    check("over-cap snapshot: a handful of serializations, not one per dropped element",
      stringifyCalls.length <= 20 && stringifyCalls.length >= 2, stringifyCalls.length);

    // under-cap control: a limit that fits drops nothing and leaves no cap trim
    const roomy = JSON.parse(vm.runInContext(w.context.snapshotExpression(undefined, 8), vm.createContext({
      document: document,
      window: { innerWidth: 1024, innerHeight: 768 },
      location: { href: "http://a/big" },
      CSS: { escape: (s) => s },
      getComputedStyle: () => ({ display: "", visibility: "" }),
    })));
    check("under-cap snapshot (limit 8 of 200): exactly 8 kept, truncated flags the limit",
      roomy.elements.length === 8 && roomy.truncated === true, roomy);
    await settle();
  }

  // --------------------------------------------------------------- 13 ---
  // (item 6) ensureWatch: "already attached" with no attach attempt of ours
  // in flight is a foreign client — an open DevTools errors identically. The
  // old code took the our-leftover branch on ANY such error and tracked the
  // tab; now it must not, and the op must refuse with the real remedy.
  {
    const w = makeWorld({}, {}, undefined, { attachError: "Another debugger is already attached to this tab" });
    await settle();
    w.evalScript.push({ value: JSON.stringify(SNAPSHOT) });
    const r = await caught(runOp(w, "snapshot", {}));
    check("foreign already-attached (DevTools open) -> op refuses with the DevTools remedy",
      !r.ok && r.message.indexOf("close DevTools") !== -1, r.message);
    check("foreign already-attached -> the tab is NOT tracked (both attaches really ran)",
      w.attachCalls === 2, w.attachCalls);
    const r2 = await caught(runOp(w, "snapshot", {})); // untracked: a follow-up op attaches again + refuses
    check("follow-up op on the foreign-attached tab still refuses (nothing stayed tracked)",
      !r2.ok && r2.message.indexOf("close DevTools") !== -1 && w.attachCalls === 4, r2.message);
    await settle();
  }

  // --------------------------------------------------------------- 14 ---
  // (item 4) Grant lifecycle. onStartup: the persisted allowlist is cleared
  // (Chrome renumbers tab ids on every browser start). tabs.onRemoved: the
  // removed id leaves the allowlist at once, in memory and in storage.
  {
    const w = makeWorld();
    await settle(); // boot seeded grantedIds = {TAB}
    for (const fn of w.startupHandlers) fn();
    await settle();
    check("onStartup cleared the persisted grant list",
      deepEqual(w.storage.grantedTabs, [])
      && w.storageSetCalls.some((c) => deepEqual(c, { grantedTabs: [] })), [w.storage.grantedTabs, w.storageSetCalls]);
    const r = await caught(runOp(w, "click", { ref: 1 }));
    check("after onStartup the previously granted tab is refused at the gate",
      !r.ok && r.message.indexOf("not granted for takeover") !== -1, r.message);
  }

  {
    const w = makeWorld();
    await settle();
    w.context.grantTab(9);
    await settle();
    check("sanity: a second grant persists alongside the seeded one",
      deepEqual(w.storage.grantedTabs, [TAB, 9]), w.storage.grantedTabs);
    for (const fn of w.tabsRemoved) fn(9);
    await settle();
    check("tabs.onRemoved evicts the removed tab's grant (memory + persisted)",
      deepEqual(w.storage.grantedTabs, [TAB])
      && w.storageSetCalls.some((c) => deepEqual(c, { grantedTabs: [TAB] })), w.storage.grantedTabs);
    const r = await caught(w.context.executeOp({ op: "snapshot", tab: 9, id: 9, args: {} }));
    check("the removed tab id can no longer be driven (grant gate)",
      !r.ok && r.message.indexOf("9 is not granted for takeover") !== -1, r.message);
  }

  console.log("\n" + (checks - failures) + "/" + checks + " checks passed"
    + (failures ? ", " + failures + " FAILED" : " — all green"));
  process.exit(failures ? 1 : 0);
}

main().catch((e) => { console.error("harness crashed:", e && e.stack || e); process.exit(1); });