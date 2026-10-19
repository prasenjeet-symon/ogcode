# HOWTO.md — Project Procedures

Procedures this project has actually run, one `##` heading per task. Each entry carries
`Verified:` (date @ version it was last run at — a stale one means re-check the first step),
`Touches:` (what the steps write to), the ordered steps, and `Done when:` (the check that
says it worked).

Read the entry before running it; fix a step that fails in the same turn. Rules, invariants
and canonical values live in `AGENT.md` and `MEMORY.md`, not here.

## Cut a release

Verified: 2026-10-19 @ v0.48.0 — migrated from MEMORY.md, not re-run
Touches: `internal/version/version.go`, `web/package.json`, `web/package-lock.json`, `RELEASE_NOTES.md`, git tags

1. Bump the version in all four entries: `internal/version/version.go:18` `Version` as `vX.Y.Z`
   (**with** `v`), then `web/package.json:5`, `web/package-lock.json:3` and
   `web/package-lock.json:9` as `X.Y.Z` (**no** `v`). The lockfile's two entries are the ones
   most often missed; nothing validates them at build time.
2. Add a `# Release Notes — vX.Y.Z` section at the **top** of `RELEASE_NOTES.md`. Never fold new
   work into a published version's section — tags are immutable.
3. Commit and push to `main`; CI (`.github/workflows/ci.yml`) must pass.
4. `git tag vX.Y.Z && git push origin vX.Y.Z` (annotated). Nothing is built locally: the tag fires
   Release (GoReleaser — `release.yml` is the only place that sets ldflags), Docker (`docker.yml`)
   and Pages. `.goreleaser.yaml` copies prebuilt binaries via `tools/gobuild-shim.sh`.
5. Set the GitHub Release body by hand — GoReleaser's default body is only `## Changelog`, and
   `.goreleaser.yaml` has no `header`/`footer` key:
   `head -n <last content line> RELEASE_NOTES.md > /tmp/notes.md` then
   `gh release edit vX.Y.Z --notes-file /tmp/notes.md`. Use `head -n`, never an
   `awk … $0=="---"` idiom — it overshoots by a whole release.

Done when: the three workflows are green, the Release page carries the notes body, and a
downloaded asset's `ogcode version` prints `vX.Y.Z` (not `dev`, not a blank).

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

Done when: the PNG shows the intended page; for an injected overlay, verify it rendered (printToPDF
can silently drop it) — a `bg-black/60` backdrop reads ~0.4× the undimmed capture and
`getComputedStyle(backdrop).backgroundColor` is `oklab(0 0 0 / 0.6)`.

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

Verified: 2026-10-19 @ v0.48.0 — migrated from MEMORY.md, not re-run
Touches: `/usr/local/bin`, the Homebrew tap, `%LOCALAPPDATA%`, the Go module cache

1. macOS/Linux: `curl -fsSL https://ogcode.in/install.sh | sh` → installs to `/usr/local/bin`.
2. Homebrew: `brew install prasenjeet-symon/tap/ogcode`.
3. Windows: `install.ps1`, or winget.
4. `go install github.com/prasenjeet-symon/ogcode@latest` works, but gives no CGO control — the
   swift tree-sitter binding and libmupdf are cgo, so it is the least suitable channel.
5. For a local build from a checkout use `make install` (web bundle first, then the server, then
   an ad-hoc codesign on macOS); `make build` alone leaves the binary in the repo root.

Done when: `ogcode version` prints a version string (there is no `--version` flag).

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

1. Cross-compile in `/Users/prasenjeet/Downloads/work/oglab` — the box has no Go:
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
