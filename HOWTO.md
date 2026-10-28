# HOWTO.md — Project Procedures

Procedures this project has actually run, one `##` heading per task. Each entry carries
`Verified:` (date @ version it was last run at — a stale one means re-check the first step),
`Touches:` (what the steps write to), the ordered steps, and `Done when:` (the check that
says it worked).

Read the entry before running it; fix a step that fails in the same turn. Rules, invariants
and canonical values live in `AGENT.md` and `MEMORY.md`, not here.

## Cut a release

Verified: 2026-10-21 @ v0.50.2
Touches: `internal/version/version.go`, `web/package.json`, `web/package-lock.json`, `RELEASE_NOTES.md`, git tags

1. Bump the version in all four entries: `internal/version/version.go:18` `Version` as `vX.Y.Z`
   (**with** `v`), then `web/package.json:5`, `web/package-lock.json:3` and
   `web/package-lock.json:9` as `X.Y.Z` (**no** `v`). The lockfile's two entries are the ones
   most often missed; nothing validates them at build time.
2. Add a `# Release Notes — vX.Y.Z` section at the **top** of `RELEASE_NOTES.md`. Never fold new
   work into a published version's section — tags are immutable.
3. Commit and push to `main`; CI (`.github/workflows/ci.yml`) must pass.
4. `git tag vX.Y.Z && git push origin vX.Y.Z` (annotated). Nothing is built locally: the tag fires
   Release (GoReleaser — `release.yml` is the only place that sets ldflags) and Docker (`docker.yml`).
   **The tag does not fire Pages** — that needs a push touching `docs/**`, `blog/**` or `docs-site/**`;
   see *Publish the docs site* below. `.goreleaser.yaml` copies prebuilt binaries via
   `tools/gobuild-shim.sh`.
5. Set the GitHub Release body by hand — GoReleaser's default body is only `## Changelog`, and
   `.goreleaser.yaml` has no `header`/`footer` key:
   `head -n <last content line> RELEASE_NOTES.md > /tmp/notes.md` then
   `gh release edit vX.Y.Z --notes-file /tmp/notes.md`. Use `head -n`, never an
   `awk … $0=="---"` idiom — it overshoots by a whole release.

Done when: Release and Docker are green, the Release page carries the notes body, and a
downloaded asset's `ogcode version` prints `vX.Y.Z` (not `dev`, not a blank).

## Publish the docs site

Verified: 2026-10-20 @ v0.50.0
Touches: GitHub Pages (`ogcode.in`, artifact built from `docs/`), the `gh` workflow dispatch

1. `gh workflow run pages.yml --ref main`. `pages.yml` triggers only on a push to `main` carrying
   `docs/**`, `blog/**` or `docs-site/**`, so a release with no site change leaves the live site a
   version behind — `workflow_dispatch` is enabled for exactly this. Dispatch off `main`, not the
   tag: the site is built from the tree, and `main` is at or ahead of the tag (`git rev-parse HEAD
   origin/main` should match).
2. `gh run watch <run-id> --exit-status` (the id is printed by the dispatch). The job builds the blog
   (`blog/` → `docs/blog`) and the docs site (`docs-site/` → `docs/docs`) before uploading `docs`;
   both `npm ci` runs hard-fail on a `package-lock.json` drift.
3. `gh api repos/prasenjeet-symon/ogcode/deployments --jq '.[0] | {ref, created_at}'` and
   `gh api repos/prasenjeet-symon/ogcode/deployments/<id>/statuses --jq '.[0].state'` → `success`.
4. Prove the live site is this tree, not a cache: the homepage is uploaded byte-for-byte, so
   `curl -sS https://ogcode.in/ | shasum -a 256` must equal `shasum -a 256 docs/index.html`. Then
   `curl -o /dev/null -w '%{http_code}' https://ogcode.in/docs/install/` → 200.

Done when: the run is green, a fresh `github-pages` deployment reports `success`, and the homepage
hash matches `docs/index.html`.

Note: the homepage `#versionPill` is **not** a deployment check — it fetches
`releases/latest` at runtime and only replaces its text for a `v0.39.x` tag, so it still reads
`v0.39.1` after a much newer release. That is deliberate, not stale content.

## Build and smoke-test the Docker image

Verified: 2026-10-19 @ v0.48.0 — migrated from MEMORY.md, not re-run
Touches: `Dockerfile`, `.dockerignore` (read-only check of `docker.yml`)

1. `docker build -t ogcode:dev .` — `sh -c` on the Dockerfile text validates nothing, so a real
   build is the syntax check.
2. `docker run --rm ogcode:dev version` and **assert the version string**, not just exit 0: the
   failure mode is a blank version, which exits 0. The image reads its version out of
   `web/package.json` because `.dockerignore` excludes `.git`, so `git describe` always fails.
3. `docker run -d -p 9595:9595 ogcode:dev`, then `GET /api/config` → JSON 200. The image's own
   `HEALTHCHECK` probes the same endpoint with `--start-period=120s`.
4. `docker run --rm ogcode:dev sh -c 'git --version'` — the final stage must carry `git`, or every
   workspace reports "not a git repository".

Done when: `version` prints `vX.Y.Z`, `/api/config` answers 200, and `git --version` runs.

## Deploy the standalone server

Verified: 2026-10-19 @ v0.48.0 — migrated from MEMORY.md, not re-run
Touches: the host's nginx/Caddy config, systemd unit or `docker run` flags, environment

1. Open `docs/DEPLOY.md` and pick one of the three supported setups — SSH tunnel, reverse proxy +
   HTTPS/auth, Docker. There is no VPN option, and the server has **no built-in authentication**.
2. `ogcode serve` binds **all interfaces by default** (only `--port`/`-p` exists) — put it behind
   the tunnel or the proxy, never on the open internet.
3. Turn off proxy buffering for the event stream or the UI looks frozen: `proxy_buffering off`
   (nginx), `flush_interval -1` (Caddy). `GET /api/event` already sends `X-Accel-Buffering: no`.
4. For previews reached from another machine, set `OGCODE_PREVIEW_DOMAIN` to a wildcard DNS name
   (the default `preview.localhost` only resolves on the same machine) and give it wildcard TLS.
5. Probe `GET /api/config` — JSON 200 is the health check.

Done when: `/api/config` answers 200 through the proxy and a session streams progress without
stalling.

## Run the control plane and a worker locally

Verified: 2026-10-19 @ v0.48.0 — migrated from MEMORY.md, not re-run
Touches: `controlplane/bin/`, `/tmp/cp-config.json`, `~/.ogcode/worker-id`, `/etc/hosts`

1. `cd controlplane && go build -o bin/ogcode-control-plane ./cmd/ogcode-control-plane` (no CGO
   needed — it is a separate module).
2. Write `/tmp/cp-config.json` with `listen :8443`, a `pairingSecret`,
   **`cookieDomain: ".panel.example"`** and no `tls` block (h2c). Without `cookieDomain` the apex
   cookie is host-only and subdomain UIs 401 after apex login.
3. Seed an operator — bbolt is single-writer, so do this before starting the master:
   `OGCODE_CONTROL_PLANE_USER_PASSWORD='…' ./bin/ogcode-control-plane users add operator`
   (password via env, never argv).
4. `./bin/ogcode-control-plane serve --config /tmp/cp-config.json`.
5. `OGCODE_PAIRING_SECRET=<same> ./ogcode worker --master http://panel.example:8443 --name dev-mac
   --workspace <ABS dir>` — `--workspace` must be absolute. The worker binds no inbound port and
   logs the loopback port it gave the worktree server (`eager tunnel: worktree UI reachable … port=`).
6. Add `/etc/hosts` entries for `panel.example` and `*.panel.example`: `*.localhost` does not
   resolve on macOS. The console renders links with the port the browser used
   (`Host: panel.example:8443` → `http://<id>-test.panel.example:8443/`).
7. To redeploy: `cd controlplane && make build`, kill the pid, relaunch with `nohup … >> log 2>&1 &`
   — the DB survives. On macOS there is no `timeout` command; use `curl --max-time`.

Done when: the console answers on `:8443`, `POST /__operator/login` (fields `username` +
`password`, form-encoded) returns 303 with an `og_operator` cookie, and the worker logs its eager
tunnel after the master restarts (~1 s, no worker restart).

## Screenshot the running UI

Verified: 2026-10-19 @ v0.48.0 — migrated from MEMORY.md, not re-run
Touches: `/tmp` PNG/PDF files, the Chrome profile dir

1. `pkill -f chrome-headless-shell` first — an orphan spins CPU and makes the next launch hang —
   then start it with an explicit URL: `chrome-headless-shell --headless
   --remote-debugging-port=9227 --remote-allow-origins='*' --user-data-dir=/tmp/x about:blank`.
   With no URL `/json` returns `[]`; without `--remote-allow-origins` the handshake gets 403.
2. Connect through `http://127.0.0.1:<port>/json`: take the entry whose `"type"` is `"page"` and
   its `webSocketDebuggerUrl` (rewrite `localhost` → `127.0.0.1`; `/devtools/page` 404s). Kill
   the browser in a `finally` block.
3. `Emulation.setDeviceMetricsOverride{width,height,deviceScaleFactor:1,mobile:false}`, then
   `Page.printToPDF{printBackground:true, paperWidth:W/96, paperHeight:H/96, margin*:0, scale:1}`
   → base64 → `.pdf` → `sips -s format png`. `Page.captureScreenshot` hangs here (ego lite
   0.5.0.32). A 1440×900 override yields a 1080×675 PNG (~0.75×).
4. Hide what the shot should not show with an inline `style.display="none"` rather than clicking
   "Don't show again" — the persisted profile is the user's. Walk UP from a banner's button to
   the nearest `position:fixed` ancestor; the button's own parent is not the fixed wrapper.
5. Never wait for `load` on the app (it holds an open SSE connection) — sleep instead. For reveal
   or spring animations wait ~2.8 s and poll until the transform is stable before asserting.
6. To shoot a component the running app cannot reach, inject its exact markup into
   `document.body` over CDP — copy every Tailwind class verbatim and let the running bundle's own
   stylesheet style it.

7. `Page.captureScreenshot` on a stock "Google Chrome for Testing" launched with `--remote-debugging-port`
   works (2026-10-24) — the hang above was ego lite's. The CLI flag `--screenshot` HANGS on any page with
   an open SSE stream (the session page never goes idle), so drive CDP instead; there `Page.captureScreenshot`
   returns promptly with `--hide-scrollbars --window-size=W,H`.

8. **The rig resolves the theme, not the user's**: headless chrome reports a light OS and the app's
   boot reconcile (server-global `GET /api/appearance`) OVERRIDES a `localStorage['ogcode-theme-mode']`
   written pre-load — a setItem + reload still resolved light (2026-10-24). To shoot dark, send
   `Emulation.setEmulatedMedia{features:[{name:'prefers-color-scheme',value:'dark'}]}` BEFORE
   `Page.navigate`; nothing is written to localStorage or `/api/appearance`, and
   `document.documentElement.dataset.theme` answers `dark`.

Done when: the PNG shows the intended page; for an injected overlay, verify it rendered (printToPDF
can silently drop it) — a `bg-black/60` backdrop reads ~0.4× the undimmed capture and
`getComputedStyle(backdrop).backgroundColor` is `oklab(0 0 0 / 0.6)`.

## Drive the web UI end to end with ego-browser

Verified: 2026-10-22 @ v0.50.2
Touches: `/tmp/<rig>/`, an ego task space, nothing in the repo

1. `cd web && npm run build` FIRST — `web/dist` is an `embed.FS`, so a stale dist means the browser runs
   code that is not in your tree.
2. `CGO_ENABLED=1 go build -o /tmp/<rig>/ogcode .`; a `git init`'d `/tmp/<rig>/proj` as the cwd;
   `HOME=/tmp/<rig>/home` (chmod 700) — and **copy `~/.ogcode/config.db` into `$HOME/.ogcode/`**, else no
   provider resolves.
3. `HOME=… nohup ogcode serve --port 79xx > log 2>&1 &`, then poll `GET /api/config` for 200. An explicit
   `--port` leaves `~/.ogcode/ports.json` alone; never drive the user's own listener.
4. `~/.local/bin/ego-browser nodejs <<'EOF' … EOF` with
   `const task = await taskSpace("<name>"); const page = task.page("p1");` then
   `page.goto/fill/click/selectOption/reload/waitForSelector/waitForTimeout/evaluate`. Node `fetch` works
   inside the script — use it to act BEHIND the page (e.g. prove an SSE-driven refresh).
5. `page.snapshot()` takes a ref (`@21`) or nothing; a css locator throws `TypeError`. Read state with
   `page.evaluate(() => …)` instead.
6. Finish: `await task.finish({ keep: [] })`, `pkill -f '<rig>/ogcode serve'`, `rm -rf <rig>` — the copied
   `config.db` holds API keys.
7. A turn that dies on provider credit (`402 Insufficient Balance`) leaves every agent-side path
   unverified — report that, not as a defect. Confirm the server said so in
   `$HOME/.ogcode/logs/<proj>-*/ogcode.log`.

Done when: the interaction ran through the rendered UI, the visible result matches, and the persisted state
is cross-checked against `GET /api/…` — for a live-update claim, assert it appeared with no manual refresh.

## Drive an agent turn end to end on the OGX gateway

Verified: 2026-10-22 @ v0.50.2
Touches: `/tmp/<rig>/` (its copied `config.db` holds API keys), nothing in the repo

1. Build as in the ego-browser entry, plus the assertion secret or every call 401s:
   `SEC=$(tr -d '\n' < ~/.ogcode/ogx-app-secret)`; `CGO_ENABLED=1 go build -ldflags "-X
   github.com/prasenjeet-symon/ogcode/internal/provider.OGXAppSecret=$SEC" -o /tmp/<rig>/ogcode .`; confirm
   `strings /tmp/<rig>/ogcode | grep -c -F "$SEC"` is > 0. Never print the secret.
2. `HOME=/tmp/<rig>/home` (chmod 700) with `~/.ogcode/config.db` copied in, `git init /tmp/<rig>/proj`,
   `HOME=… nohup ogcode serve --port 79xx &`, poll `GET /api/config` for 200.
3. `POST /api/session/` with `{"directory":"/tmp/<rig>/proj","model":"ogx-deepseek-v4.1-flash",
   "provider":"ogx"}` → read `.id`; then `PATCH /api/session/<id>` `{"permission":"yolo"}`, else every tool
   call blocks on a permission prompt nobody will answer.
4. `POST /api/session/<id>/prompt` with `{"content":"…"}` → **204**. No other body shape works: the session
   stores an empty text part and the turn never starts.
5. Poll `GET /api/session/<id>/message?limit=40` while it runs. Each tool call shows up TWICE — an `assistant`
   message carrying the call and a `user` message whose part echoes the same tool+input beside the result — so
   count calls, not messages.
6. Take ground truth from the DB, not the transcript:
   `sqlite3 /tmp/<rig>/proj/.ogcode/ogcode.db "select handle,kind,status,notified_at from board_agent;"`
   (067_agent_tree.sql dropped `board_post` and `board_agent.last_seq`; the tree is the log now).
7. Watch `$HOME/.ogcode/logs/<proj>-*/ogcode.log` for `board: turn started` — the turn really began.
8. Teardown: `pkill -f '/tmp/<rig>/ogcode serve'`, `rm -rf /tmp/<rig>`. A HOME-isolated `go test` leaves a
   read-only module cache — `chmod -R u+w <home>` before deleting it.

Done when: the transcript's tool calls are the ones you meant, the board rows agree with them — each agent's
row moves to `status=done` and `notified_at` is stamped when the child reports — and the log carries
`board: turn started`.

## Inspect a session's plan-intent routing

Verified: 2026-10-19 @ v0.48.0
Touches: nothing — read-only queries against a project's `.ogcode/ogcode.db`

1. Every `update_intent` call is a `part` row: `json_extract(data,'$.data.tool')='update_intent'`,
   the plan under `$.data.state.input.steps[]`, the tool result (and any match hint) under
   `$.data.state.output`. The persisted plan is `session.intent`, one row per session.
2. Count calls and how many carried a match hint (the result appends `name (skill)` /
   `name (mcp)` lines):
   ```sql
   WITH calls AS (
     SELECT p.session_id,
            CASE WHEN instr(json_extract(p.data,'$.data.state.output'),'(skill)')>0
                   OR instr(json_extract(p.data,'$.data.state.output'),'(mcp)')>0
                 THEN 1 ELSE 0 END AS hinted
     FROM part p WHERE json_extract(p.data,'$.data.tool')='update_intent'
   )
   SELECT session_id, count(*) calls, sum(hinted) hinted_calls
   FROM calls GROUP BY session_id ORDER BY calls DESC LIMIT 8;
   ```
   Both numbers are roughly ×2: a call is stored as two part rows ~1 ms apart. No separate
   `matched*` bookkeeping exists on the part — the hint in the output IS the record.
3. To compare a chip that appeared against the revision that lost it, pull one session's calls
   with their steps and result:
   ```sql
   SELECT time_created, json_extract(data,'$.data.state.input.steps'),
          json_extract(data,'$.data.state.output')
   FROM part WHERE json_extract(data,'$.data.tool')='update_intent'
     AND session_id='ses_…' ORDER BY time_created;
   ```
   Chips ride the step's **label**, not its description (see MEMORY.md), and a done/dropped
   step is never re-routed — so a closing revision that only records outcomes carries no hint.

Done when: the query returns the session's calls, and each hint in the output names a step in the
same revision's `steps[]`.

## Convert an image to WebP

Verified: 2026-10-19 @ v0.48.0
Touches: the output file

1. `npx -y sharp-cli -i <in.png> -o <out.webp> resize --width <N>` — there is no ImageMagick,
   `cwebp` or PIL on this machine, and the installed ffmpeg has no libwebp encoder
   (`Unknown encoder 'libwebp'`).
2. Resizing is a **subcommand** after the input/output flags; a `--resize` flag silently prints
   help instead.

Done when: `file <out.webp>` reports `RIFF (little-endian) data, Web/P image`.

## Re-encode the README hero GIF

Verified: 2026-10-22 @ v0.50.2
Touches: `docs/media/ogcode-demo.gif`, `README.md` (the hero `<img>`)

1. The hero must be an animated **GIF** — GitHub renders neither animated WebP nor `<video>`. There is
   no gifski or gifsicle here, so ffmpeg's two-pass palette flow is the encoder (it has no libwebp).
2. One palette for the whole clip:
   `ffmpeg -y -i <rec.mov> -vf "fps=15,scale=1200:-1:flags=lanczos,palettegen=stats_mode=diff" /tmp/pal.png`
3. Encode — the trim, the speed-up and the encode are one chain, `setpts` **before** `fps` or the frame
   count follows the source instead of the shortened timeline:
   `ffmpeg -y -ss 12 -t 48 -i <rec.mov> -i /tmp/pal.png -lavfi "setpts=(PTS-STARTPTS)/2,fps=15,scale=1080:-1:flags=lanczos[x];[x][1:v]paletteuse=dither=bayer:bayer_scale=3:diff_mode=rectangle" -loop 0 docs/media/ogcode-demo.gif`
   A 3420×1978 / 60 fps / 60 s screen recording becomes **1080×625, 360 frames, 24 s, 5.1 MB** at 2×.
4. `-loop 0` is the infinite loop; keep the `<a href="https://ogcode.in">` wrapper and `width="100%"`.
5. Watch the recording before shipping it: this repo's own session titles and internal paths have leaked
   into the hero twice. `~/…` placeholder paths in a throwaway workspace are fine.

Done when: `git check-ignore -v docs/media/ogcode-demo.gif` exits 1, the README `src` resolves from the
repo root, and both frames and loop are right (`gif` header w/h, `NETSCAPE2.0` loop count `0`).

## Post a Discord notification

Verified: 2026-10-19 @ v0.48.0 — migrated from MEMORY.md, not re-run
Touches: the Discord channel (no local files)

1. `DISCORD_WEBHOOK_URL` is exported in `~/.zshrc` inside the `discord()` shell function (aliases
   `dmsg`, `dcode`, `dpy`, `djs`, `dbash`).
2. `curl -X POST -H "Content-Type: application/json" -d '{"content":"…"}' "$DISCORD_WEBHOOK_URL"`.
3. Treat **HTTP 204** as success — Discord returns no body, so an empty response is the pass.

Done when: the message appears in the channel.

## Drive WhatsApp from the command line

Verified: 2026-10-19 @ v0.48.0 — migrated from MEMORY.md, not re-run
Touches: `~/.claude/skills/whatsapp/`, WhatsApp.app state

1. Use the installed skill at `~/.claude/skills/whatsapp/` (`SKILL.md` +
   `scripts/{list-chats,read-chat,send-message}`). WhatsApp.app has no AppleScript dictionary, so
   everything is System Events accessibility navigation.
2. Grant the `osascript` process **Accessibility** permission first; refusal is error `-25211`.
3. Key by accessibility description: `"List of chats"`, `"Messages in chat with <name>"`,
   `"Compose message"`. A chat button's description IS its name; bubbles read
   `"Your message, CONTENT, TIMESTAMP, Sent to NAME"` / `"message, CONTENT, TIMESTAMP, Received
   from NAME"`.
4. Enumerate with a BFS over `groups of el`, **inlined inside `on run`** —
   `entire contents of window 1` is too slow and loses focus mid-enumeration, and a handler
   called as `my findGroup(...)` loses the `tell process` context and returns nothing.
5. Strip the U+200E prefix from descriptions (`lstrip("\u200e\u200f")`) before parsing. `do shell
   script` steals focus the same way `entire contents` does, so avoid it mid-run.
6. The compose textarea sits 370+ elements deep — allow ~600 BFS steps. Send with `set focused` →
   `set value` → `keystroke return`, not a Send button.

Done when: the message appears in the named chat. Limits: only currently rendered messages are
readable, and the compose field is text-only.

## Install ogcode on a host

Verified: 2026-10-23 @ v0.50.2-3-g033be0f-dirty (step 5 + the boot check re-run; the live server was left
running, not restarted)
Touches: `/usr/local/bin`, `~/.local/bin`, the Homebrew tap, `%LOCALAPPDATA%`, the Go module cache

1. macOS/Linux: `curl -fsSL https://ogcode.in/install.sh | sh` → installs to `/usr/local/bin`.
2. Homebrew: `brew install prasenjeet-symon/tap/ogcode`.
3. Windows: `install.ps1`, or winget.
4. `go install github.com/prasenjeet-symon/ogcode@latest` works, but gives no CGO control — the
   swift tree-sitter binding and libmupdf are cgo, so it is the least suitable channel.
5. For a local build from a checkout: `make -s install OGX_APP_SECRET=$(tr -d '\n' < ~/.ogcode/ogx-app-secret)`
   — web bundle first, then the CGO server, then an ad-hoc codesign on macOS. `-s` stops make echoing the
   secret, and the newline must be stripped or the gateway HMAC 401s. `make build` alone leaves the binary
   in the repo root.

Done when: `ogcode version` prints a version string (there is no `--version` flag), and for a local build the
new binary boots — fresh `HOME` + temp cwd + `serve --port <spare>`, `GET /api/config` returns 200, then `kill`
(expand `$HOME/.local/bin/ogcode` to `$BIN` **before** overriding `HOME`). Restart the live server separately by
listening pid: the file is replaced but the running process keeps the old image. The version string is NO proof of
a fresh image for uncommitted edits (`git describe --dirty` only flips the `-dirty` suffix), so prove it by mtime or
a compiled-in literal (`LC_ALL=C grep -c '<a string you just added>' ~/.local/bin/ogcode`); `cmp ./ogcode
~/.local/bin/ogcode` differs on macOS because `codesign` rewrites the Mach-O, not because the copy failed.
6. Restarting the live server kills the session that asked for it, so schedule it detached instead of running
   `kill` inline: write a script (`sleep 12; kill $(lsof -nP -iTCP:<port> -sTCP:LISTEN -t); wait for the port to
   free; cd <dir>; $BIN`), then launch it with `python3 -c "import os; os.setsid(); os.execv('/bin/sh', …)"`
   under `nohup` so a process-group kill cannot reach it. The 12 s delay lets the reply persist first; startup
   `ResetRunningStatuses` (session/store.go) clears the stale running dot the kill leaves behind.

## Trace the install-id stitch

Verified: 2026-10-19 @ v0.48.0 — migrated from MEMORY.md, not re-run
Touches: `web/` homepage, `install.sh`, `~/.ogcode/install-id`, `~/.ogcode/install-reported`

1. **Site**: the homepage's copy button stamps the visitor's PostHog id onto the command it copies
   only (the on-screen line stays clean). The stamped form is
   `export OGCODE_IID='<id>'; curl … | sh` — `VAR=x cmd | sh` does not pass `VAR` to the piped `sh`.
2. **Installer**: persists the id to `~/.ogcode/install-id` (0600).
3. **Binary**: `internal/server/install_event.go` `EnsureInstallID(home)` mints a `local-…` id when
   none is recorded, and `ogcode_installed` fires once — only when the id was stitched — guarded
   by `~/.ogcode/install-reported`, written after the capture.
4. **UI**: `GET /api/config.installId` lets the app adopt the id via `bootstrapAnalytics()`.

Done when: `/api/config` returns the same `installId` the installer wrote. A hand-typed URL or a
brew/winget install lands on a fresh `local-…` id, so funnel numbers are a floor, not a census.

## Prove a build-time secret is in a binary

Verified: 2026-10-19 @ v0.48.0 — migrated from MEMORY.md, not re-run
Touches: nothing (reads a binary; may write a temp extract)

1. `LC_ALL=C grep -c '<64hex>' <binary>` — the `-X` value is one contiguous string. Plain `grep -o`
   needs `-a` or it prints `Binary file … matches` once and a 3-occurrence build reads as 1; `strings`
   is unreliable and has silently dropped a longer secret here.
2. Read the count as a two-state answer: `0` means the variable was empty at link time (`-X` on an
   unset value is a silent no-op by design); ≥1 means baked in. An unstripped `make` build greps **3**
   (symtab + value + DWARF); a release-style build (`-trimpath -ldflags "-s -w -X …"`) greps exactly
   **1**. Either count means signed — do not read 3-vs-1 as drift.
3. From a downloaded asset, find the offset first — it is build-specific, so re-run the grep every
   time: `LC_ALL=C grep -aboE '[0-9a-f]{64}' <asset>`, then
   `dd if=<asset> bs=1 skip=<offset> count=64`.

Done when: the count is ≥1 and the extracted 64 hex characters match
`tr -d '\n' < ~/.ogcode/ogx-app-secret`. Compare with `cmp` or a hash — never echo the secret itself.

## Deploy the oglab gateway router

Verified: 2026-10-19 @ v0.48.0
Touches: `/tmp/oglab-router-linux-amd64`, the oglab box's `/usr/local/bin/oglab-router`,
`/root/oglab-router.bak-<ts>`, `systemctl oglab-router`, `/etc/nginx/sites-available/oglab`

1. Cross-compile in `~/Downloads/work/oglab` — the box has no Go:
   `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o /tmp/oglab-router-linux-amd64 ./cmd/router`,
   then `shasum -a 256` it for the post-deploy comparison.
2. Connect with `export SSHPASS="$SUNOFLOW_SSH_PASSWORD"` then
   `sshpass -e ssh -o StrictHostKeyChecking=no -p 20009 root@162.19.81.108 '<cmd>'` (`scp -P 20009`).
3. On the box: `cp /usr/local/bin/oglab-router /root/oglab-router.bak-$(date +%Y-%m-%d-%H%M%S)`, scp the
   new binary to `/usr/local/bin/oglab-router.new`, `chmod 755`, `mv -f` it into place (a fresh inode),
   `systemctl restart oglab-router`, then `systemctl is-active oglab-router` and `systemctl show -p
   ActiveEnterTimestamp` to prove it is the new process.
4. For an nginx change: back up `/etc/nginx/sites-available/oglab`, edit only the intended `location`,
   `nginx -t && systemctl reload nginx` (a reload, never a restart — it is a shared vhost).
5. Verify with `python3` on the box (it has no `curl`): `http://127.0.0.1:18083/healthz` → 200, and
   `http://127.0.0.1/v1/models` with `Host: ogx.ogcode.in` → 401 JSON with no `Content-Encoding`.

Done when: the box's `sha256sum` equals step 1's hash, `/healthz` answers 200, and the public
`https://ogx.ogcode.in/v1/models` returns the same 401 JSON through Cloudflare. A signed SSE probe
against a real model must show intact `data:` frames and no gzip.

## Defragment MEMORY.md

Verified: 2026-10-21 @ v0.50.0-1-g1383bfd-dirty
Touches: `MEMORY.md`, `HISTORY.md`, `HOWTO.md`, `MEMORY-ARCHIVE.md`

1. `cp MEMORY.md /tmp/MEMORY.pre-defrag.md` first — `MEMORY.md` is gitignored, so there is no undo.
2. Move history out first: dated measurements, session narratives, experiment logs and release/benchmark
   outcomes go to `HISTORY.md` under `## <date> — <what changed or was learned>`. Keep the gotcha and the
   invariant in `MEMORY.md`; the story goes.
3. Move procedures second: anything that reads as ordered steps for a task — a one-off command sequence, a
   deploy or probe recipe — becomes its own `## <task>` entry here (Verified / Touches / steps / Done when),
   not a bullet in `MEMORY.md`. Update an existing entry in place; never add a second copy.
4. Regroup what is left to one section per topic: split a grab-bag section by subject, and merge facts on one
   subject that sit in three places (install/version facts were scattered across three sections). Reword only
   what repeats; carry every bullet's facts across.
5. Re-measure while you are in there — `wc -c MEMORY.md AGENT.md HOWTO.md HISTORY.md` — and correct the sizes line.
6. Prove nothing was dropped with a small `python3` script: collect every backticked span and every numeric
   literal from old and new (`MEMORY.md` + `HOWTO.md` + `HISTORY.md`) and diff them. Each surviving
   difference must be a deliberate correction or a line-wrap artifact of the old file, not a lost fact.
7. Append the snapshot to `MEMORY-ARCHIVE.md` as
   `## Snapshot <date> — MEMORY.md immediately before the defragmentation pass (<size>, <lines> lines)`,
   then the raw file.

Done when: `MEMORY.md` is under about 20 KB, the token diff shows no lost span beyond deliberate corrections,
`grep '^## ' MEMORY.md` lists no repeated topic, procedures that were in the old file live in a HOWTO entry,
and the archive carries the new snapshot.

## Run the agent benchmark offline with a reused code index

Verified: 2026-10-20 @ v0.49.0-1-g7705e0a-dirty
Touches: `~/Downloads/work/ogcode-bench/{index-backup,runs,RESULTS.md}` (outside this repo)

1. `cd ~/Downloads/work/ogcode-bench` — the harness is not in the ogcode tree and is not under git.
2. Build the index backup once per template commit:
   `./benchmark.py backup-index --from indexbuild` → `index-backup/ogcode.db` (sqlite backup API) +
   `index-backup/index.json` (`template_commit`, page count). Every `run` restores this instead of
   indexing; `--rebuild-index` forces `ogcode index` from the template source instead.
3. Run: `./benchmark.py run --agent {ogcode,opencode} --label smoke --time-limit 90`. The run's env puts
   both arms behind an allowlist CONNECT proxy — only `ollama.com:443` tunnels; every other host is
   `403` and logged as `DENY <target>` in `runs/<id>/egress.log`, summarised into `run.json.egress`.
4. Read the setup block: `index: restored, 0.0s, 306 pages` and `egress: … proxied_model: true`.
   `proxied_model: false` means the model call escaped the proxy — the run is not comparable. With the
   default `wiretap=True`, `runs/<id>/wire.jsonl` holds one row per request (status, body bytes, ms,
   usage) and `run.json.wire` the summary; `--no-wiretap` tunnels opaquely.
5. `./benchmark.py report` → `RESULTS.md`; runs whose label starts `smoke` are skipped.

Done when: the setup line says `restored` with a non-zero page count, `proxied_model: true`, and the
blocked list holds no host the agent needs. Bump the backup (`backup-index`) whenever the template repo
moves; a stale backup is refused by commit, not silently reused.

## Measure multi-agent against single-agent on the benchmark

Verified: 2026-10-23 @ v0.50.1-1-g664be42-dirty
Touches: `~/Downloads/work/ogcode-bench/runs` (outside this repo; not under git)

1. `cd ~/Downloads/work/ogcode-bench` and confirm `SANDBOX_NOTE` is appended to the prompt for **both** modes in
   `run_serve`. The graded clone has no gitignored `server/configs/*.conf`, so a bare `go test ./server` panics —
   without the note runs blow the time limit and the cells are not comparable.
2. One cell per invocation, ~8–40 min each (turn + up to 10 min of grading):
   `python3 benchmark.py run --agent ogcode-serve --task {bug|build} --mode {single|multi} --label <rep>-<task>-<mode> --time-limit 1800`.
   Chain the cells in one detached `/tmp/run_cellsN.sh` that logs `=== START/END <cell> <UTC>`; `pgrep -f run_cells`
   gates a second script so two cells never share the machine.
3. Read the grade from `runs/<id>/run.json`: `success`, `grade.tests_ok`, `grade.build_ok`, `timed_out`,
   `cost_usd`, `turns`, `changes.files`. A `timed_out: true` run is **censored**, not a failure — it still grades,
   so report it as `>1800 s` rather than as a time.
4. Decompose before drawing conclusions: model seconds = sum of `ms` in `runs/<id>/wire.jsonl`; tool seconds and
   the parent-vs-teammate split from `runs/<id>/repo/.ogcode/ogcode.db` (`part.data.type == "tool"`, dedupe on
   `(tool, state.time.start)` — every call is stored twice — and group by `part.session_id`).
5. Grep the run's session DB for sibling run labels: an agent that wanders into `runs/` can borrow another run's
   `home/go/pkg/mod`, which contaminates that cell only.

Done when: each cell's `run.json` says `proxied_model: true` and its grade is in, and the per-run tool/model
split is in hand — wall clock alone says nothing when a cold `GOCACHE` turns each `go test ./server` into a
150–420 s compile.

## Audit UI contrast in both themes

Verified: 2026-10-21 @ v0.50.0-1-g1383bfd-dirty
Touches: a throwaway rig under `/tmp` only — the live server on the project port is never the target

1. Build first (`make build`): the server serves `web/dist` from an `embed.FS`, so a web-only change is
   invisible until the Go binary is rebuilt.
2. Copy the tree to the rig — `rsync -a --exclude node_modules --exclude .git --exclude dist . /tmp/contrast-verify/`
   plus the fresh `./ogcode` — and start it on a spare port:
   `cd /tmp/contrast-verify && nohup ./ogcode serve --port 7802 >/tmp/contrast-verify-serve.log 2>&1 &`,
   then poll `curl -s http://localhost:7802/api/config` for a 200.
3. **Save the live appearance first** — `GET /api/appearance` — because the theme mode is machine-wide in
   `~/.ogcode/config.db` (`s.globalDB`), so the rig on another port flips the user's real UI. Restore that
   body at the end.
4. Walk `/`, `/settings` and a `/session/<id>` in dark, then again with the mode switched to light, for
   each accent you care about. Collect every rendered text node, composite its colour over the nearest
   opaque ancestor background, and compare against the WCAG minimum (4.5:1 body, 3:1 for ≥24px/18.66px-bold).
5. Ignore `bg-clip-text text-transparent` nodes: the computed colour is `rgba(0,0,0,0)` and the real ink is
   the gradient, which must be judged by its stops, not by the reported transparent text.
6. Stop the rig by command — `pkill -f 'ogcode serve --port 7802'`. Never kill by port: `lsof -ti tcp:7802`
   also lists WebKit/ego helper processes that share it.

Done when: every non-gradient node clears its threshold in both themes at each accent, and
`GET /api/appearance` on the live port returns what step 3 saved. The checker script itself is
session-local — re-create it from step 4; nothing in the repo ships it.

## Wire the OGX assertion secret into a release build

Verified: 2026-10-19 @ v0.48.0
Touches: `~/.ogcode/ogx-app-secret`, the `OGX_APP_SECRET` repo secret, `.github/workflows/release.yml`

1. `tr -d '\n' < ~/.ogcode/ogx-app-secret | gh secret set OGX_APP_SECRET -R prasenjeet-symon/ogcode` — the
   file is 65 bytes (64 hex + newline); the trailing newline HMACs differently and every request 401s.
2. Add three `env:` blocks and one `-X` each to `build-linux`, `build-macos` and `build-windows` in
   `release.yml`, injecting `internal/provider.OGXAppSecret` from `secrets.OGX_APP_SECRET`. An unset secret
   signs nothing, which is correct for a fork.
3. For a local run instead: `make build-server OGX_APP_SECRET=$(tr -d '\n' < ~/.ogcode/ogx-app-secret)` —
   local builds get no CI secret.
4. Prove it landed with `HOWTO.md` → Prove a build-time secret is in a binary; `0` means the variable was
   empty at link time.

Done when: `LC_ALL=C grep -c '<64hex>' <binary>` is ≥1 and the extracted hex matches the secret file.

## Prove which search backend served a request

Verified: 2026-10-19 @ v0.48.0 — migrated from MEMORY.md, not re-run
Touches: nothing in the repo; runs the binary with proxy env vars

1. Start a blind CONNECT-logging proxy on a loopback port — it records the `CONNECT host:443` target and
   tunnels the bytes.
2. Run the search with `HTTPS_PROXY=http://127.0.0.1:<port>` and both `NO_PROXY` and `no_proxy` exported
   empty — Go silently bypasses the proxy otherwise.
3. Read the proxy log against the result's `Provider` stamp (`tavily`/`native`/`safari`/`chromium`), which is
   set BEFORE the TTL cache. No fallback line means the primary backend served.

Done when: the CONNECT log names the expected backend and the result's `Provider` stamp matches it.

## Inspect the session database

Verified: 2026-10-21 @ v0.50.0-1-g1383bfd-dirty
Touches: reads `<project>/.ogcode/ogcode.db` and `~/.ogcode/config.db`; writes nothing

1. Two DBs: `.ogcode/ogcode.db` per project (sessions, messages, parts, index) and `~/.ogcode/config.db`
   global (permission grants and mode, UI prefs).
2. `message` holds metadata only; content lives in `part.data`, including `state.input`/`state.output`. A
   seeded `message.data` must also carry `id`/`sessionId`/`createdAt` — `GetMessages` does not copy SQL
   columns back.
3. Per-turn input tokens:
   `sqlite3 ogcode.db "SELECT time_created, json_extract(data,'$.tokens.input') FROM message WHERE session_id='<id>' AND data LIKE '%\"role\":\"assistant\"%' ORDER BY time_created"`.
   `tokens.input` is UNCACHED input; the billed prompt is `input + cacheRead`.
4. A hand-seeded session row must set `permission` or `Store.List` 500s.

Done when: the query returns the expected rows and no `database is locked` error appears (a live server
holds the DB — read, do not write, while it runs).

## Re-test a benchmark run's saved repo

Verified: 2026-10-21 @ v0.50.0-1-g1383bfd-dirty
Touches: `~/Downloads/work/ogcode-bench/runs/<label>-<arm>/repo` (outside this repo)

1. `cd ~/Downloads/work/ogcode-bench/runs/<label>-<arm>/repo` — the snapshot is the agent's final tree.
2. Copy the missing config fixture: `cp -R ../../../template/server/configs server/` (or from
   `/tmp/probe-tpl`). Without it every `TestConfigReload*` fails with `open ./configs/reload/…: no such file`.
3. A raw `CONNECT {}` gets `+OK\r\n` before `PONG\r\n` (the server defaults to verbose), so a handler test
   writes `CONNECT {"verbose": false}`; asserting `+PONG` first fails on the pristine template too.
4. `go test ./server/ -run 'TestConfigReload' -timeout 900s`.

Done when: the reload tests run — pass or fail on the agent's code, not on a missing fixture.

## Verify a web change against the live server without restarting it

Verified: 2026-10-24 @ v0.50.2-3-g033be0f-dirty
Touches: writes a throwaway `web/vite.verify.config.ts` (delete after); spawns a vite dev server on :5199; reads live data from the running ogcode server on :7801 without touching it

1. Committed `web/vite.config.ts` proxies `/api` to :9595 (`make dev`), so it cannot reach the live server.
   Write a throwaway `web/vite.verify.config.ts` instead: `defineConfig` with the solid + tailwind plugins,
   `server: { port: 5199, strictPort: true, proxy: { '/api': 'http://localhost:7801' } }`. Copy the plugin
   imports from the committed config.
2. `cd web && nohup npx vite --config vite.verify.config.ts >/tmp/vite-verify.log 2>&1 &` — open the printed
   URL (http://localhost:5199).
3. ego-browser: `taskSpace()`, `page.goto('http://localhost:5199')`, `page.snapshot()`. Selectors are plain
   CSS (e.g. `button[aria-label="Show 10 agents"]`) — accessibility refs like `r=13` are invalid in
   `page.click`, and there is no `page.setViewportSize`.
4. Click the changed control, snapshot again, and confirm the live server's real data rendered (the proxy
   means SSE and polls hit the actual sessions).
5. Scrolling a TRANSCRIPT view (message-list.tsx anchored scroll): `el.scrollTop = N` fights the app's
   holdReader anchoring and snaps back. Drive REAL input over CDP instead: launch Chrome with
   `--remote-debugging-port=<port> --user-data-dir=/tmp/x`, open a ws to the page target, and send
   `Input.dispatchMouseEvent {type:'mouseWheel', x:850, y:800, deltaY:-190}` in small batches
   (16 × 15 ms apart). Page.captureScreenshot accepts a `clip` of `getBoundingClientRect` coords.
6. SAVE AND RESTORE APPEARANCE: the app syncs `localStorage['ogcode-theme-mode']` to the server-global
   `GET/POST /api/appearance` THROUGH THE PROXY — setting it in the throwaway page flips the user's
   machine-wide mode on :7801. Probe `curl :7801/api/appearance` BEFORE rendering, and POST the original
   value back after; also remove the localStorage key.
7. Clean up: `pkill -f vite.verify.config` and `rm web/vite.verify.config.ts`. Never restart :7801 — it runs
   the live session.

Done when: the changed component renders real live data in the dev server and the interaction works; the
live ogcode server was never restarted and the throwaway config is gone.

## Generate a blog hero image with the Gemini key

Verified: 2026-10-28
Touches: `blog/public/media/*-hero.jpg`, the post's frontmatter

1. The key is `GEMINI_API_KEY` in `~/.zshrc`; `source ~/.zshrc` first. Model is
   `gemini-2.5-flash-image` (nano banana) at
   `https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash-image:generateContent`.
2. POST JSON: `contents[0].parts[0].text` = the prompt, `generationConfig` =
   `{"responseModalities":["IMAGE"],"imageConfig":{"aspectRatio":"16:9"}}`. The response's
   `candidates[0].content.parts[].inlineData.data` is base64 PNG (~965 KB raw).
3. Prompt recipes that worked: dark charcoal isometric scene in the sibling hero's style
   (`blog/public/media/memory-is-table-stakes-hero.jpg`), brand orange `#ff5e1f` as the ONLY vivid
   color, deeper `#d1440c` for shadowed glow; say "absolutely no text, letters, or numbers".
4. Decode base64 to a file, convert with
   `sips -s format jpeg -s formatOptions 88 /tmp/hero_raw.png --out blog/public/media/<slug>-hero.jpg`
   — lands at 1344x768.
5. Wire the post: frontmatter `hero: "/blog/media/<slug>-hero.jpg"`, `heroAlt: "..."`,
   `ogImage: "https://ogcode.in/blog/media/<slug>-hero.jpg"`; `blog/src/pages/[id].astro` renders
   `post.data.hero`/`heroAlt` and Base.astro takes ogImage.

Done when: `python3` script prints "saved image/png <bytes>" and `cd blog && npm run build` completes
with the new page built.
