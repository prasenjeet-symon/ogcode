# ogweb

A Chrome MV3 companion extension for ogcode: it connects to a locally running
ogcode server over a WebSocket bridge and lets the server-side **agent view and
drive your real Chrome tabs** — the logged-in session, your actual browser.
Access is strictly opt-in per tab.

**Load it:**

1. Open `chrome://extensions`
2. Enable **Developer mode** (top right)
3. Click **Load unpacked** and select this `browser-extension/` directory
4. Open the panel: click the extension's toolbar icon (or Chrome's puzzle-piece
   menu — click the pin first to keep it visible).

**Pair it:**

1. In ogcode, print the pair URL — it looks like
   `http://localhost:7801/api/browser/bridge?token=<hex>`
2. In the side panel, paste the origin (`http://localhost:7801`) into
   **Server origin** and the hex `<token>` into **Pair token**
3. Press **Connect**. The status line turns green when the bridge is up; it
   reconnects automatically with backoff if the server restarts.
4. In **Open tabs**, tick **allow** on every tab you want ogcode to control.
   (A tab ogcode itself opens — a navigate with `newTab` — is granted
   automatically and shows up ticked here; untick it to lock the agent out.)

### Automatic pairing (native messaging host)

If nothing is paired yet, the extension first tries to pair itself with a
locally running ogcode app — no pasting: it asks the ogcode native messaging
host (`com.ogcode`) once for the local pair token, stores the origin and
token it got back exactly like a manual paste, and connects. This needs the
host registered, which happens automatically when the ogcode server boots
(or manually with `ogcode native-host install`). Pasted credentials always
win: with an origin and token already stored, the native host is never
contacted. When the host is missing, silent for 5s, or reports no running
ogcode server, the extension falls back to the manual paste steps above,
says so on its status line, and does not retry — the single attempt is
in-memory only (no extra storage key), so a service worker restart tries
again exactly once.

**Granting a tab** is what makes any op on it possible. Untick it at any time
and the agent is immediately locked out — the allowlist is re-checked on every
operation, not once at connect.

### The yellow "started being debugged" banner

Browser ops attach Chrome's remote debugger to the tab (`chrome.debugger`),
which always shows Chrome's own banner:
*"ogweb started debugging this browser"*. That is expected
and unavoidable — Chrome shows it to any tool with debugger access. The first
op on a tab also arms a persistent debugger session (see the wire protocol's
"Watch capture") that feeds network and console capture for the takeover
tools, so the banner stays up while that tab is being driven. Clicking
**Cancel** dismisses the debugger and the in-flight op fails with a
"debugger detached" error; the next op re-attaches (and re-arms capture). The
banner appears once per debugger attach, not once per extension.

### Click, type and snapshot (v1.2.0)

- **Clicks aim before they fire.** One in-page re-check verifies the element
  still matches the snapshot (tag/text), clamps the click point into the
  element's own box and the viewport, and hit-tests with
  `document.elementFromPoint`. An element covered by an overlay is refused —
  `click point blocked by overlay: <hitTag> "<hitText>"` — and nothing is
  clicked; the result carries the post-click `url`, `title` and a `changed`
  flag so the caller can tell whether the click moved the page.
- **Typing refuses dead fields.** A `disabled` or read-only field fails with
  `element is disabled or read-only: <selector>`; a successful type returns
  the field's final `value` (capped 300 chars).
- **Snapshots see inside shadow DOM.** Elements in open shadow roots are
  collected recursively (depth ≤ 6) and their selectors pierce the boundary
  with a ` a >> b ` segment; every item also reports its real tag (`tn`,
  lowercase) and whether it is `disabled`. More interactive elements match
  now: `[onclick]`, keyboard-focusable `[tabindex]` and `[aria-labelledby]`
  controls (labelled from their target elements).

### Security model

- **Per-tab allowlist.** Every op carries the target tab id; ops on ungranted
  tabs are refused (`tab <id> is not granted for takeover`) before anything
  else happens. New tabs are ungranted by default — except a tab ogcode
  itself opened for the agent (navigate with `newTab`), which is granted at
  creation and listed in the panel so you can untick it like any other.
  The one op that needs no granted tab is that same navigate-newTab: it acts
  on no existing tab.
- **Local pairing.** The WebSocket only ever dials the origin you entered,
  with the pair token in the query — no other endpoint, no telemetry, no
  external requests of any kind (vanilla JS, no npm, no build step).
- **Data flows one way.** Page data (snapshot, content, screenshots, the
  network/console capture the agent can read with `browser_network` and
  `browser_console`) returns over the same paired WebSocket only. Ops cannot
  make the extension send anything anywhere else.
- **Existence re-check.** If a granted tab was closed, the op fails instead of
  acting on a recycled tab id.
- **You can watch it.** The debugger banner, the panel's status line, and the
  allowlist toggles are live: nothing runs on a tab you have not ticked.

### Storage keys (`chrome.storage.local`)

| key | value |
|---|---|
| `serverOrigin` | server origin string, e.g. `http://localhost:7801` |
| `pairToken` | hex pair token |
| `grantedTabs` | array of granted tab ids (cleared on browser start — tab ids renumber) |
| `bgConnected` | `true` after Connect (lets the worker self-reconnect) |

### Troubleshooting

- **"cannot attach debugger to tab N: another debugger is already attached"** —
  DevTools is open on that tab (or another debugger client holds it). Close
  DevTools and retry.
- **"snapshot stale — run snapshot again"** — the page changed since the last
  numbered snapshot; the agent re-runs `snapshot` and new refs map cleanly.
- **"click point blocked by overlay: ..."** — something covers the element at
  the aimed point (modal, cookie bar, sticky header); the agent takes a fresh
  snapshot and picks a visible control instead.
- **"element is disabled or read-only: ..."** — the field cannot take typed
  input; pick a different control or enable it via the page's own UI.
- **Connection loops** — check the origin (no trailing path), and that the
  ogcode server is running on that port; the status line shows the retry
  countdown.

### Files

| file | role |
|---|---|
| `manifest.json` | MV3 manifest (debugger, tabs, sidePanel, storage, nativeMessaging) |
| `background.js` | service worker: WebSocket bridge, ops, allowlist gate |
| `sidepanel.html/.js/.css` | pairing UI, status line, per-tab allowlist |
| `PROTOCOL.md` | the wire contract (the Go mirror implements the same shapes) |
| `native_pair.test.js` | self-contained harness for the auto-pair logic — `node native_pair.test.js` |
| `pipeline.test.js` | snapshot-pipeline pin harness — `node pipeline.test.js` |