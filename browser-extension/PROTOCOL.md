# ogweb — Wire Protocol (v1)

Contract between the ogcode server (Go mirror) and this extension
(`background.js`). **Both sides MUST implement exactly these shapes.** Every
message is a single JSON object sent as a UTF-8 **text frame** on the
WebSocket. Binary frames are ignored.

- Transport: WebSocket.
- URL: `ws://127.0.0.1:<port>/api/browser/bridge?token=<hex token>`.
  Token and server origin are entered by the user in the side panel and stored
  in `chrome.storage.local` (keys: `serverOrigin` e.g. `"http://localhost:7801"`,
  `pairToken`). The ws URL = serverOrigin with scheme `http -> ws` /
  `https -> wss`, host kept as-is, path `/api/browser/bridge`, query
  `token=<pairToken>`.

## Extension -> server

### hello — on connect, and whenever tabs change

```json
{"type":"hello","version":1,"tabs":[{"id":1,"url":"...","title":"...","granted":true}]}
```

`granted` reflects the user's per-tab allowlist managed in the side panel.

### ping — every 15s while connected

```json
{"type":"ping"}
```

The server may reply `{"type":"pong"}` — the extension ignores it gracefully
if it does not.

## Server -> extension — one operation

```json
{"id":"op_<random>","op":"<name>","tab":<tabId>,"args":{...}}
```

The `tab` id may be omitted for exactly one op — `navigate` with
`newTab: true` — which acts on no existing tab: the extension creates the
tab and grants it at creation.

The extension replies **exactly once per op id**:

```json
{"type":"result","id":"<same id>","ok":true,"data":{...}}
{"type":"result","id":"<same id>","ok":false,"error":"<human readable>"}
```

## Ops

| op | args | result `data` | notes |
|---|---|---|---|
| `navigate` | `{"url":"https://...","newTab":false}` | `{"tabId":<id>,"url":...,"title":...}` | if `newTab` with no `tab` id, `chrome.tabs.create` — the created tab is granted at creation — then wait for load |
| `snapshot` | `{}`, or optional `"query":"..."` and `"limit":<n>` (both optional, either may be used alone) | `{"url","title","viewport":{"w":..,"h":..},"elements":[{"ref":1,"tag":"a\|button\|input\|select\|textarea\|role","tn":"real tag (lowercase)","text":"...","href":...,"value":...,"placeholder":...,"checked":bool,"disabled":bool,"selector":"css path"}],"truncated":bool,"totalMatching":<int, only when a query was applied>}` | COMPACT numbered list of interactive elements (a with href, button, input, select, textarea, `[role=button\|link\|tab\|checkbox\|radio]`, `[contenteditable]`, plus `[onclick]`, `[tabindex]:not([tabindex="-1"])` and `[aria-labelledby]` — the label comes from the `aria-labelledby` targets when the control has no own text), refs starting at 1, in DOM order. Additive per item: `tn` is the real tagName (lowercase — `tag` stays the semantic label) and `disabled` is true only when the element is `disabled` or `aria-disabled`. Elements inside OPEN shadow roots are collected recursively (depth ≤ 6); their selectors pierce a shadow boundary with a ` a >> b ` segment. Produced via `chrome.debugger` attach + `Runtime.evaluate` in the page — the optional args are applied INSIDE that evaluation, so a filtered payload is genuinely smaller on the wire. `query` = case-insensitive substring against the element's searchable text: tag, text, placeholder, href, value, plus the exact tokens `"checked"`/`"unchecked"` for checked state; absent/empty = no filtering. `limit` = positive int, at most that many returned — the FIRST N matches in DOM order; absent keeps the normal cap. Filtered elements keep their ORIGINAL refs (unfiltered DOM-order numbering, never renumbered, so later `click`/`type` by ref still resolve). When a query or the limit dropped a matching element, top-level `truncated` is true; `totalMatching` counts matches BEFORE the limit and is present ONLY when a query was applied. Output kept under ~50 KB for a normal page (element text capped ~120 chars). |
| `content` | `{}` | `{"url","title","text":"<document.body.innerText, capped 20000 chars>"}` | |
| `read_text` | `{}` | same as `content` | alias — the server may send either name |
| `click` | `{"ref":1}` or `{"selector":"css"}` | `{"tabId":<id>,"x":..,"y":..,"url":...,"title":...,"changed":bool}` | one in-page **aim** evaluation before anything is sent: resolves the element (extension remembers the last snapshot's ref->selector map per tab; if it cannot resolve, `ok:false` error `"snapshot stale — run snapshot again"`) and — for a ref — verifies the snapshot's stored tag/text, refusing with the same snapshot-stale error on drift; clamps the click point into the element's own box, then the viewport, and hit-tests with `document.elementFromPoint` — if the target is covered (the hit is not the element, a descendant, or a wrapper ancestor) the op FAILS with `click point blocked by overlay: <hitTag> "<hitText>"` and no mouse event is dispatched. Otherwise `chrome.debugger` `Input.dispatchMouseEvent` pressed+released at the aimed point, then the page location is read back: `url`/`title` are post-click values (fall back to pre-click when the read-back failed) and `changed` is true when either changed or the read-back failed |
| `type` | `{"ref":1,"text":"...","submit":false}` | `{"tabId":<id>,"typed":<n>,"submit":bool,"cleared":bool,"value":"..."}` | refuses a `disabled` or read-only field with `element is disabled or read-only: <selector>`; focus element (click), clear existing value for inputs (unless contenteditable set), `Input.insertText` for the text, and if `submit` true send Enter keydown+keyup; `value` is the field's actual value (contenteditable: innerText, capped 300 chars), captured after typing and before any submit-Enter |
| `key` | `{"key":"Enter\|Tab\|Escape\|ArrowDown\|ArrowUp\|Backspace"}` | `{"tabId":<id>,"key":"..."}` | dispatched via debugger `Input.dispatchKeyEvent` (proper keyCode/code mapping for named keys) |
| `scroll` | `{"direction":"up\|down","amount":<px>}` | `{"tabId":<id>,"direction":"...","amount":<px>}` | mouse wheel event |
| `screenshot` | `{"format":"png"}` | `{"base64":"<jpeg bytes b64>","format":"image/jpeg","width":<w>,"height":<h>}` | `chrome.debugger` `Page.captureScreenshot` (png intermediate), then re-encoded to JPEG quality 0.8 with the long edge capped at 1024 px — larger captures are downscaled, and a capture already ≤1024 px is STILL re-encoded for consistency, so the delivered bytes are always JPEG ≤1024 px on the long edge. `width`/`height` are the DELIVERED dimensions. Args and errors unchanged: `format` other than `png` → `unsupported screenshot format: ...`; no image data → `screenshot returned no image data`. |
| `network` | `{"url":"substring filter","requestId":"...","max":100}` | without `requestId`: `{"watching":bool,"entries":[{"id","url","method","type","startedAt","status","statusText","mimeType","resourceSize","encodedSize","durationMs","fromCache","failed"}]}`; with `requestId`: `{"watching","entry","body","base64Encoded","bodyTruncated"}` | read-only over the capture ring (see Watch capture); body via `Network.getResponseBody`, raw body capped 65536 chars; `max` clamped 1..300, entries insertion order, newest last |
| `console` | `{"level":"...","source":"...","max":100}` | `{"watching":bool,"entries":[{"at","level","source","text","url","line"}]}` | read-only over the capture ring (see Watch capture); `source` is one of `console` (Console API calls), `exception` (uncaught page errors) or `browser-log` (CDP `Log` entries); `max` clamped 1..300, newest last |

`"Another debugger is already attached"` errors are turned into a clear error
string naming the tab and the remedy (close DevTools / detach the other
client).

## Watch capture — network + console

The first op that targets a granted tab attaches `chrome.debugger` to it and
enables the CDP `Network`, `Runtime` and `Log` domains, then **stays
attached** while the tab is driven: passive capture of requests, console
calls, uncaught exceptions and browser log entries into per-tab ring buffers
(300 entries each, oldest evicted). `navigate` with `newTab: true` arms the
watch before it waits for load, so that first page load is captured too.

- Events collect only while the tab is **granted**; unticking it in the side
  panel detaches the debugger and drops its capture. Re-granting re-arms on
  the next op.
- A navigation keeps the watch and the ring: network request ids are unique
  for the whole debugger session, not per page, so entries accumulate across
  navigations on the tab.
- If the MV3 service worker is killed between ops, the rings are empty when
  it wakes and the next op re-arms the watch — every result carries a
  `watching` flag so the caller can tell when capture is live for the tab.
- Entries are advisory, not transactions: a request still in flight has no
  `status`/`durationMs` yet, and response bodies are best-effort
  (`Network.getResponseBody` fails once the renderer has dropped the body,
  e.g. for streamed or discarded responses).

Chrome's "started being debugged" banner therefore stays up while a tab is
being driven — that persistent session is what feeds `network` and
`console`. DevTools open on the tab still prevents (re)attach and keeps the
existing remedy error.

## Security rule

**The extension refuses ANY op on a tab whose id is not in the user's per-tab
granted allowlist**, with error text `tab <id> is not granted for takeover`.
The one exception is `navigate` with `newTab: true` carrying no tab id: it
acts on no existing tab, and the tab it creates is granted at creation — the
side panel shows it ticked, and unticking locks it again. The extension also
re-verifies the tab still exists before acting. It never follows op
instructions that would exfiltrate page data anywhere except back over the
paired WebSocket.

## Native messaging pair leg (extension <-> com.ogcode)

A separate leg from the WebSocket protocol above: this pairs the extension
with the locally installed ogcode app and carries no page data. Chrome
launches the native messaging host when the extension calls
`chrome.runtime.connectNative("com.ogcode")`; each direction is a UTF-8 JSON
message framed per Chrome native messaging — a 4-byte little-endian unsigned
length prefix, then that many bytes.

The extension asks at most once per service-worker life, and only while no
`serverOrigin`/`pairToken` is stored. Request:

```json
{"type":"pair"}
```

Reply — success (non-empty `servers` = at least one reachable local ogcode
server; the extension takes `servers[0]`):

```json
{"ok":true,"version":"0.51.0","servers":[{"origin":"http://127.0.0.1:7801","url":"ws://127.0.0.1:7801/api/browser/bridge?token=<token>","token":"<token>","port":7801,"project":"/abs/project/dir"}]}
```

Reply — failure (also sent for a request without the `type` field):

```json
{"ok":false,"error":"<human readable>"}
```

On success the extension stores `serverOrigin` (the entry's `url` with the
scheme swapped `ws -> http` / `wss -> https` and any path + query dropped)
and `pairToken` (the entry's `token`), sets `bgConnected: true`, disconnects
from the port, and the WebSocket leg above takes over. An empty `servers`
array, an `ok:false` reply, or 5s of silence sends the extension back to the
manual paste flow. The host keeps running until the port closes.