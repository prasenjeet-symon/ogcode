# Project history

The chronological record of what happened in this project: dated changes,
measurements with their numbers, experiments and what they ruled out. Durable
facts a future session must know *before acting* live in `MEMORY.md`; verified
procedures live in `HOWTO.md`; behavioural rules live in `AGENT.md`.

This file is not loaded into the prompt. Open it with `read`, or search it with
`grep` by topic or date, when a task touches past work or before re-running an
experiment. Newest entry last.

## 2026-10-20 — MEMORY.md defragmentation pass

- `MEMORY.md` was 54 KB / 646 lines after the pass, `AGENT.md` 0 B, `HOWTO.md`
  17 KB (pointer only). It reached 79,474 B by 2026-10-21, which is the growth
  the HISTORY.md split is meant to stop.

## 2026-10-21 — System-prompt budget measured; MEMORY.md split from history

- Measured the build agent's system prompt in this repo: entry [0] was
  110,727 B ≈ 27.7k tokens, of which `MEMORY.md` was 78,495 B (65%) and
  `BuildAgent.System` 19,669 B (16%); the project-index block was 7,123 B. The
  per-step entries added 9,127 B (`update_intent` 3,597 + `compact_context`
  3,160 + `ask_user` 2,370).
- That measurement is why `MEMORY.md` now states a ~20 KB budget and sends dated
  measurements and session narratives here instead.
- Duplication removed from the prompt: the exploration flow is stated once under
  one `## Mandatory: Explore Before You Read` heading; the edit contract lives
  only in `parallelToolCallsPrompt`; the verification ladder lives in the
  workflow arrows; `updateIntentPrompt` points at the exploration section
  instead of restating it; `ask_user` is named in step 1 of the process.
- Rejected: a default `OGCODE_MEMORY_MD_MAX_BYTES` cap. `truncateForPrompt` is
  head-biased, so it would silently drop the trailing sections and keep the
  oldest narrative; curating and moving history out is the fix.

## 2026-10-21 — MEMORY.md split into MEMORY / HOWTO / HISTORY

- `MEMORY.md` was 79,965 B / 914 lines before this pass. The three-way split:
  ordered procedures moved to `HOWTO.md` as their own `##` entries (wire the OGX
  secret, prove the search backend, inspect the session DB, re-test a benchmark
  repo), measurements and run outcomes moved here, and `MEMORY.md` kept only
  gotchas with fixes, invariants with their pinning files/tests, canonical values
  and decisions.
- Snapshot of the pre-split file appended to `MEMORY-ARCHIVE.md` as
  `## Snapshot 2026-10-21 — MEMORY.md immediately before the defragmentation pass`.
- `MEMORY-ARCHIVE.md` is untracked; `MEMORY.md` is gitignored, so the snapshot is
  the only undo for a bad prune.
- Final sizes after the pass: `MEMORY.md` **21,086 B / 277 lines** (down 73.6% from 79,965 B, 20 sections),
  `HISTORY.md` 79,278 B, `HOWTO.md` 28,556 B, `MEMORY-ARCHIVE.md` 785,194 B. Every backticked identifier in
  the old file was checked against the union of the three new files; the few that had been dropped while
  compressing were re-recorded under `### Residual detail dropped in the 2026-10-21 defrag` at the end of the
  Reference-detail entry.

## 2026-10-21 — Agent benchmark: harness mechanics, measured runs and outcomes

Moved out of `MEMORY.md`; the mechanics a run depends on stay there, and the
procedure is `HOWTO.md` → Run the agent benchmark offline with a reused code index.

- The harness lives at `~/Downloads/work/ogcode-bench/benchmark.py` (outside this
  repo, not under git). `./benchmark.py run --agent {ogcode,opencode} …` then
  `./benchmark.py report` → `RESULTS.md`; labels starting `smoke` are skipped.
- **Skills need no wiring for a skills-enabled run**: both arms auto-discover the
  same `~/.agents/skills` and `~/.claude/skills` (71 shared user skills; ogcode
  also `~/.ogcode/skills` + `~/.config/ogcode/skills`, plus built-ins
  `customize-ogcode`/`update-ogcode` vs OpenCode's `customize-opencode`). Verified
  by name-set diff, `opencode debug skill`, and a live ogcode probe that loaded a
  global skill. Only the `skill` TOOL differs in practice: neither agent called it
  on the NATS task, and ogcode's build agent ships no skill list in the prompt
  (they arrive via the update_intent matcher) while OpenCode lists them.
- **Matched effort did not rescue the NATS task for ogcode** (parity run,
  reasoning 0 on both arms): OpenCode PASSED, ogcode missed the hidden
  `TestReloadMaxPayloadAppliesToConnectedClients`. The cause is in the solution:
  `server/reload.go`'s `maxPayloadOption.Apply` calls `client.updateMaxPayload()`,
  whose `applyAccountLimits` funnels into `minLimit` (`server/client.go:936`),
  which only ever LOWERS, so a raised limit never reaches `c.mpay` and the pushed
  client INFO still says 65536; OpenCode's 3-line fix wrote
  `atomic.StoreInt32(&client.mpay, m.newValue)` outright. **Refined by the v050
  runs**: `applyAccountLimits` itself CAN raise a limit — it opens with
  `atomic.StoreInt32(&c.mpay, jwt.NoLimit)` (`server/client.go:955`) and only then
  re-derives account/user limits and `minLimit(&c.mpay, opts.MaxPayload)` from the
  post-reload `s.getOpts()`, so calling it AFTER `info.MaxPayload` is raised lands
  the new value; "only lowers" is true of `updateMaxPayload`/`minLimit` alone.
- **Harness traps**: ogcode runs `go test` itself (15 calls, one whole-package
  `./server/ -timeout 900s` → 1,077 s of the 1,337 s wall, while OpenCode ran only
  narrow `-run` filters → 72.6 s), so wall clock needs test time footnoted or
  excluded. The old default `--max-turns` 100 truncated a run MID-EDIT (that
  capped run died on a compile error in its own half-written `reload_test.go`) —
  no longer a trap, since the flag now defaults to `0`.
- **OpenCode's turn cap**: verified 1.18.30, `agent.<name>.steps` is `steps:3` →
  2 agentic iterations, `steps:1` → reasoning only and no tool call, and both `0`
  and 2^53 are rejected by the config validator; unset means uncapped.
- **The v050 triple** (binary `v0.50.0-1-g1383bfd-dirty`, uncapped
  `--max-turns`): the reload bug was NOT fixed, and the uncapped default made it
  worse — ogcode uncapped FAILED at the 2700 s limit, 534 turns, $7.2455, 25.7 M
  billed prompt, 96 MB of up bodies, because after writing a fix it spent 17
  compactions debugging its OWN flaky `TestConfigReloadMaxPayload` (repeated
  `go test -race -count=3`, `sed -i` line deletes, heredoc rewrites) and never
  returned. The same binary PASSED capped at 100 turns: 345 s, $0.7732 (fix =
  skip internal clients, then `client.mu.Lock(); client.applyAccountLimits();
  client.mu.Unlock()` per client). OpenCode PASSED at 561 s / $0.8573 / 53 turns
  with a 4-line unconditional `atomic.StoreInt32(&client.mpay, m.newValue)` loop.
  The uncapped fix was gated on `client.opts.Protocol >= ClientProtoInfo`, so the
  hidden test's raw CONNECT (no `protocol` field ⇒ 0) is skipped and `c.mpay`
  stays 1024 — a gate a real client has and the graded connection does not.
- **Wiretap measurement**: both arms send `stream_options.include_usage`, so
  Ollama's SSE appends a `choices: []` chunk with `usage`
  (`prompt_tokens`/`completion_tokens`/`prompt_tokens_details.cached_tokens`).
  `runs/<id>/wire.jsonl` carries one row per request (status, body bytes, ms,
  usage, capped at 400) and `run.json.wire` is `wire_summary()`; `report()` emits
  "## Measured at the wire" only for runs carrying a `wire` block. Untended, the
  tap let requests outrun responses by 3/5/9 per run and made ogcode's
  self-report look 6–10% above the provider sum; fixed, every run is N/N with
  `responses_with_usage == requests`. ogcode's `served_prompt_tokens` matches its
  self-report EXACTLY because it reads that same usage block — that tests the
  tap, not an independent measurement. OpenCode's JSONL tally omits its FIRST
  request of every run (~764 prompt tokens, no `step_finish` event): residual
  0.02% prompt / 0.1–2% completion. Both arms land at 3.76–3.87 up-body bytes per
  served prompt token.
- **Run outcomes, nosearch pair**: the first GRADED runs with egress active —
  ogcode 83 turns / 261.5 s / $0.869, OpenCode 66 / 285.3 s / $1.197, both
  passed. A tapped re-run superseded both: $0.5829 / $0.8704 — run-to-run spread
  is large. Before them only two 90 s smokes had egress, and both timed out
  (exit -15) with `build_ok` true / `tests_ok` false: ogcode edited only
  `.gitignore`, OpenCode changed nothing. A `run.json` without an `egress` key
  predates the proxy; the four graded notests/pilot runs do, so RESULTS.md's
  egress prose describes the harness, not their numbers.
- **NATS repo replay**: `runs/<label>-<arm>/repo` has no `server/configs/`, so
  every `TestConfigReload*` fails with `open ./configs/reload/…: no such file`;
  copy `template/server/configs` in first. A raw `CONNECT {}` gets `+OK\r\n`
  before `PONG\r\n` (server default verbose), so a handler test writes
  `CONNECT {"verbose": false}`; asserting `+PONG` first fails deterministically
  on the pristine template too. Both arms also litter the run repo (`MEMORY.md`,
  `.gitignore`). Procedure: `HOWTO.md` → Re-test a benchmark run's saved repo.
- **Index reuse measured**: `backup-index` restores 306 pages in ~0 s instead of
  running `ogcode index`; the restored copy inherits the source's WAL mode
  because the sqlite backup API copies page 1.

## 2026-10-21 — Agent prompt, tooling and prompt-cache experiments

Moved out of `MEMORY.md`; the rules and final values stay there.

- **`update_intent` used to own the next block**: `Execute`'s tail invited
  another call ("call it once more"), so in 112 of the 118 build turns that
  opened with `update_intent` the SECOND call was `update_intent` again and
  `codebase_map` was call #2 in zero — measured over `.ogcode/ogcode.db`, build
  sessions after 2026-10-17 19:17 UTC. Cause: the tail was the freshest
  instruction in context. Fixed by making the tail speak only for a fully closed
  plan.
- **The first-call rule was briefly reversed the same day**: a boundary rule —
  orient first, plan before the first mutation, by the fifth call — deferred the
  matcher until after the reading it is meant to steer, so the skills and MCP
  tools it names arrived too late. `updateIntentPrompt()` and `Description()`
  were put back to "first, alone in your first block of every turn".
- **Removed ceilings**: the matcher once ran at most 4 routing calls per block
  and capped matches per step via `OGCODE_INTENT_TOOL_MATCHES`; both are gone,
  and `MaxTokens` is unset so a long answer is not truncated.
- **Title generation** once applied a "fast model" heuristic and ran every title
  on `minimax-m3:cloud`; it now inherits the session model and falls back only
  when the session names no model.
- **Auto-mode risk calibration**: `riskLLMMaxTokens` measured 8 tokens → 3/16
  correct verdicts, 512 → 14/16, 1024 → 16/16. The value is 1024 for that reason;
  an unanswered check warns and returns Ask and is not cached, answered verdicts
  are.
- **Read pressure defaults**: `OGCODE_READ_PRESSURE_THRESHOLD_TOKENS` 40000
  (clamped 2000–1M); `OGCODE_RESEND_COST_WINDOW_MULTIPLE` 1, a multiple of the
  model's context window, fallback 128000.
- **Compaction**: mid-turn compaction is env-gated by `OGCODE_COMPACT_CONTEXT`
  (unset or empty = ON, falsey `0/false/no/off` = off); `maxCompactions = 2` per
  RunLoop and `compactionKeepRecent = 12` were tuned here, and
  `compactionThresholdTokens(window)` falls back to 128000 for an unknown window.
- **Prompt caching**: Anthropic's minimum cacheable prefix is 1024 tokens, 4096
  on Opus 4.5+, TTL 5 min; OpenAI caches prefixes automatically. Entry [0] stays
  byte-identical — the date rides `systemReminderPrompt` as a later entry and
  `osEnvPrompt` sits in the cached prefix.

## 2026-10-21 — Theme, contrast and image-attachment measurements

Moved out of `MEMORY.md`; the floor rule, tokens and final values stay there.

- **The old muted-text values were measured against `--bg-elevated`** and failed
  on hovered rows: `--text-muted`/`--text-label` #86868f 4.39:1, zinc-600
  #808088 4.38:1 on overlay, light zinc-700 #6b6b75 4.36:1. The floor is the
  LIGHTEST surface the token can sit on.
- **Light-contrast failures were dark literals in components, not tokens**:
  `git-diff.tsx` add/remove → `var(--success)`/`var(--danger)`,
  `index-changes.tsx` rename → `var(--color-blue-400)`, `file-tree.tsx` glyphs →
  text tokens, four page headers → `color-mix(in srgb, var(--bg-surface) 82%,
  transparent)`. Unfixed by design: the html-iframe `srcdoc` CSS
  (`markdown-content.tsx`) and language colours.
- **axe**: 22/22 routes clean under axe 4.10.2; the audit rig (Indigo and orange,
  both modes, `/`, `/settings`, `/session/<id>`) reports 0 failures except the
  two gradient-clipped hero nodes. The unread badge is `bg-red-600` (white on
  `--danger` is 3.91:1); `ModelItem`'s disabled row uses per-state tokens, not
  `opacity-60`.
- **Audit rig**: `/tmp/contrast-audit`, run as `ego-browser nodejs < audit4.mjs`
  against a copy on :7802; it reads computed styles per text node and can only
  SEE variables, so `bg-clip-text text-transparent` nodes report
  `rgba(0,0,0,0)` — the `pages/home.tsx` hero is a measured ink (large text, 3:1
  needed), not a fail. Procedure: `HOWTO.md` → Audit UI contrast in both themes.
- **onPrimary history**: the old `onPrimaryFor`/`0.17913` luminance crossover gave
  white at 3.06:1 on orange because it never looked at the hover fill. `labelFor`
  now maximises the MINIMUM ratio over `--accent` and `--accent-hover`, then
  `clampForLabel` nudges lightness by `labelStep` 0.0025. `accent`/`onPrimary`/
  `accentInk` are recomputed on read in `handleGetTheme`'s `else`-branch, so
  stored themes are retroactively clamped.
- **Composer image downscaling, measured in Chromium**: a 4000×3000 JPEG attaches
  as 175,571 B / 1024×768 (~234 K base64), a 7.65 MB 3000×1942 PNG as 121,048 B
  WebP / 1024×663; a 1024×768 JPEG and a 203 KB 400×259 PNG pass through
  byte-identical. Billed image tokens fall ~40% on 4:3 and ~50% on 16:9 versus
  1568, and repeat on every replayed turn. 768 would cut another ~30% but renders
  13px CSS text ~6px — use 1024 for screenshots.

## 2026-10-21 — Session and deployment observations

Moved out of `MEMORY.md`; the rules stay there.

- **Pre-lock binaries shared a DB**: this machine still had four (ports
  7790/7794/7797/7799) sharing this project's DB while only the :7801 server
  held the lock — the user chose to leave them, so they are not test targets.
- **Cold-cache bind delay**: the 133 MB embedder preflight delayed bind ~40-45 s
  and failed healthchecks before it was moved after bind into a goroutine.
- **OGX nginx routing bug**: `/v1/models` answered 200 `text/html` because nginx
  had no `/v1/` location (all to `oglab_web`); the symptom was "Connected, but
  your plan grants no models" beside an `Active · ogx` chip from
  `/api/ogx/status`. Fix = `upstream oglab_router` + `location /v1/` (with
  `proxy_buffering off`, `proxy_cache off`, `gzip off`, read/send 3600 s) BEFORE
  `location /`.
- **Cloudflare origin**: nginx answers only on **40009** (a co-hosted Caddy holds
  `:80`), so `ogx`/`oglab` need an `http_request_origin` rule with
  `origin.port=40009`. The vhost is server-only — back it up first.
- **`ogcode.xyz` is fully removed**; anything still pointing at `.xyz` is a bug.
  `admin.ogcode.xyz` is a DELIBERATE survivor (`admin.ogcode.in` has no DNS);
  trimming it 404s the panel.
- **Outstanding email**: `licensing@`/`support@ogcode.in` get nothing until the
  domain is verified in the Google Workspace console AND a new DKIM key is
  generated.
- **`.gitignore` root-anchoring history**: a bare `GIT_DIFF_PLAN.md` rule silently
  ignored the tracked `plans/GIT_DIFF_PLAN.md`, and an unanchored `dist/` would
  swallow `web/dist/` because GoReleaser writes to the repo root.
- **Asset-name case works by accident**: `install.sh`/`install.ps1` request
  `ogcode_<v>_Darwin_arm64.tar.gz`, GoReleaser emits lowercase; only GitHub
  download URLs are case-insensitive. Do not "fix" it.
- **The homepage `#versionPill`** fetches `releases/latest` at runtime and only
  replaces its text for a `v0.39.x` tag (`data-release="v0.39."`,
  `docs/index.html`), so it still reads `v0.39.1` after a much newer release —
  deliberate, not stale content.

## 2026-10-21 — Reference detail: subsystem facts moved out of MEMORY.md

The long-form text below was moved verbatim out of `MEMORY.md` in the 2026-10-21 split, to bring
that file near its ~20 KB budget. `MEMORY.md` keeps the danger card for each area; the full
reasoning, file/test inventory and secondary values are here. Search this section by symbol name.

### Repo layout & git hygiene

- Root: markdown only; plans in `plans/` (refs inside them bare, `plans/<name>` outside).
- **Branch integration is `--no-ff` with a `merge: <summary>` subject** (`b83d97b`, `c760015`) so a feature
  branch stays one unit in `git log --graph`.
- **`.gitignore` paths stay root-anchored**: `/GIT_DIFF_PLAN.md` (a bare rule ignored the tracked
  `plans/GIT_DIFF_PLAN.md`), `/dist/` (GoReleaser writes at the repo root; unanchored would swallow
  `web/dist/`). Demo media ignored; `MEMORY.md`/`MEMORY-ARCHIVE.md` gitignored; `AGENT.md` tracked.
- **Tracked `AGENT.md` is 0 bytes** (`ffc4698`); its rules live here. A `see AGENT.md` pointer is stale.

### Releases, version & install

- **Tags are immutable**: `git ls-remote --tags origin` + `gh release view vX.Y.Z` first — a bump commit can sit
  on main with the tag already shipped. Never fold new work into a published release.
- **The GitHub Release body is hand-written** (GoReleaser emits only `## Changelog`). Bump sites:
  `internal/version/version.go` (`vX.Y.Z`) and both `web/package*.json` (`X.Y.Z`, must match). Steps +
  `head -n`-never-`awk`: `HOWTO.md` → Cut a release.
- **A red Linux build job blocks the release** while CI/Pages/Docker stay green: a grammar with `build.zig`
  writes `.zig-cache` beside the read-only module cache. Fix = top-level `ZIG_LOCAL_CACHE_DIR: /tmp/zig-cache`
  in `.github/workflows/release.yml`; repro only cold (`go clean -cache`).
- **`release.yml` is the only ldflags site**; `.goreleaser.yaml` only copies prebuilt binaries. Pages publishes
  only on a push carrying `docs/**`, `blog/**`, `docs-site/**` — **a release tag does not fire it**; manual
  route `gh workflow run pages.yml --ref main`. CI on any push; Release/Docker tag-only.
- **The homepage `#versionPill` is not a deployment check**: it fetches `releases/latest` at runtime and only
  replaces its text for a `v0.39.x` tag (`data-release="v0.39."`, `docs/index.html`), so it still reads
  `v0.39.1` after a much newer release. Deliberate.

**Install** (`make install` is canonical — `HOWTO.md` → Install ogcode on a host): also `install.sh`, the
Homebrew tap, `install.ps1`, winget, or `go install` (least suitable — no CGO control).

- **macOS re-sign**: replacing `~/.local/bin/ogcode` in place keeps a stale signature → `Killed: 9`.
  `make install` `rm -f`s first (fresh inode) then ad-hoc signs; the installed file is ~18 KB larger — check
  inode/mtime, not size. **`make` echoes its recipe**, so `make install OGX_APP_SECRET=…` prints the secret —
  add `-s`.
- **`make install` restarts nothing**: a live `ogcode serve` keeps the old inode + old `web/dist`. Never kill a
  user's instance unasked. A Go-only change leaves the web bundle byte-identical, so the served
  `assets/index-*.js` hash matches a fresh `web/dist` either way — it CANNOT prove the new binary is live;
  check the listening pid's start time instead.
- **`detectInstallCommand()`** (`internal/version/version.go`) resolves `os.Executable()` through
  `EvalSymlinks` and matches **scoop → Homebrew → cargo → install.ps1 dir → winget → install.sh**. Homebrew
  means `/Cellar/` in the resolved path or an `/opt/homebrew/` prefix — **never "any `/usr/local/`"**. Pinned
  by `internal/version/install_detect_test.go`; `TestEmbedded_UpdateSkillMatchesDetection` requires the
  `update-ogcode` skill to carry all six.
- **`ogcode check-updates` exits 1 when an update is available** (0 when not).
- **Asset-name case works by accident**: `install.sh`/`install.ps1` request `ogcode_<v>_Darwin_arm64.tar.gz`,
  GoReleaser emits lowercase; only GitHub download URLs are case-insensitive. Do not "fix" it.
- **Verify against a throwaway rig, not the real config**: a fresh HOME alone does not isolate `serve` (the
  project DB is per-directory), so use a temp working dir too, plus a stub `open` on `PATH`. **Expand the
  binary path BEFORE exporting HOME** or `~/.local/bin/ogcode` resolves inside the temp home.

### Build & dev loop

- `make build` = `build-web` (`npm install --legacy-peer-deps --cache /tmp/npm-cache && npm run build` →
  `web/dist`) then `build-server` (`CGO_ENABLED=1 go build -o ./ogcode .`; repo-root `main.go`, no `cmd/`;
  ldflags inject `internal/version.Version`). `make dev` = `go run . serve` :9595; `make dev-web` = Vite :5173.
- **`CGO_ENABLED=1` mandatory** (cgo Swift grammar); `make test` omits it. OGX-signed local build:
  `make build-server OGX_APP_SECRET=$(tr -d '\n' < ~/.ogcode/ogx-app-secret)` — without it the gateway 401s.
- `ogcode version` (no `--version`); a dirty build prints `…-dirty`. No linter: gofmt only the files touched.
- **`ogcode run --max-turns` defaults to `0` = no limit** (`internal/cli/run.go`). The CLI maps `<= 0` to
  `agent.MaxStepsUncapped = -1` (`internal/agent/loop.go`), which `stepBudgets` resolves to `math.MaxInt` and
  skips the auto-resume multiply for. **A literal `MaxSteps == 0` in RunLoop still means the 1000-step default**
  — a zero-value `LoopRunner` and `subagentMaxSteps` depend on it, so "uncapped" must be negative, never zero.
  The interactive server loop (10000, `MaxAutoResumes: 2`) and `ogcode index` (50) stay capped. Pinned by
  `internal/agent/step_budget_test.go`.

### Deployment & first boot

- **A project keeps its port**: `internal/portmap` stores `dir → port` in `~/.ogcode/ports.json`; a bare
  `ogcode` in a project dir reuses it (this project → **7801**), an explicit `--port` overwrites it. The restart
  after `make install` is `kill <pid>; cd <dir> && ogcode` — no `--port`, or the remembered port moves.
- **One server per project dir**: `Server.serve()` takes an advisory flock on `<dir>/.ogcode/server.lock`
  before opening the DB (`internal/server/projectlock.go`; `lock_unix.go` `syscall.Flock`, `lock_windows.go`
  `x/sys/windows.LockFileEx`). A second `serve` exits 1 naming the holder's pid and `http://localhost:<port>`.
  The file is never removed — the OS lock is the authority. Two servers in tests need two dirs
  (`internal/server/public_test.go`).
- **Listener before preflight**: the 133 MB embedder preflight runs in a goroutine started after bind,
  cancelled at shutdown. `/api/config` (GET, JSON 200) is the health probe.
- **`Registry.DefaultUsable`** (`internal/provider/provider.go`) walks `ProviderPriority` but skips a
  registered-but-unreachable ollama (probe fails AND `OLLAMA_API_KEY` unset). Server, both headless CLIs, the
  loop, search and `handleModels` resolve through it.
- **End-user macOS signing is NONE**: ad-hoc, linker-signed; runs because install sets `com.apple.provenance`,
  not `com.apple.quarantine`. `spctl -a -t execute` "rejected" is not "blocked" without that xattr.
- **Docker**: final stage installs `git` (else every workspace says "not a git repository") and
  `HEALTHCHECK … wget -qO- /api/config --start-period=120s`; `docker.yml` needs `type=raw,value=latest`; mirrors
  to Docker Hub as **`prasenjeetsimon`**, login guarded `if: env.DOCKERHUB_USERNAME != ''`. Nested module: COPY
  `controlplane/go.mod` + `go.sum` before `go mod download`; CI builds no image.
- **Docker ldflags traps**: Go ignores `-X` on a missing symbol (shipped `commit: none` once) and an empty `-X`
  does not fall back — `.dockerignore` drops `.git`, so `sed` the version out of `web/package.json` (re-add
  `v`). A blank version reads DEV and disables the update check while exiting 0.

### OGX gateway: identity, secret & provider

**Assertion (ogcode signs, oglab verifies).** On `POST /v1/chat/completions` and `GET /v1/models`:
`X-Client-App`, `X-Client-Timestamp`, `X-Client-Signature` =
`base64.RawURLEncoding(HMAC-SHA256(secret, payload))`, **payload = `app + "\n" +
strconv.FormatInt(ts,10) + "\n" + method + "\n" + path`**, skew 5 min. It binds app/time/call, authorises no
other endpoint and cannot be replayed; each repo spells it locally. **Gate is CONDITIONAL**: no secrets ⇒ open;
any secret ⇒ missing/unknown-app/stale/bad-MAC ⇒ 401.

- ogcode: `internal/provider/appassertion.go`; `OpenAIProvider.setChatHeaders` calls it and
  **`fetchDynamicModels` calls `setChatHeaders` too** (`TestOtherProvidersDoNotAssert`). oglab:
  `internal/httpx/httpx.go` owns consts + `SignAssertion`/`VerifyRequestAssertion`; gate at the top of
  `chatUser` (`internal/router/chat.go`); `cmd/router/main.go` reads **`OGLAB_APP_SECRETS`** (`app=secret,…`).
- **Secret**: `internal/provider/ogx.go` — `OGXProviderID = "ogx"`, `OGXAppID = "ogcode"`, `var OGXAppSecret`
  (ldflags). One live value — never paste it — in `~/.ogcode/ogx-app-secret` (600, 64 hex + newline), the
  `OGX_APP_SECRET` repo secret, and oglab's `OGLAB_APP_SECRETS=ogcode=<same>`. **`tr -d '\n'` is mandatory**:
  the file is 65 bytes; unstripped it HMACs differently → silent 401. Wiring: `HOWTO.md` → Wire the OGX
  assertion secret into a release build.
- **Provider**: ordinary `*OpenAIProvider` (`NewOGXProvider(token)`), base `https://ogx.ogcode.in/v1`
  (`OGX_GATEWAY_URL`); model and collection stay empty so `modelGroup` yields `ogx`. **`/v1/models` IS the
  plan**: `case OGXProviderID` in `Models()` fetches dynamic models, marks all `ActiveByDefault`, resolves a
  default, **no static fallback** — empty/failed ⇒ no models (`TestOGXProviderEmptyCatalogueHasNoFallback`).
- **Registration is plan-gated, network-free**: `session.OGXPlanNone` + `(*OGXAccount).HasPlan()` (nil-safe,
  true when non-empty and != `"none"`). Builders: `internal/server/server.go loadProviderMap`,
  `internal/cli/run.go`, `internal/worker/env.go buildProviderRegistry`, `internal/cli/root.go runIndex`. A
  planless link would serve nothing yet outrank others as `DefaultUsable`.
- **`ProviderPriority` = `anthropic, openai, openrouter, ollama, ogx`** — ogx below ollama on purpose
  (`TestRegistryOGXPriority`). **`prompt_cache_key`** for ogx and any `ogx.ogcode.in` host is the **session
  identity for token spend** in oglab, not a cache hint; omit it and usage is unattributed.
- **Outgoing body capped at 4 MiB for OGX**: `OpenAIProvider.checkBodyFits` (`internal/provider/openai.go`,
  `ogxMaxRequestBytes = 4 << 20`, when `isOGX()` = provider id `ogx` or host `ogx.ogcode.in`) refuses with
  `provider.ErrRequestTooLarge`. oglab's `maxChatBodyBytes` is also 4 MiB and fails as
  `400 {"error":"invalid request body"}`, matched by `APIError.IsMalformedBody()`; both fatal non-resumable.
  **Raising it is three hops** — oglab's `maxChatBodyBytes` (router) **and** `maxChatBytes` (web) + nginx
  `client_max_body_size` (8m on the box, the next ceiling), then ogcode's mirror; ship the gateway first.
- **The composer downscales at attach time** — `prepareImageAttachment` in `web/src/lib/images.ts` (from
  `addFiles` in `web/src/components/prompt-input.tsx`): **1024 px long edge**, JPEG q0.8 for JPEG sources else
  WebP, byte-identical passthrough when ≤512 KB AND ≤1024 px (keeps small PNG/GIF incl. animation), original
  bytes on any decode/encode failure. `MAX_IMAGE_SIZE` (10 MB) is only the pick/decode limit. 768 renders 13px
  CSS text ~6px — use 1024 for screenshots. Measurements: `HISTORY.md`.
- **`redirect_uri` is loopback-pinned** (`http://127.0.0.1:<port>/api/ogx/callback`); oglab's
  `loopbackRedirect` refuses a non-loopback Host. Connect/disconnect both call `reloadProviders()`.
- **A JSON endpoint returning HTML = proxy routing bug**: `/v1/models` answered 200 `text/html` because nginx
  had no `/v1/` location (all to `oglab_web`); symptom "Connected, but your plan grants no models" beside an
  `Active · ogx` chip from `/api/ogx/status`. Fix = `upstream oglab_router` + `location /v1/` (proxy_buffering
  off, proxy_cache off, `gzip off`, read/send 3600s) **before** `location /`; the vhost is server-only.
- **The gateway emits an SSE keep-alive** (`internal/router/heartbeat.go`: `: keep-alive\n\n` every 15s —
  `heartbeatInterval` 15s, `upstreamSilenceLimit` 90s) once the upstream stream is relayed, removing
  Cloudflare's ~100s proxied idle cut. It stops after 90s of upstream silence so it cannot mask a hung
  upstream from the client's 120s `streamIdleTimeout`.
- **The gateway masks the vendor; the id leaks it.** `/v1/models` sends `owned_by: "oglab"` (never rendered).
  **OGX shows the raw id as the name**; other silent OpenAI-compatible hosts get the catalogue name — `if name
  == "" && p.id != OGXProviderID` in `fetchDynamicModels` (`TestOGXProviderShowsRawModelIDs`,
  `TestDynamicModelsFallBackToCatalogNames`). Empty `Collection` is pinned by
  `TestOGXProviderModelsComeFromTheGateway`.

**Settings tab (web)**: leads the strip (`slots()` unshifts), `readOnly`; connected renders a `ModelList` with
no-op handlers and `reloadModels()` after connect/disconnect. `planLabel()` mirrors `HasPlan`: Not connected /
**No plan** (warn) / Active · plan; Usage links `https://oglab.ogcode.in`.

- **The not-connected branch carries `data-setting` on its fallback `<div>`** — the shell hides a section with
  `[data-section]:not(:has([data-setting]:not([hidden])))` and this branch has no `<Row>`, so it vanishes
  offline.
- **Search trap**: `CREDENTIAL_TERMS` matched every slot and search opens the first match — "key" opened the
  plan tab. `slotSearchMatches(slot, q)` gates that vocabulary on `!slot.readOnly`. Gated by PostHog `ogx-tab`
  (fail-safe OFF); offline testing stubs `fetch` + `XMLHttpRequest` for `/decide` and `/flags` before
  `Page.navigate`.

**oglab (sibling repo, nothing here imports its `internal/`).**

- **The router's upstream circuit breaker ignores `400`/`422`** (`shouldGateUpstream` in
  `internal/router/gate.go`, from `proxyCompletion`): gating on any non-200 let one malformed client request
  lock the account's slots for 5 min and starve everything into 30s `queueWait` → 429
  (`TestAnAccountIsNotGatedOnAClientPayloadError`).
- The `pgx` (Postgres) plan is README-only there — do not implement it from an ogcode session. `pgx`'s default
  `QueryExecModeCacheStatement` breaks a multi-statement schema: use `default_query_exec_mode=exec` in the DSN.
  Embedded Postgres: `fergusstrange/embedded-postgres` `V17` = 17.5.0, unique per-test database names,
  `CachePath`/`Port`/`RuntimePath` set, and **close the `*sql.DB` before `Stop()`** or the pool hangs.

### Providers, models & resolution

- **`OpenAIProvider.Models()` is a pure read** — never network; returns the compiled-in fallback until a
  catalogue loads, and returns a copy. Fetching is `RefreshCatalog`, seeding `SetCatalog`, behind
  `CatalogRefresher`/`CatalogSetter` (`TestModelsNeverFetches`).
- `internal/modelcatalog` + migration 051 `model_catalog`; the server seeds at startup, refreshes in a
  background goroutine after bind and in `reloadProviders`, serialises concurrent refreshes and publishes
  `models.updated`. `Prune` is server-only.
- **Provider resolution is deterministic**: `orderedSnapshot()` sorts by `(providerRank, id)`;
  `ResolveProviderFor(modelID, providerID)` = custom model → explicit provider if still registered → catalogue
  lookup (preferring `ActiveByDefault`) → highest priority. Persisted as `provider` on session/plan/task
  (migration 047). **Trap**: a column added to a `SELECT` but not to `rows.Scan` is a silent runtime error.
  `lookupModel` compares the raw `m.ID`: a bare `deepseek-v4.1-flash` routes to ollama even while OpenRouter's
  prefixed `deepseek/deepseek-v4.1-flash` row is present.
- **Catalogue names are aliases, not new entries**: `normalizeModelID` lowercases, strips the org prefix,
  `:latest`/`:cloud`, the OpenRouter variant suffix, and folds separators; `newCatalogIndex` is
  first-claim-wins. A rename just adds its raw id to `Aliases` (`TestCatalogResolvesRealHostSpellings`).
- `gemini-4-argon` is the flagship: `MaxOutputTokens: 32_768` (its own API's write cap beats OpenRouter's
  65,536), `RejectsSampling`, high effort, implicit caching only. Deprecations and promo prices are in the
  catalogue header comment.
- **The Ollama slot lists only what the local endpoint serves** — `FetchOllamaCloudCatalog`/`mergeOllamaModels`
  and the `OLLAMA_CLOUD_CATALOG`/`OLLAMA_CATALOG_URL`/`OLLAMA_SHOW_URL` envs are deleted, `staticFallback`
  returns nil. `ollamaShowFill` still fills context windows from `POST /api/show`.
- **The community free pool is REMOVED** (`freepool.go`, `keys.json`, `OGCODE_FREE_KEYS_URL`,
  `handleFreeProviders`); `needsOnboarding` is hardcoded false. Fixtures with `ogcode-*` provider ids need
  priority-correct replacements.

**Request budget** (`internal/provider/budget.go`): a limiter, not a rate throttle.
`OGCODE_PROVIDER_MAX_CONCURRENT` default `8`, `interactiveRequestReserve = 2`, read once via `sync.OnceValue`;
unparseable or `< 2` warns and keeps the default. Index acquire takes `indexGate` then `total`; interactive
takes only `total` — the reserve stays reachable and the paths cannot deadlock. `processBatch` wraps its ctx
with `provider.AsIndexSession`; both `StreamChat`s wrap the body in `budgetBody` (idempotent Close releases
once). **`streamEvents` defer order is a concurrency invariant**: `defer close(ch); defer body.Close(); defer
cancel()` — LIFO runs cancel, body close, channel close; the old order woke the consumer on the closed channel
and held the previous body's slot → 429 on OGX's concurrency-1 tier (`TestStreamEventsClosesBodyBeforeChannel`).

**Network-failure recovery.**

- **`write tcp …: write: result too large` is ERANGE (34) from the LOCAL `write(2)`**, not the gateway: it
  reaches the UI via `send request: %w` in `internal/provider/openai.go` wrapping `client.Do`, so no HTTP status
  was received. On macOS the cause is network-intercepting software (VPN/TUN fake-IP; golang/go#79706); a
  source in 192.0.2.0/24 is the tell. **Fix: `sendStreamingRequest`** (`internal/provider/provider.go`, both
  providers) retries ONCE on a fresh connection — `isLocalWriteFailure` requires `syscall.ERANGE` AND a
  `*net.OpError` with `Op == "write"`, then `CloseIdleConnections()` + rebuild. Not in `isTransientNetErr`, so
  the loop neither backs off nor double-retries. HTTPS writes are already ≤16 KB TLS records.
- **A mid-stream RST is recovered only when no TEXT and no tool call exists** — streamed reasoning does not
  block it. `EventError` (`internal/agent/loop.go`) and the no-finish path guard on `streamTextPart == nil &&
  currentText.Len() == 0`; reasoning is safe to discard because it is never replayed and `DeleteMessage`
  cascades its parts. Any text still means Resume, never replay
  (`TestMidStreamReset_RetriesWhenOnlyReasoningWasProduced`).
- **IPv6 fallback counts ECONNRESET, not just unreachable errno**: `isIPv6PathFailure` matches
  EHOSTUNREACH/ENETUNREACH **and** ECONNRESET when `*net.OpError.Addr` is an IPv6 `*net.TCPAddr`; two strikes
  (`noteIPv6Failure`) narrow `dialStream` to `tcp4` until exit (`OGCODE_FORCE_IPV4=off` pins IPv6,
  `hasGlobalIPv4` gates the trip). The keepalive stop leaves a ~20s window where Cloudflare's ~100s idle cut
  fires before the client's 120s `streamIdleTimeout`.

### Token accounting

- The token pill reads a whole-transcript total from `GET /api/session/{id}/token` (`ListStepUsage` +
  `UtilityTokens`), not the client's newest page. `utility = input + cacheWrite + output`,
  `total = effective + cacheRead`; utility is a subset of the component rows.
- **`session.updated` must patch every session**, not just the active one — a title for a non-active session
  used to be dropped by the active-session guard. Safe because the effect keys on `server.eventTick`.
- **Title generation inherits the session model** — no "fast model" heuristic. Fall back only when the session
  names no model; with `model == ""` the code does not call `ResolveProviderFor`, so a test must set
  `defaultProvider` too.
- **Gemini `thought_signature` must be echoed verbatim** from `extra_content.google` on every replayed tool
  call, or the second tool turn 400s. Gate: `EchoesToolExtra()` is `effortHost() == hostGemini`.
- `usageFromOAI` reads a cache hit from `prompt_tokens_details.cached_tokens` when > 0, else
  `prompt_cache_hit_tokens`. Anthropic's `input_tokens` excludes cache read and creation. Utility usage: the
  five `session.utility_*` columns (migration 052); `AddUtilityUsage` increments in one UPDATE and no-ops on
  all-zero.
- **Ollama Cloud meter**: `GET https://ollama.com/api/usage` with `Authorization: Bearer $OLLAMA_API_KEY` →
  `limits.session.usage` (5-hour blocks), `limits.weekly.usage` (rolling 72h) as 0–1 fractions; no daily
  bucket. Free-tier concurrency is 1 — the real ceiling. Budget by output; cache reads are a 50× discount.
- `message.data.tokens.input` is UNCACHED input, so the billed prompt is `input + cacheRead`; `cacheRead` is
  absent (not 0) on uncached turns. Query: `HOWTO.md` → Inspect the session database.

### Permissions

- `internal/permission/store.go`: `ModeAsk`/`ModeAuto`/`ModeYolo`. **Yolo only changes what would have been
  Ask** — an explicit `Deny` is evaluated first and still denies, and the risk classifier is never reached. The
  bash `isDangerousCommand` denylist (`internal/tool/bash_safety.go`) is a separate layer.
- **Auto mode's risk gate needs room to think**: `riskLLMMaxTokens = 1024` (calibration 8 → 3/16 verdicts, 512
  → 14/16, 1024 → 16/16). An unanswered check warns and returns Ask, and is not cached; answered verdicts are.
- `internal/permission/risk.go`: `rm`/`sudo`/`git push`/pip-to-shell = Ask; read/build/test = Safe; the middle
  (`mkdir`, `git commit`, `cp`, `chmod`, `go mod tidy`, `npm install`, `curl`, unknown) = Unclear, the only
  tier reaching the LLM.
- Grants and the mode are **machine-wide** in `~/.ogcode/config.db` (`permission_grant`, `permission_mode`;
  migration 048). A grant's grain is the exact command/path/skill, never `"*"`; last-toggle-wins; no UI, so
  revoke by editing the DB.
- **Trap**: `internal/server/serve_lifecycle_test.go` does not set HOME, so a plain `go test ./...` advances
  the real `~/.ogcode/config.db`. Isolate HOME **and** run from a temp working dir.

### Agent memory files

- **Caps: none by default.** `OGCODE_MEMORY_MD_MAX_BYTES` / `OGCODE_AGENT_MD_MAX_BYTES`; unset/empty means the
  whole file and `truncated` false. In `truncateForPrompt` (`internal/agent/mdblock.go`) `limit <= 0` means
  **uncapped**; a positive budget is head-biased — curate instead.
- The admission rule is enforced by `memoryMDPrompt` (`internal/agent/prompt_builder.go`,
  `TestMemoryMDPromptSavesFactsNotSessionNarratives`). `staticSystemPrompt` (`internal/agent/loop.go`) carries
  **one constant pointer per file, never content or a heading index** (`TestStaticSystemPromptNeverInlinesHowToMD`,
  `TestBuildSystemPrompt_HistoryMDSectionIsAPointer`).
- Turn summaries stay with the scribe; procedures never go there. `buildTurnDigest` carries tool names and the
  final response only — no results, no reasoning — so a procedure would be derived from claims, and the store
  is append-only with no repair loop.

### Prompt caching, read pressure & context windows

- **Prompt caching**: Anthropic takes explicit `cache_control` on the last tool def and the first system block
  (min prefix 1024 tokens, 4096 on Opus 4.5+; 5-min TTL); OpenAI caches prefixes automatically. Entry [0] stays
  byte-identical: the date rides `systemReminderPrompt` as a later entry, `osEnvPrompt` is in the cached prefix.
- `internal/agent/read_pressure.go`: `OGCODE_READ_PRESSURE_THRESHOLD_TOKENS` (default 40000, clamped 2000–1M);
  `OGCODE_RESEND_COST_WINDOW_MULTIPLE` (default 1, a multiple of the context window, fallback 128000).
  Reminders are a directive, never capped; `contentReturningTools` excludes `write`/`edit`; `reset()` runs on
  real compaction only.
- **Message history is TURN-SCOPED**: `RunLoop` sends only `watermark.sliceTurn(messages,
  findLastTextUserMessageIndex(messages))` (`internal/agent/loop.go:780-785`), so a PRIOR turn's images are
  never re-sent — only `<previous_response>` text and the compaction summary cross the boundary. Within the
  turn every step rebuilds that request, so the prompt image and every tool image (`view_image`,
  `read_pdf_page`, a rendered LaTeX page) repeat; Anthropic's last-message `cache_control` breakpoint turns the
  repeats into cache reads. The full-history `convertMessages(messages)` fallback runs only when no text user
  message exists (`continuity_e2e_test.go:86`).
- **The turn's original prompt and images survive compaction**: `compactionWatermark.sliceTurn` keeps
  `messages[turnStart]` at index 0, `prependCompactionSummary` folds the summary in and preserves
  `out[0].Images`, `stripCompactionSummary` restores the raw prompt for `llmCompact`
  (`TestSliceTurnKeepsTheTurnPromptAtIndexZero`,
  `TestPrependCompactionSummaryPreservesExistingTurnPromptAndImages`). A compacted step's **tool** images
  (`view_image`, `read_pdf_page`) do NOT survive — the agent must name them in the summary or re-read.
- **The ask_user Q&A must survive verbatim**; the clause is conditional (it also reaches agents without
  `ask_user`, since `canCompactContext` is `HasTool("read")`) and appears on all three surfaces:
  `compactContextPrompt()`, `CompactContextTool.Description()`, `const compactDirective`
  (`TestCompactContextRequiresTheUserAnswersVerbatim`).
- Mid-turn compaction: `OGCODE_COMPACT_CONTEXT`, **unset or empty = ON**, falsey (`0/false/no/off`) = off.
  `compactionThresholdTokens(window)` = window − reserve, unknown window → `fallbackMaxRequestTokens = 128000`;
  `maxCompactions = 2` per RunLoop, `compactionKeepRecent = 12`.
- **Learned context windows**: `provider.ParseContextWindowFromBody` walks ordered patterns (OpenAI, Anthropic,
  Gemini, generic) in a 4096–10M band; the loop keeps it only when `learned > promptTokens` and persists via
  `session.LearnModelContextWindow`. Resolution is catalog → learned → 0. `SetModelCapability` is a merge
  upsert, so an image probe writing 0 does not erase a learned window. `loop.compacted` is published only by
  `compactRequest`.

### Agent tools & prompt

- **`update_intent` is the FIRST tool call** (build agent, alone in the first block of every turn): state the
  plan before `codebase_map`, grep or any read. `internal/agent/intent_gate.go` appends `intentGateReminder` to
  the first result that is not `update_intent` when no plan is set (`Metadata["intentReminder"]`); a denied
  call spends nothing. Pinned by `TestIntentGate*`, `TestUpdateIntentDescriptionPutsThePlanFirst`.
- `update_intent` is a whole-list replace of 1–7 steps; `normalizeIntentStatus` folds natural words
  (`in_progress`, `complete`; `doing` deliberately unmapped). Persisted in `session.intent` (migration 064),
  cleared at turn start, published as `intent.changed`. The result **echoes the plan back**, with a nudge that
  self-extinguishes once nothing is active; a fully closed plan's tail never invites another call
  (`TestUpdateIntentOutputEchoesThePlan`).
- **The matcher** runs ONE routing completion per block when the revision adds a step it has not routed yet —
  no cap per turn or per step (the old 4-call ceiling and `OGCODE_INTENT_TOOL_MATCHES` are gone; `MaxTokens`
  unset). It scores over skills plus one entry per MCP SERVER; a restated plan or status flip spends nothing;
  failures are silent; counts ride the intent JSON (`matchedSkills`/`matchedMCPs`). **A step with no
  `description`, or not pending/active, is never routed** (`describedSteps`,
  `internal/agent/intent_tools.go:201`). `formatIntentHint` asks the agent to act or say in one line why not;
  nothing *checks* that a named skill loaded or MCP tool ran — wording only.
- **Chip identity is the step LABEL, not its description** (`intentStepIdentity`, case/whitespace folded): a
  description-keyed map dropped every chip exactly when a step finished. Counts are never removed on
  consumption — a chip stays until the step leaves the whole-list replace, its label is reworded, or the next
  `RunLoop` clears the intent (`loop.go:176`). Adding a step re-scores every described step; `remember` writes
  only the keys present (`intent_tools.go:262-275`).
- **The build agent has no upfront skill or MCP list**: `turnTools` withholds the `mcp_*` glob and
  `skillGuidancePrompt` is gated, while registration is never gated. A bare `skill` call is refused in a
  matcher session; the `mcp_*` glob stays in the allowlist for `HasTool` expansion. All per-turn, never entry
  [0].
- **ask_user** takes up to 5 questions per call; `canAskUser()` is build-only and also needs permission gating
  (server-only) + a Questions manager — headless closes it at three levels. It is a question CHANNEL, not a
  last resort; a blank answer to a content question means unanswered — do not invent one. Conditional screens
  use `Question.ShowWhen` with `Condition.Match`/`AnswersByID`/`Visibility`, evaluated live.
- **Edit contract**: `edits` is the only accepted form (legacy flat args refused); a no-op edit fails;
  `notFoundHint` quotes the file's own bytes plus the line number; the read line prefix is `|`. `bash` appends
  a status sentinel (`[exit status N]`, `[command timed out]`, `[command canceled …]`).
- **pathlock** (`internal/tool/pathlock.go`): a per-path mutex acquired before the read in both write and edit.
  Same-file edits may batch; the ONE forbidden pair is write-vs-edit on the same file, pinned literally in
  `parallelToolCallsPrompt` and the hard rules. **Same-file ordering keys on ONE path**: `sameFileWaits`
  (`internal/agent/tool_order.go`) extracts only a top-level `path` from
  `read`/`file_map`/`check_syntax`/`edit`/`write`, so a `paths: []` batch form would silently lose the
  guarantee — thread every path through it.
- `tool.DecodeArgs` rewrites integral float literals to ints before unmarshal; integer-bound schemas declare
  `"integer"`.
- **Rich output**: every option is a fenced block holding a declarative spec (mermaid, plotly/rough JSON, a
  whole LaTeX doc, raw HTML). `motion` was declined — imperative, no spec, no Solid adapter. HTML is extracted
  before DOMPurify into a sandboxed iframe; every diagram gets a zoom button via idempotent `attachZoom`.
- **Decision summary**: the build agent ends with `## Decisions & why` only when real decisions were made; the
  memory scribe preserves it; TaskAgent gets nothing.
- **`read` windows have ONE spelling**: `ranges: [{start_line, end_line}]` (1-based inclusive; several =
  fan-out via `renderRanges`). Top-level `start_line`/`end_line` are refused by name
  (`TestReadTool_StartEndLineArgsAreRefused`); `offset`/`limit` stay paging and cannot combine with ranges.
  Residual silence: an unknown key (`range:`) or misspelled inner key (`start`) is dropped by `DecodeArgs`'s
  plain `json.Unmarshal` and degrades to a whole-file read.
- **Images**: `convertMessages` strips tool-result images when the model lacks vision;
  `APIError.IsImageRejection` marks it resumable; MCP image output goes to a temp file, not inline.
- **Skills** may declare `requires:` env names (`Skill.Requires`, `MissingEnv`, checked in
  `internal/tool/skill.go`, body withheld). `skills.env` in `ogcode.json` fills them via `setIfUnset` (a real
  env var wins) and reaches worktrees through `cli.Execute()`. Traps: `parseRequires` must strip comments
  before splitting; a backtick inside `embedded.go`'s raw string must be split.
- **codemap / map tools**: `codebase_map`/`memory_map` render a folder with more than 10 descendant files as
  ONE line (top 40 labels + count); `subdir` re-roots in full. Both budgets are 100 KB via
  `Result.Truncated: true` (opts out of `capToolOutput`). Ladder {30,40}→{10,10}→{3,3}→{1,1};
  `tool.MaxLabelsPerPage = 30` is the single source. Labels are a **spread-aware sample**, not top-N.
  `dirPrefixFilter` must carry `ESCAPE '\'`. Project-scope tests pass
  `WithRecallScope(…, {Scope:"project", ProjectID:"/proj"})`. codemap: HTML naming is a kind-walk; CSS patterns
  stay UNANCHORED and an at-rule's first word names it. **HCL is outlineOnly** — never syntax-checked
  (`TestHCLIsNotSyntaxChecked`). `file_map`'s description lists every parsed language
  (`TestFileMapDescription_NamesEveryParsedLanguage`). A Swift `as?`/`as!` cast + newline + `??` misparses:
  parenthesise the cast.

### MCP & search

- **`OGCODE_SEARCH_ENABLED=false` is not a deep_search switch**: `internal/cli/run.go` then leaves
  `searchBridge` nil, so `web_search`, `fetch_page` AND `deep_search` all leave the tool set and the prompt. To
  suppress only deep_search, add a prompt clause (`benchmark.py --no-deep-search`).
- **MCP**: `internal/mcp/` on the official SDK. Transport is INFERRED from which keys are set (`Command` ⇒
  stdio, `URL` ⇒ streamable-http; an explicit `"transport"` overrides) — **there is no `type` key**. Tool ids
  are `mcp_<server>/<name>`; the `mcp_` prefix is load-bearing. Connect is lazy, after bind. A URL server with
  no `headers` gets OAuth (DCR + PKCE + one shared loopback receiver; tokens in `~/.ogcode/mcp-tokens/`, 0600);
  `headers` means static bearer. Penpot's `…/mcp/stream` is streamable-http: forcing `sse` gives 400.
- **Search**: results carry a `Provider` stamp set BEFORE the TTL cache (`tavily`/`native`/`safari`/`chromium`);
  prove one with `HOWTO.md` → Prove which search backend served a request.
- Search config is `/settings/search` (migration 058: `fetch_top_k` 4, `page_chars` 6000, `browser` `''`);
  `handleSetSearchConfig` is a FULL-STRUCT replace, so the UI must send every field. `""` = HTTP first with
  browser fallback; `safari`/`chrome` = browser first. `NewBrowserBackend()` is per-OS, so `chrome` on macOS
  means Safari and either value is a no-op on Linux. The UI hides backends the host cannot run using
  `server.os()` from `/api/config`.
- **Browsers**: macOS prefers headless Chromium (`--dump-dom --virtual-time-budget`, temp-profile pool of 3,
  `--disable-blink-features=AutomationControlled`), falls back to Safari (`osascript`, real cookies, max 3
  windows; needs Automation permission and "Allow JavaScript from Apple Events"). Windows Chrome/Edge is built
  but unverified; **check it with `GOOS=windows CGO_ENABLED=0 go build ./internal/search/`**.
- **Deep research**: `searchFetchTimeout` 45s with an 8s straggler grace (a 15s stage timeout used to expire
  the ctx before the browser fallback could launch). `isJunkResultHost` blocks `google.*` account and product
  subdomains, not technical ones (`cloud.`, `developers.`, `firebase.`).

### Data, migrations & transcript

- **Migrations**: goose keys by VERSION NUMBER only, so a number taken by a branch that never landed silently
  SKIPS your file. Check `git log --all --oneline -- internal/db/0NN*` AND the real DB's `goose_db_version`.
- **The transcript is append-only** — nothing prunes messages or sessions. The only per-row bound is
  `capToolOutput` (50 KB, skipped when a tool sets `Result.Truncated`); the only deletes are `Store.Delete` and
  `DeleteMessage`. Compaction changes what is re-sent, never what is stored.
- **FK 787 mid-turn ⇒ the session row was deleted by ANOTHER writer.** `part`→`message`→`session` cascade: one
  `DELETE FROM session` site (`internal/session/store.go:234`); `handleDeleteSession` cancels `s.running`,
  deletes the maps, then waits up to `loopStopTimeout` (5s) on `runningDone` before `store.Delete`, so an
  in-process delete ends the turn as `context canceled`, never FK. **Handled**: `CreateMessage`/`CreatePart`
  wrap FK-787 as `session.ErrSessionDeleted` (`internal/session/errors.go`); `Store.Get` returns `(nil, nil)`
  for a missing row; `LoopRunner.sessionDeleted` is checked at step start and before every tool run and
  tool-result write; a part-insert FK sets `sessionLost` and stops the stream. `classifyInterruption` maps
  `ErrSessionDeleted`, `provider.ErrRequestTooLarge` and `IsMalformedBody()` to `InterruptFatal`.
- **Session DB**: `.ogcode/ogcode.db` (project) and `~/.ogcode/config.db` (global); `message` is metadata,
  content is in `part.data`; a seeded `message.data` needs `id`/`sessionId`/`createdAt`. Queries:
  `HOWTO.md` → Inspect the session database.
- **`GetMessages` must return `Parts` as `[]`, never nil.** Pagination is `?before=<id>` + `X-Has-Older` at
  300 rows, merged by `web/src/lib/transcript.ts`.

### Web UI: composer, sidebar & navigation

- **Composer drafts/attachments are per session**: a module-scope `drafts` map keyed by session id — the effect
  stashes, `delete`s then assigns, so `onCleanup` revokes each URL once.
- **The running pause glyph IS the stop control** (`session.abort()`; `is-stop` hovers to `--danger`), and the
  `+` attach menu renders in a `Portal` — the composer's `rounded-[1.25rem]` would clip a fixed popover, which
  also needs a mobile bottom-sheet override.
- **The plan-mode flag gates only the warning banner**; `ogcode plan` is registered unconditionally. Mode comes
  from `GET /api/mode`, not `/api/config`. Banner ladder: plan `top-4 z-[151]`, git-sync `top-16 z-[150]`,
  desktop-notification `top-4` in build and `top-28` in plan. Dismiss key `ogcode-plan-mode-banner-dismissed`.
- **The session-row dot reports task status, not the provider**: `session.status` (migration 062,
  `StatusRunning`/`StatusFailed`, `ResetRunningStatuses()` on boot) riding `session.updated`. A hand-seeded
  session row must set `permission` or `Store.List` 500s.
- Sidebar width/pins are localStorage only: `ogcode-sidebar-width` (208–420, default 260),
  `ogcode-pinned-sessions` (no server field). Skills is removed from both sidebars; Settings → Skills stays.
- **`⌘K` is guarded on `offsetParent`** — `SidebarShell` renders its children twice (desktop + mobile drawer)
  and both would fight for focus.

### Web transcript rendering

- **Virtualization** (`message-list.tsx`): pad-based windowing keyed by message id. Unmeasured rows are
  estimated from running per-role totals over EVERY row measured so far — averaging the mounted window made
  window and estimate chase each other (~1,000 remounts/s). `scrollTop` is updated synchronously in a handler
  registered after chat-scroll's, and `measure()` moves it by the top pad's delta in the same batch.
- Scroll memory is per session (`scroll-memory.ts`); **the id-change effect must call `restore()` too**, and
  chat-scroll's ResizeObserver must not `holdAnchor` when the list is `anchoredByList` — double-writing
  `scrollTop` in one frame was the upward-scroll jitter.

### Theme, contrast & light mode

- **Light mode is server-global**: migration 063 `ui_prefs`, `internal/session/appearance_store.go`
  (`NormalizeThemeMode` folds unknown to `system`), `internal/server/appearance_routes.go` serves
  `GET/POST /api/appearance` off **`s.globalDB`** — so a throwaway rig on another port POSTs the USER's live
  machine-wide mode. Save `GET /api/appearance` before an audit and restore it after (localStorage only masks
  the change in a browser that already carries `ogcode-theme-mode`). `web/index.html` runs a pre-paint inline
  script off `localStorage['ogcode-theme-mode']`; `context/theme.tsx` holds `mode()`, `resolvedMode()`,
  `setMode()` and a live `matchMedia` listener. **Settings General is `/settings`, not `/settings/general`.**
- **Muted-text floor rule: 4.5:1 on the LIGHTEST surface the token can sit on** — dark `--bg-hover #222226`,
  light `--bg-hover #e9e9ee` (light's `--bg-overlay #f0f0f3` is NOT the worst). Dark `--text-muted` =
  `--text-label` = `#8a8a93` (4.63 hover / 5.59 surface), `--text-tertiary #8b8b94` (4.69, one step brighter
  on purpose), zinc-500/600/700 `#94949d`/`#8f8f98`/`#8a8a93`; light zinc-500/600/700
  `#5f5f67`/`#63636b`/`#686870`, light text tokens stay #67676f. Rejected: darkening `--bg-hover` (weakens
  hover feedback and cannot lift zinc-600 at all). History and audit: `HISTORY.md`, `HOWTO.md`.
- **The default accent is brand orange `#ff5e1f`**, in four places: `theme.tsx DEFAULT_THEME`, `index.css
  :root`, `settings/general.tsx DEFAULTS`+`PRESETS`, `deriveTheme` in `theme_routes.go`.
- **Accent ink is its own PER-MODE token, never `--accent`**: one colour cannot be both a fill and text.
  `Theme.AccentInk`/`AccentInkLight` → `--accent-ink-dark`/`-light` (`applyThemeCSS`) → `--accent-ink`, with a
  `:root[data-theme="light"]` override; 82 sites use `text-[color:var(--accent-ink)]`. `deriveTheme` derives it
  per surface set (`contrastRatio`/`compositeHex`, incl. the `--accent-soft` wash), `inkStep` 0.005.
- **`onPrimary` is `labelFor`, not a luminance crossover**: white vs black by the max of the MINIMUM ratio over
  `--accent` AND `--accent-hover`, then `clampForLabel` nudges lightness by `labelStep` 0.0025 until AA holds.
  `accent`/`onPrimary`/`accentInk` are **recomputed on read** in `handleGetTheme`'s else-branch, so stored
  themes are retroactively clamped. Pinned by `internal/server/theme_routes_test.go` (`TestDeriveTheme_*`,
  `TestLabelFor_*`) over 10 presets.
- WCAG: 22/22 routes clean under axe 4.10.2 (details in `HISTORY.md`).

### Preview, public serving & device panel

- **The Preview page lists only this project's announced ports**; the lsof scan and transcript mining are
  deleted. An announcement is the hostname form only — `agent.AnnouncedPreviewPorts(text)` matching
  `<port>.<PreviewDomain()>`. `LoopRunner.recordAnnouncedPorts` runs at every turn end for `agentID == "build"`,
  reads **PartText only** (reasoning was the ~30 phantom tiles), writes `announced_preview_port` (migration
  054). The add-port form is removed; `?ports=` still exercises the manual path.
- **Live preview is host-based**: `http://<port>.<preview-domain>:<server-port>/`, default `preview.localhost`
  (`OGCODE_PREVIEW_DOMAIN`). The proxied app sees `pathname === "/"`, so Next/Nuxt/SvelteKit boot unchanged.
  `previewHostDispatch` (after `corsMiddleware`) proxies via `Rewrite` to literal `127.0.0.1` and
  `ModifyResponse` strips `X-Frame-Options` + the CSP `frame-ancestors` directive — **without that any
  frame-guarded service renders blank**. Legacy `/preview/<port>/` is 301-only.
- **The server's own port must be written** in a preview URL: portless `http://3000.<domain>/` goes to :80
  (`TestBuildAgent_SystemPrompt_ContainsPreviewServing`). The Chromium-only note is emitted **only for a
  `.localhost` domain** (`TestPreviewServingPrompt_BrowserNoteOnlyForLocalhost`). Use the DASHED ip form with
  nip.io/sslip.io (`192-168-1-50.sslip.io`) — the dotted form lets a port ≤255 steal an octet.
- `/public` serves `<data-dir>/public` through `http.StripPrefix("/public", …)`; **HEAD must be mapped
  explicitly** (chi does not auto-map). No ETag, no Cache-Control; a bare `/public/` lists the directory. The
  server origin enters the prompt only for the build agent: `serverURLPrompt(origin)` appended after the skill
  entry so entry [0] is untouched; `requestOrigin` validates the Host byte by byte
  (`internal/server/request_origin_test.go`).
- **scrcpy panel**: SPA `/device`; ws-scrcpy deep links need all four of `action`/`udid`/`player`/`ws` (`wss`
  on https); `GET /api/scrcpy/{status,devices}` shells `adb devices -l` with a 5s timeout and skips
  present-but-not-executable SDK candidates. **New URL-writing controls must use `resolvedUdid()`, never the
  raw `udid()`** (empty on a bare `/device`).

### Indexing & the document index

- Auto-index is gated per project by file count (`OGCODE_AUTO_INDEX_MAX_FILES`, default 500, env-only) plus
  consent (migration 061, `internal/docindex/consent.go`, `docindex.consent` event, web dialog). **Manual builds
  are never gated**; CLI/headless unaffected; a count ERROR logs and indexes anyway.
- The post-turn refresh uses mtime (`doc_page_index.mod_time`, migration 049); `OGCODE_AUTO_INDEX` is the kill
  switch. The guard is `agentID == "build"` (a task session is stored `SessionType:"build"`) **and** session
  `Directory == ProjectID`, because a task session works in a git worktree.
- Auto-index must run on the finished turn's resolved model AND provider; empty values fell back to
  `DefaultUsable` (and a 402). Threaded as `LoopRunner.AutoIndex(dir, model, provider)` → `Indexer.WithProvider`.
- **`.ogcode/` is skipped unconditionally** (`const StateDirName`, same shape as `.git`) in `collectFiles` and
  `findNestedGitignores`; no rule can re-include it.
- Default excludes are 38 entries (`internal/docindex/excludes.go`) matched on directory names and file
  basenames anywhere; a name earns a place only when it is generated AND holds indexable files. The seed is
  marker-based (migration 050); `IsDefaultExclude` derives the badge at read time.

### Controlplane, remote workers & Incus

A **nested module** at `controlplane/` (`replace … => ./controlplane`) building `ogcode-control-plane` — it stays
separate because ogcode's `internal/` already has `bus` and `config`. Build/test it alone
(`cd controlplane && go build ./...`), no CGO.

- **One in-process `server.Server` per worktree**, hosted by the worker daemon — one process serves sessions and
  the tunneled UI per dir, removing two writers on one DB. `Serve(ctx)`/`Stop()`/`Port()` replace `Start()`;
  `Port()` reads `listener.Addr()` after bind so `port:0` surfaces. `NewWithOptions(port, dir, mode,
  Options{NoBrowser, Loopback})` (`internal/server/serve_lifecycle_test.go`); `s.port` is `port atomic.Int32`
  and `stopCh` is guarded by `stopMu`.
- **`internal/server/host_session.go`** holds the seams (`HostSession`, `Guidance`, `ReplyPermission`,
  `SubscribeEvents`, `SessionRow`, `SessionPort`). **`HostSession` must `store.Get` first** — `Store.Create`
  violates UNIQUE on re-delivery.
- **Eager tunnels**: after Hello each workspace root gets a per-dir server + `tunnelAndRelayOnce(dir, sh)`,
  deduped via `tunnelled.LoadOrStore`; failures logged, never fatal; a late dir wires on first StartAgent.
  `yamux.Session.Open()` takes no ctx.
- **Per-dir routes**: `TunnelChunk.route`; master key `<workerID>-<route>`, empty route = legacy bare id.
  `worktreeLabel(dir)` = lowercased sanitised base (cap 63, empty → `"w"`); `filepath.Clean` before `Base`.
  Lookup is **exact whole-label, no hyphen-split** (`validWorkerID` allows hyphens).
- **Stable worker id**: `~/.ogcode/worker-id` (atomic rename, 0600) sent as `RegisterRequest.worker_id`; master
  adopts well-formed, mints on empty, `InvalidArgument` on malformed. `validWorkerID` = RFC 1123 label
  (`TestRegisterHonorsStableWorkerID`). **Reconnect**: `internal/worker/cred.go` persists token + expiry to
  `~/.ogcode/worker-cred`; `Run` retries 1s→30s, reuses an unexpired token, re-pairs on `Unauthenticated`.
- Stream RPCs (`Guidance`/`ApproveTool`/`RejectTool`) are the live command path: the master resolves
  `hostServerFor(sessionID)`; `loop.done` drives `sessions.remove`.
- **Superseded — do not resurrect**: a master inside the ogcode server, `proto/master.proto`,
  `MasterConfig`/`WorkerConfig`, a master-side REST/SSE panel, `master`/`worker` keys in `ogcode.json`.
- Proto `controlplane/proto/controlplane/v1/controlplane.proto`; **no seq on SessionEvent** (the relay assigns a
  fresh master seq). Codegen `cd controlplane && PATH="$HOME/go/bin:$PATH" make generate` (buf 1.72.0);
  `buf.yaml` excepts `RPC_REQUEST_RESPONSE_UNIQUE`.

**bbolt registry** (`controlplane/internal/registry/store.go`; buckets `workers`/`tokens`/`sessions`;
`config.MasterConfig.DBPath` default `~/.ogcode-control-plane/controlplane.db`, 0600).

- **A byte-truncated bbolt file SEGFAULTS at `bolt.Open`**. Validate first: `registry.checkDBFile` checks the
  meta page (magic `0xED0CDAED`, version 2, at the 16-byte page-header offset).
- **Restore sets `status=offline` + `LastSeen=boot time`** — a persisted LastSeen is past, so `ReapStale` would
  kill every restored worker on the first tick. `Registry.Add` re-keys `tokenIndex`, never `sessions`.
- **Accounts**: a `users` bucket + `auth.NewGate`/`NewLockedGate` (Disabled/Legacy/Accounts/Locked). A removed
  user's sessions break at validate time (`UserExists`, fail-closed); unknown users still pay a bcrypt round.
  Total registry loss brings the gate up **Locked**, never open. Passwords from
  `$OGCODE_CONTROL_PLANE_USER_PASSWORD` or `--password-file`, never argv. **The cookie key is per-process** —
  operator cookies die on master restart, deliberately.
- **Workspace allowlist**: cookie `"<expiry>|<userID>|<wsFP>"`, `wsFP` = 16 hex of sha256 over the sorted list;
  fail-closed. `Server.workspaceAllowed` + `tunnelMatchesAllowlist` match route label, workspace name, or a
  segment-aligned path suffix. `AssignUser` sets `Repo` and pins `Workspaces=[userWorktreeSlug(name)]`;
  **admins are refused**. Worktree-per-user is designed only (`plans/MULTI_USER_REPOS_PLAN.md`);
  `repoSlugFromURL` ≡ `safeRepoName`.
- **`/__operator/sessions`**: worker (ONLINE only), workspace (**SELECT over that worker's advertised dirs**),
  optional repo URL, agent, first prompt. Containment is enforced both sides.
- **Console**: fixed left nav, mobile drawer ≤860px; body-only templates into a `strings.Builder`
  (`internal/master/chrome.go`). **Live Session Monitor** `/sessions/<id>` + `/events` (SSE subscribes BEFORE
  writing headers, filters `sessionId`, 15s ping); an unrouted session 404s.
- **Security pins**: the worker-subdomain operator gate never contacts the origin (HTML → login, else 401);
  `TestWorkerStreamRoundTrip_ConcurrentWorkers`; `TestTunnel_FirstFrameRejects` (re-`Add` the same worker id to
  expire a token, no sleep). `t.Cleanup(clientConn.Close)` needs a func wrapper; httptest must be h2c-wrapped.
- **Incus** (`plans/INCUS_WORKERS_PLAN.md`): one container per user-repo assignment, name = `~/.ogcode/worker-id`.
  Driver `github.com/lxc/incus/v6/client` behind a `Driver` iface; `SkipGetEvents: true`. A macOS unix-socket
  path ≤104 bytes. Placements in a bbolt bucket. Gotchas: masterURL = bridge IP 10.0.192.1; Docker's nft
  FORWARD swallows incusbr0 (DOCKER-USER rules); **bbolt is single-writer — stop the master before `users
  add`**; cloud-init needs `/dev/lxd/sock` → `/dev/incus/sock`; `incus exec` has no `--timeout`.
  `controlplane/docs/deploy.md` describes ONE worker; text showing `ogcode serve --port 9595`, `--serve-addr`,
  or `ogcode-serve.service` is stale.

### Infrastructure, feature flags & telemetry

- **The ogcode.in homepage is `docs/index.html`** — one hand-written file with inline CSS, unlike `docs-site/`.
  Desktop nav `.nav-menu`, mobile sheet `#navSheet`; a new link goes in both. Breakpoints: 1180 CTA tiles, 1000
  star count + 4-col cells, 900 links swap to the burger. The `#skills` section is scoped to itself with
  `sk-`-prefixed classes.
- **`docs/DEPLOY.md`** is the deployment guide (SSH tunnel, reverse proxy + HTTPS/auth, Docker; no VPN).
  `ogcode serve` binds **all interfaces** (only `--port`/`-p`) with **no auth** — middlewares are
  RequestID/previewHostDispatch/RealIP/accessLog/recoverer/cors. A proxy MUST NOT buffer (`proxy_buffering off`,
  `flush_interval -1`). Cross-machine previews need `OGCODE_PREVIEW_DOMAIN` + wildcard DNS/TLS.
- **`blog/` and `docs-site/` stay separate Astro projects** — one `base` each (`/blog`, `/docs`); `docs-site`
  writes `outDir: '../docs/docs'`. `docs-site/src/lib/nav.ts` is the **single source of truth** for sidebar,
  pager, landing cards and sitemap — **a page is in the docs exactly when listed there**, links full
  `/docs/...` (`trailingSlash: 'always'`). `install.mdx` is `.mdx` because `.md` drops its raw script. Search is
  Pagefind via `postbuild.mjs`; `package-lock.json` must stay in sync — `pages.yml` runs `npm ci`.
- **Brand button token**: `--brand-btn: #d1440c` (white on `#ff5e1f` is 3.06:1), in all three stylesheets
  beside `--brand-line`.
- **Cloudflare**: `ogcode.in` (zone `ab21935c…`) — apex A ×4 to Pages, `www` CNAME →
  `prasenjeet-symon.github.io`, `ogx`/`oglab` A → `162.19.81.108`, `ogcode-dev` CNAME → the tunnel. `ogcode.xyz`
  is fully removed. **Origin port**: nginx answers only on **40009** (a co-hosted Caddy holds `:80`), so
  `ogx`/`oglab` need an `http_request_origin` rule with `origin.port=40009`. The vhost is server-only — back it
  up first. **Box access is password-only**: `sshpass -e ssh -o StrictHostKeyChecking=no -p 20009
  root@162.19.81.108`, `SSHPASS=$(SUNOFLOW_SSH_PASSWORD)`; `scp -P 20009`. Editing `sites-available` is enough,
  then `nginx -t && systemctl reload nginx`.
- **PostHog project `497411`**: ingestion token is the public `phc_…`; REST needs the `phx_` key
  (`POSTHOG_PERSONAL_API_KEY`, `POSTHOG_HOST=https://us.posthog.com`), always `/api/projects/497411/…`. The
  website has autocapture ON; the app OFF, typed events only from `web/src/lib/analytics.ts` — **never send
  content**, only lengths and counts. The server reports boot/stop as `server-<hostname>`. **Headless browsers
  drop every event** (UA `headlesschrome`, `navigator.webdriver`) — override the UA and patch `webdriver`.
  `posthog.identify()` needs an explicit id (`web/src/lib/posthog.ts` mints `ph_ogcode_distinct_id`).
- **`ogcode_turn_resources`** is one event per agent turn: `LoopRunner.TurnResources(agentID)` →
  `Sampler.BeginTurn()`/`EndTurn()` (`internal/resource/sampler.go`) around `RunLoop`, so the sampling goroutine
  is held awake (it otherwise suspends at `watchers <= 0`). Gated by `turnResourceAgents` in
  `internal/agent/loop.go` (`build/plan/note/task` only — every other RunLoop nests inside one and would double
  count; `internal/agent/turn_resources_test.go`). `distinct_id` is `s.installID`, not `posthogDistinctID()`.
  Values are PROCESS-wide and coarse: only `os`/`arch`/`host_ram_mb`/`cores` are machine facts.
- Server logs → PostHog Logs over OTLP/HTTP (`internal/logging/posthog.go`), **opt-in** via
  `OGCODE_POSTHOG_LOGS`; `DO_NOT_TRACK` overrides. Remote floor Warn; NAMED loggers only; attributes pass
  `redactAttr`/`Scrub`.
- **Install-id stitch**: `VAR=x cmd | sh` does not pass `VAR` to the piped `sh` — use
  `export OGCODE_IID='<id>'; curl … | sh`. Hops: homepage copy → `~/.ogcode/install-id` → `ogcode_installed` →
  `installId` in `/api/config` (`HOWTO.md` → Trace the install-id stitch). Funnel numbers are a floor.
- **Feature flags**: `internal/server/feature_flags.go` fetches every flag (`ogx-tab`, `notes-feature`,
  `device-panel`, `plan-mode`) in ONE `/decide` per refresh into `map[string]bool`, reuse window TTL/2,
  fail-safe OFF, publishing `<flag>.changed` only on change. The web reads the cached value from `/api/config`
  and re-reads on `reconnectTick`. **The gates differ**: notes also drops its prompt section (`lr.notesOn()`
  must be an `*atomic.Bool` — a value field fails `go vet` copylocks) and 404s its routes per handler;
  device-panel 404s routes and the proxy separately; plan-mode gates only the banner. In a Serve test a bare
  `flag.Store(true)` is racy — call `stubFeatureFlagsOn(t)`.
- **Project-type detection** (`internal/server/project_type.go`): one dominant label per project from ~30
  extensions, reported once as `ogcode_project_detected`; marker `<dir>/.ogcode/project-reported`, and **no
  marker when nothing recognisable** so a source-less checkout is re-checked. Backgrounded.

### Machine tools

- `dblm` is a separate Go CLI at `/Users/admin/go/bin/dblm` (config `~/.dblm/config.json`), not in this repo;
  recipes for WebP, Discord, WhatsApp and UI screenshots are in `HOWTO.md`.
- **Agent benchmark harness is `~/Downloads/work/ogcode-bench/benchmark.py`** (outside this repo; procedure:
  `HOWTO.md` → Run the agent benchmark offline with a reused code index). Two CLI token contracts: **ogcode
  `run -o json` emits ONE json at the end** whose `output` already counts reasoning, so bill `output`;
  **OpenCode `run --format json` emits JSONL** with reasoning DISJOINT from `output` per `step_finish`, so bill
  `output + reasoning`. Never count OpenCode turns as `step_start`s and never read ogcode tool parts from both
  the call and result message (`role == "assistant"` only). `run.json` carries the numbers.
- **Both arms run the same model**: `deepseek-v4.1-flash` on Ollama Cloud (`https://ollama.com/v1`), ogcode via
  the bare id (bench `MODEL_OGCODE`), OpenCode via `ollama-cloud/deepseek-v4.1-flash` (`MODEL_OPENCODE`).
  **`--no-deep-search`** appends an ogcode-only prompt clause; OpenCode still has `webfetch`.
- **`cost_usd` bills cache reads at the FULL input price** ($0.28/M), so prompt volume decides it; at this
  endpoint's real prices (in $0.30, out $1.20, cache-read $0.006/M) a 24–47% prompt saving is worth ~$0.01 while
  thinking is the bill. **ollama.com defaults to thinking ON** — a bare request reasons,
  `reasoning_effort:"none"` stops it; ogcode sends no effort when `session.effort` is empty
  (`resolveRunEffort` → `applyEffort` early return), so compare arms with `run --effort none`.
- **OpenCode has no turn flag** (`run --help` lists none); the only cap is `agent.<name>.steps` in
  `opencode.json` (integer, 1..9007199254740991; `maxSteps` is the deprecated alias), unset means `Infinity`.
  `--max-turns` reaches only the ogcode command line.
- **The harness blocks all egress but the model endpoint** via `EgressProxy`, an allowlist CONNECT proxy on
  `127.0.0.1:0` (`MODEL_HOSTS` = `ollama.com:443` only): everything else gets `403` + `DENY <target>` in the
  run's `egress.log`, summarised as `run.json.egress`. `child_env()` sets `HTTP(S)_PROXY`/`ALL_PROXY` (upper
  **and** lower) and forces `NO_PROXY`/`no_proxy` to `""` — Go otherwise bypasses the proxy entirely; this
  applies to `grade()` too, so a COLD Go module cache cannot reach `proxy.golang.org`. `OGCODE_AUTO_INDEX=0`
  rides the same env.
- **`wiretap=True` (default) makes the same proxy a TLS tap**; `--no-wiretap` tunnels opaquely.
  `ensure_wiretap_certs` mints an openssl CA + `ollama.com` leaf in `.wiretap/`. **The arms reach it by
  different legs**: ogcode is pointed at `OGCODE_BASE_URL = http://ollama.com/v1` because darwin Go reads roots
  from SecTrust (`crypto/x509/root_darwin.go`) and ignores `SSL_CERT_FILE`, so it arrives absolute and `_plain`
  dials TLS upstream itself — `wire_bytes` are HTTP framing only; OpenCode keeps https and is MITM'd (Bun
  honours `NODE_EXTRA_CA_CERTS`) — body bytes compare, wire bytes do not. Usage is visible only because both
  arms send `stream_options.include_usage` (ogcode `internal/provider/openai.go:1466`). Outputs
  `runs/<id>/wire.jsonl` and `run.json.wire`. **The tap must drain an aborted stream** or a completed response
  loses its row (`EOFError` = end-of-body; `send_down` swallows a client `OSError` and keeps reading upstream).
  **Trap**: a client hanging up mid-handshake reaches `_handle` as `BrokenPipeError` — exempt with
  `SSLEOFError`; a real trust failure is a certificate alert.
- **Index reuse**: `backup-index [--from <dir>]` writes `index-backup/ogcode.db` (sqlite backup API) +
  `index.json` (`template_commit`, page count); `build_index` restores it unless `--rebuild-index`.
  `restore_index` refuses when the template commit differs and must re-root the absolute
  `doc_page_index.doc_path` (plus `index_excludes`/`index_exclude_seed` `directory`, `ESCAPE '\'`) and delete
  all session/message/part rows. **The restored copy inherits the source's WAL mode** (the backup API copies
  page 1), so `copy_db` ends with `pragma journal_mode=delete`.

### Residual detail dropped in the 2026-10-21 defrag

Secondary values and test names trimmed while compressing the text above. None of it is loaded into the
prompt; it is kept so a later pass does not have to rediscover it.

- **oglab web signing**: the router reads `OGLAB_APP_SECRETS` (`app=secret,…`), while the oglab **web** app
  signs with `OGLAB_APP_ID`/`OGLAB_APP_SECRET`.
- **OpenCode's only step cap** is `agent.<name>.steps` in `opencode.json` — integer, exclusiveMinimum 0,
  maximum `9007199254740991` (2^53−1), with `maxSteps` the deprecated alias; unset means `steps ?? Infinity`.
  Verified on 1.18.30: `steps:3` → 2 agentic iterations, `steps:1` → reasoning only and no tool call, `0`
  and 2^53 both rejected by the config validator.
- **Contrast ratios behind the muted-text floor**: dark zinc-500/600/700 `#94949d`/`#8f8f98`/`#8a8a93`
  (5.27/4.94/4.63); light zinc-500/600/700 `#5f5f67`/`#63636b`/`#686870` (5.23/4.92/4.56).
- **Alias examples**: `ogx-deepseek-v4.1-flash` and `gemini-pro-latest` are aliases on real catalogue rows
  (`TestCatalogResolvesRealHostSpellings`), not entries of their own.
- **`DefaultEffort` never reaches the wire**: `resolveRunEffort` → `applyEffort` early-returns when
  `session.effort` is empty, so a catalogue `DefaultEffort` is a UI hint only.
- **Image-normalization ceilings** the 1024 px choice sits below: Anthropic ~1.15 MP / ~1568 visual tokens,
  OpenAI shortest side 768, Gemini 768 tiles.
- **Wiretap drain internals**: `_chunked_body` treats `EOFError` as end-of-body, `_relay`'s `send_down`
  swallows a client `OSError` and keeps reading upstream so the final usage chunk still lands, and an EOF on
  the response head writes a `status: 0` row.
- **Pinning tests dropped from the cards**: `TestIntentGateFiresOnEveryCallExceptUpdateIntent`,
  `TestIntentFinderAnnotatesRestatedPlans`, `TestIntentFinderSpendsOneCallPerNewStep`,
  `TestOGXProviderShowsRawModelIDs`, `TestExplorationIsMapThenBatchedReadsInEveryProject`,
  `TestProjectIndexPromptLoopsBackWhenAReadPointsElsewhere`.
- **Where to change it**: the OGX settings mark is `web/public/oglab-logo.png` (the PNG carries its own rounded
  tile, so a CSS radius clips it); `collectionFromBaseURL` maps a host to a collection name against
  `COMPATIBLE_PRESETS`; the batching rule lives in `projectIndexPrompt` under
  `## Mandatory: Explore in Batches`, with `indexStatusPrompt(0)` carrying the no-index wording.
- **Wiretap false alarm**: a client hanging up mid-handshake reaches `_handle` as `BrokenPipeError` (OpenCode
  opens a second tunnel and drops it) and is exempted from `tls_failures` beside `SSLEOFError`; a real trust
  failure arrives as a certificate alert instead.
- ogcode opens its DB in WAL (`internal/db/db.go`), which is why a copied DB needs `journal_mode=delete`.

## 2026-10-21 — search-before-descent nudge

- A live session called `codebase_map` at the root, thought "let me descend into internal and services", then
  grep'd and glob'd instead — no `subdir` call ever. The prompt already mandated the descent, and the map output
  already ends with a `subdir=` hint, so this was a compliance gap, not a missing instruction.
- Fix = nudge + wording (user's choice): `internal/agent/descent_gate.go` appends `descentGateReminder` to the
  first `grep`/`glob` result of a turn that has neither descended (`codebase_map` with a `subdir`) nor located a
  file (`read`/`file_map`). Rejected: hard refusal (would block "I know where this lives" searches and unindexed
  files), and per-call repetition (the reminder is a correction, not a chant — once per turn).
- `projectIndexPrompt` sharpened at the same time: the "unindexed files, binary patterns" escape hatch now says
  to search *after* the map has located the area, and "Do not search the whole tree to find the files the map
  would name" sits in the navigate-by-labels paragraph.
- Wiring pinned by `TestRunLoop_DescentReminderReachesTheModel` (two grep rounds through the real `RunLoop`,
  reminder counted in the request the provider receives) and
  `TestRunLoop_DescentReminderStaysOffWithoutAnIndex`.

## 2026-10-21 — em-dash sweep of public copy (homepage, OGX page, blog, docs-site)

- `docs/index.html` 96 occurrences across 85 lines, `docs/ogx/index.html`, the `blog/` sources (and the
  gitignored `docs/blog/` build output), and `docs-site/src/**` (109 string replacements) now hold **zero**
  em dashes and zero `&mdash;`/`&#8212;`/`&#x2014;` entities. Decorative `──` box-drawing runs were left alone;
  internal docs (`docs/DEPLOY.md` 2, `docs/_config.yml` 1) were deliberately out of scope.
- Replacement policy that read well: appositive/list separators → `: `; asides → parentheses; clause joins →
  `; ` or `. `; titles `Ogcode — X` → `Ogcode: X`; CSS/JS/HTML comments → `:` or `,`.
- **Gotcha**: `docs-site` frontmatter `description:` values became `": "`-bearing and broke `astro build` with
  `bad indentation of a mapping entry` (core-concepts.md:2:43) — an unquoted YAML scalar cannot contain `: `.
  Fixed by quoting; `npm run build` then passed (12 pages indexed).
- Pre-existing and unrelated: `[glob-loader] Duplicate id "<slug>"` for every `.md` page, from
  `docs-site/src/content.config.ts`'s `**/*.{md,mdx}` glob.

## 2026-10-21 — agent-to-agent messaging bus study (reactive programming / RxGo)

- Read the runtime end to end. The blocker is structural, not a missing library: the parent goroutine is parked
  inside `TaskTool.Execute` → `RunTaskSession` → `RunLoop` for the whole child lifetime (`internal/tool/task.go:91`,
  `internal/agent/loop.go:4790`), so a child message can only land at the parent's next iteration boundary, and a
  child `ask` that waits for a model-authored answer **deadlocks** by construction. A true back-and-forth needs an
  asynchronous `task` (handle + status/result tools), not a better bus.
- There is no agent identity graph to route on: `session.Session` has no `ParentSessionID`
  (`internal/session/message.go:8-31`), and `TaskFunc` does not even receive the caller's session id. The parent
  edge has to be created at spawn time and threaded through a signature change, never supplied by the model.
- The delivery primitive already exists and is under-used: `LoopControl` guidance (`loop_control.go:8-128`) drains
  at the top of every iteration (`loop.go:683-693`) and is re-appended for the rest of the turn (`loop.go:901-914`).
  Any second queue must join the three race re-checks at `loop.go:746-756`, `949-954`, `1974-1977`.
- RxGo v2: latest tag visible is v2.5.0, pre-generics (`Observe() <-chan rxgo.Item`, `interface{}` items, type
  assertions in the README's own examples); release year and last-commit date were not visible on the fetched
  pages, so neither was recorded. Rejected: the wanted semantics are Debounce/Distinct (progress coalescing),
  Timeout (ask/reply) and Merge (any/all children), ~10 lines each against `map[SessionID]*inbox`.
- `internal/bus` is UI fan-out and lossy by design (non-blocking send, `dropped` counter, `bus.go`), so it cannot
  carry a control message. Progress can ride it; `done`/`blocked`/`question` need a durable per-session inbox.
- Verdict delivered to the user: in-process durable inbox + spawn-time parent edges first; async task tool as the
  prerequisite for any real conversation; cross-process routing last (controlplane has only `Guidance`).

## 2026-10-22 — README hero switched from a static poster to an animated GIF

- Source was the developer's own screen recording `~/Desktop/232.mov` (h264, 3420×1978 — 2× of a ~1710×989
  window, 60 fps, 60.3 s, 48.7 MB, no audio). Kept the **12–60 s** range at **2× speed**, 1080w, 15 fps
  (`HOWTO.md` → Re-encode the README hero GIF), landing on `docs/media/ogcode-demo.gif`:
  **1080×625, 360 frames, 24 s, 5.1 MB**, infinite loop.
- Sizes rejected on the way: 1200w full clip 7.3 MB, 1200w 12–60 s 6.3 MB, 1080w at 20 fps 6.3 MB, 960w/12 fps
  4.3 MB (too soft). Bayer dither (`bayer_scale=3`, `diff_mode=rectangle`) beat `sierra2_4a`/`none` on size at
  equal quality; `palettegen=stats_mode=diff` across the whole clip beat per-segment palettes.
- The developer chose the full tour knowing the 48–60 s tail shows the Project Index page with rendered
  `HOWTO.md`/`MEMORY.md` text and a `~/Downloads/work/swe-bench` path. At 1080w that text is ~4 px and
  unreadable, which is why it was acceptable — the 2026-10-03 retake (`4f04ec0`) removed the same class of leak
  from the poster at a legible resolution.
- Cost of the switch: the old `demo-poster-framed.webp` had a macOS window baked in (traffic lights, `ogcode.in`
  title bar); the GIF is a bare viewport, and the clip itself flips dark → light theme mid-way, so the loop
  restarts on a light frame and jumps back to the dark Settings page. `demo-poster-framed.webp` stays on disk,
  unreferenced.

## 2026-10-22 — Where the tokens actually go (usage_event ledger, 102,347 rows)

Measured over the whole `~/.ogcode/config.db` `usage_event` table, to answer "does turning thinking off save
anything?":

- **Prompt 98.7%, output 1.3%.** input 1.704 B + cache_read 5.432 B (7.136 B prompt) vs output 91.0 M,
  reasoning 1.59 M. Overall cache-hit rate **76.1%**; mean **69,726 prompt tokens per step vs 889 output**.
- **Reasoning is 67.9% of output** where a provider itemises it (3,009 of 102,347 rows — `glm-5.3-flash`,
  `gpt-5.6-luna`, `grok-4.6`). The other 97% report 0 because their provider gives no breakdown, not because
  they do not think.
- **Cost shape** (cache_read 0.1× input, output 4× input): prompt **86%**, output **14%**. Killing *all*
  thinking therefore saves at most ~9% of spend, and that is the accuracy-expensive trade
  (`models_catalog.go:183-186` already refuses it for Claude).
- **The real lever is cache-hit rate, and it is model-dependent.** Billed prompt units (input + 0.1×cache_read):
  `glm-5.3-flash:cloud` **949 M at 78% full price** (17.8 k steps), `glm-5.2:cloud` **284 M at 100% full price**,
  `deepseek-v4.1-flash` **264 M at 5% full price** (22.6 k steps), `deepseek-v4.1-flash:cloud` 208 M at 5%.
  The two GLM routes are **55% of all prompt spend** at DeepSeek's work-per-step. Had they cached at 5% they
  would bill ~212 M — a **39% cut in total spend with the same model answering the same prompts**.
- Ruled out: treating `reasoning` as the savings target, and reading `reasoning = 0` as "this model does not
  think". Ollama Cloud's DeepSeek rows show heavy `cache_read`, so the "Ollama has no prompt caching" note in
  `MEMORY-ARCHIVE.md:1713` is stale for that route at least.

## 2026-10-22 — Agent board and teammates shipped (build agent only)

`plans/MULTI_AGENT_COMMS_PLAN.md` §1–§6 built: `internal/board` store + migrations, `internal/server/board.go`
+ `board_routes.go` (post/roster/claim/urgent, delivery at the top of `RunLoop`), `internal/tool/board.go`
(`board`, `teammate`), web `agent-part.tsx`.

- **"Build only" needed agent-id gates, not tool absence.** `codingAgentTools` is shared by `BuildAgent` and
  `TaskAgent`, the roster auto-join sits in `startSessionLoop`, and `DeliverBoard` runs for every `RunLoop` —
  so `canUseBoard`/`canSpawnTeammate`/`joinBoard`/`DeliverBoard` all test `agentID == "build"`, `teammate` is
  offered via `allowIDs`, and the prompt section lives in the existing `if !task` branch. Without this a plan
  session would appear on the roster and receive folded board posts.
- **Bug found end to end**: an urgent post to a name nobody held returned `{"delivered":"injected"}`.
  `board.Store.Get` returns the ZERO Handle with a nil error (its doc comment wrongly said "or nil"), and
  `deliverUrgent` tested `h.Handle == "" || h.SessionID == ""` together and returned nil. Now two distinct
  errors, `GET`/log show `queued`, and `internal/server/board_test.go` pins all four cases.
- **Second bug**: a plain successful post returned Notice `""`, and `formatBoard` renders "The board is empty"
  for an empty notice — so the model was told nobody had posted right after posting. Every post now carries
  `Posted as #N.`.
- **Deferred**: §7 (the board drawer / roster panel) is not built; only the `agent` transcript part renders.
- Verification: `go build ./internal/... .` (CGO on), `go test ./internal/server ./internal/board
  ./internal/tool ./internal/agent`, plus a fresh-HOME serve rig on :7877 — ghost urgent → `queued` + log
  `no agent on the board is called "ghost"`, broadcast urgent and a plain post landed as `#2`/`#3` and listed
  by `GET /api/board?limit=3`.

## 2026-10-22 — Board drawer (§7) built, plus two bugs only the live UI caught

- **§7 shipped**: `web/src/components/board-drawer.tsx` — a 28 px header pill (`aria-label="Board"`) that opens
  a right-side `<Portal>` drawer (roster with status dots + `you` badge, frozen paths with an X release, posts,
  and a compose form with Everyone/topic select + urgent). It refreshes on mount, on SSE `board.*` (150 ms
  debounce), on `server.reconnectTick()`, and every 15 s while open; requests are serialized through a `chain`
  promise. Mounted in `web/src/pages/session.tsx` first in the header-right cluster, before `<IntentPill />`;
  the API helpers (`getBoard`, `postToBoard`, `getBoardRoster`, `claimBoardPath`, `releaseBoardPath`,
  `stopBoardTeammate`) and their types live in `web/src/api/client.ts`.
- **No live session ever joined the roster** before the UI was exercised: `sendPrompt` in `client.ts` never
  sends an `agent` field, so `handlePrompt` passed `agentName == ""` and `joinBoard` (which refuses anything but
  `"build"`) silently skipped it — with a spurious `agent/session type mismatch` warn from `loop.go`.
  Fixed inside `startSessionLoop` (`internal/server/resume_routes.go`) by normalizing `"" → "build"` so every
  caller gets the same resolution, mirroring the loop's own `GetAgent` fallback.
- **Zero Go time does not survive `omitempty`**: an unexpiring (user) claim's `expires` arrives as
  `"0001-01-01T00:00:00Z"` and rendered as a date in the past. `claimExpiry()` now shows "no expiry" for it.
- **Claim/release posts carry their path twice**: `board.Store.Claim`/`Release` set `Text: "claim "+rel` AND
  `Path: rel`, so the drawer's mono path line duplicated the row text. `PostRow` suppresses the path line when
  the text already ends with it (the store's own Text left alone).
- Live evidence (rig `/tmp/ogcode-ui-rig`, `HOME=/tmp/ogcode-ui-home`, port 7979, `OGCODE_AUTO_INDEX=false`,
  ego lite at 1200×872): screenshots `/tmp/ogcode-ui-shots/drawer.png`, `drawer2.png`, `drawer3.png`,
  `drawer-light2.png`. Compose → seq 4 + notice `Posted as #4 — queued for the next turn`; freeze
  `web/src/pages/session.tsx` → row + `Froze …`; X → row gone, seq 6, `Released …`; header count 3→4→5→6;
  Escape closes and the pill reopens; light mode renders a white panel with legible muted tokens. Log showed
  `board: joined session=… handle=build-8d5f` and no mismatch warn.
- Green: `go test ./internal/server`, `npx tsc --noEmit`, `npm run build` (re-embeds `web/dist`, gitignored),
  `CGO_ENABLED=1 go build .`.

## 2026-10-22 — Reference detail moved out of MEMORY.md (controlplane, docs, releases, measurements)

Moved here so MEMORY.md fits its ~20 KB budget; MEMORY keeps the pointer lines.

- **Controlplane**: `controlplane/` is a nested module; the worker daemon hosts one in-process
  `server.Server` per worktree (`Serve(ctx)`/`Stop()`/`Port()`, `NewWithOptions`, `port:0` surfaces). Eager
  tunnels dedupe via `tunnelled.LoadOrStore`; per-dir routes key `<workerID>-<route>` with **exact whole-label**
  lookup. Stable worker id `~/.ogcode/worker-id` (0600, RFC 1123); reconnect persists token + expiry to
  `~/.ogcode/worker-cred` and re-pairs on `Unauthenticated`. Accounts: removed users fail closed, total registry
  loss brings the gate up **Locked**, passwords from env/`--password-file`, the cookie key is per-process,
  `AssignUser` refuses admins. Incus: one container per assignment (`incus/v6/client`), masterURL = 10.0.192.1,
  Docker's nft FORWARD swallows incusbr0. **Superseded, do not resurrect**: a master inside the ogcode server,
  `proto/master.proto`, `MasterConfig`/`WorkerConfig`, `master`/`worker` keys in `ogcode.json`.
- **Docs/marketing**: `blog/` sources are pre-rendered into gitignored `docs/blog/`; `ogcode.xyz` is removed.
  `check-updates` exits 1 when an update is available. Release page triggers: `release.yml` is the only ldflags
  site and Pages fires only on `docs/**`/`blog/**`/`docs-site/**` pushes, never on a tag.
- **Measurements**: where a provider itemises reasoning, thinking was 68% of output tokens; output is ~1% of all
  tokens (98.7% is prompt), so the spend lever is prompt cache-hit rate, not effort. `riskLLMMaxTokens = 1024`
  was calibrated at 16/16 verdicts against 3/16 at 8 tokens.
- **Machine tools**: `dblm` config is `~/.dblm/config.json`; the harness is
  `~/Downloads/work/ogcode-bench/benchmark.py` with its json/Bill accounting traps in `HOWTO.md`.

## 2026-10-22 — §7 board gets HTTP route coverage and one regression pin

- New `internal/server/board_routes_test.go` (5 tests, ~0.25 s) drives `srv.routes()` with `httptest`:
  board draw/post validation, claim→roster→release, teammate stop (200/404/400), 409 claim conflict
  naming the holder, and 503 on all six routes with `s.board == nil`.
- `TestStartSessionLoopJoinsAnEmptyAgentAsBuild` pins the live-found `"" → "build"` normalisation. Mutation
  check: deleting only the block inside `startSessionLoop` (there are **two** copies — the resume handler has
  its own at ~line 84) makes it fail with `board: session did not join … err="the  agent does not join the board"`.
- Left untested on purpose: the web drawer (`web/package.json` has no test runner) and the SSE→15 s polling
  fallback; both stay live-rig verified. `CGO_ENABLED=1 go test ./internal/server/ ./internal/board/` green,
  `go vet` clean.

## 2026-10-22 — §7 board drawer driven end to end by ego-browser

- Throwaway instance (v0.50.2, `--port 7911`, isolated `HOME`, `config.db` copied in) + ego lite 2.1.0
  against the real `web/dist`. Session `ses_01M5J25DSK3SAK3W7PPXQ5Y1H5` joined the roster as `build-dfd3`
  (`msg="board: joined"`), so the `"" → "build"` normalisation holds through the actual web client, not just
  the Go test.
- Drawer verified by hand: open from the header, `1 agent · 0 posts`, post → `Posted as #1 — queued for the
  next turn`, freeze `web/src/App.tsx` → `no expiry` on a zero `time.Time`, release → `No path is frozen.`,
  `page.reload()` → all 4 posts/released claims still there, `Escape` closes without stopping the composer.
- SSE: a `POST /api/board/post` issued from Node `fetch` outside the page reached the open drawer in **238 ms**
  with no manual refresh (board.* event → 150 ms debounce → refetch). `GET /api/board` cross-check: cursor 4,
  kinds note/claim/release, `leaseUntil` zero.
- NOT exercised, and the reason: the turn itself 402'd (`anthropic … Insufficient Balance`), so the agent-side
  board tools, post delivery into a next turn, and spawn/stop of a real teammate stayed untouched — and the Stop
  button correctly never renders for `kind:"session"`.

## 2026-10-22 — §7 board agent side driven end to end on ogx-deepseek

- Throwaway instance (v0.50.2, `--port 7915`, isolated `HOME`, `config.db` copied in) rebuilt with
  `-X internal/provider.OGXAppSecret=$SEC` (`strings | grep -c -F "$SEC"` → 3). Session
  `ses_01M5J2HG12Q1JKXE9WEZP8Q0VA` joined as `build-92a4` on `ogx-deepseek-v4.1-flash` — so the three paths the
  ego run could not reach (agent-side board tools, post delivery into a turn, spawn/stop of real teammates) are
  now exercised against a real model.
- Turn 1, one complex task: **three teammates** built an `md2site` project in the workspace; **40 pytest tests
  pass** (`40 passed in 0.06s`, re-run by hand after the turn).
- Turn 2, a forced turn isolating the wait path: teammate `wait-worker` writes `wait.txt` and posts a note to
  `build-92a4`; the parent waits twice with `handles:["wait-worker"]`, then reads the file. First wait showed the
  claim at `seq 25`; second showed the note at `seq 26` and did **not** replay the claim. `board_agent`:
  `build-92a4 last_seq=26` (newest post), `wait-worker done`; posts 25 claim / 26 note / 27 release; `read` →
  `wait worker ok`; no `could not advance cursor` in the log.
- Three bugs only a live run finds, all fixed in `internal/server/board.go`: (A) nothing announced turn start, so
  a spawned session sat unclaimed — `boardTurnStart` now mints the handle idempotently per turn, sets
  `StatusRunning` for `KindSession`, logs `board: turn started`, and is called from `startSessionLoop`; (B) the
  post-wait `Seen`/roster reads reused a possibly-expired op context — they now get a fresh one; (C) a
  `handles`-filtered wait never advanced its cursor (the advance was guarded on `len(req.Handles) == 0`), so the
  same post came back forever — it now advances past every post the wait showed and stops before the first one the
  filter held back, so nothing is skipped.
- `TestWaitAdvancesCursorPastAFilteredWait` pins (C): reverting `waitTeammate` to the old advance makes it fail
  with `cursor 0 after a fully shown wait, want 1`; with the fix it passes. `HOME=… CGO_ENABLED=1 go test
  ./internal/... . -count=1` → 29 packages ok, `gofmt -l` and `go vet ./internal/server/` clean.
- Rig-specific gotcha for the next run: `GET /api/session/{id}/message?limit=N` lists every tool call twice (the
  `assistant` call plus a `user` part echoing it beside the result), so do not read two invocations out of it. A
  HOME-isolated `go test` leaves `$HOME/go/pkg/mod` read-only — `chmod -R u+w` before `rm -rf`.

## 2026-10-23 — Multi-agent vs single-agent measured as a 2×2 on the benchmark

- Setup: `ogcode-bench` `ogcode serve` arm, `ogx-deepseek-v4.1-flash`, `--time-limit 1800`,
  n=2 replicates per cell (bug|build × single|multi), same hidden-test + build grading. All 8 runs graded
  `success` (`tests_ok` and `build_ok`), every model call proxied (`proxied_model: true`), 0 egress denials.
- Harness change first: `SANDBOX_NOTE` appended to the prompt for both modes in `run_serve` — the graded clone
  lacks gitignored `server/configs/*.conf`, so a bare `go test ./server` panics (and one agent backgrounded a 40 h
  timeout and polled). Without the note the comparison is unusable: bug/single 1276 s vs 487 s with it,
  bug/multi pinned at the 2700 s cap, build/single killed before it finished.
- Wall clock (s): bug/single 487.2 / 445.0; bug/multi 461.0 / 627.4; build/single 439.2 / **1800.2 capped**;
  build/multi 601.6 / **1800.6 capped**. Means: 466 / 544 / (1120 censored) / (1201 censored).
- Cost ($) and turns: bug/single 1.14·112, 0.90·75; bug/multi 1.75·140, 3.46·265; build/single 1.20·116,
  1.03·98; build/multi 2.24·211, 1.55·156. Multi is 1.7–2.6× the spend and 1.5–3.5× the turns for no wall win.
- Model seconds summed over sessions (wire.jsonl `ms`): bug 347/276 single vs 557/985 multi; build 328/303 vs
  624/485 — the multi parent plus teammates think 1.6–3.6× as long in total.
- Parent's last edit vs run tail: clean bug 323 s/128 s tail (multi) vs 391/87 (single); clean build 390/201 vs
  329/100; rep2 bug 592/24 vs 353/81; rep2 build 1264/**710** vs 1845/4. So multi moves the parent's edits earlier
  and adds a tail collecting teammate reports — the net is a loss, never a gain.
- Compile tax dominates the build task, not orchestration: cold GOCACHE per run, each `go test ./server -run …`
  costs 150–420 s. rep2 build/single: 1544 s of tool time, 1512 s of it in 6 calls >20 s. rep2 build/multi:
  4941 s of tool time across three concurrent sessions, 22 calls >20 s summing 4844 s, both parent and teammates
  recompiling the same package. Wall time measures compile rounds triggered, not agents run.
- Teammates in the bug cells were mostly read-only investigators (`reload-auditor`, `mpay-upstream`,
  `mpay-tests`) and test authors; only clean build/multi had teammates editing (windows 125–196 s and 207–272 s
  inside the parent's 119–395 s).
- One contaminated run, flagged: rep2 build/multi's teammate `QPETKV` walked to a sibling run dir and copied
  `../rep2-build-single-…/home/go/pkg/mod` (10 references; the 159 s `chmod`/`cp` and 149 s `rm -rf` calls). The
  runs directory is a harness artifact — no other run references another.
- Verdict: no cell where multi is faster. Bug is inside replicate noise (single 445–487 vs multi 461–627, ranges
  touch only at the ends); build is slower or censored in both modes. Parallel teammates do not shorten a task
  whose critical path is one agent's edit/test loop on a shared compile cache.

## 2026-10-23 — four parallel research teammates on the agent loop, permissions, indexing and the web client

- Fan-out: 4 `teammate spawn` handles (loop-runner, permissions, indexer, web-client), one subsystem each, all
  read-only, each writing a report to `public/research/<slug>.md` and posting a ≤5 KB summary to the board
  (topic `review`, addressed to the parent). Wall time ≈ 5 min, four concurrent sessions; `board claim
  public/research` (taken by `permissions`) then blocked `indexer`'s claim of its own file — claims are
  path-prefix, so one prefix per writer, or distinct prefixes, is the fix.
- Read-only held: mtimes under `internal/` and `web/src/` were unchanged across the run (19:46–20:12 vs spawn at
  01:11); only `public/research/*.md` appeared.
- **Two MEMORY.md lines were stale and are now corrected**, both verified in source before editing:
  `codebase_map` has **no >10-descendant collapse** — "One level, never recursive, and no size threshold"
  (`internal/tool/project_index.go:618-629`, the old <10-file expansion was deleted); the shipped exclude list is
  **39** entries, not 38 (`internal/docindex/excludes.go:51-72`).
- Index labels are generated by **`IndexAgent`** from a locally-extracted keyword corpus, never embeddings —
  `grep -n 'embedding' internal/indexer internal/docindex` returns nothing; storage is `doc_page_index`
  (db/022, `mod_time` from db/049).
- Permission detail worth keeping: `requestPermission` evaluates the ruleset **before** the mode switch, and the
  catch-all `{Permission:"*", Pattern:"*", Action:Allow}` rule means mode only ever affects bash/write/edit (and
  the ask-path of teammate). The risk classifier's answer is cached process-globally by exact command string.
- Cost note: this is the read-only case where fan-out does pay — the four reports are ~125 KB of cited analysis
  produced in one wall-clock pass, against a serial version of the same reads.

## 2026-10-23 — teammate spend in the parent's token pill

- Built by fan-out, two teammates on disjoint files: `usage-api` (backend) and `usage-pill` (web); I held the
  review, verification and memory.
- Shape: `/session/{id}/token` and `/session/{id}/usage` each gained a `teammates` array (per handle +
  sessionId + status, tokens or priced `Totals`); the headline totals stayed the session's own and
  `token-pill.tsx` does the fold — headline counts session + teammates, popover shows "of which teammates" plus
  one row per handle. Rejected: folding into the ledger/`foldChildUsage`, which would double the same tokens in
  the parent pill and the child's own page.
- Link is the board roster, not a schema change: `spawnedTeammates` does `board.BySession(parent)` then filters
  roster rows to `KindTeammate && SpawnedBy == parent.Handle`. Handle names are random (`board.NewHandle`) and
  `UNIQUE(board_agent.session_id)` pins one handle per session, so a reused name cannot pull in a stranger's
  teammates.
- Poll: the parent transcript does not move while a teammate works, so the pill refetches on `board.*` events
  plus a 5s beat while any teammate is running/idle.
- Verified: `go test ./internal/server/ ./internal/agent/` (23.8s / 37.4s, isolated HOME), `gofmt -l` clean,
  `CGO_ENABLED=1 go build ./internal/... .`, `web: tsc --noEmit` + `npm run build` (14.05s). Live check on a
  throwaway dir/HOME at :7799 (never the :7801 server this session runs in): created a session via
  `POST /api/session`, both endpoints answered `"teammates":[]` and the parent totals 0. Non-empty lists are
  covered by the seeded-board tests, not by a live teammate run.

## 2026-10-23 — teammate nesting unbounded; depth-1 guard and the 4-live cap removed

- Asked for: a teammate may spawn teammates, with no ceiling on how many run at once.
- The real gate was never the comment in `agent.go` ("never offered the tool"): the child runs as
  `SessionType: "build"` and got the tool on the wire, and `spawnTeammate` refused it on
  `parentHandle.Kind == board.KindTeammate`. Deleted that guard and `maxLiveTeammates = 4` outright — no
  soft or hidden limit, since none was asked for. What bounds nesting now is spend, `teammateMaxSteps = 300`,
  0 auto-resumes and spawn = `Ask`.
- Stale comments fixed to name the real mechanism: `canSpawnTeammate` (`internal/agent/agent.go`),
  `spawnTeammate` doc + `teammatePrompt` (`internal/server/board.go`), the `TeammateTool` description, and
  `cancelLoop` (`internal/server/host_session.go`) — that cascade already recursed, since each cancelled
  child's `cancelLoop` re-enters `stopTeammatesOf`. Spawn notice dropped "of %d".
- Test trap worth remembering: the obvious assertion, `liveTeammates >= 7`, FAILED in the full-suite run
  (2 live) after passing alone — `leaveBoard` flips a teammate to `done` the moment its loop exits, and with
  an empty registry the test loops exit immediately, so the count is a race. The pin now reads
  `board.Roster` and asserts six `nested-*` rows exist. Six spawns is deliberate: past the old ceiling of 4.
- Verified: `go test ./internal/server/... ./internal/tool/... ./internal/board/... ./internal/agent/...`
  all ok (24.3s / 2.8s / 1.1s / 38.1s, isolated HOME), test `-count=5` stable, `gofmt -l` clean,
  `CGO_ENABLED=1 go build ./internal/... .`. No live multi-agent run: nesting is pinned by unit test only.

## 2026-10-23 — board claim/release noise in a user turn: by design, user chose to keep it

- User asked why 8 `permissions/usage-pill/usage-api → you · claim/release` blocks appeared under a plain
  "hii". Not agent-attached text: `Store.Claim`/`Release` write `board_post` rows with `to_handle = ''`
  (broadcast, `internal/board/store.go:471`, `:496`), and `deliverBoard` (`internal/server/board.go:841` via
  the `DeliverBoard` hook at `internal/agent/loop.go:386`) appends every undelivered broadcast post to the
  turn's own user message (`lastUserMessageID`). `agent-part.tsx:39` labels any inbound post "→ you", so a
  broadcast reads as addressed to the reader.
- The batch arrived because three parallel agents claimed/released in the seconds before the message and no
  turn had run since; the per-turn cap is 20 posts / 8 KB (`boardDeliveryPosts`/`boardDeliveryBytes`).
- Presented three fixes (filter claim/release in `Store.Deliver`; addressed-posts-only delivery; UI collapse)
  and the user chose **no change** — delivery semantics stay as designed in `plans/MULTI_AGENT_COMMS_PLAN.md`
  §2.1/§2.5. Do not re-open this without asking.

## 2026-10-23 — board delivery redesign agreed: push, ephemeral, filtered (design only)

- User's complaint: the board "costs too many tokens". Diagnosis (no code changed): the pull is not the cost.
  Four drivers, in `plans/BOARD_PUSH_PLAN.md` §0 — (1) delivered posts are persisted as a `PartAgent` on the
  turn's user message (`internal/server/board.go:841` → `appendAgentPart`, rendered at
  `internal/agent/loop.go:3908`) and re-sent every step of every later turn; (2) claim/release are broadcast
  (`internal/board/store.go:471`, `:496`) so N agents pay for every claim; (3) claims are state delivered as
  events though `claimHolder` already enforces collisions at write time; (4) 20 posts / 8 KB dumped at turn
  start regardless of relevance.
- Decisions taken: claims → **collision notices only**; routine posts **interrupt the stream** (guidance tier,
  behind a once-per-step burst gate, or interrupts cost more than the pull); turn-start catch-up
  **directed-only**, broadcast replay deleted.
- Two facts found while designing: `deliverUrgent` (`internal/server/board.go:789`) pushes `board.Render(p)`
  through `s.Guidance`, so a peer post reaches the model labelled "the user sent this while you were working",
  contradicting its own fence — push-per-post would multiply that. And `Store.Deliver` (`store.go:330`)
  advances `last_seq` itself; the new design needs ack-on-inject, so it must become a read-only `pending()`.
- Correction to an earlier belief: `board.Store.mu` guards only the `wait` channel, not the `Insert` Exec, so
  an insert hook cannot deadlock on it — it is async for latency and for re-entrancy safety, not to avoid a
  deadlock.
- Estimated, not measured: a busy 4-agent turn carries ~1.5k tokens of claim fences per agent, permanently.
  Measure before/after on one real multi-agent session when P1 lands.

## 2026-10-23 — claim-on-mutation reviewed; notify-on-release rejected

- Proposal reviewed (no code changed): check the board on **every mutation**, and on a collision leave a
  "tell me when it is released" subscription instead of waiting. Check-on-mutation already exists
  (`claimHolder` → `tool.Context.HeldBy` → `write`/`edit` refuse); task-completion push does not exist yet
  (P1/P2 of `plans/BOARD_PUSH_PLAN.md`).
- Two real holes found in the existing claim protocol, both more urgent than the subscription: **nothing
  auto-claims** (only `board claim` creates one, so the write check protects a holder, not two unclaimed
  writers) and **the lease has no heartbeat** — `ClaimLease` (10 min) is set in `Store.Claim` only
  (`internal/board/store.go:469`); the comment at `internal/board/board.go:25` saying a write refreshes it is
  wrong, so a long edit silently loses its claim and a second agent may then write.
- Why notify-on-release is the wrong lever: a released claim is usually **not an event** — the common way a
  path frees is the lease expiring, which `Store.Claims` drops by wall clock with no post (`store.go:407`), so
  the case most worth waking for is the one with no hook; push only reaches `StatusRunning` handles, so an
  idle waiter that ends its turn cannot be woken (wakes are still design-only); a waiter registry needs cancel,
  prefix matching (`Conflicts`), FIFO ordering and cycle detection. Payoff is one retry step.
- Cheaper substitutes agreed in the same review: the collision notice goes to the **holder** (cause: shorter
  hold) rather than the waiter (symptom: retry); `wait` gains a `path` mode that blocks until `HeldBy` is free
  or a deadline, reusing `Store.signal()`. A blocked agent waiting one step is cheaper than ending the turn — a
  new turn re-sends the whole prompt.

## 2026-10-23 — board push delivery implemented, completion always reported

- `plans/BOARD_PUSH_PLAN.md` is no longer design-only: posts ride `LoopControl` notices injected at the prompt
  tail, acked once per turn (`catchUpBoard`/`ackBoard`), turn-start catch-up directed-only
  (`board.DeliverDirected`), mid-turn fan-out through the store's `OnInsert` → `pumpBoardPosts` →
  `fanOutBoardPost`, with `InterruptForNotice` behind the once-per-step gate and `urgent` cancelling the stream.
- Gap found by the user's framing ("always a push"): a teammate that simply STOPPED without posting told its
  spawner nothing. Fixed in `leaveBoard` → `reportTeammateDone`: a `StatusDone` end-of-turn with no post to
  `SpawnedBy` **after the teammate's last message** posts a `status` carrying that message.
  Stopped and stale handles still return early, so a cancellation is never announced as a completion.
- "Was it reported?" is an ORDERING question, not a flag (`boardTurn.reportedAt`, compared against the newest
  assistant message's `CreatedAt`): a mid-turn question to the parent must not suppress the final report, while
  a post written after the last message is the report itself. Both stamps are wall clock (messages in unix
  millis via `session.Now()`, posts `time.Now()`), so `lastAssistantText` converts with `time.UnixMilli`.
- Prompt surfaces aligned with the guarantee: `teammatePrompt` says a post-less stop still sends the last
  message, so make the last message the result; `boardPrompt` and `TeammateTool.Description` say the completion
  is pushed (wait is optional, `status`/`roster` is the pull path).
- Tests: `TestLeaveBoardReportsACompletionTheTeammateNeverPosted` (mutation-checked — fails without the hook),
  `TestLeaveBoardLeavesAReportedCompletionAlone` (a post after the message suppresses the report, exactly 1
  notice), and `TestLeaveBoardReportsAMessageWrittenAfterAPost` (the ordering rule).

## 2026-10-23 — conflict-free mutations: read-bookmark file change gate shipped

- Delivered `plans/CONFLICT_FREE_MUTATIONS_PLAN.md` v1: migration `066_file_change.sql` (history rows +
  per-session bookmarks), `internal/filechange` (store, `Log` with a 50-entry per-file ring, `Stale`), the gate
  in `tool/write.go` / `tool/edit.go` (`staleness.go` renders the refusal with the other session's diff),
  `description` required on both tools, the `read` bookmark, and `GET /api/file-changes`.
- Two test failures, both fixed: the rendered diff has a space after each sign (`"- one"`, not `"-one"`), and the
  route's display relativization must run against a symlink-resolved root (`tool.FileKey(s.dir, ".")`) because
  keys are `FileKey`-normalized on darwin (lowercased, `/var` → `/private/var`) — the route test also has to
  create `internal/tool/edit.go` BEFORE keying it, since keying a path whose parent does not exist never resolves
  `/var`.
- Verified: `CGO_ENABLED=1 go build ./internal/... .`, `go vet ./internal/...`, and
  `go test ./internal/tool/ ./internal/server/ ./internal/filechange/ ./internal/agent/ ./internal/cli/` all
  clean; live `make -s build-server` run on port 7911 in `/tmp/fcgate-e2e` returned 200 from `/api/config` and
  `{"changes":[],"cursor":0}` from `/api/file-changes?path=notes.md`, and `goose_db_version` showed 66 applied.
- Deferred on purpose: cross-process staleness while both `serve` and `run` are up (would need a DB read per
  mutation), `bash`/MCP/`latex_to_pdf` blind spots, `file_map` not counting as a read, and any history UI.

## 2026-10-23 — bash joins the file change gate (fingerprint, not command parsing)

- Chosen by the developer: **stat the files the log already tracks after each bash call**, rather than parsing
  the command (`sed -i`, `>`, `mv` are unbounded) or snapshotting the workspace (cost grows with the tree, not
  with the bookmarks). One `os.Stat` per tracked file per command.
- `filechange.Fingerprint{Size, MTime}` + `FingerprintOf` (`fingerprint.go`); `Log.Read` stamps it for every
  caller, `Log.Files()` lists what the log could have stalened. `observeShell` (`tool/bash_observe.go`, called at
  the end of `BashTool.Execute`) records tool `bash` with the command as the note and no diff — the gate never
  holds the bytes, so a diff would mean reading the file back on every command. Deletion works because
  `Record`→`Read` re-stamps after the change and a missing file is the zero fingerprint.
- Same turn, the bookmark sentinel bug: `Seen` returned 0 for "never read", so a file first read at revision 0
  (seeded outside the tools) could never be refused. Now `-1`, `Stale` guards on `seen < 0`, and the revision-0
  case is pinned by `TestBookmarkAtRevisionZeroStillRefuses`.
- Rejected: hashing contents (byte cost per tracked file per command for a signal mtime+size already gives at
  tool granularity); recording changes for untracked files (no bookmark ⇒ nobody to stale, so it is history
  noise); auto-claiming a path from bash (claims stay opt-in, `claimHolder` only refuses).
- Verified: `gofmt` clean, `CGO_ENABLED=1 go build ./internal/... .`, `go vet ./internal/filechange ./internal/tool`,
  and `CGO_ENABLED=1 go test` on `filechange`, `tool` (4 new `bash_gate_test.go` cases through the real
  `BashTool.Execute`), `server`, `agent`, `cli`, `session` — all ok, HOME isolated for the server suite.
- Live: rebuilt (`/tmp`, `ogcode v0.50.2`), `serve --port 7923` in a throwaway dir with a throwaway HOME —
  `/api/config` 200 after 2s, `/api/file-changes?path=notes.md` → `{"changes":[],"cursor":0}`, `goose_db_version`
  showed 66 applied. **Not** driven through a live agent turn: `bash` is `Ask` in the ruleset and `ogcode run`
  has no approval flag, so the agent-turn path stayed at tool level (real `Execute`) + the filechange store tests.

## 2026-10-23 — board reduced to an agent tree (posts and claims deleted)

- Developer's call: board messaging had no value once mutations became conflict-free. What is left is the tree —
  who spawned whom, each handle's status, and a jump into its session. Reporting back is a status event, not a
  post to anyone.
- Cut: `board_post` (migration `067_agent_tree.sql` drops the table and its claim/release rows), post/read/wait/
  claim/release ops, `board.DeliverDirected`/`Claims`/`HeldBy`/`Claim`/`Release`, the push pump + burst gate
  (`pumpBoardPosts`/`fanOutBoardPost`/`OnInsert` board hook), `CatchUpBoard`/`AckBoard`, `boardTurn.reportedAt`,
  the `claimHolder` check in `write`/`edit`, the drawer's posts/claims panes, and ≈400 Go lines. Kept: roster
  (`Register`/`Get`/`BySession`/`SetStatus`/`Roster`/`Children`/`MarkReported`/`WaitStatus`/`Reconcile`), spawn/
  stop cascade, and the roster's use by the file gate to name a session by handle.
- Delivery rule (developer chose **queue until next turn** over waking an idle parent): child loop end sets
  `status=done` + `updated_at`; spawner RUNNING → `LoopControl` notice carrying the child's last assistant message
  (4 KB, plus `/session/<id>`) and `MarkReported`; spawner idle → `notified_at < updated_at` stays unreported and
  `CatchUpTeammates` (replacing `CatchUpBoard`) hands it over at the parent's next turn start. Nothing starts a
  turn on its own.
- Accepted cost, stated up front: a teammate can no longer ask its parent mid-task, and a parent cannot ask a
  running child — the only channel is spawn in, completion out.
- Verified: `CGO_ENABLED=1 go build ./internal/... .`, `go vet` on board/agent/server/tool, `go test` on
  `board`, `agent`, `tool` and the whole `internal/server` suite (HOME isolated), `npx tsc --noEmit`,
  `npm run build` — all clean.
- Live (throwaway git dir `/tmp/ogtree2.2kgPpG`, port 7899, real HOME): `goose_db_version` 67, `board_agent` has
  `spawned_by`/`notified_at`, `board_post` gone, `/api/board/roster` → `{"agents":[]}` on an empty project. A
  real spawn (`probe`) went `teammate → done`, `spawnedBy=build-b522`, `updated_at == notified_at` (push path),
  and the parent's turn ended with "it finished with: `ok`" from the child's last message with no persisted part.
  A second turn calling `board` `op=roster` printed the indented tree.
- Build gotcha, again: the live binary must be `make -s build-server OGX_APP_SECRET=$(tr -d '\n' <
  ~/.ogcode/ogx-app-secret)` — an unsigned build 401s "a valid client signature is required". Default `anthropic`
  model returned **402 Insufficient Balance**, so the live session had to run `ogx-deepseek-v4.1-flash`.
- Not exercised live: the idle-parent catch-up path (tests only — `internal/server/board_test.go`).

## 2026-10-23 — teammate caps removed: report size and step ceiling

- Deleted `teammateReportBytes` (4 KB) from `internal/server/board.go`: `teammateReportNotice`, `waitTeammate` and
  `statusTeammate` now carry the child's last message whole, truncation removed from all three call sites.
- Deleted `teammateMaxSteps` (300): `stepBudgetFor` returns `agent.MaxStepsUncapped` + 0 auto-resumes;
  `internal/agent/loop.go` grew `overrideStepBudgets`, which maps a negative budget to `math.MaxInt` for both
  `maxSteps` and `hardMax` (previously the override block was inline in `RunLoop`).
- Investigated `LoopControl` as a residual clip: `maxNoticeBytes` (2 KB) bounds a BATCH only — `DrainNotices`
  always-take-one rule means a 40 KB notice is delivered alone and intact. No change needed there.
- Nothing pinned the old values before this; now `TestOverrideStepBudgets` (`internal/agent/step_budget_test.go`),
  `TestStepBudgetLeavesTeammatesUncapped` and `TestTeammateReportKeepsTheWholeMessage` (40 KB body) do.
- Still bounded, deliberately: `capToolOutput` 50 KB / 2000 lines on every tool result (so `teammate wait` output),
  `teammate wait` timeout 60 s default / 120 s max, compaction 2 per run.

## 2026-10-23 — build prompt now favors teammate splits

- Rewrote `boardPrompt()` (`internal/agent/agent.go`) around the DECISION with the mechanics second. Two currencies
  a teammate buys: **wall-clock** (independent streams finish in the time of the slowest, not the sum) and
  **tokens** (a child's greps, reads and dead ends are billed in its own context, and the parent re-sends less
  history on every step). "When either pays, favor the split" — spawn one per stream and take one yourself.
- No mandate and no negation: nothing says "you must spawn", nothing says "don't do it all yourself" (this model
  obeys negations literally and would read a ban as a prohibition). The four cases where one agent stays right
  are stated positively instead: sequential pieces, one file, an edit smaller than its briefing, and research.
- Verification is written as *review the diff + run the build and tests*, not "don't re-read" — it would
  otherwise contradict the prompt's existing **Reviewing work** section.
- Process step 1 gained the pointer, in the interactive branch only, so `TaskAgent` (which overrides step 1)
  never sees it; `internal/tool/teammate.go` `Description()` was left untouched — the system prompt stays the one
  source of truth on when to spawn.
- Pin: `TestBoardPromptPrefersTheSplit` (`internal/agent/prompt_workflow_test.go`) — both driver labels present,
  decision index before the `teammate spawn` mechanics index, and no non-build agent prompt mentioning `teammate`.

## 2026-10-23 — Three HIGH bugs fixed in the file change gate and the board (fail-before proven)

Found by reviewing the uncommitted multi-agent/changefile tree; medium/low findings left alone (#4, #7, #9 among them).

- **Bash laundered a foreign change** (`internal/tool/bash_observe.go`): the observer recorded its change with
  `Session: tctx.SessionID`, which made the running session the author of bytes it never held *and* advanced its
  bookmark, so the next `write` clobbered another session's edit with no refusal. The record is now unattributed
  (no `Session`), so the observer is refused like anyone else until it re-reads. **Deliberate behaviour change**:
  `TestOwnShellChangeIsNotStale` was replaced by `TestOwnShellChangeRefusesUntilReRead` — a session's own shell
  command now stales it, because size+mtime cannot attribute the write. Proof: temporarily re-adding the `Session`
  field failed both new tests (`title = "notes.md", want the stale refusal`).
- **Empty ring refused everything after a restart** (`internal/filechange/log.go`): `coveredRev < seen` is true for a
  bookmark of 0 with no rows in the ring, so the `History` fallback never ran and `dropped` refused every
  previously-read file. `covered := len(ring) > 0 && ring[0].Rev <= seen+1`.
  `TestRestartRefusesABookmarkOfRevisionZero` failed against the old formula with `dropped = 1, want 0`.
- **Stop/leave race re-delivered a completion** (`internal/board/store.go`): `stopTeammate` wrote `stopped` *after*
  `cancelLoop` (which does not wait), so `leaveBoard` could write `done` and push a "finished" report, and the later
  `stopped` write bumped `updated_at` → reported a second time. New
  `SetStatusUnlessTerminal` (`WHERE status NOT IN (done,stopped,stale)`) claims the row first; stop and leave both
  return early on a missed claim. `TestStopTeammateIsNotRewrittenDone` /
  `TestStopTeammateOfAFinishedTeammateChangesNothing` failed against the unconditional UPDATE.
- Verified: `CGO_ENABLED=1 go build ./internal/... .`, `go vet` on the four packages, `go test -count=1 ./internal/...`
  all green; gofmt clean on the seven touched files.
- `MEMORY.md` is ~29.5 KB (over its ~20 KB budget before this turn): its two largest sections are the board and the
  file change gate, both live-feature invariants rather than history — a curation pass needs trimming spread across
  ~28 small sections, so it was not done here.

## 2026-10-23 — a teammate's permission Ask surfaced in the parent session

- Before: the prompt was registered under the CHILD's sessionID on the one shared `permission.Manager`, and the
  composer renders only the active session's queue, so a teammate blocked in Ask mode was invisible until the
  child's page was opened (board drawer or desktop notification) and hung `running` forever when nobody did.
- Now: `LoopRunner.ParentSessionOf` (`agent/loop.go`, wired to `server.parentSessionOf`, `board.go`) climbs the
  board's `SpawnedBy` edges to the first non-teammate session, and `requestPermission` publishes a SECOND
  `permission.requested` carrying `sessionId` = root session, `ownerSessionId` = the child, `teammate` = handle;
  the cancel path mirrors `permission.replied` "cancelled". `GET /session/{id}/permission` appends
  `pendingTeammatePermissions` (roster BFS, any depth) so a reload restores it; `handlePermissionReply` reads the
  owner from `permissions.Get` before `Reply` and publishes to both sessions.
- Client: `PendingPermission.sessionId` is the OWNER, `teammate` marks a mirrored prompt; `respondPermission`
  posts to the owner's session; `permission-prompt.tsx` chips the handle and skips the focus grab for a mirrored
  one. `ask_user` deliberately not mirrored.
- Verified: `CGO_ENABLED=1 go test ./internal/server/ ./internal/permission/ ./internal/agent/` green (24s/1.4s/37s),
  `npm run build` clean, and the fresh binary run on a throwaway rig (fresh HOME, temp dir, port 7923) served the
  new `index-DvoV_PhC.js`, `/api/board/roster` 200 and an unknown permission reply 404. Not verified visually: the
  live server on 7801 still runs the old embedded `web/dist` and a restart would kill the session.
- Follow-up the first pass missed: answering from the CHILD's page published `permission.replied` only for the path
  session (= the owner), so the parent's mirrored prompt stayed in its composer and 404'd when clicked. The handler
  now notifies every distinct queue holding the prompt — path session, owner, and `parentSessionOf(owner)` —
  pinned by `TestReplyingFromTheChildPageClearsTheParentQueue` (verified failing before the change: reached
  `map[sess-child:true]` only).

## 2026-10-23 — board prompt flipped from "look for a split" to "reach for a teammate first"

- `boardPrompt()` (`internal/agent/agent.go`) led with "Look for a split before you read" and closed with "One
  agent stays the right answer when…", which read as teammate-is-optional. Now it opens with "**Reach for a
  teammate first**", states the default explicitly ("your default is a teammate working beside you"), lists the
  cases that suit a split, makes spawning "the opening move of the turn, not the fallback", gives the one-sentence
  brief test, and demotes the solo case to "Stay with one agent only when…".
- Process step 1 (interactive build prompt) went from "when it splits into independent streams, plan the
  teammates" to "your first instinct is a teammate working beside you … decide the split … before you start
  reading".
- Verification is a separate paragraph now (was folded into the "Spawn early" sentence). `TestBoardPromptPrefersTheSplit`
  asserts the new strings and now REJECTS "Look for a split" / "Spawn early", so the fallback framing cannot creep
  back in. Rendered prompt checked by dumping `agent.BuildAgent.System` from a throwaway `main` inside the module
  (the package is `internal/`, so an external module cannot import it).
- Rule unchanged and still a judgement call: strict sequence, one shared file, an edit too small to brief, or
  research still want one agent.

## 2026-10-23 — board prompt reframed from "reach for a teammate" to "you orchestrate"

- Developer's ask: the main agent should spend its time thinking and deciding **how and how many** teammates to
  swap in, acting as an orchestrator, and only do work itself when it has free time. The previous wording made a
  split the default but read as "spawn one per stream and take a stream yourself", i.e. the parent still
  implemented.
- `boardPrompt()` now opens with "You orchestrate; teammates do the work", adds "Decide the fan-out before your
  first read" (speed and token arguments), "How many, and which" (one per independent stream, no more than the
  parent can verify, disjoint files, shared surface kept by the parent, spawn the batch in ONE tool block), and
  "What you do with your own time" (next decision, shared files, verification, a user question). Process step 1
  asks for the plan, then the teammate count and stream split, before any reading.
- Two prompt-only rails added: do not end the turn while a spawned teammate is still working (nothing starts the
  parent's turn; `catchUpTeammates` only delivers at the next user turn), and a teammate briefed to implement one
  stream implements it rather than delegating again.
- Deliberately NOT changed in code: no live-teammate cap (removed earlier on purpose,
  `TestSpawnTeammateAllowsNestingAndCapsNothing`), and nothing auto-starts the parent's turn when a child finishes.
  Both rails are prompt-enforced only; a harness option stays open if the prompt proves too weak.
- Verified: `gofmt -l` clean, `CGO_ENABLED=1 go build ./internal/... .` OK,
  `CGO_ENABLED=1 go test ./internal/agent/ ./internal/tool/ -count=1` green, `go vet ./internal/agent/` clean, and
  the assembled section dumped from `BuildAgent.System` reads coherently. `TestBoardPromptPrefersTheSplit` asserts
  the new wording and rejects "Reach for a teammate first" / "take a stream yourself".

## 2026-10-23 — Task tool gains background mode

- Why: a sync `task` runs a whole sub-agent inside one tool call, and the parent's batch blocks on `wg.Wait()` until the slowest tool returns — minutes-long research tasks froze the turn's output while everything else idled.
- Design: `task` with `"background": true` → `spawnBackgroundTask` (`internal/server/board.go`) registers a KindTask board child (session type `"subagent"`, read-only prompt, model/permission inherited, budget = sync parity via `stepBudgetFor`) and returns a `task-*` handle + session URL immediately. Delivery reuses the existing teammate completion pipeline end to end — running-turn notice, idler's `CatchUpTeammates`, `teammate wait <handle>`, drawer Stop — no new transport. The launch report states the answer is NOT in the result and points at early collection.
- Child ungating: `resume_routes.go` skips `WithPermissionGating` when `agentName == "subagent"` (read-only toolset); deliberate scope-out: permission BFS/`ParentSessionOf` stay teammate-only.
- Verified: `CGO_ENABLED=1 go test ./internal/tool ./internal/board ./internal/server` all ok (new pins `TestTaskBackgroundRouting`, `TestSpawnBackgroundTaskRegistersATaskChild`, `TestLeaveBoardReportsABackgroundTaskToARunningSpawner`, `TestStepBudgetForATaskChildMatchesTheSyncSubAgent`), `CGO_ENABLED=1 go build ./internal/... .` OK, `npm --prefix web run build` OK, agent board-prompt tests green after the prompt teaching line.

## 2026-10-23 — teammate "op is required": a model-side missing-arg loop, now self-healed

- Reported from the adhyay-platform workspace: a spawning agent kept calling `teammate` with `{name, prompt}` and no `op`, got the soft refusal, and retried — the screenshot showed the call card with the refusal as its output.
- Diagnosis on session `ses_01M5MNCVJVRRXPHB1Y4KJM7XN6` (DB `~/Downloads/work/adhyay/.ogcode/ogcode.db`), model `ogx-glm-5.3-flash`: 8 real failed calls (4× guard-review, 4× timing-review) across ~4 turns before one landed; the model's own reasoning part says "I passed name/prompt but the schema requires op… I keep forgetting the required op" — it copied the broken shape from its earlier failed tool_use replays. The provider does not enforce schema `required`, so the bad call reached Execute instead of being rejected client-side.
- Ruled out: ogcode dropping args (sibling `board` calls in the same session carried `op:"roster"` fine; other teammate calls carried op — 14 wait, 2 status, 8 eventual spawns). The 61 "op is required" text matches in ogcode's own DB and 7 in oglab are TEXT inside reasoning/memory parts, not tool failures — a strict per-part query found 0 real missing-op tool parts in both.
- Fix (user chose "self-heal + better error") in `internal/tool/teammate.go`: op inferred as `spawn` when a prompt is present (inference keys on the prompt, so `{name}` alone still refuses); the empty-op refusal is shape-aware via `missingOpHint` — `handles`/`timeout`→`wait`, `handle`→`stop`/`status`, `name`→`spawn`; the schema `op` description now spells out all four ops instead of "The operation.". An inferred spawn still goes through spawn's prompt validation and its output opens with `op omitted — treated as "spawn"` so the model learns the shape without losing the call.
- Pins: new `internal/tool/teammate_test.go` (`TestTeammateSpawnWithoutOpInfersSpawn`, `TestTeammateMissingOpNamesTheOperation`, `TestTeammateExplicitOpsKeepTheirShape`). Soft refusals stay soft `Result`s — the UI's green check is by design so the model reads and corrects.
- Verified: `gofmt -l` clean on both touched files (`task.go`/`task_test.go` flagged — pre-existing, untouched), `CGO_ENABLED=1 go build ./internal/... .` OK, `go test ./internal/tool ./internal/server -run 'Teammate|Board'` and the full `internal/tool` suite green.

## 2026-10-23 — teammate self-heal installed locally on the ogcode machine

- `make -s install OGX_APP_SECRET=$(tr -d '\n' < ~/.ogcode/ogx-app-secret)` (the HOWTO `Install ogcode on a host` local-checkout line) rebuilt web (board drawer, agent-part UI included from the uncommitted tree) in 14s and the CGO server, `rm -f` + `cp` + ad-hoc re-sign. Installed `~/.local/bin/ogcode` v0.50.2-3-g033be0f-dirty, built 21:01.
- Neither the 20:46 repo `./ogcode` nor the 20:46 installed binary contained the teammate fix — both predated the final `teammate.go` edit — so the earlier "install" did not actually carry the self-heal. The 21:01 install does (strings marker `op omitted`, teammate.go:171).
- Verification: `ogcode version` executes (no stale-signature `Killed: 9`), `codesign -dv` shows `flags=0x2(adhoc)`, `spctl -a` says "rejected" — the known normal state for ad-hoc (binary runs via `com.apple.provenance`, no quarantine xattr). Installed file is 76,598,432 vs repo 76,580,290 bytes — the delta is the embedded signature, identical to the pre-deploy pair.
- Servers up at install time: pid 24536 = ogcode project :7801 (this chat; keeps the old binary until restarted), pid 24405 = adhyay project server (same old binary; needs its own restart to pick up the fix when it matters).

## 2026-10-23 — orchestration-prompt fixes from the review turn

Five findings fixed across six files; all prompt-side claims of the board/teammate surfaces now match loop reality.

- **Teammates no longer get `ask_user`** (the substantive one): `askUserOffered` (internal/agent/loop.go) ends
  `&& !lr.isTeammateSession(sessionID)`; `isTeammateSession` sits just before `parentSessionOf` and is nil-safe
  (CLI/tests → false). Reasoning: teammates are gated for mirroring, not because a prompt reaches anyone; the
  `"subagent"` skip was the precedent. `teammatePrompt` (internal/server/board.go) now forbids the call outright
  ("its dialog would render only on your own page, where nobody is looking, and nothing notifies the parent
  agent mid-task") instead of asserting a premise the model could disprove. Pin: `TestRunLoop_AskUserAbsentForTeammate`.
- **The board rail now names its exits** (internal/agent/agent.go `boardPrompt`): `teammate stop <handle>`
  (cascades to spawns) for a wrong/stuck brief, and parking — end the turn with a plain "so", report delivered
  by CatchUpTeammates at the next turn's start; short-wait chaining called out as "polling with a timer".
- **Spawn timing reconciled with the intent gate**: "as the opening move of the turn" → "right after
  `update_intent` states your plan (the plan goes first, and alone)"; the old phrase is on the pins' STALE list.
- **Dead boilerplate removed**: "Tell it that its last message is its result" cut from `TeammateTool.Parameters`
  (internal/tool/teammate.go) — `teammatePrompt` already wraps every brief with that contract.
- **backgroundTaskPrompt**: "nobody sees progress" → "nobody is watching your page for progress" (the child's
  /session URL is handed back, so the old phrasing was literally false).

Pins extended in `TestBoardPromptPrefersTheSplit` (internal/agent/prompt_workflow_test.go). Verified: `gofmt -l`
clean on the six files, `CGO_ENABLED=1 go build ./internal/... .` OK, `HOME=$(mktemp -d) go test ./internal/agent
./internal/tool ./internal/server -run 'Board|Teammate|AskUser'` all ok (server run isolated — config DB untouched).

## 2026-10-23 — teammate ask_user now mirrors to the parent session

The user asked for parity with the permission mirror, reversing the removal written up in the entry above; a
blocked teammate's ask_user now reaches the human live instead of being deleted from its wire.

- **Loop side** (internal/agent/loop.go): the teammate carve-out in `askUserOffered` is gone (`isTeammateSession`
  deleted — the offer needs only interactive loop + Questions manager). `mirrorQuestion`, right after
  `mirrorPermission`, republishes `question.requested` with SessionID=parent + OwnerSessionID=owner and
  Request.Teammate=child handle; no-op without a bus or when parent==owner; the cancelled path mirrors
  `question.replied` "cancelled".
- **Route side** (internal/server/question_routes.go): the list route BFS-appends the subtree's batches via
  `teammateChildrenOf` (shared with permission routes — a mid-tree session lists its children's questions too;
  only a leaf lists just its own); the reply route is addressed to the OWNER and three-way notifies
  path+owner+owner's root (dedup map by sessionId), so every queue clears from whichever page answered.
- **Client** (web/src): mirrored entries file under the PARENT keyed `ownerSessionId || sessionId`, reply posts
  to that owner (context/session.tsx); ask-user-dialog.tsx gains a teammate chip naming the child.
- `teammatePrompt` now tells a blocked teammate to ASK instead of ending its turn; the block still has no
  timeout, but a mirrored dialog is answerable at once, so the child-hang risk the removal targeted is gone.

Pins: `TestRunLoop_AskUserOfferedForTeammate` (replaces the absent-pin), `TestTeammateQuestionMirrorsToTheParentSession`
(+cancellation, +silent-without-a-parent — question_mirror_test.go), `TestParentSessionListsTeammateQuestions` and
`TestReplyingAQuestionFromTheParentUnblocksTheTeammate` (question_routes_test.go). Verified: `CGO_ENABLED=1 go build
./internal/... .` OK; web `npx tsc --noEmit` + `npm run build` green; go test ./internal/question ./internal/tool
./internal/agent and the full ./internal/server suite (isolated HOME) all ok. No live E2E with a real model.

## 2026-10-24 — agent drawer UI/UX pass

- Split `board-drawer.tsx` into a shell + `board-drawer-row.tsx` + pure `web/src/lib/board-tree.ts`; added a
  filter (handle/title/model/kind/session/spawnedBy), collapse chevrons, indent rails, disjoint header chips,
  a `Finished N` tick toggle, skeleton, `role="alert"` error banner with Retry, and a `<details>` explainer.
- `GET /api/board/roster` rows now carry `title`/`model` joined from `s.store.Get` (guarded, `omitempty`), so a
  long agent list is named rather than a wall of handles. Old `[]board.Handle` decoders are unaffected.
- **Chip bug found by the screenshot, not by types**: 28 agents rendered "15 live · 14 idle · 13 finished" = 42,
  because `rosterSummary().live` already includes idle. Fixed by deriving `running = live − idle`.
- **Screenshot method**: `chrome --headless --screenshot` HANGS on the session page (open SSE never idles);
  a stock "Google Chrome for Testing" with `--remote-debugging-port` + CDP `Page.captureScreenshot` returns at
  once. The old "captureScreenshot hangs" note in HOWTO was ego-lite-specific.
- Verified: tsc clean, `npm run build` green (only the >500 kB chunk warning), `go build ./internal/... .` OK,
  `go test ./internal/server -run Board` ok; live roster 28 agents / nesting levels confirmed by DOM query.

## 2026-10-24 — Finished toggle restyled neutral (accent confined to the checkbox)

- The user flagged the drawer's filter row: the checked "Finished N" toggle rendered as an accent
  slab (`accent-ring` border + `accent-soft` wash + `accent-ink` text + full accent checkbox) — in a
  palette where red = failure, sitting right under green finished chips. Restyled in
  `board-drawer.tsx`: the pill stays neutral (`border-default` + `text-primary`, h-7 like the filter
  input, hover/focus ring kept); only the 12px rounded checkbox carries accent + tick. The trigger
  button's accent open-state was deliberately left alone (it is behind the scrim when open).
- Verified dark AND light on the real 28-agent roster via the CDP rig: neutral pill both states,
  toggle flips 15→28 rows, `/api/appearance` stayed `{\"mode\":\"system\"}` throughout.
- **Throwaway screenshot rigs render LIGHT, not the user's dark**: headless chrome reports a light
  OS, and the app's boot reconcile (GET `/api/appearance`, server-global) wins over any
  `localStorage['ogcode-theme-mode']` you write pre-load — setItem + reload still resolved light.
  Emulate instead: CDP `Emulation.setEmulatedMedia{features:[prefers-color-scheme:dark]}` BEFORE
  `Page.navigate`. No server write, no localStorage write; `dataset.theme` comes back `dark`.

## 2026-10-24 — Six-stream review of the uncommitted multi-agent tree: zero BUGs

Scope: 51 modified + 30 untracked files (board/teammates, file-change gate, ask_user+permission mirroring,
token/usage teammates arrays, web drawer/pill). Method: six parallel reviewer teammates (agent loop, server
routes, tool gate, board core, web, provider/session), each running its own packages' tests with an
HOME-isolated rig; parent ran `CGO_ENABLED=1 go build ./internal/... .` + full `go test ./internal/... .`
(green, zero FAIL), `tsc --noEmit` clean, `-race` on internal/server green (122 s), go vet clean where run,
migrations 065–067 never claimed in git history (no goose collision), server↔client `teammates` contract matches.

Top unfixed RISKS in fix-priority order:
1. `internal/tool/read.go` — `log.Read` (:206) stamps the bookmark BEFORE the map-vs-content branch (:233);
   a bare >200-line read that returns an outline certifies content the model never saw, so the next edit is
   NOT refused (the gate's missed-conflict direction). Fix: move the map-vs-content decision above `log.Read`.
2. `internal/server/session_routes.go:582-594` + `usage_routes.go:84-97` — a transient error on ONE teammate
   child 500s the whole token/usage response; the roster join already degrades per-row, this should too.
3. `internal/server/board.go:121` — `boardTurnStart` is the only board helper without a nil-board guard; a
   plain `Server{}` embedder panics at first prompt (serve() is safe).
4. `internal/agent/loop_control.go:274` — `InterruptForNotice` is dead code: armed every step, never fired,
   so teammate reports never pre-empt a step (delivered only post-step). Wire `reportTeammateDone`/
   `CatchUpTeammates` to it or delete it.
5. Report-loss window: `reportTeammateDone` pushes the notice then stamps `notified_at` (board.go ~:200); a
   parent turn ENDING between push and DrainNotices loses the report permanently — same hole as the documented
   crash window, but without the crash.
6. `web/src/components/token-pill.tsx:132` — utility fold keeps only the session's own utility; teammates'
   utility (inside their input/output) never reaches the "of which utility" row (display-only).

Also (secondary): observeShell snapshots the before-fingerprint outside the file lock (TOCTOU → possible
duplicate misattributed catch-up row, safe direction); doc comments (permission.go:36-39, question.go:124)
deny Request.Teammate on the child's own batch while loop.go:3147/:3281 set it — child's own page renders a
chip naming itself; PartAgent/AgentPartData + agent-part.tsx have no writer (unreachable dead UI);
teammateLive counts idle as live so the 5 s token refetch never stops; token-pill refires per SSE event without
debounce; stale-refusal instruction sits at the tail where the 50 KB KeepHead cap can cut it; empty
question/permission lists marshal null (pre-existing); `teammate, *, Ask` makes spawn prompt where it used to
fall through catch-all Allow (intended per docs/pins); HISTORY/HOWTO carry absolute user paths (no secrets).

Confirmed sound (highlight reels): mirror payloads field-exact and pinned (7+4+6 targeted tests); cache
breakpoints stay outside entry [0] byte-identical, notices appended after the breakpoint; no reasoning
double-count; Yolo cannot bypass an explicit Deny; claim-before-cancel (`SetStatusUnlessTerminal`) pinned;
Seen -1/0 semantics + empty-ring restart fallthrough pinned; migrations SQL sound with symmetric Down;
zero-Handle contract honored at every call site that matters; queue dedup by id, replies POST to the owner;
tokens fold client-side only; no XSS, typecheck clean.

## 2026-10-24 — Fix turn for the review's six findings + secondaries (4 parallel streams)

Four fixer teammates (gate-read-fix, routes-fix, board-loop-fix, web-pill-fix) over disjoint file sets; parent
finished board-loop-fix's handovers (F4 deletion + the two doc comments). Nothing committed.

- **F1 gate**: `internal/tool/read.go` — the map-vs-content decision moved ABOVE the `log.Read` bookmark stamp;
  a map-only read no longer certifies unseen content. Pin `TestMapOnlyReadDoesNotCertifyContent`:
  ranged read → foreign write → map-only re-read keeps Seen=0 → stale edit refused → content read moves Seen →
  edit applies. Side effect: a map-only FIRST contact leaves the file untracked (no fingerprint; bash observation
  records nothing until a content read).
- **F2 rollups**: `session_routes.go`/`usage_routes.go` — a teammate child whose `store.Get`, step-sum, or
  `usage.ForSession` read errors is skipped per-child with a warn; parent-side reads still 500. Pin
  `TestSessionTokensSkipsATeammateWhoseReadErrors` (child's nullable `permission` TEXT poisoned to NULL).
- **F3 nil board**: `boardTurnStart` opens `if s.board == nil { return nil }` — embedder `Server{}` safe.
- **F4 dead interrupt deleted**: `InterruptForNotice`/`ArmNoticeInterrupt`/`interruptArmed` (`loop_control.go`)
  + their nil-safe test lines. Evidence: `RunLoop` never called it; delivery is per-step `DrainNotices` +
  `DeliveredNotices` injection.
- **F5 report-loss**: `reportTeammateDone` no longer marks at push for a running parent — notice +
  `BoardReport{Text, Mark}` ride `LoopRunner.TrackBoardReport` (package-level `boardReports sync.Map` keyed by
  `*LoopRunner`, not a field); both `RunLoop` drain sites call `BoardNoticesDelivered`, so `notified_at` stamps
  at DRAIN and a turn that ends before its drain re-delivers via `CatchUpTeammates`. Crash window became
  at-least-once (accepted). Catch-up push and `wait` keep stamping at push. Pins rewritten
  (`TestLeaveBoardReportsCompletionToARunningSpawner`, `TestStopTeammateOfAFinishedTeammateChangesNothing`,
  `TestLeaveBoardReportsABackgroundTaskToARunningSpawner`) + new `board_reports_test.go` (3 tests) +
  `TestTeammateReportUndrainedAtTurnEndStillDelivers`.
- **F6 token pill**: `token-pill.tsx` — utility fold includes teammates' `t.utility` (subtotal, not atop
  effective/total); `teammateLive`→`teammateRunning` so the 5s refetch rests when nothing runs; SSE board-event
  refetch debounced 150ms trailing (inline, mirroring `board-drawer.tsx`).
- **Secondaries**: `bash_observe.go` before-fingerprint snapshot moved inside `lockFileKey` (TOCTOU);
  `Request.Teammate` doc comments corrected in `question.go`/`permission.go` (set on every copy, incl. the
  child's own page — pins assert it IS set); parity-hole paragraphs in `permission_routes.go`/`question_routes.go`;
  absolute `/Users/prasenjeet/` paths scrubbed from HISTORY/HOWTO.
- **Left unfixed by design**: `agent-part.tsx` dead UI (no writer yet), same-size+same-mtime fingerprint gap,
  `reconcileBoard` startup demotion (intended), Ask-on-spawn (intended per docs/pins), empty permission/question
  lists marshal `null` (client keys off entries), message-driven pill refetch (per-step is intended).
- **Verification**: `CGO_ENABLED=1 go build ./internal/... .` + vet + gofmt clean; full
  `HOME=$(mktemp -d) CGO_ENABLED=1 go test ./internal/... .` all ok (29 packages); `tsc --noEmit` clean; `-race`
  server `-run 'Board|Teammate|Roster|Unreported|CatchUp|Token|Usage'` ok 36.5s; `-race` agent
  `-run 'Notice|Loop|Board'` ok 70.5s.

## 2026-10-24 — Where the 784 MB project DB actually goes (and why `file_change` is not it)

Question asked: does storing diffs pollute the DB / grow it exponentially?
Measured on this project's live `.ogcode/ogcode.db` with `sqlite3 'file:…?mode=ro'` + `dbstat`.

- **File 784,142,336 B** (`page_size` 4096 × 191,484 pages), `journal_mode=wal`, `freelist_count=0`,
  `auto_vacuum=0`.
- **`part` = 723.3 MB (92.2%)** — 154,069 rows in 1568 sessions, avg 3,993 B, max 3,117,066 B (a base64 PNG).
  Buckets: `<1KB` 49,918 / `1–10KB` 94,212 (297 MB) / `10–50KB` 8,910 (164 MB) / `>50KB` 1,029 (124 MB).
  `message` 33.6 MB, its three indexes ~20 MB.
- **`file_change` = 581,632 B (0.07%)** — 197 rows. `edit` 150 (330 KB diff), `write` 19 (39 KB), `bash` 28
  (0 diff, notes only). Avg diff 1,872 B, max 7,528 B against the 8,192 cap; **90/197 rows (46%) `diff_omitted`**.
  Avg row ≈ 2,270 B of text; 2,952 B/row all-in with both indexes.
- Rate over the table's whole 18.6 h life: **~250 rows/day, ~0.7 MB/day** — worst case bounded at 200 rows ×
  10 KB. A year of days like that ≈ 200–260 MB; a year at ordinary intensity, far less.
- **By session type**: `build` 433 sessions = 548.5 MB (1,297 KB/session) — 89% of the bytes; `index` 1,132
  sessions = 37.6 MB (34 KB/session, 1,132 rows of bookkeeping for 8% of the size). Fattest single session
  39.0 MB ("Request for Current Branch Name").
- **No pruning and no reclaim anywhere**: `internal/db/db.go:23` sets only `journal_mode(WAL)`,
  `foreign_keys(1)`, `busy_timeout(5000)`; `grep -rn "VACUUM\|auto_vacuum"` over `internal/` is empty. The only
  deletes are `Store.Delete` (session) / `DeleteMessage`, and `foreign_keys(1)` means the cascades do fire —
  `SELECT count(*) FROM part WHERE session_id NOT IN (SELECT id FROM session)` = 0.
- Sibling disk, not the DB: `.ogcode/ogcode.db.bak-057` 492 MB (stale manual backup), `~/.ogcode/memory.db`
  80 MB, `~/.ogcode/embed-model` 128 MB, `~/.ogcode/logs` 27 MB. Filesystem had 15 GiB free — a `VACUUM` of
  784 MB would need roughly that much again in temp space.

Conclusion: mutation history is linear in edits and hard-capped per row; the growth is the transcript, which
nothing bounds except session deletion.

## 2026-10-24 — Sidebar design D (agent-team chip)

Chosen from a 4-design mockup (A nest-in-place, B team sections, C full-tree rails, D cluster chip);
developer picked D. Implemented in `web/src/components/session-sidebar.tsx`:

- Roster join: climbs `spawnedBy` edges of `/api/board/roster` cycle-safely to the root board handle,
  filters the pseudo `user` handle, skips the session's own root row, keys by `root.sessionId` →
  `{rootHandle, members}`. No DB change — migration 067 already carries the edges.
- Chip between title and time: orange circle = live member count, grey = finished-only, folded by default.
  Click toggles an inline team fold (rails/elbows, status dots, "T" badge for task children) with
  stopPropagation so folding never selects the session; member rows do select.
- Refresh: SSE events prefixed `board.` debounced 150 ms + `server.reconnectTick()` + 15 s interval backstop.
- Verified against the live :7801 server via a throwaway vite config on :5199 (proxying /api) with
  ego-browser — chips rendered on real sessions ("Show 10 agents", "Show 3 agents"), fold/expand works,
  chip clicks do not navigate. Procedure recorded in HOWTO.md.
- `web/dist` rebuilt and `./ogcode` binary rebuilt with it, but the LIVE SERVER WAS NOT RESTARTED (it runs
  the active session) — the web change is not live until the developer restarts it.

## 2026-10-24 — Which of the three diff caps actually binds (`file_change`)

Asked whether `DiffMaxBytes` should be removed "while storing". Three caps stack, and they do not bind alike —
measured on `.ogcode/ogcode.db` (`sqlite3 -readonly`), 206 `file_change` rows / 388 KB of diff:

- `maxDiffBytes = 256 KB` (`internal/tool/write.go:79`, either side) — above it no diff is computed at all, and
  the row is `diff_omitted` with empty text. 94 rows omitted, 29 empty (bash rows carry notes only,
  `bash_observe.go:62`).
- `diffMaxChangedLines = 64` + `diffContext = 3` (`internal/filechange/diff.go:11,17`) — **fired in 65 rows
  (32%)**, every one of them ending in `(diff truncated)`.
- `DiffMaxBytes = 8 KB` (`internal/filechange/filechange.go:34`) — **fired in 0 rows**; no stored diff contains
  the `… (truncated)` marker. Largest real diff is 7,528 B (an `edit` to `MEMORY.md`), 664 B short of the cap.

So the 8 KB cap is nearly binding but has never bound: it is a backstop against a pathological long line
(minified bundle, JSON one-liner, base64 blob) in a file under 256 KB, not the thing that discards content.
The 64-line budget is, at 25× the frequency. Removing `DiffMaxBytes` would not restore any lost bytes today
and would remove that backstop — and because `capped()` runs once in `Log.Record` (`log.go:325`) into the ring
`staleRefusal` renders (`internal/tool/staleness.go:36`), it is also the *refusal's* display cap; after a
restart the rows are re-rendered verbatim from `history` (`log.go:296-298`), so there is no render-time
re-application. Splitting storage from display would need a fourth meaning on `DiffOmitted`, which today
conflates "line-truncated", "no baseline" and "byte-capped".

## 2026-10-24 — Board report delivery restamped at dispatch; 7-bug teammate/board sweep

Fixed the audit's 7 board/teammate bugs (two teammates: agent-side + server-side, disjoint file sets, merged and re-verified by the parent):

- **Stamp at dispatch, not drain** (bug 1; was: stamped when drained — the pre-stream gap restart then re-took nothing and shipped a one-line "already given above" digest pointing at text no request ever carried, with the row already stamped; any drain→dispatch failure kept the stamp). RunLoop hoists `wireFresh`/`wireDigest` out of `TakeNoticesForInjection` (loop.go ~1163), the gap `continue` calls the new `LoopControl.RestoreNotices` (loop_control.go:301 — restores to the FRONT of `unshown`, idempotent, leaves `deliveredNotices` alone so digests never shadow restored full text), and the only `lr.BoardNoticesDelivered` call left is inside the dispatch-success block (loop.go ~1356). Provider connection retries re-enter that block, so only the eventual success stamps, once.
- **`catchUpTeammates` no longer stamps at push** (bug 2; board.go ~314-399): each pushed report is tracked via `loopRunner.TrackBoardReport` with a Mark closure (own Background ctx + `boardOpTimeout`) like `reportTeammateDone`; a runner-less server falls back to the push-time stamp. A turn dying before its first dispatch leaves rows unreported and the next turn's catch-up re-pushes (at-least-once).
- **Lying comment rewritten** (bug 3): waitTeammate's slice loop no longer claims "a notice pushed into this loop cancels the generation" — the true reason is that a notice can only inject when the tool call returns, so the loop breaks early on `lc.HasPendingNotice()`.
- **`stopTeammate` reports "was stopped"** (bug 4): `reportTeammateDone` at board.go:935, guarded on `SpawnedBy`/`SessionID` so legacy fixture rows and `stopTeammatesOf` (the abort/delete cascade — deliberately report-free) still push nothing; `leaveBoard`'s claimed-already early return prevents double reports when the child winds down.
- **Handle collisions no longer overwrite** (bug 5): `board.RegisterFresh` = insert-once (`ErrHandleTaken`; "UNIQUE constraint failed" string-mapped; store-mutex-serialized Get-check + plain INSERT) beside the untouched `Register` upsert; server `mintFreeHandle(ctx, reg, attempts, newName)` (board.go:103) rolls a 4-hex suffix up to `boardHandleAttempts` = 8; wired into `joinBoard`, `spawnTeammate`'s random path (claims the handle BEFORE the child session is created so the "New teammate …" title stays truthful), and `spawnBackgroundTask`; typed names keep the friendly Get pre-check and go through RegisterFresh.
- **`waitTeammate` honors the turn's cancel** (bug 6): breaks its 250ms slice on `errors.Is(ctx.Err(), context.Canceled)` (board.go:791) so an abort / superseding prompt / session delete returns the partial "Still running" answer promptly instead of waiting out ~2 min; `DeadlineExceeded` is deliberately not a break — that is the tool's own op bound and the wait's wctx carries it past.
- **Bug 7 (restart cascade) was an audit overstatement**: supersede via `startSessionLoop` only cancels the old loop (resume_routes.go); the teammate cascade lives only in `handleAbortSession`, `handleDeleteSession` and the hosted worker's `cancelLoop`. Pinned as correct behavior, no code change.

New pins (13, all verified by name): agent `TestRunLoop_NoticeGapRestartRidesTheNextRequestFull`, `TestLoopBoardReportsStampOnlyAtDispatch`, `TestRestoreNoticesRequeuesFreshWithoutDigestLie`; server `TestStartSessionLoopSupersedeKeepsTeammates`, `TestMintFreeHandleRetriesPastCollisions`, `TestMintFreeHandleExhaustsItsRolls`, `TestSpawnTeammateRefusesANameSomebodyHolds`, `TestWaitTeammateReturnsPromptlyWhenTheTurnIsCancelled` (~0.55s vs the 120s bound), `TestWaitTeammateKeepsWaitingWhenOnlyItsOpContextExpires` (~2.08s); store `TestRegisterFreshClaimsAnUnusedHandle`, `TestRegisterFreshRefusesATakenHandle` (original row byte-untouched), `TestRegisterFreshRefusesReservedHandles`, `TestRegisterStillUpsertsARejoin`. Rewritten pins: `TestCatchUpTeammatesReportsAnIdleSpawnerOnce` (tracked not stamped; re-push needs a fresh lc — a dead turn's lc dies with it), `TestTeammateReportUndrainedAtTurnEndStillDelivers` (drains by hand via `BoardNoticesDelivered`), `TestStopTeammateIsNotRewrittenDone` (+ exactly one "was stopped" report).

Verified: `go build ./internal/... .` OK; `CGO_ENABLED=1 go test` — agent 37.9s, board 0.98s, tool 2.5s all ok, server scope `Board|Teammate|CatchUp|Wait|Stop|Spawn|Join|Mint` 36 tests 6.7s ok; gofmt clean on all eight touched files. Known leftovers: a stream cancelled between take and dispatch doesn't stamp (row re-delivers next turn — accepted, much narrower than the fixed windows); `TestServe_StopFromAnotherGoroutine` flaked once under full-suite load (live openrouter/ollama fetches at server start; passed 3/3 isolated — serve-lifecycle owned, pre-existing); one recovered panic seen once in a server test log (`tool.Registry.Get` nil deref at loop.go:738 from a subagent goroutine; test passed; none of the changed regions touch that code — pre-existing, unverified).

## 2026-10-24 — Released v0.51.0

First tagged release carrying the multi-agent body of work. Seven feat/chore commits landed on `feat/multi-agent` and fast-forwarded to main (tip `6e0c6e8`, "chore(release): v0.51.0"): agent board/teammates + orchestrator prompt, question & permission mirror into the parent composer, the file change gate (066), custom OpenAI-compatible providers in ogcode.json, teammate spend folded into the client-side token pill, extended cache TTL, docs/plans. Version bumped in 4 places (version.go `v0.51.0`, web/package.json + both lock entries `0.51.0`); the v0.51.0 annotated tag pushed, CI run 40083679642 green, Release run 40083933722 and Docker run 40083933674 both success. 7 assets published with edited GitHub release notes (`gh release edit --notes-file` from the top of RELEASE_NOTES.md). Done-when verified: downloaded `ogcode_0.51.0_darwin_arm64.tar.gz`, cleared quarantine, binary prints `ogcode v0.51.0` at commit 6e0c6e8.

## 2026-10-24 — Agent timers (`schedule` tool, migration 068)

One-shot agent reminders, built with a 3-teammate fan-out (timer-store / schedule-tool / delivery, integrated and verified by the parent). Design agreed with the developer first: one-shot only (cron recurring design ruled out), delivery as a system notice riding the board-report rails (fake user messages ruled out — would confuse turn-slicing and the transcript is append-only).

- **Storage**: `internal/db/068_agent_timer.sql` — table `agent_timer` (id "t"+6hex PK, session_id intentionally NO FK so a session delete can't fail an insert, note, fire_at, status, unix-second timestamps per board convention), indexes `agent_timer_session` (session_id,status) and `agent_timer_claim` (status,fire_at). Statuses pending → due → delivered, cancelled on cancel. `ClaimDue` is a single atomic `UPDATE pending→due WHERE fire_at<=now RETURNING` — two claimers can never double-claim (store pins: claim-once, restart persistence, cancel-own-session-only, delivered-not-reclaimed, list ordering).
- **Tool**: `internal/tool/schedule.go` mirroring teammate.go (`ScheduleFunc`/`ScheduleRequest`/`ScheduleResult`; ops create/list/cancel; refusal = Result-shaped, not error; exactly one of delaySeconds>0 or strictly-future RFC3339 fireAt, non-empty note). "schedule" added to `codingAgentTools`. Schema `required` carries op only (teammate.go convention — note is validated, not schema-required).
- **Delivery**: `internal/server/reminder.go` — `pollReminders` ticker (10s, immediate first pass) claims due rows; if the session's loop is UP, `PushNotice` Cause `"reminder"` + `TrackBoardReport` whose mark = `MarkTimerDelivered` (BoardReport.Text byte-identical to the notice; stamp only at dispatch, same at-least-once semantics as teammate reports). If DOWN (idle/dead), the row stays `due` and the `CatchUpReminders` LoopRunner hook (build-gated like the teammate one) delivers at that session's NEXT turn start — nothing starts a turn. `loop_control.go` renders reminder fresh/digest sections beside the teammate ones (cause-based partition; all non-reminder notices byte-identical to before).
- **Verified**: `CGO_ENABLED=1 go build ./internal/... .` + root binary ok; full test pass on timer/tool/agent/server; gofmt dirty files are pre-existing only. Memory note in MEMORY.md under Agent tools; next migration is 069.

## 2026-10-24 — Why ogcode ran ~2× slower than OpenCode on the ogx bench (mechanism)

Decomposed with a fit on `runs/*/wire.jsonl` (ms vs completion tokens): **ms ≈ 1.2–2.4 s per-request floor (queue+TTFT) + 3.0 s per 1K completion tokens (335 tok/s)**; residual is flat across prompt quintiles, so context size explains nothing (cache hit 87–94%). Runs in `/Users/prasenjeet/Downloads/work/ogcode-bench`, probe script `/tmp/ogx_test.py`.

- **The endpoint thinks by default, and BOTH arms think.** Live probe, 3 non-streaming calls to `ogx-deepseek-v4.1-flash`: no `reasoning_effort` → 72 completion tokens / 203 reasoning chars; `reasoning_effort:"none"` → 20 tokens / 0 reasoning; `"high"` → 96 / 280. ogcode sets `Thinking:true` on every loop request but `applyEffort` writes nothing when session effort is unset, so its bench runs paid default-effort thinking; catalogue default is EffortHigh. **First framing was wrong** ("OpenCode's streams carried zero reasoning parts"): a wire relay in front of the gateway captured both clients' HTTP bodies — NEITHER sends a reasoning field (OpenCode body: `max_tokens, messages, model, stream, stream_options` + tools; ogcode: `messages, model, prompt_cache_key, stream, stream_options, tools`), so with the endpoint defaulting ON, both think. `run.json` `reasoning: 0` is an accounting artifact on both sides: ogcode's `output` already contains reasoning; OpenCode's `@ai-sdk/openai-compatible` simply does not map the gateway's `reasoning` delta, so those tokens land in its completion count (wire completion vs reported: 29292/29053, 28423/27978, 24784/24749 — ~same shape, 52–58 response bytes per completion token on both arms). ogcode `--effort none` adds `reasoning_effort:"none"` and probe output drops to 2 tokens — the knob is proven.
- **Real difference: ogcode reasons ~2× longer per turn** (mean out/turn 836/635/559 vs OpenCode 338/379/349, r1–r3). The earlier "think on vs off" isolation arithmetic must be re-read with the parity caveat below.
- **Arithmetic caveat — the parity pair is NOT matched**: `runs/parity-ogcode/run.json` carries `effort: "none"` while OpenCode has no effort flag and cannot be turned off. Its 17,510 (ogcode, thinking off) vs 9,329 (OpenCode, thinking on) is therefore a LOWER bound on ogcode's reasoning habit, not a like-for-like pair. A matched both-off pair is impossible on this endpoint.
- **Latency law unchanged**: reasoning is billed inside completion and streams at the same decode rate; the extra ~2× reasoning per turn costs ~3 s per 1K tokens, which is exactly what the fit priced.
- **Turn multiplicity**: ~1 model call per tool call. r2: ogcode 147 turns / 171 tools (96 bash ≈ one grep each, 46 read) vs OpenCode 74 turns / 81 tools with compound bash (four `sed -n` windows in one call). At a 1.2–2.4 s floor, cheap local tools are exactly what a round-trip tax punishes.
- **Confound noted**: only the ogcode arm carried the prompt_suffix in the r1–r3 pairs, but both-suffixed historical pairs keep ~2× output tokens, and the token gap persists with matched model+endpoint — the gap is mostly turns + heavier reasoning, not harness asymmetry.
- Earlier "big prompt / TLS handshake" explanations are not supported by the data: small-output requests already cost ~1.2 s and the prompt-quintile residual is flat.
- **Quantified re-decomposition from wire.jsonl totals, r1–r3 (2026-10-24)**: wall clock is NOT slower — ogcode Σ 1413 s vs OpenCode Σ 1496 s (r3-opencode carried a 153 s slow-tail upstream). The real gaps are in-model time (1181 vs 715 s, +65%) and cost ($0.700 vs $0.467, +50%: +$0.112 output volume, +$0.12 uncached prompt). ~86% of the +93k output-token gap is per-turn verbosity (666 vs 357 tok/req), only ~14% is turns (270 vs 235 requests); generation throughput is equal (~150 tok/s both arms), so in-model time scales with tokens. ogcode's bookkeeping tools (update_intent 10, compact_context 8, codebase_map 3, file_map 12) = 33 calls ≈ 10% of its tool calls, each a paid round trip. Cache hit 87% ogcode vs 93–94% opencode — each compaction cliff (r1-og rows 29/55/60) re-warms the cache from scratch, ~$0.12 across the three runs. Local overhead favours ogcode (0.86 s/req vs opencode's 3.3 s/req): nothing to fix in transport or tool dispatch. Parity (`--effort none`) collapsed out/turn 666→175 (−74%) and median request latency 2930→1850 ms but FAILED the grade at the 100-turn cap still mid-task — thinking off alone makes the model too shallow for ogcode's turn structure to converge. The old "~2× slower" headline is in-model time, not wall clock.

## 2026-10-24 — Bench-gap fixes shipped in parallel (effort default, adaptive effort, compound-call prompt, intent softening)

Implemented steps 1–5 of the fix plan with two teammates (files kept disjoint: effort stream owned `loop.go`+`internal/provider`, prompt/intent stream owned `prompt_builder.go`+`intent_gate.go`+`internal/tool/update_intent.go`). All `CGO_ENABLED=1 go test` on provider/agent/tool passes; **the bench re-run has NOT happened — the out/turn ≈350 target is unverified**.

- **Step 1 — flash-class effort default**: `applyEffort` (`internal/provider/openai_effort.go`) now defaults unset session effort to `low` when the model id contains "flash" (`defaultEffortFor`), instead of letting the endpoint default thinking-ON/high apply. Explicit effort and non-flash models unchanged.
- **Step 2 — adaptive effort**: new `StreamRequest.StepMechanical`, set at the loop tail (`loop.go` `nextStepMechanical = toolCallsExecuted && currentText.Len() == 0`) — a step following a tool-only step. `applyEffort` holds mechanical steps to `none`, falling back to `low` when the ladder rejects none (glm-5.3-flash). This guards against the parity-run failure (thinking off alone failed to converge).
- **Step 3 — compound tool-calls guidance**: ~~new bullet in `parallelToolCallsPrompt` — fold repeated sed/grep probes into ONE bash call ("four sed windows in one call, not four calls").~~ **STRUCK 2026-10-25**: no such bullet exists in `parallelToolCallsPrompt` (checked in full; repo-wide grep finds nothing) — it never landed in any commit.
- **Step 5 — ritual trims**: `deepSearchPrompt` now caps the post-search report at a short synthesis; `decisionSummaryPrompt` gates `## Decisions & why` to turns with real decisions.
- **Step 4 — update_intent softening**: the standalone-first mandate and the closing-call mandate are dropped from `tool/update_intent.go`, `prompt_builder.go:1135` and the gate reminder ("it may ride the first tool block"); the intent-gate reminder now fires ONCE PER PLANDLESS STINT
  (`intentGate` registry keyed by sessionID, deleted by `markPlanSet`, so a session that planned once re-arms for a later planless stint — a markPlanSet-delete semantic, not strict once-ever). Correction 2026-10-25: this entry originally wrote "once per session" immediately beside "re-arms" — only the re-arms wording is accurate. Status-movement pins preserved. Pins rewritten: `TestIntentGateFiresOncePerSessionAcrossTurns`, `TestUpdateIntentDescriptionMakesTheClosingOptional`, `TestUpdateIntentDescriptionMayRideTheFirstBlock`; new wire pins `openai_effort_flash_test.go`, `effort_mechanical_test.go` (`TestRunLoop_StepMechanicalFollowsToolOnlySteps`).
- Rejected by data (unchanged): transport/TLS, connection pooling, local tool dispatch, more teammates, prompt-context reduction (see 2026-10-24 mechanism entry). Cache (87% vs 93%) left alone, revisit only if compaction fires more often.

## 2026-10-24 — HTTP compression audit: providers gzip responses, nothing else is compressed

Asked whether ogcode uses gzip for request/response. Answer: only implicitly, on the provider response path.

- **Provider responses ARE gzipped at transport level**, accidentally: `streamHTTPClient` (`internal/provider/provider.go:176`) leaves `DisableCompression` false and sets no `Accept-Encoding`, so Go's Transport adds `Accept-Encoding: gzip` and transparently decompresses. Proved with a local gzip test server using the same transport shape: server saw `Accept-Encoding: gzip`, client read plaintext with `resp.Uncompressed == true`.
- **Provider requests are NOT compressed** — no `Content-Encoding`, no gzip writer anywhere in `internal/provider`. So `checkBodyFits` (`openai.go:1119`) is measuring UNCOMPRESSED bytes against the 4 MiB gateway cap; gzipping the body is unused headroom there.
- **The local server compresses NOTHING**: `routes.go:11-26` middleware is RequestID, previewHostDispatch, RealIP, accessLog, recoverer, cors — no `middleware.Compress`. Live check: `GET /api/config` and `GET /` with `Accept-Encoding: gzip, deflate, br` both came back with no `Content-Encoding`. Incoming gzip request bodies are never decompressed by any handler.
- **Cost of that**: `web/dist/assets/index-*.js` is 7,794,247 bytes raw, 2,314,241 gzipped (3.37×) — every page load pulls the uncompressed bundle. Behind Cloudflare the edge likely compresses JS anyway; the Go server does not, and docs/deploy carries no nginx/compression config of its own.
- **The one explicit gzip path is search**: because browser-persona requests set their own `Accept-Encoding` (`internal/search/persona.go:64-98`), Go stops auto-decompressing, so `decompressResponse` (`internal/search/transport.go:257-330`) hand-decodes gzip/deflate/br/zstd.
- Unrelated: log rotation gzips rotated files (`internal/logging/rotate.go`, `OGCODE_LOG_COMPRESS` default on). Controlplane worker↔master (connect-go over h2/h2c, `internal/worker/worker.go:255`) has no compression options set — library defaults only.

## 2026-10-24 — flash-class effort default measured on the bench: −2.4× output, no endpoint speedup

The OGX bench run `v051-ogcode` (`ogcode-bench/runs/v051-ogcode`, 87 turns, 270.3s) was run by the binary built 21:46 from the working tree that carries the uncommitted flash-class effort default, so it measures that policy, not v0.50-vs-v0.51.

- **Endpoint speed unchanged.** Per-request regression over `wire.jsonl` (ms vs completion tokens, 89 requests): slope **2.845s/1K completion**, intercept **1.717s** — inside the 2.61–3.23s/1K slope and 1.72–4.05s intercept the three ogx-r\* runs give. The intercept is the only component that drifted down over the four runs (4.05 → 1.95 → 1.79 → 1.72), and it is gateway queue+TTFT, which no build of ours touches.
- **Wall decomposed at that fit**: 218.9s model wait (81%) + 51.4s local (19%) of 270.3s. Output fell 30,725 → 23,239 (−33s of decode) but requests rose 56 → 89 (+52s of floor), so the run is **32s slower than `ogx-r3`**, not faster. The floor is per REQUEST, so trading tokens for turns loses.
- **What the token drop is**: `applyEffort` sends `reasoning_effort: low` for a flash-named id when the session chose none, and `none` when the previous step was tool-only (`nextStepMechanical`, `loop.go:2195`). Response bytes per completion token 52–53 before, **20.6** here — the reasoning text left `completion_tokens` because the request asked for it to. `defaultEffortFor` is in the installed binary and absent from HEAD; `go test ./internal/provider -run 'Effort|Mechanical'` passes. Nothing compresses or re-bills it.

## 2026-10-24 — connection reuse: one h2 connection to ogx, one per request over h1.1

- `streamHTTPClient` (`internal/provider/provider.go:176`) is a single pooled client: no `DisableKeepAlives`, no
  `req.Close`, `ForceAttemptHTTP2: true`, `IdleConnTimeout: 30s`, no `MaxIdleConnsPerHost` (Go default 2). Every
  streaming request — OpenAI, Anthropic, OGX — goes through it. The only `CloseIdleConnections()` call is the
  local-write-failure retry (`provider.go:211`).
- Production ogx negotiates **HTTP/2** (probe: `GET /v1/models` → `proto=HTTP/2.0`, 401 unsigned; a second request
  that closed the body after 10 bytes reported `GotConn.Reused=true`). Per-step requests are h2 streams on one
  connection, so "a new connection each step" is false against the real gateway.
- `streamEvents` (`openai.go:1217`) breaks at `data: [DONE]` and closes without draining. On HTTP/1.1 that
  discards the socket because the chunked terminator was never read. Local probe with the same transport fields
  and the same read pattern: 6 break-at-[DONE] requests opened 5 connections (one buffer-luck reuse); the same 6
  drained to EOF opened 1.
- The bench agrees independently, from a different mechanism: the tap pins ALPN to http/1.1 on both sides
  (`benchmark.py:734,739`), and `runs/v051-ogcode.harness.log` reports **79 connections for 89 requests**
  (1.13 req/conn, 11% reuse — the same ~1-in-6 the probe saw).
  **Corrected**: that ratio is a property of v0.51, not of the reader — across the stored runs `v050-ogcode` is
  400 wire rows / 501 connections (0.80 req/conn, *worse*), `v050cap-ogcode` 101/102. So the bench has never
  shown meaningful keep-alive, and only three runs carry both a `wire.jsonl` and a harness connection count, so
  it cannot say whether the tap is the whole story.
- Cost of that harness artefact: a fresh handshake from this machine is `connect 0.116s + tls 0.251s = 0.367s`
  (curl to the same host), so the tap pays a dial+TLS per tunnel — ~0.3s of every request's `ms`, roughly 20% of
  the 1.72s intercept, ~29s of a 270s run — none of which production pays.
  **Corrected**: 79 × 0.37s ≈ 29s is an UPPER BOUND on the tap's total TLS+connect spend, not a proven 20% of
  each request's `ms` — the tap's upstream leg is lazy, so the delay lands on the first request of each tunnel
  and some tunnels carry non-model traffic. The defensible claim is only: the 1.72s intercept cannot be fully
  attributed to gateway queue, because up to ≈29s of the 270s run is harness setup.
- Not measured: whether production step gaps exceed the 30s `IdleConnTimeout` (the capture has no timestamps).
  That is the only route by which production re-handshakes, at ~0.37s per occurrence.
- Candidate fix, **now APPLIED** (same date, later in the day): drain after a clean `[DONE]`, bounded — see the
  `h1.1 stream drain applied` entry below.

## 2026-10-24 — h1.1 stream drain applied: a finished SSE body now returns its socket to the pool

- **The fix**: `drainFinishedStream(r io.Reader, cancel context.CancelFunc, budget time.Duration)`
  (`internal/provider/provider.go`, after `idleWatchdog.Stop`) arms `time.AfterFunc(budget, cancel)`, then
  `io.Copy(io.Discard, io.LimitReader(r, postDoneDrainBytes))`. `postDoneDrainBudget` = 2s (a `var`, so tests
  shrink it; 0 falls back to the default), `postDoneDrainBytes` = 64 KiB. It cancels the REQUEST CONTEXT rather
  than calling `body.Close()` from the timer — that is what unblocks a read parked in net/http's `persistConn`
  without racing an in-flight `Read`.
- **Where**: `openai.go` `streamEvents` sets `sawDone` at the `[DONE]` break and calls the drain at the very END
  of the function, after the text-tool-call recovery block — so the `scanner.Err()` path returns first and a
  failed stream never drains. Both of the developer's constraints are met: clean-`[DONE]` only, and bounded.
- **The transport's 120s idle watchdog does NOT cover this case** — it fires on a socket that goes quiet between
  reads, not one that never sends another byte, which is why the drain carries its own bound.
- **Cost, accepted**: the drain runs before the defers, so the concurrency slot (`budgetBody`, `budget.go`) is
  held for up to `postDoneDrainBudget`; consumers range to channel close, so a provider that hangs after `[DONE]`
  delays that step by up to 2s. Bounded, not eliminated.
- **`anthropic.go` deliberately unchanged**: its loop handles `message_stop` without breaking and already reads
  to EOF, so it has no discard bug.
- **Measurement** (independent reproduce, fresh `httptest` + `ConnState` counter, 6 requests): no-drain **6/6**
  connections, drained **6/1** — slightly worse than the earlier probe's 6/5, same conclusion. The earlier 6/5
  was buffer luck: the terminator happened to land in the same 64 KiB read.
- **Test-timing caveat that makes a naive pin pass falsely**: the handler must FLUSH after `[DONE]` and then pause
  (~50ms). With no gap the whole body lands in one read and the socket reuses even unfixed (measured 4/6 no-drain
  at delay=0 vs 6/6 at 20–200ms). `stream_drain_test.go` pins all four: connection reuse through `streamEvents`,
  reuse through `StreamChat` (proving the drain survives the `budgetBody` wrap and the `go`), the budget bound,
  and no drain on the `scanner.Err()` path. RED proven by disabling the call (3 connections, want 1).

## 2026-10-25 — `read` output caps audited: the file-map branch was the one uncapped path

- Asked whether the new multi-file `read` lets output grow unbounded. It does not: one `readBudget`
  (`MaxToolOutputBytes` 50 KB, `MaxToolOutputLines` 2000, `MaxLineLength` 2000 — `internal/tool/truncate.go`)
  is shared across the whole `files=[...]` call, and there is no file-count cap by design (the byte budget is
  the bound). Aggregate volume across parallel calls is only ADVISORY: `internal/agent/read_pressure.go`
  (40000 tokens, `OGCODE_READ_PRESSURE_THRESHOLD_TOKENS`) asks the model to compact; it never refuses.
- **The hole**: `readOne`'s map branch returned `mapInsteadOfWholeFile(...)` whole while setting
  `out.truncated = true`, and `capToolOutput` (`internal/agent/loop.go`) skips any result carrying
  `Result.Truncated` — so the loop backstop was disabled exactly where nothing else capped. Probe:
  `codemap.Outline`+`Render` over a 116,793-byte Go file (450 funcs with long signatures) produced a
  **88,874-byte map** (400 symbols = `MaxSymbols`, `Omitted=51`); `codemap/render.go` has no byte cap.
- **Fix**: the map branch pipes through `fitBody`, and `fitBody` now reserves room for its own
  `(N more line(s) not shown — the output cap was reached; …)` notice so the 50 KB is exact rather than
  notice-overshooting. Pin `TestReadTool_MapStaysWithinTheCallBudget` (RED without the fix).
- Lesson worth keeping: `Result.Truncated` is a promise that the tool already capped itself. Set it only
  after `fitBody`/`capToolOutput`-equivalent truncation, never alongside an uncapped body.

## 2026-10-25 — read and file_map output caps removed at the developer's request (reverses the entry above)

- The developer asked for **no output cap on `file_map` or `read`**, one turn after the map branch was
  brought under the 50 KB call budget. Removed from `internal/tool/read.go`: `readBudget`/`readCap`,
  `fitBody`, `droppedLinesNotice`, `limitLabel`, the per-line `truncateLongLine`, and `defaultReadLimit` — so a
  bare read returns the whole file, `files=[...]` renders every entry (no skipped files, title now `N files`),
  and a range without `end_line` runs to EOF. `Truncated` survives only as "a paging window left lines
  unread" (the `Use offset=` footer). Correction 2026-10-25: `internal/tool/file_map.go`'s success result
  did NOT "lose its `Truncated`" — it already ended `Result{Title, Output}` with no `Truncated` before this
  change; the diff only adds `Uncapped`.
- **`Result.Uncapped`** (`json:"-"`, `internal/tool/tool.go`) is the opt-out: `capToolOutput`
  (`internal/agent/loop.go`) returns early on `Truncated || Uncapped`, so the 50 KB / 2000-line loop backstop
  still bounds bash/grep/glob/MCP results but never these. The map interception itself stays — it chooses
  which answer a call gets, not how large the answer is.
- **Trade-off accepted**: nothing now clips a read, so the only remaining bound is ogx's 4 MiB request body
  (`ogxMaxRequestBytes = 4 << 20`, `checkBodyFits` → `ErrRequestTooLarge`, fatal — `internal/provider/openai.go:1133`).
  A read big enough to push the next request past that fails the turn instead of arriving shortened;
  `internal/agent/read_pressure.go` (40000 tokens) remains advisory only. A context that fills is still
  recoverable by compaction, not by a clipped tool result.
- Pins: `TestReadTool_MapIsReturnedWhole`, `TestReadTool_LongLineComesBackWhole`, `TestReadTool_RangesAreNotCut`,
  `TestReadTool_RangeWithoutEndReadsToEOF`, `TestReadTool_FilesReturnsEveryFile`,
  `TestReadTool_NoDefaultWindow`, `TestCapToolOutputHonoursUncapped` (replaces
  `TestReadTool_MapStaysWithinTheCallBudget`, `TestReadTool_DefaultLimitAndFooter` and the budget tests).

## 2026-10-25 — prompts teach the multi-file read: grep fast path, one read call per wave

- Audit before the change: `files=[...]` appeared only in `parallelToolCallsPrompt`'s bullet and as a clause in
  the workflow diagram's read line; `read`'s `Description()` buried it mid-paragraph and `file_map` said nothing
  about batching. `.ogcode/ogcode.db` (`part` data, `"tool":"read"`) held 28,688 read parts and **0 with
  `"files":`** — but the feature is uncommitted and the running server predates it, so that is not evidence the
  teaching failed.
- `internal/agent/prompt_builder.go`: the diagram maps only "every file whose shape you do not already know" and
  reads with `read(files=[...])` — ONE call in the NEXT block; a new paragraph makes a grep that already printed
  `path:line: text` stand in for the map (map only unknown shapes; a read answered with a map IS the map);
  another states one read call per wave. Build step 2 (`agent.go`) names both batched shapes.
- Contracts: `ReadTool.Description()` leads with the two shapes, `files=[...]` in its opening sentence;
  `FileMapTool.Description()` says one call per file in the SAME block and names the grep fast path.
- Cost: about 1.1 KB of prompt text (~280 tokens) sent with every request, against one saved round trip that
  re-sends the whole conversation.
- Pins: `TestWorkflowLetsASearchStandInForTheMap`, `TestWorkflowSpendsOneReadCallPerWave`,
  `TestProcessStepTwoNamesTheBatchedCalls`, `TestReadDescription_LeadsWithTheMultiFileShape`,
  `TestFileMapDescription_BatchesAndSkipsANeedlessMap`.

## 2026-10-25 — read: a bare limit of any size honours its window (the 200-line bare-limit cap removed)

- Bug (reviewer-confirmed empirically): `read(path, limit: 250)` on a 302-line mappable file returned the file
  MAP, not content — `rangeRequested` (`internal/tool/read.go`) counted only `Limit <= rangedReadThreshold`
  (200) as a window, so any larger bare limit fell through to `mapInsteadOfWholeFile`, contradicting the doc
  comment promising offset/limit paging is honoured. The cliff dated from when a read cap existed; it made no
  sense once reads went uncapped the same day.
- Fix: `rangeRequested := len(req.Ranges) > 0 || req.Offset > 0 || req.Limit > 0` — any explicit window reads
  content; the map answers only a bare whole-file read of a long mappable file (that case unchanged).
  `rangedReadThreshold` survives only as the short-file cutoff inside `mapInsteadOfWholeFile`; its comment now
  says it never qualifies a limit. `read`'s `Description()` and the `mapInsteadOfWholeFile` comment reworded.
- Adjacent: negative offset/limit (previously silently clamped to 0 / read as "every line") are refused in
  `validateReadWindows` with named-argument errors; the files-path top-level window check fires on
  non-zero, not just positive, offset/limit.
- Tests (internal/tool): rewrote `TestReadTool_LargeBareLimitStillReturnsMap` → `TestReadTool_LargeBareLimitIsHonoured`,
  `TestReadTool_BareLimitBoundary` (below/at/above the threshold all content now), and
  `TestReadDescription_StatesTheBareLimitCap` → `TestReadDescription_StatesTheWindowRule`; added
  `TestReadTool_BareLimitPagesContentPastTheThreshold` (302-line file, limit=250 → exactly 250 rendered lines,
  truncation footer, `Truncated`, `Uncapped`; offset=250+limit=100 pages to EOF above the map) and
  `TestReadTool_NegativeWindowArgsAreRefused` (single-file, files entry, top-level-beside-files).
  Full `CGO_ENABLED=1 go test ./internal/tool/ -count=1` green; the new paging pin verified RED with the old
  predicate restored, GREEN with the fix; `go build ./internal/... .` clean.

## 2026-10-25 — Browser takeover shipped (Option B: first-party browser_* tools + embedded skill)

- One-turn vertical slice: `internal/browser/bridge.go` (WS bridge on coder/websocket v1.8.15; Dispatch
  pending-map, 30s op budget / 60s navigate+screenshot, re-pair fails pending), `internal/tool/browser.go`
  (browser_status/navigate/snapshot/content/click/type/key/scroll/screenshot), `internal/server/browser_routes.go`
  (pair GET/POST, status, bridge — loopback-only + token + chrome-extension origin), embedded skill
  `browser-takeover` in `internal/skill/embedded.go`, and the companion MV3 extension `browser-extension/`
  (built by teammate browser-extension, session ses_01M5QPJKTFXYZRN7PVYWE73RWX).
- Two real bugs caught by the tests: coder/websocket OriginPatterns need `://` (`chrome-extension://*`,
  else a real extension 403s on origin); `failPending`'s string Err had to be mapped back to
  `ErrNotConnected` in `Dispatch` or re-pair broke `errors.Is`. Also fixed a hello race in the op/result
  test (poll version+tabs, not bare Connected).
- Tests green: `go test ./internal/browser/ ./internal/tool/ ./internal/skill/ ./internal/server/` —
  server suite 30.5s. Server browser routes covered: end-to-end extension dial (hello → status → close),
  wrong token 401, non-loopback 403, token rotation, 503s without a bridge.
- Gotcha observed once: a combined `go test` run failed building internal/server with
  "undefined: tool.NewBrowserTools" while a fresh `go build ./internal/server/` was clean — stale test
  build cache; rerun before debugging a phantom undefined.
- Deliberately not done: no commit, no web Settings panel, no CLI subcommand; v1 risk-gating = skill
  instructions (DATA-not-injections rails) + extension per-tab allowlist, not the permission manager.

## 2026-10-25 — Browser takeover opens its own tab and takes it over

- Gap found the morning after the ship: the extension's `executeOp` gated EVERY op behind the per-tab
  allowlist, so the newTab navigate existed (`tabs.create` + wait) but the created tab was never granted —
  open-only, no control. Go `browser.run()` also documented a "defaults to last granted tab" for an omitted
  `tab` that did not exist.
- Fix, three layers: extension auto-grants its own creation (`grantTab` persists to `grantedTabs`,
  revocable in the panel; side panel needs nothing — it listens to storage.onChanged); the exemption is
  **navigate + newTab + NO tab id only** (`newTab` plus a tab id means "act on that tab", gate still applies);
  server `run()` defaults an omitted `tab` to the last Granted tab in the hello inventory (`lastGrantedTab`),
  refuses Result-shaped when the inventory exists but nothing is granted, and passes nil through when the
  inventory is empty (no hello yet — no tab info, old behavior).
- Pins: `internal/tool/browser_test.go` +4 (last-granted default, newTab needs no tab, refusal carries the
  remedy, empty-inventory passthrough; TestBrowserClickRoundTrip fake now owns a granted tab id 2).
- Verified: `CGO_ENABLED=1 go test ./internal/tool ./internal/browser ./internal/skill` green; Browser
  -run 16/16; `go build -o /tmp/ogcode-shipcheck .` OK; scratch vm harness on background.js
  (`/tmp/og_bg_harness.js`, stubbed chrome.*) 6/6 — auto-grant + persistence, gate boundary on
  newTab+tab:5, unknown-op naming before the gate. Harness traps: vm sandbox needs `URL`/`process`/`assert`
  injected, and a host variable the fake storage reassigns must be exposed to the vm via a getter.
- Not verified live (no Chrome on this box, same caveat as the original ship): unpacked-extension pairing
  and a real click-through on an auto-granted tab.

## 2026-10-25 — Browser tools were registered but never offered to any agent

The first live extension pairing (extension connected to :7801, one granted chrome://newtab tab) exposed the
seam instantly: a demo teammate's `browser_status` refused `tool "browser_status" is not available to the
build agent` (the `!a.HasTool` executeTool guard, `internal/agent/loop.go`). `serve()` had registered the
eleven browser tools alongside the board tools, but the loop's per-turn allowlist (`allowIDs`, LoopRunner turn
setup, loop.go ~781) had no browser append — same registration, missing offer.

Fix, build-only, mirroring board/teammate/schedule:
- `internal/agent/agent.go`: `canControlBrowser()` gate `{ return a.ID == "build" }` next to `canSchedule`.
- `internal/agent/loop.go`: `browserOffered := agent.canControlBrowser() && lr.Tools.Get("browser_status") != nil`
  then `allowIDs = append(allowIDs, "browser_*")`. `newTurnToolset` hides only `mcp_*` globs, so `browser_*`
  passes through to the wire, and `Registry.ForAgent` expands any glob containing `*` (sorted, dedup) — future
  browser ops ride the same seam. CLI-safe: the CLI registry has no bridge, so the `Get("browser_status")`
  check keeps the ids off there.
- Pin: `internal/agent/browser_gate_test.go` (`TestBrowserOpsAreOnlyTheBuildAgent`) — build true; task,
  subagent, memory-recall, plan, breakdown, note, index, search all false; `codingAgentTools` carries no
  `browser_`-prefixed id.

Not verifiable in-session: the running :7801 process predates the fix, so its teammates still refuse — the
developer restarts that process on the new binary (`make -s build-server OGX_APP_SECRET=$(tr -d '\n' <
~/.ogcode/ogx-app-secret)` done; binary at ./ogcode v0.51.0-dirty). Demo (example.com newTab + snapshot)
deliberately deferred to the restarted server's next turn.

## 2026-10-25 — the browser demo missed twice: the restart ran a stale install

- The gate fix (`canControlBrowser`, shipped ~12:00) never reached the process serving :7801: the developer's
  restarts (two "restart & pair" confirmations) left the SAME pid 57596 (started 12:03:10, launch pre-fix at
  11:43) listening, `/api/browser/status` connected:false throughout. Root cause of the relaunch trap: from a
  plain shell `ogcode` resolves to `~/.local/bin/ogcode` (the 11:43 install; `strings … | grep -c
  canControlBrowser` = 0 vs 2 in the repo build) — a fix without a fresh install re-arms itself on every restart.
- Fix applied: `cd /Users/prasenjeet/Downloads/work/ogcode && make -s install OGX_APP_SECRET=$(tr -d '\n' <
  ~/.ogcode/ogx-app-secret)` at 12:06 (build → ad-hoc re-sign → install; verified mtime 12:06, symbol count 2).
- Also verified the pairing contract live: the bridge token is per-boot in-memory (`internal/browser/bridge.go`
  `New()`), and `browser-extension/background.js` `scheduleReconnect` re-dials the stored token without
  re-fetching `/api/browser/pair`, so each server restart needs a side-panel Connect press. Prior claim that
  "the extension auto-reconnects across a restart" (previous turn) was wrong and is corrected in MEMORY.md.
- Third handover made self-verifying: one line that kills, checks LISTEN, and launches — so "already running /
  held by pid" output can't be mistaken for a completed restart.

## 2026-10-25 — Auto-kick relaunch lost the flock race; pairing needed a pasted token

- The kicker (started 12:09:43 IST) SIGTERMed 57596, saw "port free", and launched — but the dying server's
  graceful shutdown had freed the listener while the flock on `.ogcode/server.lock` was still held, so the
  relaunch exited 1 (`already running … pid 57596`). No server ran until the developer's own `ogcode` at
  13:38:24 (pid 60924, installed fixed binary — `lsof` txt = `~/.local/bin/ogcode`, strings grep = 2).
- Verified live: `GET /api/browser/pair` returns the boot's token WITHOUT rotating (rotation is POST,
  `handleBrowserPairRotate` → `RotateToken()`); the server's 401 text itself points there. The extension never
  fetches it — its reconnect re-dials the stored token (background.js), so it 401s forever until re-pair:
  Disconnect → paste new token → Connect in the side panel (sidepanel.js:73-97; `storage.onChanged` at
  background.js:1148 also drops a live socket when the token changes).
- Demo (example.com via navigate+newTab) still pending on the developer's "go".

## 2026-10-25 — Ogweb extension prepped for Chrome Web Store submission

- Assessment: `browser-extension/` is clean MV3 (no content scripts, no
  `chrome.scripting`, no eval/`new Function`/fetch/XHR, no remote hosted code);
  `minimum_chrome_version: "114"` matches the `sidePanel` API. Real blockers
  were: no icons, a privacy-policy URL (website-content data use), zip
  packaging, and the store registration itself.
- **`host_permissions: ["<all_urls>"]` dropped from the manifest** — nothing
  uses it (no content scripts or scripting injection, no cross-origin fetch;
  the WebSocket dials the user-entered origin; `chrome.debugger` requires only
  the `debugger` permission). Verified by inspection + doc research; flagged
  UNVERIFIED at runtime until a live reload + op pass (navigate/snapshot/
  screenshot on a granted tab).
- Icons: `sips` resize of `docs/media/logo.png` to 16/48/128 under
  `browser-extension/icons/`; manifest now declares top-level `icons` +
  `action.default_icon`.
- `make extension-zip` (Makefile) builds `ogweb-<manifest-version>.zip` from
  INSIDE the directory so `manifest.json` sits at the zip root; 10 files;
  `/ogweb-*.zip` gitignored.
- Privacy page: `docs-site/src/content/docs/privacy.md` (Astro docs schema,
  nav entry in `docs-site/src/lib/nav.ts` Guides group). Not yet deployed.
- `browser-extension/STORE_LISTING.md`: listing copy, single-purpose sentence,
  ready-to-paste permission justifications (debugger/tabs/storage/sidePanel),
  data-use disclosures ("Website content" collected, never sold or shared,
  transmitted only to the user's own local server over the paired WebSocket),
  review notes with repro steps (`GET /api/browser/pair`), and the pre-upload
  checklist. Public copy carries no em dashes.
- Accepted: chrome.debugger draws extra CWS review (possibly a demo video
  request); $5 developer registration is a manual developer step.

## 2026-10-25 — Browser-takeover demo; heavy page ops kill the MV3 service worker, not pairing

- Demo against the live paired Chrome (server :7801): navigate, snapshot on a small
  page, content on a big page, scroll, network/console reads all worked; the user
  watched the Wikipedia tab scroll under agent control.
- Deterministic failure pattern, proven with a 100ms `GET /api/browser/status`
  sampler (/tmp/snapwatch.sh): `browser_snapshot` on a large page and
  `browser_screenshot` each dropped the extension's ws ~0.1–0.6 s after dispatch
  (sample numbers 70.09→71.21, 43.49→43.57+, 23.929→24.860 for screenshot) and it
  came back exactly ~1.0–1.1 s later = the extension's own reconnect
  (BACKOFF_MIN_MS=1000, browser-extension/background.js:19).
- Small-page snapshot on chrome:// surfaces returns a legit op error with the
  connection SURVIVING ("Cannot access a chrome:// URL"), so normal op errors don't
  kill the ws; only the heavy ones do. Suspected trigger: the in-page
  `Runtime.evaluate` snapshot walk (ELEMENT_CAP=300) / `Page.captureScreenshot`
  spikes the SW → Chrome suspends it. Not proven; the per-op-class determinism is.
- The refusal text is misleading: the reconnect's `swapConn` →
  `failPending(ErrNotConnected)` (internal/browser/bridge.go:225,229,337-343)
  fails the in-flight op, and the tool's `unpaired()` (internal/tool/browser.go)
  renders it as "extension not paired" though pairing was fine. Self-heal is
  ~1.1 s, so the NEXT op succeeds — do not re-pair on this text.
- Server read limit (maxResultFrameBytes 4 MiB, bridge.go:40) is NOT the cause;
  op replies are capped far below it.
- Code wart found: `dropConn` (bridge.go:269-283) comments that it fails in-flight
  ops but does NOT call failPending — a drop with no reconnect would hang the op to
  its 60 s timeout.
- Fix candidates, left unimplemented (turn was a demo): screenshot via
  `chrome.tabs.captureVisibleTab` (no debugger session needed); bridge-side
  retry-once on the ErrNotConnected sentinel or failPending inside dropConn.
- Restart gap measured the same morning: a SIGTERM'd server frees its port before
  its project flock, so the scheduled relaunch exited 1 "already running" and the
  demo sat ~90 min until a manual `ogcode` (12:10 → 13:38). Fix rule is in
  MEMORY.md (wait for old PID to exit, not the port).

## 2026-10-25 — Pair token survives restarts; auto-install research

- The chrome-extension pair token is no longer minted fresh on every boot: the server
  pins it from `.ogcode/browser-pair-token` (`browser.NewWithToken` + `loadBrowserPairToken`
  in server.go / browser_routes.go), and every rotate persists the rotated value
  (0600 file). Boot chain: good token file → pinned; missing/empty/corrupt → mint
  fresh and save. Pins: `TestNewWithTokenPinsThePersistedToken` +
  `TestNewWithTokenFallsBackToFreshOnBadInput` (bridge_test.go),
  `TestBrowserPairTokenSurvivesARestart` + `TestBrowserPairRotatePersistsTheToken`
  (browser_routes_test.go). This LIVE project's token was pre-seeded with the value
  the developer's extension already holds (7e210265…6690ea) so their next restart
  needs no re-pair at all.
- Auto-install research (deep_search) conclusions: on-branded Chrome ≥ 137 dropped
  `--load-extension`; external CRX install via registry/plist outside the Web Store
  is blocked; enterprise `ExtensionInstallForcelist`/`ExtensionSettings` force-install
  works but marks the browser managed. Sanctioned smooth path = CWS listing +
  deep-link install (one user click), then the WS pairing is automatic since the
  token is now stable; a native-messaging host could also auto-supply the token.

## 2026-10-25 — Native-messaging host shipped (zero-paste extension pairing)

- Built `internal/nativehost` (Chrome stdio host: 4-byte-LE length framing; `{"type":"pair"}` → probes
  portmap registry, returns servers[]), `internal/cli/native_host.go` (`ogcode native-host [install|uninstall]`),
  exported `portmap.All()`/`AllIn(path)`, and boot auto-install `nativehost.EnsureRegistered(projectDir)` in
  server.go (warn-only, idempotent, skipped entirely by falsey `OGCODE_NATIVE_HOST_INSTALL`).
- Extension side: manifest +`nativeMessaging` permission (v1.0.0→1.1.0); `background.js` fires
  `autoPairThenConnect` ONCE per worker life (5 s timeout) when credentials are absent — success writes
  `{serverOrigin, pairToken, bgConnected:true}` and connects exactly like manual Connect; failure
  (`ok:false`, disconnect, 5 s timeout, no host installed) ⇒ unconfigured status with manual-paste hint,
  stored values never overwritten. Harness `browser-extension/native_pair.test.js` = 34/34.
- Extension id is Chromium path-derived (sha256 of LOWERCASED abs path → nibbles a..p): repo
  `browser-extension/` ⇒ `cgeoedmgjadadineklkmhgpmolmnpfbn`, pinned by golden test; no unpacked extension
  was loaded in any Chrome profile at the time, so `chrome.runtime.id` could not be cross-checked live.
- Live verification: pipe-smoke `printf '\x0f\x00\x00\x00{"type":"pair"}' | ~/.local/bin/ogcode native-host`
  returned the full servers[] payload (token 7e210265…6690ea, port 7801) against the then-running server —
  note `{"type":"pair"}` is 15 bytes, framing head `\x0f`, not `\x0d`. Manifest written for Google Chrome
  (Chromium/Brave/Edge/Vivaldi/Arc dirs absent ⇒ correctly skipped). `make -s install` v0.51.0-dirty carries
  `com.ogcode` (5 strings hits).
- Chrome reads native-messaging manifests at browser startup: a first-ever `com.ogcode.json` needs one
  Chrome restart (or extension reload after restart) before connectNative succeeds.

## 2026-10-25 — MEMORY.md diet: repo layout, releases and deployment pre-diet text (from MEMORY.md diet)

Pre-diet MEMORY.md section bodies, byte-verbatim (line ranges noted). Moved 2026-10-25 by teammate memory-diet while compressing MEMORY.md from 66179 bytes to its ~20 KB budget, so nothing was lost: the compressed forms in MEMORY.md keep the rules, these hold the full original text.

### Repo layout & git hygiene (pre-diet lines 15-23)

- Root: markdown only; plans in `plans/` (refs inside bare, `plans/<name>` outside); the next migration is `069` (068 is the agent timer's, 066 the file change gate's, 065 the board's).
- **`.gitignore` paths stay root-anchored**: `/GIT_DIFF_PLAN.md` (a bare rule ignored the tracked
  `plans/GIT_DIFF_PLAN.md`), `/dist/` (unanchored would swallow `web/dist/`), `/agent/` (a stray duplicate of
  the golang-* skills — `.agents/skills` is the one `internal/skill/discover.go` reads; it also breaks
  `go build ./...`, whose examples import viper/fatih/color, so build `./internal/... .`).
  `MEMORY.md`/`MEMORY-ARCHIVE.md` gitignored. **`AGENT.md` is tracked but 0 bytes** — a `see AGENT.md` pointer is stale.
- **README hero is `docs/media/ogcode-demo.gif`** — GitHub renders no animated WebP and no `<video>`, so a GIF
  is the only option; re-encode via `HOWTO.md` → Re-encode the README hero GIF, and check for leaked internal
  paths at legible resolution (twice already).

### Releases, version & install (pre-diet lines 27-41)

- **Tags are immutable**: `git ls-remote --tags origin` + `gh release view vX.Y.Z` first. Bump
  `internal/version/version.go` (`vX.Y.Z`) + both `web/package*.json` (`X.Y.Z`); steps in `HOWTO.md`.
- **A red Linux build job blocks the release** while CI/Pages/Docker stay green: `build.zig` writes `.zig-cache`
  beside the read-only module cache — fix = top-level `ZIG_LOCAL_CACHE_DIR: /tmp/zig-cache`.
- **macOS re-sign**: an in-place replace keeps a stale signature → `Killed: 9`; `make install` `rm -f`s first
  then ad-hoc signs. **It restarts nothing** — check the listening pid's start time — and `make` echoes its
  recipe, so add `-s`. The installed file runs **~18 KB larger than `./ogcode`** — that is the embedded
  ad-hoc signature, not divergent content (same delta every install; `spctl` "rejected" is normal too).
- **After a code fix, `make -s install …` BEFORE handing over a restart** (2026-10-25 demo missed twice): a
  relaunch runs `ogcode` from PATH = `~/.local/bin/ogcode`, not the repo `./ogcode` — the user's restart can
  re-run a pre-fix installed copy. Verify the install carries the change: `strings ~/.local/bin/ogcode | grep -c <new-symbol>` (expect ≥1), and after they restart check the listener's pid actually changed (ps start time).
- **`detectInstallCommand()`** matches **scoop → Homebrew → cargo → install.ps1 dir → winget → install.sh**;
  Homebrew means `/Cellar/` or `/opt/homebrew/`, **never any `/usr/local/`** (`install_detect_test.go`).
  **`check-updates` exits 1 when an update is available.** **Verify install changes on a throwaway rig** (fresh
  HOME + temp cwd + stub `open`), expanding the binary path BEFORE exporting HOME.

### Deployment & first boot (pre-diet lines 56-71)

- **A project keeps its port**: `~/.ogcode/ports.json`; a bare `ogcode` reuses it (this project → **7801**), an
  explicit `--port` overwrites it. `make -s install OGX_APP_SECRET=$(tr -d '\n' < ~/.ogcode/ogx-app-secret)` works
  (the env var reaches `build-server`). **Never restart the host server unasked** — it runs the live session, so
  killing it ends the turn mid-reply; `ask_user` and hand over `kill <pid>; cd <dir> && ogcode` (no `--port`). The
  developer has picked "I'll restart it myself" every time since 2026-10-16; a detached script only once (09-28).
- **One server per project dir**: an advisory flock on `<dir>/.ogcode/server.lock` before the DB opens
  (`projectlock.go`); a second `serve` exits 1 naming the holder's pid and port. Two servers in tests need two
  dirs. **`port free` ≠ dead** (2026-10-25 kicker): a SIGTERM'd server frees its listener before releasing the
  flock, so a relaunch fired straight off a port check exits 1 `already running …` and nothing comes back until
  someone runs `ogcode` again (~90 min here) — a restart script must wait for the old PID to EXIT, not the port.
- **Listener before preflight** (133 MB embedder, after bind); `/api/config` GET JSON 200 is the health probe.
  **End-user macOS signing is NONE**: ad-hoc, and it runs because install sets `com.apple.provenance`, not
  `com.apple.quarantine` — `spctl` "rejected" is not "blocked" without that xattr.
- **Docker**: install `git` (else every workspace says "not a git repository"); `HEALTHCHECK … /api/config
  --start-period=120s`; `docker.yml` needs `type=raw,value=latest`. **ldflags traps**: `-X` on a missing symbol
  is silent, an empty `-X` does not fall back.

## 2026-10-25 — MEMORY.md diet: OGX gateway, providers and token accounting pre-diet text (from MEMORY.md diet)

### OGX gateway (pre-diet lines 74-92)

**Assertion**: base64url HMAC-SHA256 over `app + "\n" + ts + "\n" + method + "\n" + path` (skew 5 min) in
`X-Client-App`/`-Timestamp`/`-Signature`. **Gate is CONDITIONAL**: no secrets ⇒ open, any secret ⇒ any failure
⇒ 401. ogcode `appassertion.go`; oglab `internal/httpx/httpx.go`, secrets from `OGLAB_APP_SECRETS`.

- **Secret**: `OGXProviderID = "ogx"`, `OGXAppID = "ogcode"`, `var OGXAppSecret` (ldflags); one live value
  (never paste) — `~/.ogcode/ogx-app-secret` (600). **`tr -d '\n'` mandatory** (unstripped HMAC → silent 401).
- **Provider**: `https://ogx.ogcode.in/v1`; **`/v1/models` IS the plan** — dynamic models, all
  `ActiveByDefault`, **no static fallback**. **Registration is plan-gated, network-free** (`HasPlan()`).
  **Priority** `anthropic, openai, openrouter, ollama, ogx` (ogx last on purpose). **`prompt_cache_key`** is the
  session identity for token spend, not a cache hint.
- **Bodies cap at 4 MiB** (`checkBodyFits`, `ErrRequestTooLarge`); oglab's `maxChatBodyBytes` matches, both
  fatal. Raising it is three hops (oglab router, web `maxChatBytes`, nginx) — ship the gateway first.
- **Composer images**: `prepareImageAttachment` → **1024 px long edge**, JPEG q0.8 / WebP, byte-identical
  passthrough ≤512 KB AND ≤1024 px; `MAX_IMAGE_SIZE` (10 MB) is only the pick limit.
- **JSON endpoint returning HTML = nginx routing bug** (no `/v1/` location ⇒ all to `oglab_web`); fix with
  `upstream oglab_router` + `location /v1/` before `location /`. A 15s SSE keep-alive stops Cloudflare's ~100s
  idle cut and stops after 90s of upstream silence.
- **Settings tab** is `readOnly`; its not-connected fallback div needs `data-setting` or it vanishes offline.
  **oglab's** breaker ignores `400`/`422`.

### Providers, models & resolution (pre-diet lines 96-143)

- **`OpenAIProvider.Models()` is a pure read** — never network; fallback until `SetCatalog`, returns a copy.
  Fetching is `RefreshCatalog` behind `CatalogRefresher`/`CatalogSetter` (`TestModelsNeverFetches`).
- **Resolution**: `orderedSnapshot()` sorts by `(providerRank, id)`; `ResolveProviderFor` = custom → explicit
  provider → catalogue (`ActiveByDefault`) → highest priority; persisted as `provider` (migration 047).
  **Trap**: a column in a `SELECT` but not in `rows.Scan` is a silent runtime error. `lookupModel` compares the
  raw `m.ID`, so a bare `deepseek-v4.1-flash` beats OpenRouter's prefixed row.
- **`ogx-deepseek-v4.1-flash` THINKS BY DEFAULT** (live probe 2026-10-24): a request with no `reasoning_effort` field ⇒ the endpoint streams `reasoning_content` and bills it inside completion tokens (72 vs 20 with effort=none, 96 with high — on a one-line question). ogcode's loop sets `Thinking:true` on every request (`internal/agent/loop.go`) but `applyEffort` (`internal/provider/openai_effort.go`) writes nothing unless session effort ≠ "" — unset effort = endpoint default = thinking ON; catalogue default for the model is EffortHigh. `--effort none` suppresses it. **FIXED 2026-10-24 — flash-class effort default; UPDATED 2026-10-25 (Bugs A+B)**: `defaultEffortFor(modelID, spec)` (openai_effort.go) maps flash-class ids to `low` when session effort is "", but a spec default LIGHTER than high stands (gemini-2.5-flash-lite `none`, flash-lite siblings `minimal`, gemini-3.8-flash `medium` — vendor-tuned, no deep default to claw back); a recorded default ≥high keeps the low hold (ogx-deepseek-v4.1-flash records high — a literal spec-default-wins would have re-billed reasoning); non-flash keeps "" even with a recorded default (deepseek-v4-pro pin sends nothing). The MECHANICAL CONTINUATION hold (previous step of the turn was tool-only with at least one WORK call — a round whose every call was read-only planning/exploration (`explorationOnlyCalls`: update_intent, compact_context, codebase_map, file_map, read, grep, glob, ask_user, internal/agent/loop.go:2201/2336) is exempt so the next step keeps thinking — `StreamRequest.StepMechanical`, set at the loop tail in `internal/agent/loop.go`) to `none` (fallback `low` when the ladder lacks none, e.g. glm-5.3-flash) applies ONLY to default-derived levels — an explicit user effort is NEVER rewritten by the mechanical path even when a mid-run spec change drops the level (the old code wrote `low` over explicit `high`). Pins `openai_effort_flash_test.go` + `effort_mechanical_test.go`. **BOTH bench arms think** (wire-relay capture 2026-10-24): neither client sends a reasoning field, so `reasoning: 0` in `run.json` is an accounting artifact on both sides — ogcode folds reasoning into `output`, OpenCode's SDK drops the `reasoning` delta into completion (wire completion ≈ self-reported output, r1–r3: 29292/29053, 28423/27978, 24784/24749). Real difference is ogcode reasons ~2× per turn (mean out/turn 836/635/559 vs 338/379/349). **The `parity` pair is NOT matched** — only the ogcode arm ran `--effort none` (17,510 vs 9,329 output), so it is ogcode-OFF vs OpenCode-ON and a lower bound; no matched both-off pair is possible (OpenCode has no effort flag).
- **ogcode.json custom OpenAI-compatible providers** (2026-10-24): extra named ids
  beyond the four slots + `ogx` register end-to-end — `config.CustomProviders()` →
  `provider.NewOpenAICompatibleProvider(id, apiKey, baseURL, model, label)` (reserved
  ids and non-slugs refused; collection = `label` else `collectionFromBaseURL`), wired
  in `cli/run.go`, `cli/root.go` and `server.go loadProviderMap`, so `reloadProviders`
  picks up edits. Entry schema: `type` (only ""/"openai"; else skipped), required
  `baseUrl`, optional `model`/`label`; `disabled` merges ONE-WAY (project can disable,
  never re-enable a globally-disabled one). **Trap**: `Load`'s field-by-field merge
  dropped `Type` — every new ProviderConfig field must be copied there or a custom
  entry silently re-types to openai (caught by server wiring test
  `TestLoadProviderMapRegistersCustomProviders`). **UI needs nothing** (2026-10-24): the picker groups by
  `collection` (label, or vendor inferred from the host, else the raw id) and Settings→Models spawns a
  readOnly tab for any unknown providerId appearing in the catalogue (`models.tsx` extra-slot path) —
  no credential rows, no add row, `isConfigured` always true. Wart left as is: a def-less tab's
  description reads "Bundled with ogcode — no credentials required" (`models.tsx:761`).
- **Catalogue names are aliases**: `normalizeModelID` strips the org prefix, `:latest`/`:cloud` and the
  OpenRouter variant suffix; first-claim-wins. `gemini-4-argon`: `RejectsSampling`, `MaxOutputTokens: 32_768`.
- **The Ollama slot lists only what the local endpoint serves**; the community free pool is REMOVED
  (`freepool.go`, `OGCODE_FREE_KEYS_URL`), `needsOnboarding` hardcoded false.
- **Budget**: `OGCODE_PROVIDER_MAX_CONCURRENT` 8, reserve 2; index takes `indexGate` then `total`, interactive
  only `total`. **`streamEvents` defer order** `close(ch); body.Close(); cancel()` — a slot held across the
  channel close 429'd OGX's concurrency-1 tier.
- **`write tcp …: result too large` is ERANGE from the LOCAL `write(2)`** (macOS VPN/content filter), not the
  gateway; `sendStreamingRequest` retries once on a fresh connection, a mid-stream RST only when no text and no
  tool call exists, and IPv6 fallback counts ECONNRESET too, two strikes narrowing to `tcp4`.
- **Connections: ONE pooled h2 conn to ogx in production; h1.1 sockets are now DRAINED, not dropped.**
  `streamHTTPClient` (`internal/provider/provider.go:176`) is a single pooled client — no `DisableKeepAlives`,
  no `req.Close`, `ForceAttemptHTTP2: true`, `IdleConnTimeout: 30s`, no `MaxIdleConnsPerHost` (Go default 2).
  ogx negotiates **HTTP/2** (probe: `proto=HTTP/2.0`; a body closed after 10 bytes still reported
  `GotConn.Reused=true`), so a step is an h2 STREAM on one connection. On HTTP/1.1 a body closed before EOF
  discarded the socket (6 break-at-`[DONE]` requests → 6 conns); `drainFinishedStream` (`provider.go`) now runs
  at the END of `streamEvents` gated on `sawDone` only — never on the `scanner.Err()` path — with
  `postDoneDrainBudget` 2 s (bounded by cancelling the request context) and `postDoneDrainBytes` 64 KiB.
  **Anthropic needs nothing**: its loop handles `message_stop` without breaking, so it already reads to EOF.
  **A test that wants to observe the discard must put a gap (~20–50 ms) between the sentinel and the end of the
  response** — otherwise the whole body lands in one read and the socket reuses by luck, so the test passes
  against the unfixed code. Pins: `internal/provider/stream_drain_test.go` (reader + `StreamChat` end-to-end;
  both RED with the drain disabled). **The bench tap pins ALPN to http/1.1 on both sides**
  (`benchmark.py:734,739`), so its runs are the h1.1 case; per-run req/conn (v050 0.80, v051 1.13) is not a
  property of this reader, and each tunnel's dial+TLS (~0.37 s here) is an UPPER BOUND on harness setup, not a
  measured share of the 1.72 s intercept.

### Token accounting (pre-diet lines 147-167)

- **Reasoning is billed INSIDE `output`, never separately**: `ModelPrice.Cost` takes no reasoning term (`internal/provider/pricing.go`), `TokenCounts.Consumed/Effective` leave it out to avoid double-counting (`internal/session/message.go`), and `usageFromOAI` folds Gemini's total-only thinking into output. It is a display count only — output is ~1% of all tokens, so cache-hit rate, not effort, is the spend lever.
- `tokens.input` is UNCACHED: bill `input + cacheRead`; a cache hit is `prompt_tokens_details.cached_tokens`
  when > 0, else `prompt_cache_hit_tokens`. The pill totals the transcript from `GET /api/session/{id}/token`.
- **Title generation inherits the session model** — with `model == ""` it skips `ResolveProviderFor`, so a test
  must set `defaultProvider`. **Gemini `thought_signature` must be echoed verbatim** from `extra_content.google`
  on every replayed tool call or the second turn 400s (`EchoesToolExtra()`).
- **Teammate spend is counted, but only on the child**: a teammate is a real `build` session (`board.go`
  `spawnTeammate` → `startSessionLoop`), so its steps land on its own messages + `usage_event` (kind=step) and
  its utility calls on `session.utility_*` — same project bucket (one ledger per workspace, `server.go`
  `NewLedger(global, s.dir)`), so Settings→Usage totals include it. `foldChildUsage` (`agent/usage_charge.go`)
  is for EPHEMERAL task/memory children only; a teammate is never deleted or folded (a fold would double it in
  session views). Verified live: 6 teammate sessions, all with step+utility ledger rows.
- **The parent's token pill folds teammates in on the CLIENT, never in the DB**: `/session/{id}/token` and
  `/usage` each carry a `teammates` array (always `[]`) beside session-only headline totals; `token-pill.tsx`
  sums them into its figures — input/output/cache AND `utility` (a subtotal inside effective/total, never added
  on top). A teammate child whose read ERRORS is skipped per-child with a warn (parent-side reads still 500; pin
  `TestSessionTokensSkipsATeammateWhoseReadErrors`). `spawnedTeammates` (`server/board.go`) enumerates via
  `board.BySession` + roster `KindTeammate && SpawnedBy == parent.Handle` — there is no parent column on
  `session`; a deleted child is skipped while its roster row stays. Pinned by `session_token_test.go` /
  `usage_routes_test.go`.
- **Ollama Cloud meter**: `GET https://ollama.com/api/usage` with `OLLAMA_API_KEY`.

## 2026-10-25 — MEMORY.md diet: permissions, agent memory files and prompt caching pre-diet text (from MEMORY.md diet)

### Permissions (pre-diet lines 171-215)

- **The ruleset gates, not the mode**: `Ruleset.Evaluate` runs BEFORE the mode switch and only read/glob/grep
  are `Allow` by default — bash/write/edit are `Ask`, and the catch-all rule `Allow`s every other tool in
  **every** mode. **Yolo only changes what would have been Ask** (skips the classifier entirely) — an explicit
  `Deny` still denies; `isDangerousCommand` is a separate layer. **There is no Deny tier**: classifier outputs
  are only Safe/Ask/Unclear. PATCHing a session's mode ALSO `SetDefaultMode`s machine-wide. **Grants never
  expire** and the pattern is the verbatim string (`./f.go` ≠ `/abs/f.go`), not a glob.
- **`riskLLMMaxTokens = 1024`** is calibrated; an unanswered check warns, returns Ask and is not cached. Ask =
  `rm`/`sudo`/`git push`; Safe = read/build/test; the rest (incl. `curl`, unknown) = Unclear, the only tier
  reaching the LLM.
- **A teammate's Ask reaches the HUMAN, in the PARENT's composer** (never the parent AGENT): the child's loop is
  gated exactly like a parent's (`startSessionLoop` → `WithPermissionGating`, `resume_routes.go`) on the SAME
  server-global `permission.Manager` (`server.go:533`, one shared `loopRunner`), tagged with the CHILD's sessionID;
  mode is inherited, not reset (`board.go` `Permission: parent.Permission`). **The mirror is SERVER-side**, so a
  reload restores it too: `LoopRunner.ParentSessionOf` (`agent/loop.go:163`, wired to `server.parentSessionOf`,
  `board.go:726`, which climbs `SpawnedBy` to the first non-teammate session, depth 32) makes `requestPermission`
  publish a second `permission.requested` with `sessionId` = ROOT session, `ownerSessionId` = the child and
  `teammate` = the child's handle (cancel mirrors `permission.replied` "cancelled"); the parent's
  `GET /session/{id}/permission` appends `pendingTeammatePermissions` (BFS over the roster, any depth), and a reply
  is delivered by permissionID alone yet clears EVERY queue holding it — the path session, the owner and the
  owner's root, so answering from the child's page does not leave a stale prompt in the parent's composer
  (`permission_routes.go`, `TestReplyingFromTheChildPageClearsTheParentQueue`). Client: the queue
  still renders only the ACTIVE session's (`session.tsx` `pendingPermissions()`), so the chip in
  `permission-prompt.tsx` marks a mirrored one and `respondPermission` posts to `entry.sessionId` (the owner).
  **Teammate `ask_user` IS offered and MIRRORS into the parent's composer** (2026-10-23, the build-1e15 evening
  run — it REVERSED the same-evening exclusion written at ~23:00, replaced 23:12-23:16; the on-disk `file_change`
  ledger rows 22:53→23:19 tell the arc): `askUserOffered` (`internal/agent/loop.go`) needs only an interactive
  loop + Questions manager — no teammate carve-out (`isTeammateSession` deleted). Mechanics = the permission
  mirror's: `mirrorQuestion` (loop.go, right after `mirrorPermission`) republishes `question.requested` with
  `SessionID`=parent + `OwnerSessionID`=owner and `Request.Teammate`=child handle (cancellation mirrors
  `question.replied` "cancelled"); `GET /api/session/{id}/question` appends the SUBTREE's batches via BFS
  `teammateChildrenOf` (shared with permission routes — a mid-tree session lists its children's questions, a
  leaf only its own); the reply POST is addressed to the OWNER and three-way notifies path+owner+owner's root
  (dedup by sessionId) so every queue clears from whichever page answered — pages BEYOND those three
  that already GET-fetched the subtree keep a stale copy until their reply 404s or they reload (the identical
  hole exists for the permission mirror, so it is parity, not a bug). Client: mirrored entries file under
  the PARENT keyed `ownerSessionId || sessionId`, reply posted to that owner, teammate chip in
  `ask-user-dialog.tsx`. `teammatePrompt` (`internal/server/board.go:455`) directs a blocked child to ASK rather
  than end its turn early. The child blocks until the human answers (no timeout) — but the dialog is answerable
  live, so no child-hang. Pins: `TestRunLoop_AskUserOfferedForTeammate` (the old absent-pin no longer exists),
  `TestTeammateQuestionMirrorsToTheParentSession` (+cancellation/+silent-without-parent, `question_mirror_test.go`),
  `TestParentSessionListsTeammateQuestions`, `TestReplyingAQuestionFromTheParentUnblocksTheTeammate`
  (`question_routes_test.go`).
- **Grants and mode are machine-wide** in `~/.ogcode/config.db` (migration 048); grain is the exact
  command/path/skill, never `"*"`. **Trap**: `serve_lifecycle_test.go` does not set HOME, so `go test ./...`
  advances the real config DB — isolate HOME and the working dir.

### Agent memory files (pre-diet lines 219-222)

- **Caps: none by default** (`OGCODE_MEMORY_MD_MAX_BYTES`/`OGCODE_AGENT_MD_MAX_BYTES`; `limit <= 0` is
  uncapped) — a positive budget is head-biased, so curate instead. `staticSystemPrompt` carries **one constant
  pointer per file, never content or a heading index**; turn digests carry tool names and the final response
  only, so procedures go to `HOWTO.md`, not the scribe.

### Prompt caching, read pressure & context windows (pre-diet lines 226-234)

- Anthropic `cache_control` on the last tool def and first system block (min 1024 tokens, 4096 on Opus 4.5+,
  5-min TTL); OpenAI automatic. **Entry [0] stays byte-identical** — the date rides `systemReminderPrompt`.
- Read pressure: `OGCODE_READ_PRESSURE_THRESHOLD_TOKENS` 40000; `OGCODE_RESEND_COST_WINDOW_MULTIPLE` 1.
- **History is TURN-SCOPED**: only `sliceTurn(messages, findLastTextUserMessageIndex(...))` is sent, so a prior
  turn's images never re-send; within a turn images repeat and ride the last-message cache breakpoint.
- **The turn's prompt and images survive compaction, tool images do not** — name them in the summary or
  re-read. **The ask_user Q&A must survive verbatim** on all three surfaces.
- `OGCODE_COMPACT_CONTEXT`: unset/empty = ON, falsey = off. `maxCompactions = 2`, `compactionKeepRecent = 12`,
  unknown window → 128000. **Learned windows** are kept only when `learned > promptTokens`.

## 2026-10-25 — MEMORY.md diet: agent tools & prompt and MCP/search pre-diet text (from MEMORY.md diet)

### Agent tools & prompt (pre-diet lines 238-298)

- **`schedule` tool = one-shot agent timers** (2026-10-24): ops `create` (delaySeconds OR strictly-future RFC3339 `fireAt` + required `note`)/`list`/`cancel` in `internal/tool/schedule.go`; refusals are Result-shaped, not errors. Server store `internal/timer` (migration `068`, table `agent_timer`, statuses pending→due→delivered/cancelled; `ClaimDue` is one atomic `UPDATE … RETURNING` so two claimers can never double-claim). Ticker `pollReminders` (10s, `internal/server/reminder.go`) claims due rows and delivers via the board-report rails: `PushNotice` Cause `"reminder"` + `TrackBoardReport` with mark=`MarkTimerDelivered` (byte-identical Text in both). Idle session → row stays `due`, `CatchUpReminders` hook (LoopRunner field, build-gated beside `CatchUpTeammates`) delivers at the next turn start of THAT session — never starts a turn. If nobody holds the loop, delivery logs and leaves the row due (catch-up later). If the server restarts, pending timers survive in the DB and due-but-undelivered ones arrive via catch-up — at-least-once. **FIXED 2026-10-25 (was the schedule gap)**: `schedule` no longer rides `codingAgentTools` — it is on `buildAgentTools` (`internal/agent/agent.go`), offered only to BuildAgent; TaskAgent/subagent and every other non-build agent never see it (`canSchedule()` gate, `agent.ID == "build"`, beside `canUseBoard`/`canSpawnTeammate`). Pins: `TestScheduleIsOnlyTheBuildAgent` + `TestScheduleLeftCodingAgentToolsShared` (`internal/agent/schedule_gate_test.go`); `TestBuildVsTaskAgent_Framing` pins build = task + exactly `schedule`.
- **`update_intent` rides the first work block** (2026-10-24, no longer standalone-first): state the plan
  before the first meaningful exploration, and it MAY ride the first tool block alongside other calls; a
  closing status update is welcome but NOT mandatory. `intent_gate.go` appends `intentGateReminder` ONCE PER
  SESSION when no plan is set (process-lifetime registry keyed by sessionID, deleted by `markPlanSet`, so a
  session that planned once re-arms for a later planless stint). Whole-list replace of 1–7 steps, persisted
  in `session.intent` (migration 064); a closed plan never invites another call. **Descent gate**
  (`descent_gate.go`): a `grep`/`glob` landing before any `codebase_map(subdir=…)`/`read`/`file_map` gets
  `descentGateReminder`, once per turn, armed only when `indexedFiles > 0`; a root map is not a descent.
- **Matcher**: ONE routing completion per block for a newly added step, **no cap**; scores skills + one entry
  per MCP SERVER; failures silent. **A step with no `description`, or not pending/active, is never routed.**
- **Chip identity is the step LABEL, not its description**; counts persist through `done`/`dropped` until the
  step leaves the list or the next `RunLoop` clears. **Nothing closes the plan at turn end**, so
  `updateIntentPrompt()`/`Description()`/the `status` schema demand STATUS movement, not a restatement
  (`intent_test.go`). **Status moves land mid-turn** (2026-10-23): "a status move is a plan change" — active
  before a step's first block, done after its last, not banked for the closing call; schema says "Move it as
  the work moves", no longer "Move this on every call". Pins: the softened-contract wants in
  `intent_test.go`, `update_intent_test.go` `TestUpdateIntentDescriptionMakesTheClosingOptional` +
  `TestUpdateIntentDescriptionMayRideTheFirstBlock`, `intent_gate_test.go`
  `TestIntentGateFiresOncePerSessionAcrossTurns` (the per-turn gate pin and
  `TestUpdateIntentDescriptionRidesTheWork` were rewritten for the softened contract).
- **The build agent has no upfront skill or MCP list** (`turnTools` withholds the `mcp_*` glob), and a bare
  `skill` call is refused in a matcher session. **Rich output** is fenced declarative specs (mermaid,
  plotly/rough, LaTeX, sandboxed HTML); `motion` declined; `## Decisions & why` only for real decisions.
- **`read` takes `files=[{path,ranges,offset,limit}]`** (2026-10-24): exclusive with top-level `path`
  (neither / both refused); each entry is exactly the single-file shape; per-file error sections and
  `== path ==` headings relative to SessionDir; every entry rendered whole (title `N files`, base name for
  one); bookmark stamped per CONTENT section only — never for a map, listing, binary or error — captured
  before ReadFile; single-file output unchanged.
  Pins `read_files_test.go`, `TestReadTool_FilesStampsEveryContentRead`,
  `TestReadTool_FilesMapEntryDoesNotStamp`, `TestReadTool_FilesReturnsEveryFile`.
- **`read` and `file_map` are UNCAPPED by design** (2026-10-25, the developer's call, reversing the day-old
  `fitBody` cap): no byte/line/per-line limit, no shared call budget, and **no default window** — a bare read
  returns the whole file and a range without `end_line` runs to EOF; `Truncated` now only means "a paging
  window left lines unread" (the `Use offset=` footer). **A bare `limit` of ANY size is a window past the
  map** (same-day fix: `rangeRequested` = ranges || offset>0 || limit>0 in `read.go`, so `limit: 250` on a
  302-line file reads content, not the map; negative offset/limit are refused in `validateReadWindows`; the
  map answers only a bare whole-file read). Pins the flip: `TestReadTool_BareLimitPagesContentPastTheThreshold`,
  `TestReadTool_NegativeWindowArgsAreRefused`.
  Both set **`Result.Uncapped`** (`json:"-"`, beside
  `Truncated` in `tool.go`), and `capToolOutput` (`internal/agent/loop.go`) returns early on
  `Truncated || Uncapped`, so the 50 KB loop backstop still bounds bash/grep/glob/MCP but never these two.
  **The one bound left on a read is ogx's 4 MiB request body** (`ogxMaxRequestBytes`, `checkBodyFits` →
  `ErrRequestTooLarge`, fatal): an oversized read now fails the request instead of being clipped, and
  `read_pressure.go` (40000 tokens) is only advisory. Pins `TestReadTool_MapIsReturnedWhole`,
  `TestReadTool_FilesReturnsEveryFile`, `TestReadTool_NoDefaultWindow`,
  `TestCapToolOutputHonoursUncapped`. Kept: the map interception itself (`mapInsteadOfWholeFile`, the
  200-line `rangedReadThreshold`) — it decides which answer you get, not how big it is.
- **The prompts make `files=[...]` the primary read shape** (2026-10-25): the exploration diagram's read step,
  build step 2, and the first sentence of `read`'s `Description()` all name ONE `files` call for several files;
  the map section adds a **grep fast path** (a search that printed `path:line: text` has already located the
  code — map only files whose shape is unknown, and a read answered with a map IS the map) and the **one read
  call per wave** rule. Pins: `TestWorkflowLetsASearchStandInForTheMap`, `TestWorkflowSpendsOneReadCallPerWave`,
  `TestProcessStepTwoNamesTheBatchedCalls`, `TestReadDescription_LeadsWithTheMultiFileShape`,
  `TestFileMapDescription_BatchesAndSkipsANeedlessMap`.
- **Tool contracts the schemas do not show**: a blank ask_user answer to a content question is unanswered;
  **pathlock** forbids write-vs-edit on one file but allows batched edits; **codemap** is one level per call
  with **no size threshold** (the old <10-file expansion was deleted — `project_index.go`), `dirPrefixFilter` needs `ESCAPE '\'`, **HCL is outlineOnly**, and a Swift `as?` + newline
  + `??` misparses.
- **Images** are stripped for non-vision models (`IsImageRejection` is resumable); MCP images go to a temp
  file. **Skills** `requires:` env names are filled by `skills.env` via `setIfUnset`.

### MCP & search (pre-diet lines 302-310)

- **`OGCODE_SEARCH_ENABLED=false` removes `web_search`, `fetch_page` AND `deep_search`**; to suppress only
  deep_search, add a prompt clause.
- **MCP**: transport is INFERRED (`Command` ⇒ stdio, `URL` ⇒ streamable-http; `"transport"` overrides) — **no
  `type` key**. Tool ids `mcp_<server>/<name>`; connect is lazy. A URL server with no `headers` gets OAuth
  (`~/.ogcode/mcp-tokens/`); `headers` means static bearer. Penpot's `…/mcp/stream` needs streamable-http.
- **Search** results carry a `Provider` stamp set before the TTL cache. Config is `/settings/search`, a
  FULL-STRUCT replace; `""` = HTTP first, `safari`/`chrome` = browser first (`chrome` on macOS means Safari).
  macOS prefers headless Chromium with a Safari fallback; Windows Chrome/Edge is unverified — check with
  `GOOS=windows CGO_ENABLED=0 go build ./internal/search/`.

## 2026-10-25 — MEMORY.md diet: data/migrations/transcript, file change gate, web UI, theme and preview pre-diet text (from MEMORY.md diet)

### Data, migrations & transcript (pre-diet lines 314-330)

- **Migrations**: goose keys by VERSION NUMBER only, so a number taken by a branch that never landed silently
  SKIPS your file. Check `git log --all -- internal/db/0NN*` AND the real `goose_db_version`. Gaps resolved
  (2026-10-23): 019–021 = callgraph migrations (landed then removed, 65fb5d6); 031 was first `posthog_config`
  (removed 8fa5af3) before model_collection reused it; 057 = You.com search provider that never landed (its branch
  briefly renumbered itself to 059, also unlanded). Next free number = next integer past the highest file.
- **The transcript is append-only**; the only bound is `capToolOutput` (50 KB; skipped by `Result.Truncated`
  and `Result.Uncapped`);
  the only deletes are `Store.Delete`/`DeleteMessage`. Compaction changes what is re-sent, never what is stored.
  **Nothing prunes and nothing vacuums**: `db.go` sets only WAL/foreign_keys/busy_timeout — no `auto_vacuum`,
  no `VACUUM` anywhere in the repo — so deleting sessions only fills the freelist and the file never shrinks;
  reclaiming needs an explicit `VACUUM` (~DB size in temp space). Measured shape: `part` 92% of the project DB,
  `build` sessions 89% of that (1.3 MB/session vs 34 KB for an `index` session); `file_change` 0.07%. Details in `HISTORY.md`.
- **FK 787 mid-turn ⇒ another writer deleted the session**: `handleDeleteSession` cancels and waits up to 5s
  before `store.Delete`; `Store.Get` returns `(nil, nil)`; `classifyInterruption` maps it and
  `ErrRequestTooLarge` to `InterruptFatal`.
- **Session DB**: `.ogcode/ogcode.db` / `~/.ogcode/config.db`; `message` is metadata, content in `part.data`.
  **`GetMessages` must return `Parts` as `[]`**; pagination is `?before=<id>` + `X-Has-Older` at 300 rows.

### File change gate (pre-diet lines 334-368)

- **Migration 066 + `internal/filechange`**: a `file_change` history row per `write`/`edit`/`bash` plus a
  per-(session,file) bookmark (`seen` revision); history is ring-capped at `RingPerFile` (50) per file; read API
  `GET /api/file-changes` (`file_change_routes.go`, 503 when `s.fileChanges == nil`) — **no web client calls it yet** (`plans/CONFLICT_FREE_MUTATIONS_PLAN.md:319` left the panel out of v1), so the stored `diff` has one live consumer: the gate's own refusal after a restart (`log.go:287` `covered` false ⇒ `History`), the ring being memory-only.
- **The gate refuses in `write`/`edit`** (`internal/tool/staleness.go` `staleRefusal`): a file changed by
  ANOTHER session since this session last read it refuses, and the refusal is a `Result` (not an error) that
  shows the diff — same shape as the board-claim refusal. `Stale` drops the caller's own changes and refuses
  nothing when `Seen < 0` (never read). **`Seen` returns -1, not 0**, for never-read — 0 is a real bookmark
  (a file seeded outside the tools is read at revision 0), pinned by `TestBookmarkAtRevisionZeroStillRefuses`.
  **The ring explains nothing when it is empty** (a restart): `covered := len(ring) > 0 && ring[0].Rev <= seen+1`,
  so an empty ring always falls through to the `History` query — a bookmark of 0 was otherwise counted as
  `dropped` and refused every previously-read file (`TestRestartRefusesABookmarkOfRevisionZero`).
- **`bash` is COVERED, by fingerprint not by parsing** (`internal/tool/bash_observe.go` `observeShell`, called at
  the end of `BashTool.Execute`): after every command it stats each file the log already tracks
  (`Log.Files()`/`Fingerprint{Size,MTime}`/`FingerprintOf`) and records a change (tool `bash`, note = the
  command, `DiffOmitted`) for any that moved — a deletion included, since a missing file is a zero fingerprint.
  **It records the change UNATTRIBUTED (no `Session`), so no bookmark moves** and the session that ran the
  command is refused by its own shell until it re-reads: size+mtime cannot say whether this command, another
  session or an unseen process wrote the bytes, and crediting the caller would launder exactly the foreign change
  it must be refused by (`bash_gate_test.go` `TestOwnShellChangeRefusesUntilReRead`,
  `TestUnrelatedCommandDoesNotLaunderAForeignChange`). Nothing auto-claims, and an **untracked** file is skipped
  (no bookmark ⇒ it can stale nobody).
- **`description` is required on `write`/`edit` now** — it is the history note; a bash row's note is the command.
- **A nil `FileLog` is a no-op** (`filechange.NewLog(ctx, nil)` still gates from memory), so tests and other
  embedders need not wire the store; `observeShell` too.
- **Remaining blind spots**: MCP tools, `latex_to_pdf`'s `.tex` write, and a change made outside the agent
  (an `go test` that rewrites fixtures) until the next `bash` call stats it. `file_map` is NOT a read (a summary
  would suppress the diff exactly when it is wanted). A **map-only read does NOT stamp the bookmark** — the
  map-vs-content decision runs ABOVE `log.Read` (`read.go`; pin `TestMapOnlyReadDoesNotCertifyContent`) — but a
  map-only read that is the gate's first contact leaves the file untracked (no fingerprint), so bash observation
  records nothing for it until a content read.
- **It FAILS OPEN when the history row is missing**: with no change to show there is no catch-up to explain a
  refusal, so the write proceeds (warn-logged).
- **Cross-process staleness appears only after a restart**: bookmarks and fingerprints are in-process memory,
  not re-read per mutation — `serve` + a separate `run` on one dir can each miss the other's changes while both
  are up.

### Web UI: composer, sidebar & navigation (pre-diet lines 372-380)

- **Composer drafts/attachments are per session**; **the running pause glyph IS the stop control**; the `+`
  attach menu renders in a `Portal`. **The plan-mode flag gates only the warning banner**; mode comes from
  `GET /api/mode`. **The session-row dot reports task status** (`session.status`), not the provider, and a
  hand-seeded session row must set `permission` or `Store.List` 500s. **`⌘K` is guarded on `offsetParent`**
  (the shell renders twice).
- **Virtualization and scroll memory** in `message-list.tsx`/`scroll-memory.ts`: pad-based windowing keyed by
  message id, rows estimated from running per-role totals over all rows measured so far (averaging the mounted
  window remounted ~1,000×/s); the id-change effect must call `restore()`, and ResizeObserver must not
  `holdAnchor` when `anchoredByList`.

### Theme, contrast & light mode (pre-diet lines 384-390)

- **Light mode is server-global**: `GET/POST /api/appearance` off **`s.globalDB`** — a throwaway rig flips the
  USER's machine-wide mode, so save before an audit and restore after. `web/index.html` pre-paints from
  `localStorage['ogcode-theme-mode']`. **Settings General is `/settings`.**
- **Muted-text floor: 4.5:1 on the LIGHTEST surface the token can sit on** (`--bg-hover`), not the darkest;
  every muted/label/zinc token is calibrated to it. Exact values: `HISTORY.md`.
- **Default accent is brand orange `#ff5e1f`**; **accent ink is its own per-mode token**, never `--accent`.
  **`onPrimary` is `labelFor`** (max of the minimum ratio over `--accent` AND `--accent-hover`), recomputed on

### Preview, public serving & device panel (pre-diet lines 395-398)

- **The Preview page lists only announced ports** (`<port>.<PreviewDomain()>`); `recordAnnouncedPorts` reads
  **PartText only** at turn end for the build agent; manually `?ports=`. `previewHostDispatch` strips
  `X-Frame-Options` + CSP `frame-ancestors` or a frame-guarded app renders blank.
- `/public` serves `<data-dir>/public` with **HEAD mapped explicitly**. **scrcpy** deep links need

## 2026-10-25 — MEMORY.md diet: browser takeover pre-diet text, native-messaging block excluded (from MEMORY.md diet)

The protected native-messaging block (pre-diet lines 482-497) was left untouched in MEMORY.md, so it is not archived here — it is unchanged in both places.

### Browser takeover (pre-diet lines 403-480)

- **Option B, shipped 2026-10-25** (developer chose built-in skills, not MCP): the agent drives the user's
  real logged-in Chrome via a first-party MV3 extension in `browser-extension/` (vanilla JS, no build)
  paired over a WS bridge to serve. Pieces: `internal/browser/bridge.go` (Dispatch/pending-map over
  coder/websocket), `internal/tool/browser.go` (11 tools: status/navigate/snapshot/content/click/type/key/
  scroll/screenshot + network/console), `internal/server/browser_routes.go`, embedded skill `browser-takeover`
  (`internal/skill/embedded.go`). Tools are registered for every turn like board (unpaired calls return
  Result-shaped pairing refusals, never errors); screenshot rides `Result.Image` png and is refused when
  `!ModelSupportsImages`. Ops budget 30s (60s navigate/screenshot).
- **Endpoints**: `GET /api/browser/pair` → `{token, url}` (Host echoed; wss under TLS), POST rotates;
  `GET /api/browser/status` always 200 (zero Status when no bridge); `GET /api/browser/bridge?token=…`
  accepts only loopback + chrome-extension origin + token (401/403/503 otherwise).
- **coder/websocket origin trap**: OriginPatterns match the BARE origin host unless the pattern contains
  `://` — use `"chrome-extension://*"`, not `"chrome-extension:*"` (a real extension 403s on the latter).
- **Re-pair fails every pending op with ErrNotConnected**: `failPending` ships the sentinel as a plain
  string, so `Dispatch` maps `res.Err == ErrNotConnected.Error()` back to the sentinel (errors.Is must
  keep holding); extension-op errors keep the `"browser: %s"` text path.
- **Hello version is a bare JSON NUMBER** (`version: 1`, not `"1"`): `helloFrame.Version` is `any` +
  `versionString()` (strconv.FormatFloat) — a `string` field unmarshal-errors and hello drops silently,
  leaving the extension stuck on "connecting" with no error on either side.
- **FIXED 2026-10-25 — the 32 KiB websocket read limit killed every screenshot/snapshot**: coder/websocket
  defaults read to 32769 bytes; a snapshot/screenshot REPLY frame (base64 PNG, DOM dump) is hundreds of KB, so
  `message too big: read limited at 32769 bytes` killed the conn (the extension re-dials silently in ~1.3s
  bursts — one hour saw 119 "paired" logs vs 15 logged deaths, overwhelmingly client-initiated close/swap, no
  server WARN — and failPending drops every in-flight op). Fix: const `maxResultFrameBytes = 4 << 20` +
  `conn.SetReadLimit(maxResultFrameBytes)` in `internal/browser/bridge.go` `Serve` (line 237) before the
  read loop; pin `TestBridgeAcceptsAResultFarPastTheOldDefaultLimit` in `internal/browser/bridge_test.go`.
- **navigate `newTab` opens an auto-granted tab** (2026-10-25, the open-without-control gap): exemption is
  navigate + newTab + NO tab id — `grantTab` adds to `grantedIds`, persists sorted to `grantedTabs`,
  revocable in the panel (side panel needs nothing, listens to storage.onChanged); `newTab` PLUS a tab id
  means "act on that tab", gate still applies. Server `run()` defaults an omitted `tab` to the last Granted
  tab in the hello inventory (`lastGrantedTab`), refuses Result-shaped with the panel remedy when tabs exist
  but none granted, passes nil through on an empty inventory (no hello yet). Pins: browser_test.go
  (TestBrowserNavigateNewTabNeedsNoTabAtAll + 3 more).
- **Watch capture: `browser_network` + `browser_console`** (2026-10-25, same-day followup): the FIRST op on
  a GRANTED tab attaches a PERSISTENT chrome.debugger session (`ensureWatch`: Network/Runtime/Log.enable,
  never detached while watched — the debug banner stays up the whole time) and fills per-tab in-memory
  rings — network 300, console 300, evict-oldest, emptied by an MV3 service-worker restart (re-arms on the
  next op). `browser_network` lists last-N (default 100) with a `url` substring filter; `requestId` fetches
  the body (cap 65536, `bodyTruncated`, `base64Encoded`); `browser_console` filters level/source exactly.
  Watch-drop hooks: ungrant rides storage.onChanged (no runtime message exists), tab close, and
  debugger onDetach (user Cancels the banner → next op re-attaches fresh). **CDP timestamps are epoch
  SECONDS** (`cdpIso` ×1000); `Log.entryAdded`/`startedAt` carry no CDP ts → wall-clock — don't unify.
  Pins: browser_test.go TestNewBrowserToolsElevenUniqueIDs + 7 capture tests; extension logic pinned only
  by a throwaway vm harness — **VERIFIED LIVE 2026-10-25 against real Chrome** (hello, grant + watch banner all
  served; a probe conn was swap-kicked "replaced by a new pairing" in 1.2 s by the real extension's retry, so
  the loop does dial). The panel's "Reconnecting — connection lost — retrying in Ns" is `scheduleReconnect`'s
  backoff text (`background.js:249`, panel renders it at `sidepanel.js:160`); it is NORMAL for one or two
  rounds after any drop (extension reload, re-pair press rewrites origin/token storage and closes the live
  socket by design). If it sits unchanged on the screen: the service worker died mid-retry (a killed SW sends
  no more statuses) — reopen the side panel, whose `get-status` wakes it, and it self-heals + re-syncs.
**FIXED 2026-10-25 — a server restart no longer breaks pairing**: the pair token is persisted at
  `.ogcode/browser-pair-token` (0600, in the project dir's data dir) — `server.go` boots the bridge with
  `browser.NewWithToken(loadBrowserPairToken(s.dir))` and re-saves after every rotate (`RotateToken`); the
  extension's reconnect re-dials the SAME stored token, so it now reconnects automatically across restarts.
  Empty/malformed file ⇒ fresh mint + save (pinned by
  `TestNewWithTokenPinsThePersistedToken`, `TestBrowserPairTokenSurvivesARestart`,
  `TestBrowserPairRotatePersistsTheToken`). HISTORICAL behaviour (now gone): the token was in-memory only (`bridge.go`
  `New()` minted one per boot) — **Re-pair = paste the NEW token, then Connect** (press the button once first —
  "reconnecting" shows as Disconnect, pressing it stops the loop; sidepanel.js:73-97). The agent can hand the user the
  token: `GET /api/browser/pair` returns the boot's token WITHOUT rotating; POST rotates (and now persists).
- **FIXED 2026-10-25 — registered but never offered**: browser tools are registered in `serve()` (server.go
  ~436) but the LOOP's per-turn allowlist had no append, so every op refused `tool %q is not available to the
  build agent` (loop.go executeTool guard). Fix: `canControlBrowser()` (agent.go, build-only, mirrors
  canSchedule) + `browserOffered := agent.canControlBrowser() && lr.Tools.Get("browser_status") != nil`
  appending `browser_*` (ForAgent expands any glob; new browser ops ride the same seam; CLI-safe — its
  registry has no bridge so the Get check fails there). Pin: `internal/agent/browser_gate_test.go`.
- **Snapshot on a big DOM and screenshot KILL the SW, not pairing — don't re-pair on "extension not paired"** (2026-10-25, live-verified with a 100ms /api/browser/status sampler): a heavy page op drops the ws ~0.1–0.6 s after dispatch; the extension's own reconnect (BACKOFF_MIN_MS=1000, background.js:19) restores it ~1.1 s later, and `swapConn` → `failPending(ErrNotConnected)` (bridge.go:225,229,337-343) fails the in-flight op — the tool wraps that as the misleading `unpaired()` refusal (tool/browser.go) though pairing was fine. Small-page snapshot, content, navigate, scroll, network/console read fine; a legit op error ("Cannot access a chrome:// URL") does NOT drop the conn — only heavy ops do. Suspect the in-page `Runtime.evaluate` snapshot walk (ELEMENT_CAP=300) / `Page.captureScreenshot` suspending the SW (unproven mechanism). Wart: `dropConn` (bridge.go:269-283) comments it fails in-flight ops but doesn't call failPending. Fix candidates, not implemented: screenshot via `chrome.tabs.captureVisibleTab`; bridge retry-once on the sentinel or failPending in dropConn. Evidence + numbers: `HISTORY.md` 2026-10-25.
- **CWS submission kit (2026-10-25)**: `browser-extension/` prepped for the Chrome Web Store — icons
  `browser-extension/icons/icon{16,48,128}.png` (from `docs/media/logo.png`), manifest got `icons` +
  `action.default_icon`; listing copy + permission justifications + pre-upload checklist in
  `browser-extension/STORE_LISTING.md`; privacy page `docs-site/src/content/docs/privacy.md` (nav slug
  `/privacy`, public URL `https://ogcode.in/docs/privacy/` — deploy via docs/DEPLOY.md before the store
  form). Zip: `make extension-zip` → `ogweb-<ver>.zip`, manifest at ZIP ROOT (zip from inside the dir),
  gitignored `/ogweb-*.zip`. **`host_permissions: ["<all_urls>"]` was DROPPED (2026-10-25) — nothing
  uses it** (no content scripts/scripting/cross-origin fetch; WS dials the user-entered origin;
  chrome.debugger needs only `debugger`). NEVER re-add <all_urls>; the drop is VERIFIED-BY-INSPECTION
  only until a live reload+op pass (navigate/snapshot/screenshot on a granted tab). chrome.debugger
  draws extra CWS review; bump manifest version before every re-upload.

## 2026-10-25 — MEMORY.md diet: build loop, indexing, controlplane, infrastructure and board pre-diet text (from MEMORY.md diet)

### Build & dev loop (pre-diet lines 45-52)

- `make build` = `build-web` (npm build → `web/dist`) then `build-server` (`CGO_ENABLED=1 go build -o ./ogcode .`;
  repo-root `main.go`). `make dev` :9595; `make dev-web` Vite :5173.
- **`CGO_ENABLED=1` mandatory** (cgo Swift grammar); `make test` omits it. OGX-signed build:
  `make build-server OGX_APP_SECRET=$(tr -d '\n' < ~/.ogcode/ogx-app-secret)` — else the gateway 401s.
- `ogcode version` (no `--version`); dirty builds print `…-dirty`; no linter, gofmt files touched.
- **`ogcode run --max-turns` defaults to `0` = no limit** (→ `MaxStepsUncapped = -1` → `math.MaxInt`). **A
  literal `MaxSteps == 0` still means the 1000-step default** — a zero-value `LoopRunner` depends on it. The
  server loop (10000, 2 auto-resumes) and `ogcode index` (50) stay capped (`step_budget_test.go`).

### Indexing & the document index (pre-diet lines 501-506)

- Auto-index is gated by `OGCODE_AUTO_INDEX_MAX_FILES` (500, env-only) + consent (migration 061); **manual
  builds are never gated**. The post-turn guard is `agentID == "build"` **and** `Directory == ProjectID`;
  `OGCODE_AUTO_INDEX` is the kill switch. It must run on the turn's resolved model AND provider.
- **Labels are LLM-made, not embeddings**: `IndexAgent` (one tool, `submit_doc_index`) labels a page from a
  local keyword corpus (`text.go`/`pdf.go`); there is no vector store anywhere in `internal/indexer`.
- **`.ogcode/` is skipped unconditionally**; **39** default excludes match directory names and basenames

### Controlplane, remote workers & Incus (pre-diet lines 511-515)

- **Nested module** `controlplane/`: one in-process `server.Server` per worktree on the worker daemon.
  **`HostSession` must `store.Get` first** (`Store.Create` violates UNIQUE on re-delivery); routes key
  `<workerID>-<route>`, lookup **exact whole-label**. **bbolt**: a byte-truncated file SEGFAULTS at `bolt.Open`
  (`checkDBFile` validates the meta page), a restore must set `status=offline` + `LastSeen=boot time` or
  `ReapStale` kills every worker, and it is **single-writer**. Workers, accounts, Incus: `HISTORY.md`.

### Infrastructure, feature flags & telemetry (pre-diet lines 519-531)

- **Homepage is `docs/index.html`** (hand-written; nav `.nav-menu`, mobile `#navSheet` — add links to both).
  **Public copy carries NO em dashes**; a `: ` in an unquoted YAML frontmatter `description:` breaks the
  `docs-site` build — quote it. Docs nav lives in **`docs-site/src/lib/nav.ts`**, deploy in `docs/DEPLOY.md`,
  and `ogcode serve` binds all interfaces with **no auth** (a proxy MUST NOT buffer). Brand button `#d1440c`.
- **Cloudflare**: `ogx`/`oglab` A → `162.19.81.108`; **origin nginx listens only on 40009**, so those vhosts
  need `origin.port=40009`. Box access is password-only (`sshpass … -p 20009`); edit `sites-available`, then
  `nginx -t && systemctl reload`.
- **PostHog `497411`**: **never send content**; headless browsers drop every event (patch UA + `webdriver`).
  `ogcode_turn_resources` is one event per turn with `distinct_id` = `s.installID`; logs need
  `OGCODE_POSTHOG_LOGS`. **Install-id**: `VAR=x cmd | sh` does not pass `VAR` — `export OGCODE_IID='…'; curl …
  | sh`.
- **Feature flags**: one `/decide` per refresh, fail-safe OFF. Notes also drops its prompt section;
  device-panel 404s routes and proxy; plan-mode gates only the banner. Serve tests use `stubFeatureFlagsOn(t)`.

### Agent board & teammates (pre-diet lines 535-648)

- **The board IS the agent tree, not a message log** (migration `067_agent_tree.sql`): `board_agent` rows carry
  `spawned_by` (the tree edge), `status`, `notified_at`; **`board_post` is dropped**. `internal/board` (store +
  migrations) behind `internal/server/board.go` + `board_routes.go`; tools are `internal/tool/board.go` (**one op,
  `roster`** → `treeText`) and `teammate.go` (spawn/stop). Routes: `GET /api/board/roster` →
  `{"agents":[…]}` and `POST /api/board/teammate/{handle}/stop`. `plans/MULTI_AGENT_COMMS_PLAN.md` and
  `plans/BOARD_PUSH_PLAN.md` are HISTORICAL (posts, push pump, claims — all deleted).
- **The header drawer is GONE — the board now has a dedicated Agents page** (2026-10-25, the developer's
  call): the session header carries no drawer node; instead `web/src/pages/agents.tsx` at route `/agents`
  (registered before `/preview` in app.tsx), nav label "Agents" from session-sidebar + plan-sidebar (nav
  button + rail icon button, active = `startsWith('/agents')`). `board-drawer.tsx` DELETED;
  `board-drawer-row.tsx` KEPT and reused by the page, which ported the drawer's whole logic (SSE 150ms
  debounce via `server.lastEvent()`, 15s poll, filter, showFinished, collapse, two-press stop).
- The former drawer was a filterable, collapsible tree (2026-10-24, now the Agents page's tree card): filter +
  `Finished N` tick toggle + collapsed set over `board-drawer-row.tsx` (indent rails, elbow, `you` pill,
  `animate-pulse-ring` on a running dot, two-step Stop), with the pure logic in `web/src/lib/board-tree.ts`
  (`buildTree`, `decorateRows`, `matchesQuery`, `rosterSummary`). **Header chips must stay DISJOINT:
  `running = live − idle`, because `live` ⊃ `idle`** — "live · idle · finished" read 42 for a 28-agent roster.
  Rows show the session **title + model joined per row** in `board_routes.go` (`boardRosterRow` embeds
  `board.Handle`, `s.store.Get` per row guarded, `omitempty` so a deleted session serializes as before; flat
  JSON — a board row has no title of its own).
- **An op-less teammate call self-heals**: ogx flash models (glm-5.3-flash, adhyay 2026-10-23) dropped the
  required `op` on spawn calls → 8-retry loops; `teammate` now infers `spawn` when `prompt` is present
  (output says `op omitted — treated as "spawn"`) and the empty-op refusal is shape-aware —
  `handles`/`timeout`→`wait`, `handle`→`stop`/`status`, `name`→`spawn` (`missingOpHint`,
  `internal/tool/teammate.go`), pinned by `internal/tool/teammate_test.go`.
- **A child reports back by FINISHING; there is no mid-task channel in either direction** — a teammate cannot ask
  its parent, a parent cannot ask a running child. Child loop end → `status=done` + `updated_at`; spawner
  RUNNING gets a turn-scoped `LoopControl` notice carrying the child's last assistant message **whole — no byte
  cap** (the old 4 KB `teammateReportBytes` cut is gone; `DrainNotices`' `maxNoticeBytes` 2 KB only bounds a
  BATCH, and an oversized notice is always taken alone, so its text is never clipped) + `/session/<id>`) then
  `MarkReported`; spawner IDLE keeps `notified_at < updated_at` and `CatchUpTeammates`
  (`server/board.go:263`, hooked in `loop.go:384`) hands it over at the parent's next turn start. **Nothing starts
  a turn on its own** (developer's explicit choice); `CatchUpBoard`/`AckBoard`/`DeliverDirected` are gone.
  Pin: `TestUnreportedUntilMarkedReported`, `TestCatchUpTeammatesReportsAnIdleSpawnerOnce`.
- **A terminal status is claimed BEFORE the loop is cancelled** (`board.Store.SetStatusUnlessTerminal`, a
  `WHERE status NOT IN (done,stopped,stale)` UPDATE): `stopTeammate`/`stopTeammatesOf` claim first and skip
  `cancelLoop` when the claim misses, `leaveBoard` returns early the same way. Cancelling first let a finishing
  child write `done` over the `stopped`, and its `updated_at` bump re-delivered the old report as new.
  Pin: `TestStopTeammateIsNotRewrittenDone`, `TestStopTeammateOfAFinishedTeammateChangesNothing`.
- **That pending report is durable, delivery is not automatic**: `notified_at`/`spawned_by` are in the project
  DB's `board_agent` (067) and `Register`'s ON CONFLICT leaves `notified_at` alone, so a restart neither loses
  nor re-delivers a report (reopen probe, 2026-10-23). Startup `reconcileBoard` (`server.go:691`) demotes a
  teammate left `running` to `stale` — terminal, so it is delivered as a stale report; nothing starts the
  parent's turn, so it waits for the user. **Marking is at DISPATCH** (2026-10-24, was at drain, was at push
  before that): RunLoop takes notices into loop-local `wireFresh`/`wireDigest` (loop.go ~1163) and stamps via
  `lr.BoardNoticesDelivered` ONLY inside the dispatch-success block (~1356); the drain sites stamp nothing, and
  the pre-stream gap restart calls the new `lc.RestoreNotices(wireFresh)` (`loop_control.go:301`, restores to the
  FRONT of `unshown`, idempotent, leaves `deliveredNotices` alone so a digest never shadows restored full text) —
  the old path re-shipped a one-line "already given above" digest pointing at text no request carried, with the
  row already stamped. `catchUpTeammates` tracks its pushes the same way (`TrackBoardReport` + Mark closure;
  nil-runner fallback stamps at push); a turn or provider-retry exhaustion that dies before the first dispatch
  leaves rows unreported → next-turn catch-up. Crashes re-deliver = at-least-once (accepted); narrow accepted
  window: a stream cancelled between take and dispatch doesn't stamp. Pins:
  `TestLoopBoardReportsStampOnlyAtDispatch`, `TestRunLoop_NoticeGapRestartRidesTheNextRequestFull`,
  `TestRestoreNoticesRequeuesFreshWithoutDigestLie`, `TestTeammateReportUndrainedAtTurnEndStillDelivers`
  (rewritten to drain by hand), `TestLoopBoardNoticesDeliveredMarkTheReport`, `TestLoopBoardReportsDieWithTheirTurn`.
  **Delivered notices re-inject as HEADLINE digests after their first step** (2026-10-24):
  `TakeNoticesForInjection` (`internal/agent/loop_control.go`) returns fresh notices full-text once
  (sourced from `unshown[]`, filled by `DrainNotices`) and everything already shown as
  `Notice.Headline()` under `noticesDigestLabel`; re-shipping the full report every step made a flash
  model re-acknowledge it and narrate re-checks at every step. Pin:
  `TestLoopControl_AReportReadsFullOnceThenDigests`. The interrupt path
  (`InterruptForNotice`/`ArmNoticeInterrupt`/`interruptArmed`) was DELETED as dead code — reports drain per-step,
  the stream interrupt never fired.
- **Handles mint FRESH; stops REPORT; waits RESPECT the turn's cancel** (2026-10-24, the 7-bug sweep):
  `board.RegisterFresh` = insert-once (`ErrHandleTaken`; UNIQUE-violation mapped; `Register`'s upsert untouched) +
  server `mintFreeHandle(ctx, reg, attempts, newName)` (board.go:103) rolls a 4-hex suffix up to
  `boardHandleAttempts`=8 — wired into `joinBoard`, `spawnTeammate`'s random path (claims the handle BEFORE the
  child session is created, so the title built from it stays truthful), `spawnBackgroundTask`; typed names keep
  the friendly Get pre-check then go through RegisterFresh. `stopTeammate` reports "was stopped" via
  `reportTeammateDone` (board.go:935, guarded on `SpawnedBy`/`SessionID`) — drawer and tool stops both land
  there; `stopTeammatesOf` (abort/delete cascade) stays report-free and its rows ride the next catch-up.
  `waitTeammate` breaks its 250ms slice on `ctx.Err()==context.Canceled` (board.go:791 — abort/superseding
  prompt/delete) but NOT on `DeadlineExceeded` (the op bound; long waits keep working). Supersede via
  `startSessionLoop` does NOT stop teammates — the cascade lives only in `handleAbortSession`/delete/hosted
  cancelLoop; pinned `TestStartSessionLoopSupersedeKeepsTeammates`, `TestMintFreeHandleRetriesPastCollisions`,
  `TestWaitTeammateReturnsPromptlyWhenTheTurnIsCancelled`, store pins `TestRegisterFresh…`/`TestRegisterStillUpsertsARejoin`.
- **Build-only by AGENT ID at every seam**, not by tool absence: `codingAgentTools` is shared with `TaskAgent`, so
  `canUseBoard`/`canSpawnTeammate` (`agent/agent.go:496,505`), `joinBoard` and the `CatchUpTeammates` hook all
  test `agentID == "build"`; the prompt section sits in the `if !task` branch of `codingAgentSystem`. Coverage:
  `internal/server/board_test.go` + `board_routes_test.go` and `TestStartSessionLoopJoinsAnEmptyAgentAsBuild`.
- **The prompt casts the build agent as an ORCHESTRATOR** (`boardPrompt`, `agent/agent.go`): "You orchestrate;
  teammates do the work" — decide the fan-out before the first read, hand streams out, and spend its own time on
  the next decision, shared files, verification and user questions (watching with `teammate wait`, never polling
  roster/status). Solo only for strict sequence, one shared file, an edit too small to brief, or research (`task`).
  **Parallel-first default stated outright** (2026-10-23, `TestBoardPromptPrefersTheSplit`): side-by-side streams
  are "the default, not an upgrade", spawned in one tool block **right after `update_intent` states the plan**
  (the plan goes first — before the fan-out — but MAY ride the first tool block, 2026-10-24
  contract; the standalone-first mandate is itself on the pins' STALE list), one teammate per independent stream, and the fan-out's only cap is
  verifiability, not cost.
  Two currencies, wall-clock (one teammate per independent file/package) and tokens (a child's greps/reads are
  billed in its own window, and the parent re-sends less history every step). Two rails live ONLY in the prompt:
  **do not end the turn while a spawned teammate is still working** (nothing starts the parent's turn for it) and
  **a teammate briefed to implement one stream implements it** (no recursive delegation). The rail names its two
  exits: `teammate stop <handle>` (wrong/duplicated/stuck brief; cascades to what it spawned) and **parking** a
  legitimately long child when the reply no longer depends on it — end the turn, nothing is cancelled, the
  finished report arrives via CatchUpTeammates at the next turn's start; the pins' wording also calls short-wait
  chaining "polling with a timer". Process step 1
  (interactive only) points at it. `TestBoardPromptPrefersTheSplit` pins the wording, decision-before-mechanics
  order, and that no non-build agent prompt mentions `teammate`.
- **`startSessionLoop` normalizes `agentName == ""` → `"build"`**: the web client never sends `agent`, and
  `joinBoard` refuses any name but `"build"`, so without it NO interactive session joins the roster
  (`internal/server/resume_routes.go`).
- **`board.Store.Get` returns the ZERO Handle with a nil error** when nobody holds the name — check
  `h.Handle == ""` yourself before acting on it.
- **Rails**: **no spawn depth limit, no live-teammate cap** — any `build` session spawns, a teammate
  included (`TestSpawnTeammateAllowsNestingAndCapsNothing` asserts roster rows, never `liveTeammates`, which
  drops as children exit to `done`). **Steps are uncapped** — `stepBudgetFor` returns
  `agent.MaxStepsUncapped` (`-1` → `hardMax = math.MaxInt` via `overrideStepBudgets`, `internal/agent/loop.go`),
  so the old 300/0-auto-resume rail is gone; spawn = `Ask`; `cancelLoop` cascades down every
  nesting level. Claims (`Claim`/`HeldBy`/`claimHolder`) and the post budget (8 KB, 20/turn) died with the posts:
  **conflict-freedom comes from the file change gate, not the board**.
- **Task tool async (`"background": true`)**: `spawnBackgroundTask` (`server/board.go`) registers a READ-ONLY board KindTask child — child session type `"subagent"`, Role `subagent`, Title `Task: …`, prompt `backgroundTaskPrompt`, model/permission inherited — and returns a `task-*` handle + `/session/…` URL at once; sync `task` is unchanged and default (CLI `run.go` registers no Spawn → background refuses). Delivery reuses the teammate-report pipeline: `leaveBoard` → "Background task" notice to a RUNNING parent, `CatchUpTeammates` otherwise; `teammate wait <handle>` blocks early; `stopTeammate`/board route/drawer accept KindTask; budget = sync parity (`stepBudgetFor` KindTask → `agent.DefaultMaxSteps`, 0 resumes).
- **Task children are ungated on purpose and NOT permission-mirrored**: `resume_routes` skips `WithPermissionGating` for `agentName == "subagent"`; the teammate BFS in permission_routes and `ParentSessionOf` stayed teammate-only (task children never request perms). Pins: `TestTaskBackgroundRouting`, `TestSpawnBackgroundTaskRegistersATaskChild`, `TestLeaveBoardReportsABackgroundTaskToARunningSpawner`, `TestStepBudgetForATaskChildMatchesTheSyncSubAgent`; `boardPrompt` teaches the mode off the research clause.


## 2026-10-25 — MEMORY.md diet: mid-diet snapshot (pass-1 wording; pre-diet originals archived above)

# Project Long-Term Memory

**Admission test.** A line earns a place only if a future session needs it *before acting*: a gotcha with its
fix, an invariant with its pinning test, a canonical value, or a decision and why. **Procedures live in
`HOWTO.md`; measurements and long-form subsystem detail in `HISTORY.md`**; the pre-split text is in
`MEMORY-ARCHIVE.md`. Budget **~20 KB** — re-read every turn on the cached prefix; curate rather than truncate.

**Standing conventions.** **Developer preference: needs SPEED — parallel by default** — batch every independent read/search in one block, and fan out teammates/task agents per independent stream instead of working serially (stated 2026-10-24). Working tree usually dirty — read hunks, never `git add -A`, stage explicit paths; nothing is committed unless asked. `web/dist` is served from an `embed.FS` (`internal/server/static.go`), so a
shipped web change needs a Go rebuild — but a live visual check does NOT: run a vite dev instance with a
throwaway config proxying `/api` to the live server (HOWTO.md: "Verify a web change against the live server
without restarting it"); Go rebuild only needed before shipping.

## Repo layout & git hygiene

- Root: markdown only; plans in `plans/` (refs inside bare, `plans/<name>` outside); next migration `069`.
- **`.gitignore` paths stay root-anchored**: `/GIT_DIFF_PLAN.md` (a bare rule ignored the tracked
  `plans/GIT_DIFF_PLAN.md`), `/dist/` (unanchored would swallow `web/dist/`), `/agent/` (duplicate of the
  golang-* skills — `.agents/skills` is the one `internal/skill/discover.go` reads; it also breaks
  `go build ./...`, so build `./internal/... .`). `MEMORY.md`/`MEMORY-ARCHIVE.md` gitignored;
  **`AGENT.md` is tracked but 0 bytes** (a `see AGENT.md` pointer is stale).
- **README hero is `docs/media/ogcode-demo.gif`** (GitHub renders neither animated WebP nor `<video>`);
  re-encode via `HOWTO.md`; check for leaked internal paths at legible resolution.

## Releases, version & install

- **Tags are immutable**: `git ls-remote --tags origin` + `gh release view vX.Y.Z` first; bump
  `internal/version/version.go` (`vX.Y.Z`) + both `web/package*.json`; steps in `HOWTO.md`.
- **A red Linux build job blocks the release** while CI/Pages/Docker stay green: fix = top-level
  `ZIG_LOCAL_CACHE_DIR: /tmp/zig-cache`.
- **macOS re-sign**: in-place replace keeps a stale signature → `Killed: 9`; `make install` `rm -f`s first
  then ad-hoc signs; it restarts nothing (check the listening pid's start time — `make` echoes its recipe,
  add `-s`). The installed file runs ~18 KB larger than `./ogcode` — the embedded ad-hoc signature, not
  divergent content (`spctl` "rejected" is normal).
- **After a code fix, `make -s install …` BEFORE handing over a restart** (2026-10-25): the relaunch runs
  `~/.local/bin/ogcode` (PATH), not the repo `./ogcode` — the restart can re-run a pre-fix copy. Verify the
  install carries the change: `strings ~/.local/bin/ogcode | grep -c <new-symbol>` ≥1; after restart, the
  listener's pid actually changed (ps start time).
- **`detectInstallCommand()`** matches scoop → Homebrew → cargo → install.ps1 dir → winget → install.sh;
  Homebrew means `/Cellar/` or `/opt/homebrew/`, **never any `/usr/local/`** (`install_detect_test.go`);
  `check-updates` exits 1 when an update is available; verify install changes on a throwaway rig (fresh HOME
  + temp cwd + stub `open`), expanding the binary path BEFORE exporting HOME.

## Build & dev loop

- `make build` = `build-web` (npm build → `web/dist`) then `build-server` (`CGO_ENABLED=1 go build -o ./ogcode .`;
  repo-root `main.go`). `make dev` :9595; `make dev-web` Vite :5173.
- **`CGO_ENABLED=1` mandatory** (cgo Swift grammar); `make test` omits it. OGX-signed build:
  `make build-server OGX_APP_SECRET=$(tr -d '\n' < ~/.ogcode/ogx-app-secret)` — else the gateway 401s.
- `ogcode version` (no `--version`); dirty builds print `…-dirty`; no linter, gofmt files touched.
- **`ogcode run --max-turns` defaults to `0` = no limit** (→ `MaxStepsUncapped = -1` → `math.MaxInt`). **A
  literal `MaxSteps == 0` still means the 1000-step default** — a zero-value `LoopRunner` depends on it. The
  server loop (10000, 2 auto-resumes) and `ogcode index` (50) stay capped (`step_budget_test.go`).

## Deployment & first boot

- **A project keeps its port**: `~/.ogcode/ports.json`; a bare `ogcode` reuses it (this project → **7801**),
  explicit `--port` overwrites it; `make -s install OGX_APP_SECRET=$(tr -d '\n' < ~/.ogcode/ogx-app-secret)`
  works. **Never restart the host server unasked** — it runs the live session (killing it ends the turn
  mid-reply); `ask_user` and hand over `kill <pid>; cd <dir> && ogcode` (no `--port`).
- **One server per project dir**: advisory flock on `<dir>/.ogcode/server.lock` before the DB opens
  (`projectlock.go`); a second `serve` exits 1 naming the holder. **`port free` ≠ dead** (2026-10-25): a
  SIGTERM'd server frees the listener before the flock, so a relaunch off a port check exits 1 `already
  running …` — wait for the old PID to EXIT, not the port.
- **Listener before preflight** (133 MB embedder, after bind); `/api/config` GET JSON 200 is the health probe.
  **End-user macOS signing is NONE**: ad-hoc, runs via `com.apple.provenance` (not quarantine); `spctl`
  "rejected" is not "blocked" without that xattr.
- **Docker**: install `git` (else every workspace says "not a git repository"); `HEALTHCHECK … /api/config
  --start-period=120s`; `docker.yml` needs `type=raw,value=latest`. **ldflags traps**: `-X` on a missing
  symbol is silent; an empty `-X` does not fall back.
## OGX gateway

**Assertion**: base64url HMAC-SHA256 over `app + "\n" + ts + "\n" + method + "\n" + path` (skew 5 min) in
`X-Client-App`/`-Timestamp`/`-Signature`. **Gate is CONDITIONAL**: no secrets ⇒ open, any secret ⇒ any failure
⇒ 401. ogcode `appassertion.go`; oglab `internal/httpx/httpx.go`, secrets from `OGLAB_APP_SECRETS`.

- **Secret**: `OGXProviderID = "ogx"`, `OGXAppID = "ogcode"`, `var OGXAppSecret` (ldflags); one live value
  (never paste) — `~/.ogcode/ogx-app-secret` (600). **`tr -d '\n'` mandatory** (unstripped HMAC → silent 401).
- **Provider**: `https://ogx.ogcode.in/v1`; **`/v1/models` IS the plan** — dynamic models, all
  `ActiveByDefault`, **no static fallback**. **Registration is plan-gated, network-free** (`HasPlan()`).
  **Priority** `anthropic, openai, openrouter, ollama, ogx` (ogx last on purpose). **`prompt_cache_key`** is the
  session identity for token spend, not a cache hint.
- **Bodies cap at 4 MiB** (`checkBodyFits`, `ErrRequestTooLarge`); oglab's `maxChatBodyBytes` matches, both
  fatal. Raising it is three hops (oglab router, web `maxChatBytes`, nginx) — ship the gateway first.
- **Composer images**: `prepareImageAttachment` → **1024 px long edge**, JPEG q0.8 / WebP, byte-identical
  passthrough ≤512 KB AND ≤1024 px; `MAX_IMAGE_SIZE` (10 MB) is only the pick limit.
- **JSON endpoint returning HTML = nginx routing bug** (no `/v1/` location ⇒ all to `oglab_web`); fix with
  `upstream oglab_router` + `location /v1/` before `location /`. A 15s SSE keep-alive stops Cloudflare's ~100s
  idle cut and stops after 90s of upstream silence.
- **Settings tab** is `readOnly`; its not-connected fallback div needs `data-setting` or it vanishes offline.
  **oglab's** breaker ignores `400`/`422`.

## Providers, models & resolution

- **`OpenAIProvider.Models()` is a pure read** — never network; fallback until `SetCatalog`, returns a copy; fetching is `RefreshCatalog` behind `CatalogRefresher`/`CatalogSetter` (`TestModelsNeverFetches`).
- **Resolution**: `orderedSnapshot()` sorts by `(providerRank, id)`; `ResolveProviderFor` = custom → explicit provider → catalogue (`ActiveByDefault`) → highest priority; persisted as `provider` (migration 047). **Trap**: a column in a `SELECT` but not in `rows.Scan` is a silent runtime error. **Catalogue names are aliases** — `normalizeModelID` strips org prefix, `:latest`/`:cloud`, OpenRouter variant suffix (first-claim-wins); `lookupModel` compares the raw `m.ID`, so a bare id beats a prefixed row. `gemini-4-argon`: `RejectsSampling`, `MaxOutputTokens: 32_768`.
- **`ogx-deepseek-v4.1-flash` THINKS BY DEFAULT** (2026-10-24): no `reasoning_effort` ⇒ the endpoint streams/bills `reasoning_content` inside completion; the loop sets `Thinking:true` always but `applyEffort` (openai_effort.go) writes nothing unless session effort ≠ "" — unset = endpoint default = ON; `--effort none` suppresses.
- **flash-class effort default (FIXED 2026-10-24/25)**: `defaultEffortFor(modelID, spec)` maps flash-class ids to `low` when effort is "", unless a spec default LIGHTER than high stands (gemini-2.5-flash-lite `none`, flash-lite siblings `minimal`, gemini-3.8-flash `medium`); a recorded default ≥high keeps the low hold (ogx-deepseek-v4.1-flash = EffortHigh); non-flash keeps "". Pins `openai_effort_flash_test.go` + `effort_mechanical_test.go`.
- **MECHANICAL CONTINUATION hold** (`StreamRequest.StepMechanical`, set at the loop tail): after a tool-only step with ≥1 WORK call, default-derived effort drops to `none` (fallback `low` when the ladder lacks none); read-only-exploration rounds are exempt; applies ONLY to default-derived levels — an explicit user effort is NEVER rewritten.
- **Bench parity (2026-10-24)**: BOTH arms think (neither client sends a reasoning field — `reasoning: 0` is an accounting artifact; ogcode folds reasoning into `output`) and the `parity` pair is NOT matched (only the ogcode arm ran `--effort none`). Probe/wire numbers: `HISTORY.md` 2026-10-24.
- **ogcode.json custom OpenAI-compatible providers** (2026-10-24): extra ids beyond the four slots + `ogx` register via `config.CustomProviders()` → `provider.NewOpenAICompatibleProvider(id, apiKey, baseURL, model, label)` (collection = `label` else `collectionFromBaseURL`), wired in `cli/run.go`/`cli/root.go`/`server.go loadProviderMap` so `reloadProviders` picks up edits. Entry: `type` only ""/"openai", required `baseUrl`, optional `model`/`label`; reserved ids and non-slugs refused; `disabled` merges ONE-WAY. **Trap**: `Load` must copy EVERY new ProviderConfig field (it once dropped `Type` silently) — pin `TestLoadProviderMapRegistersCustomProviders`. UI needs nothing (picker groups by `collection`).
- **The Ollama slot lists only what the local endpoint serves**; free pool REMOVED (`freepool.go`), `needsOnboarding` hardcoded false.
- **Budget**: `OGCODE_PROVIDER_MAX_CONCURRENT` 8, reserve 2; index takes `indexGate` then `total`, interactive only `total`; `streamEvents` defer order `close(ch); body.Close(); cancel()` (a slot held past the close 429'd OGX's concurrency-1 tier).
- **`write tcp …: result too large` is ERANGE from the LOCAL `write(2)`** (VPN/content filter), not the gateway; `sendStreamingRequest` retries once on a fresh connection; a mid-stream RST only when no text and no tool call exists; IPv6 fallback counts ECONNRESET too, two strikes narrow to `tcp4`.
- **Connections: ONE pooled h2 conn to ogx (`streamHTTPClient`); h1.1 sockets DRAINED, not dropped**: no `DisableKeepAlives`, `ForceAttemptHTTP2: true`, `IdleConnTimeout: 30s`; `drainFinishedStream` runs at the END of `streamEvents` gated on `sawDone` ONLY (never on the `scanner.Err()` path; budget 2 s / 64 KiB) — Anthropic needs nothing (reads to EOF). A test observing the discard needs a ~20–50 ms gap before the response end, else the socket reuses by luck and passes against the unfixed code. Pins `internal/provider/stream_drain_test.go`. Bench tap pins ALPN http/1.1 — per-run req/conn: `HISTORY.md` 2026-10-24.

## Token accounting

- **Reasoning is billed INSIDE `output`, never separately**: `ModelPrice.Cost` takes no reasoning term (`internal/provider/pricing.go`), `TokenCounts.Consumed/Effective` leave it out to avoid double-counting (`internal/session/message.go`), and `usageFromOAI` folds Gemini's total-only thinking into output. It is a display count only — output is ~1% of all tokens, so cache-hit rate, not effort, is the spend lever.
- `tokens.input` is UNCACHED: bill `input + cacheRead`; a cache hit is `prompt_tokens_details.cached_tokens`
  when > 0, else `prompt_cache_hit_tokens`. The pill totals the transcript from `GET /api/session/{id}/token`.
- **Title generation inherits the session model** — with `model == ""` it skips `ResolveProviderFor`, so a test
  must set `defaultProvider`. **Gemini `thought_signature` must be echoed verbatim** from `extra_content.google`
  on every replayed tool call or the second turn 400s (`EchoesToolExtra()`).
- **Teammate spend is counted, but only on the child**: a teammate is a real `build` session (`board.go`
  `spawnTeammate` → `startSessionLoop`), so its steps land on its own messages + `usage_event` (kind=step) and
  its utility calls on `session.utility_*` — same project bucket (one ledger per workspace, `server.go`
  `NewLedger(global, s.dir)`), so Settings→Usage totals include it. `foldChildUsage` (`agent/usage_charge.go`)
  is for EPHEMERAL task/memory children only; a teammate is never deleted or folded (a fold would double it in
  session views).
- **The parent's token pill folds teammates in on the CLIENT, never in the DB**: `/session/{id}/token` and
  `/usage` each carry a `teammates` array (always `[]`) beside session-only headline totals; `token-pill.tsx`
  sums them into its figures — input/output/cache AND `utility` (a subtotal inside effective/total, never
  added on top). A teammate child whose read ERRORS is skipped per-child with a warn (parent-side reads still
  500); `spawnedTeammates` (`server/board.go`) enumerates via `board.BySession` + roster
  `KindTeammate && SpawnedBy == parent.Handle` — no parent column on `session`; a deleted child is skipped
  while its roster row stays. Pinned by `session_token_test.go` / `usage_routes_test.go`
  (`TestSessionTokensSkipsATeammateWhoseReadErrors`).
- **Ollama Cloud meter**: `GET https://ollama.com/api/usage` with `OLLAMA_API_KEY`.

## Permissions

- **The ruleset gates, not the mode**: `Ruleset.Evaluate` runs BEFORE the mode switch; only read/glob/grep are `Allow` by default (bash/write/edit `Ask`), and the catch-all rule `Allow`s every other tool in EVERY mode. **Yolo only changes what would have been Ask** (skips the classifier) — an explicit `Deny` still denies; `isDangerousCommand` is a separate layer. **No Deny tier from the classifier** (Safe/Ask/Unclear only). PATCHing a session's mode ALSO `SetDefaultMode`s machine-wide. **Grants never expire**; the pattern is the verbatim string (`./f.go` ≠ `/abs/f.go`), not a glob.
- **`riskLLMMaxTokens = 1024`** is calibrated; an unanswered check warns, returns Ask and is not cached. Ask = `rm`/`sudo`/`git push`; Safe = read/build/test; the rest (incl. `curl`, unknown) = Unclear, the only tier reaching the LLM.
- **A teammate's Ask and `ask_user` reach the HUMAN in the PARENT's composer, never the parent agent** (2026-10-23) — the mirror is SERVER-side, so a reload restores it too: the child's loop is gated exactly like a parent's on the SAME shared `permission.Manager`; `LoopRunner.ParentSessionOf` climbs `SpawnedBy` (depth 32) so `requestPermission` and `mirrorQuestion` republish with `sessionId` = ROOT session, `ownerSessionId` = the child, `teammate` = its handle; `GET /session/{id}/permission|question` BFS-append the SUBTREE's pending items; a reply by id clears EVERY queue holding it — path session, owner, owner's root (`TestReplyingFromTheChildPageClearsTheParentQueue`); pages beyond those three keep a stale copy until their reply 404s or they reload (parity hole, accepted). Client: mirrored entries file under the PARENT keyed `ownerSessionId || sessionId`, replies post to the owner (chip in `permission-prompt.tsx`/`ask-user-dialog.tsx`). `teammatePrompt` directs a blocked child to ASK rather than end early; the child blocks until the human answers (no timeout — the dialog is answerable live). Pins: `TestTeammateQuestionMirrorsToTheParentSession` (+cancel/+silent, `question_mirror_test.go`), `TestParentSessionListsTeammateQuestions`, `TestReplyingAQuestionFromTheParentUnblocksTheTeammate`.
- **Grants and mode are machine-wide** in `~/.ogcode/config.db` (migration 048); grain is the exact command/path/skill, never `"*"`. **Trap**: `serve_lifecycle_test.go` does not set HOME, so `go test ./...` advances the real config DB — isolate HOME and the working dir.

## Agent memory files

- **Caps: none by default** (`OGCODE_MEMORY_MD_MAX_BYTES`/`OGCODE_AGENT_MD_MAX_BYTES`; `limit <= 0` is
  uncapped) — a positive budget is head-biased, so curate instead. `staticSystemPrompt` carries **one constant
  pointer per file, never content or a heading index**; turn digests carry tool names and the final response
  only, so procedures go to `HOWTO.md`, not the scribe.

## Prompt caching, read pressure & context windows

- Anthropic `cache_control` on the last tool def and first system block (min 1024 tokens, 4096 on Opus 4.5+,
  5-min TTL); OpenAI automatic. **Entry [0] stays byte-identical** — the date rides `systemReminderPrompt`.
- Read pressure: `OGCODE_READ_PRESSURE_THRESHOLD_TOKENS` 40000; `OGCODE_RESEND_COST_WINDOW_MULTIPLE` 1.
- **History is TURN-SCOPED**: only `sliceTurn(messages, findLastTextUserMessageIndex(...))` is sent, so a prior
  turn's images never re-send; within a turn images repeat and ride the last-message cache breakpoint.
- **The turn's prompt and images survive compaction, tool images do not** — name them in the summary or
  re-read. **The ask_user Q&A must survive verbatim** on all three surfaces.
- `OGCODE_COMPACT_CONTEXT`: unset/empty = ON, falsey = off. `maxCompactions = 2`, `compactionKeepRecent = 12`,
  unknown window → 128000. **Learned windows** are kept only when `learned > promptTokens`.

## Agent tools & prompt

- **`schedule` tool = one-shot agent timers** (2026-10-24): ops `create` (delaySeconds OR strictly-future RFC3339 `fireAt` + required `note`)/`list`/`cancel` in `internal/tool/schedule.go`; refusals are Result-shaped, not errors. Server store `internal/timer` (migration `068`, table `agent_timer`, statuses pending→due→delivered/cancelled; `ClaimDue` is one atomic `UPDATE … RETURNING` so two claimers can never double-claim). Ticker `pollReminders` (10s, `internal/server/reminder.go`) claims due rows and delivers via the board-report rails: `PushNotice` Cause `"reminder"` + `TrackBoardReport` with mark=`MarkTimerDelivered` (byte-identical Text in both). Idle session → row stays `due`; `CatchUpReminders` hook (LoopRunner field, build-gated beside `CatchUpTeammates`) delivers at the next turn start of THAT session — never starts a turn; nobody holding the loop → delivery logs and leaves the row due; a server restart keeps pending timers in the DB and due-but-undelivered ones arrive via catch-up (at-least-once). **Offered only to BuildAgent** (FIXED 2026-10-25): `schedule` is on `buildAgentTools` (`internal/agent/agent.go`) gated by `canSchedule()`, `agent.ID == "build"`, beside `canUseBoard`/`canSpawnTeammate` — TaskAgent/subagent and every other non-build agent never see it. Pins: `TestScheduleIsOnlyTheBuildAgent` + `TestScheduleLeftCodingAgentToolsShared` (`internal/agent/schedule_gate_test.go`); `TestBuildVsTaskAgent_Framing` pins build = task + exactly `schedule`.
- **`update_intent` rides the first work block** (2026-10-24): state the plan before the first meaningful
  exploration, and it MAY ride the first tool block alongside other calls; a closing status update is welcome
  but NOT mandatory. `intent_gate.go` appends `intentGateReminder` ONCE PER SESSION when no plan is set
  (process-lifetime registry keyed by sessionID, deleted by `markPlanSet` — a session that planned once re-arms
  for a later planless stint). Whole-list replace of 1–7 steps, persisted in `session.intent` (migration 064);
  a closed plan never invites another call. **Descent gate** (`descent_gate.go`): a `grep`/`glob` landing
  before any `codebase_map(subdir=…)`/`read`/`file_map` gets `descentGateReminder`, once per turn, armed only
  when `indexedFiles > 0`; a root map is not a descent.
- **Matcher**: ONE routing completion per block for a newly added step, **no cap**; scores skills + one entry
  per MCP SERVER; failures silent. **A step with no `description`, or not pending/active, is never routed.**
- **Chip identity is the step LABEL, not its description**; counts persist through `done`/`dropped` until the
  step leaves the list or the next `RunLoop` clears. **Nothing closes the plan at turn end**, so
  `updateIntentPrompt()`/`Description()`/the `status` schema demand STATUS movement, not a restatement.
  **Status moves land mid-turn**: active before a step's first block, done after its last, not banked for the
  closing call. Pins: `update_intent_test.go` `TestUpdateIntentDescriptionMakesTheClosingOptional` +
  `TestUpdateIntentDescriptionMayRideTheFirstBlock`; `intent_gate_test.go`
  `TestIntentGateFiresOncePerSessionAcrossTurns`.
- **The build agent has no upfront skill or MCP list** (`turnTools` withholds the `mcp_*` glob), and a bare
  `skill` call is refused in a matcher session. **Rich output** is fenced declarative specs (mermaid,
  plotly/rough, LaTeX, sandboxed HTML); `motion` declined; `## Decisions & why` only for real decisions.
- **`read` takes `files=[{path,ranges,offset,limit}]`** (2026-10-24): exclusive with top-level `path` (neither /
  both refused); each entry is exactly the single-file shape; per-file error sections and `== path ==` headings
  relative to SessionDir; every entry rendered whole; bookmark stamped per CONTENT section only — never for a
  map, listing, binary or error — captured before ReadFile. Pins `TestReadTool_FilesStampsEveryContentRead`,
  `TestReadTool_FilesMapEntryDoesNotStamp`, `TestReadTool_FilesReturnsEveryFile` (`read_files_test.go`).
- **`read` and `file_map` are UNCAPPED by design** (2026-10-25, the developer's call): no byte/line/per-line
  limit, no shared call budget, and **no default window** — a bare read returns the whole file and a range
  without `end_line` runs to EOF; `Truncated` only means "a paging window left lines unread". **A bare `limit`
  of ANY size is a window past the map** (`rangeRequested` = ranges || offset>0 || limit>0 in `read.go`;
  negative offset/limit refused). Both set **`Result.Uncapped`** (`json:"-"` in `tool.go`), so `capToolOutput`
  (`internal/agent/loop.go`) returns early on `Truncated || Uncapped` — the 50 KB loop backstop still bounds
  bash/grep/glob/MCP but never these two. **The one bound left on a read is ogx's 4 MiB request body**
  (`ogxMaxRequestBytes`, `checkBodyFits` → `ErrRequestTooLarge`, fatal); `read_pressure.go` (40000 tokens) is
  advisory. Kept: the map interception itself (`mapInsteadOfWholeFile`, the 200-line `rangedReadThreshold`).
  Pins `TestReadTool_MapIsReturnedWhole`, `TestReadTool_NoDefaultWindow`,
  `TestReadTool_BareLimitPagesContentPastTheThreshold`, `TestReadTool_NegativeWindowArgsAreRefused`,
  `TestCapToolOutputHonoursUncapped`.
- **The prompts make `files=[...]` the primary read shape** (2026-10-25): ONE `files` call for several files
  in the exploration diagram, build step 2 and `read`'s `Description()`; the map section adds a **grep fast
  path** (a search that printed `path:line: text` has already located the code — map only files whose shape is
  unknown) and the **one read call per wave** rule. Pins: `TestWorkflowLetsASearchStandInForTheMap`,
  `TestWorkflowSpendsOneReadCallPerWave`, `TestProcessStepTwoNamesTheBatchedCalls`,
  `TestReadDescription_LeadsWithTheMultiFileShape`, `TestFileMapDescription_BatchesAndSkipsANeedlessMap`.
- **Tool contracts the schemas do not show**: a blank ask_user answer to a content question is unanswered;
  **pathlock** forbids write-vs-edit on one file but allows batched edits; **codemap** is one level per call
  with **no size threshold** (the old <10-file expansion was deleted — `project_index.go`), `dirPrefixFilter` needs `ESCAPE '\'`, **HCL is outlineOnly**, and a Swift `as?` + newline
  + `??` misparses.
- **Images** are stripped for non-vision models (`IsImageRejection` is resumable); MCP images go to a temp
  file. **Skills** `requires:` env names are filled by `skills.env` via `setIfUnset`.

## MCP & search

- **`OGCODE_SEARCH_ENABLED=false` removes `web_search`, `fetch_page` AND `deep_search`**; to suppress only
  deep_search, add a prompt clause.
- **MCP**: transport is INFERRED (`Command` ⇒ stdio, `URL` ⇒ streamable-http; `"transport"` overrides) — **no
  `type` key**. Tool ids `mcp_<server>/<name>`; connect is lazy. A URL server with no `headers` gets OAuth
  (`~/.ogcode/mcp-tokens/`); `headers` means static bearer. Penpot's `…/mcp/stream` needs streamable-http.
- **Search** results carry a `Provider` stamp set before the TTL cache. Config is `/settings/search`, a
  FULL-STRUCT replace; `""` = HTTP first, `safari`/`chrome` = browser first (`chrome` on macOS means Safari).
  macOS prefers headless Chromium with a Safari fallback; Windows Chrome/Edge is unverified — check with
  `GOOS=windows CGO_ENABLED=0 go build ./internal/search/`.

## Data, migrations & transcript

- **Migrations**: goose keys by VERSION NUMBER only, so a number taken by a branch that never landed silently
  SKIPS your file. Check `git log --all -- internal/db/0NN*` AND the real `goose_db_version`. Next free number
  = next integer past the highest file.
- **The transcript is append-only**; the only bound is `capToolOutput` (50 KB; skipped by `Result.Truncated`
  and `Result.Uncapped`);
  the only deletes are `Store.Delete`/`DeleteMessage`. Compaction changes what is re-sent, never what is stored.
  **Nothing prunes and nothing vacuums**: `db.go` sets only WAL/foreign_keys/busy_timeout — no `auto_vacuum`,
  no `VACUUM` anywhere in the repo — so deleting sessions only fills the freelist and the file never shrinks;
  reclaiming needs an explicit `VACUUM` (~DB size in temp space). Measured shape: `HISTORY.md` 2026-10-24.
- **FK 787 mid-turn ⇒ another writer deleted the session**: `handleDeleteSession` cancels and waits up to 5s
  before `store.Delete`; `Store.Get` returns `(nil, nil)`; `classifyInterruption` maps it and
  `ErrRequestTooLarge` to `InterruptFatal`.
- **Session DB**: `.ogcode/ogcode.db` / `~/.ogcode/config.db`; `message` is metadata, content in `part.data`.
  **`GetMessages` must return `Parts` as `[]`**; pagination is `?before=<id>` + `X-Has-Older` at 300 rows.

## File change gate

- **Migration 066 + `internal/filechange`**: a `file_change` history row per `write`/`edit`/`bash` plus a
  per-(session,file) bookmark (`seen` revision); history is ring-capped at `RingPerFile` (50) per file; read API
  `GET /api/file-changes` (`file_change_routes.go`, 503 when `s.fileChanges == nil`) — **no web client calls it
  yet** (`plans/CONFLICT_FREE_MUTATIONS_PLAN.md:319`), so the stored `diff` has one live consumer: the gate's own
  post-restart refusal (`log.go:287` `covered` false ⇒ `History`), the ring being memory-only.
- **The gate refuses in `write`/`edit`** (`internal/tool/staleness.go` `staleRefusal`): a file changed by
  ANOTHER session since this session last read it refuses, and the refusal is a `Result` (not an error) that
  shows the diff — same shape as the board-claim refusal. `Stale` drops the caller's own changes and refuses
  nothing when `Seen < 0`; **`Seen` returns -1, not 0**, for never-read (0 is a real revision), pinned by
  `TestBookmarkAtRevisionZeroStillRefuses`. **The ring explains nothing when it is empty** (a restart):
  `covered := len(ring) > 0 && ring[0].Rev <= seen+1` — an empty ring always falls through to the `History`
  query (pin `TestRestartRefusesABookmarkOfRevisionZero`).
- **`bash` is COVERED, by fingerprint not by parsing** (`internal/tool/bash_observe.go` `observeShell`, end of
  `BashTool.Execute`): after every command it stats each file the log already tracks
  (`Log.Files()`/`Fingerprint{Size,MTime}`/`FingerprintOf`) and records a change (tool `bash`, note = the
  command, `DiffOmitted`) for any that moved — a deletion included (missing file = zero fingerprint).
  **Unattributed (no `Session`), so no bookmark moves** — the session that ran the command is refused by its
  own shell until it re-reads. Nothing auto-claims; an **untracked** file is skipped. Pins
  `TestOwnShellChangeRefusesUntilReRead`, `TestUnrelatedCommandDoesNotLaunderAForeignChange`.
- **`description` is required on `write`/`edit` now** — it is the history note; a bash row's note is the command.
- **A nil `FileLog` is a no-op** (`filechange.NewLog(ctx, nil)` still gates from memory), so tests and other
  embedders need not wire the store; `observeShell` too.
- **Remaining blind spots**: MCP tools, `latex_to_pdf`'s `.tex` write, and a change made outside the agent
  until the next `bash` call stats it. `file_map` is NOT a read (a summary would suppress the diff exactly when
  it is wanted). A **map-only read does NOT stamp the bookmark** (`read.go`; pin `TestMapOnlyReadDoesNotCertifyContent`)
  — but a first-contact map-only read leaves the file untracked, so bash observation records nothing until a
  content read.
- **It FAILS OPEN when the history row is missing**: with no change to show there is no catch-up to explain a
  refusal, so the write proceeds (warn-logged).
- **Cross-process staleness appears only after a restart**: bookmarks and fingerprints are in-process memory,
  not re-read per mutation — `serve` + a separate `run` on one dir can each miss the other's changes while both
  are up.

## Web UI: composer, sidebar & navigation

- **Composer drafts/attachments are per session**; **the running pause glyph IS the stop control**; the `+`
  attach menu renders in a `Portal`. **The plan-mode flag gates only the warning banner**; mode comes from
  `GET /api/mode`. **The session-row dot reports task status** (`session.status`), not the provider, and a
  hand-seeded session row must set `permission` or `Store.List` 500s. **`⌘K` is guarded on `offsetParent`**
  (the shell renders twice).
- **Virtualization and scroll memory** in `message-list.tsx`/`scroll-memory.ts`: pad-based windowing keyed by
  message id, rows estimated from running per-role totals over measured rows (remount ~1,000×/s); the
  id-change effect must call `restore()`, and ResizeObserver must not `holdAnchor` when `anchoredByList`.

## Theme, contrast & light mode

- **Light mode is server-global**: `GET/POST /api/appearance` off **`s.globalDB`** — a throwaway rig flips the
  USER's machine-wide mode, so save before an audit and restore after. `web/index.html` pre-paints from
  `localStorage['ogcode-theme-mode']`; Settings General is `/settings`. **Muted-text floor: 4.5:1 on the
  LIGHTEST surface the token can sit on** (`--bg-hover`); exact values `HISTORY.md`. Default accent is brand
  orange `#ff5e1f`; **accent ink is its own per-mode token**, never `--accent`; **`onPrimary` is `labelFor`**
  (max of the minimum ratio over `--accent` AND `--accent-hover`), recomputed on read.

## Preview, public serving & device panel

- **The Preview page lists only announced ports** (`<port>.<PreviewDomain()>`); `recordAnnouncedPorts` reads
  **PartText only** at turn end for the build agent; manually `?ports=`. `previewHostDispatch` strips
  `X-Frame-Options` + CSP `frame-ancestors` or a frame-guarded app renders blank.
- `/public` serves `<data-dir>/public` with **HEAD mapped explicitly**. **scrcpy** deep links need
  `action`/`udid`/`player`/`ws`; **use `resolvedUdid()`**, never raw `udid()`.

## Browser takeover (bridge + extension)

- **Option B (2026-10-25)**: the agent drives the user's real logged-in Chrome via a first-party MV3 extension in `browser-extension/` (vanilla JS) paired over a WS bridge — pieces `internal/browser/bridge.go` (Dispatch/pending-map over coder/websocket), `internal/tool/browser.go` (11 tools), `internal/server/browser_routes.go`, skill `browser-takeover`. Registered every turn like board (unpaired ⇒ Result-shaped pairing refusals); screenshot rides `Result.Image`, refused when `!ModelSupportsImages`; ops budget 30s (60s navigate/screenshot).
- **Endpoints**: `GET /api/browser/pair` → `{token, url}`, POST rotates (token persisted, below); `/api/browser/status` always 200; `/api/browser/bridge?token=…` accepts only loopback + chrome-extension origin + token.
- **Bridge traps**: OriginPatterns match the BARE origin host unless the pattern contains `://` — use `"chrome-extension://*"`, never `"chrome-extension:*"`; `failPending` ships the ErrNotConnected sentinel as a plain string, so `Dispatch` maps `res.Err == ErrNotConnected.Error()` back to it (errors.Is must keep holding); hello `version` is a bare JSON NUMBER — a string field unmarshal-errors and hello drops silently (extension stuck "connecting", no error either side).
- **FIXED 2026-10-25 — the 32 KiB websocket read limit killed every snapshot/screenshot** (reply frames run hundreds of KB ⇒ `message too big` dropped the conn): `maxResultFrameBytes = 4<<20` + `conn.SetReadLimit(maxResultFrameBytes)` in `bridge.go` `Serve`; pin `TestBridgeAcceptsAResultFarPastTheOldDefaultLimit`.
- **navigate `newTab` opens an auto-granted tab**: exemption = navigate + newTab + NO tab id (`grantedIds` persists to `grantedTabs`, revocable in the panel); newTab WITH a tab id means "act on that tab" (gate applies). Omitted `tab` defaults to the last Granted tab; tabs-but-none-granted ⇒ Result-shaped refusal; empty inventory passes nil. Pin `TestBrowserNavigateNewTabNeedsNoTabAtAll` (browser_test.go).
- **Watch capture (`browser_network`/`browser_console`)**: the FIRST op on a GRANTED tab attaches a persistent chrome.debugger session (`ensureWatch`) and fills per-tab rings — network 300, console 300, evict-oldest, emptied by an SW restart (re-arms on the next op). network lists last-N (default 100) with a `url` filter; `requestId` fetches the body (cap 65536, `bodyTruncated`); console filters level/source exactly. Drop hooks: ungrant (storage.onChanged), tab close, debugger onDetach (next op re-attaches). **CDP timestamps are epoch SECONDS** (`cdpIso` ×1000); `Log.entryAdded`/`startedAt` have no CDP ts → wall-clock — don't unify. Extension logic is pinned only by the throwaway vm harness.
- **Pair token survives restarts (FIXED 2026-10-25)**: persisted at `.ogcode/browser-pair-token` (0600) — boot `browser.NewWithToken(loadBrowserPairToken(s.dir))`, re-saved on rotate; empty/malformed ⇒ fresh mint (pins `TestNewWithTokenPinsThePersistedToken`/`TestBrowserPairTokenSurvivesARestart`/`TestBrowserPairRotatePersistsTheToken`). `GET /api/browser/pair` returns the token WITHOUT rotating; POST rotates.
- **Offered build-only (FIXED 2026-10-25)**: `canControlBrowser()` (agent.go) + `browserOffered := agent.canControlBrowser() && lr.Tools.Get("browser_status") != nil` appending `browser_*` to the loop allowlist (CLI-safe: its registry has no bridge). Pin `internal/agent/browser_gate_test.go`.
- **A heavy op (big-DOM snapshot, screenshot) KILLS the extension's service worker, not pairing — don't re-pair on "extension not paired"** (2026-10-25): the op drops the ws ~0.1–0.6 s after dispatch, the extension reconnects ~1.1 s later and `swapConn`/`failPending(ErrNotConnected)` surfaces the in-flight op as the tool's misleading `unpaired()` refusal; light ops and real op errors never drop the conn. Wart: `dropConn` comments it fails in-flight ops but doesn't call failPending; fix candidates (unimplemented): `chrome.tabs.captureVisibleTab` screenshot, bridge retry-once on the sentinel. Evidence: `HISTORY.md` 2026-10-25. The panel's "Reconnecting — retrying in Ns" is `scheduleReconnect`'s normal backoff text for 1–2 rounds after any drop; if it sits unchanged the SW died mid-retry — reopen the side panel (its `get-status` wakes it).
- **CWS kit (2026-10-25)**: icons `browser-extension/icons/icon{16,48,128}.png`, manifest got `icons` + `action.default_icon`; listing copy + justifications in `browser-extension/STORE_LISTING.md`; privacy page `https://ogcode.in/docs/privacy/` (deploy via docs/DEPLOY.md before the store form). Zip: `make extension-zip` → `ogweb-<ver>.zip`, manifest at ZIP ROOT. **`host_permissions: ["<all_urls>"]` DROPPED — nothing uses it; NEVER re-add** (verified-by-inspection only). chrome.debugger draws extra CWS review; bump manifest version before every re-upload.

**Native-messaging host = the zero-paste pair path** (`com.ogcode`, 2026-10-25): `ogcode native-host`
(stdio host, 4-byte-LE framing; `{"type":"pair"}` → `{"ok",\"version",\"servers":[{origin,url,token,port,project}]}`
— registry = `portmap.All()`, each candidate probed `GET /api/browser/pair` 750 ms, sorted port-then-project,
stale entries filtered); `ogcode native-host install [--extension-id …]` / `uninstall`; boot auto-installs
idempotently via `nativehost.EnsureRegistered(projectDir)` (server.go, warn-only) writing `com.ogcode.json`
into every browser dir whose PARENT exists (mac: Chrome/Chromium/Brave/Edge/Vivaldi/Arc). **Extension id is
PATH-DERIVED** (`ExtensionIDForPath` = Chromium GenerateIdForPath: sha256 of the LOWERCASED abs path, nibbles
→ a..p): repo `browser-extension/` ⇒ `cgeoedmgjadadineklkmhgpmolmnpfbn` — loading the same code from a
different dir ⇒ different id ⇒ Chrome refuses to launch the host. Extra ids: `--extension-id` flag and
`OGCODE_EXTENSION_IDS`; `WebStoreExtensionID=""` reserved until CWS publish. **Extension pair leg** (v1.1.0,
+`nativeMessaging`): `background.js` `autoPairThenConnect` fires ONCE per worker life (5 s) when credentials
are absent — success writes `{serverOrigin, pairToken, bgConnected:true}` then connects like manual Connect;
failure ⇒ unconfigured status "no ogcode app — paste token to pair", NEVER retries/overwrites. Chrome reads
native-messaging manifests at browser STARTUP — a first-ever com.ogcode.json needs one Chrome restart.
Pins: `internal/nativehost/*_test.go`, `portmap/all_test.go`, `cli/native_host_test.go`, harness
`browser-extension/native_pair.test.js` (34 checks).

## Indexing & the document index

- Auto-index is gated by `OGCODE_AUTO_INDEX_MAX_FILES` (500, env-only) + consent (migration 061); **manual
  builds are never gated**. The post-turn guard is `agentID == "build"` **and** `Directory == ProjectID`;
  `OGCODE_AUTO_INDEX` is the kill switch. It must run on the turn's resolved model AND provider.
- **Labels are LLM-made, not embeddings**: `IndexAgent` (one tool, `submit_doc_index`) labels a page from a
  local keyword corpus (`text.go`/`pdf.go`); there is no vector store anywhere in `internal/indexer`.
- **`.ogcode/` is skipped unconditionally**; **39** default excludes match directory names and basenames
  anywhere (`excludes.go`, pinned by `excludes_test.go`); `doc_page_index.mod_time` drives the refresh.

## Controlplane, remote workers & Incus

- **Nested module** `controlplane/`: one in-process `server.Server` per worktree on the worker daemon.
  **`HostSession` must `store.Get` first** (`Store.Create` violates UNIQUE on re-delivery); routes key
  `<workerID>-<route>`, lookup **exact whole-label**. **bbolt**: a byte-truncated file SEGFAULTS at `bolt.Open`
  (`checkDBFile` validates the meta page), a restore must set `status=offline` + `LastSeen=boot time` or
  `ReapStale` kills every worker, and it is **single-writer**. Workers, accounts, Incus: `HISTORY.md`.

## Infrastructure, feature flags & telemetry

- **Homepage is `docs/index.html`** (hand-written; nav `.nav-menu`, mobile `#navSheet` — add links to both).
  **Public copy carries NO em dashes**; a `: ` in an unquoted YAML frontmatter `description:` breaks the
  `docs-site` build — quote it. Docs nav lives in **`docs-site/src/lib/nav.ts`**, deploy in `docs/DEPLOY.md`,
  and `ogcode serve` binds all interfaces with **no auth** (a proxy MUST NOT buffer). Brand button `#d1440c`.
- **Cloudflare**: `ogx`/`oglab` A → `162.19.81.108`; **origin nginx listens only on 40009**, so those vhosts
  need `origin.port=40009`. Box access is password-only (`sshpass … -p 20009`); edit `sites-available`, then
  `nginx -t && systemctl reload`.
- **PostHog `497411`**: **never send content**; headless browsers drop every event (patch UA + `webdriver`).
  `ogcode_turn_resources` is one event per turn with `distinct_id` = `s.installID`; logs need
  `OGCODE_POSTHOG_LOGS`. **Install-id**: `VAR=x cmd | sh` does not pass `VAR` — `export OGCODE_IID='…'; curl …
  | sh`.
- **Feature flags**: one `/decide` per refresh, fail-safe OFF. Notes also drops its prompt section;
  device-panel 404s routes and proxy; plan-mode gates only the banner. Serve tests use `stubFeatureFlagsOn(t)`.

## Agent board & teammates

- **The board IS the agent tree, not a message log** (migration `067_agent_tree.sql`): `board_agent` rows carry
  `spawned_by`/`status`/`notified_at`; `internal/board` (store + migrations) behind `internal/server/board.go`
  + `board_routes.go`; tools `internal/tool/board.go` (**one op, `roster`** → `treeText`) + `teammate.go`
  (spawn/stop); routes `GET /api/board/roster` + `POST /api/board/teammate/{handle}/stop`.
- **The board now has a dedicated Agents page** (2026-10-25, the developer's call): `web/src/pages/agents.tsx`
  at route `/agents` (registered before `/preview` in app.tsx), nav label "Agents" from session-sidebar +
  plan-sidebar; `board-drawer-row.tsx` KEPT and reused — the page ports the drawer's whole logic (SSE 150ms
  debounce via `server.lastEvent()`, 15s poll, filter, showFinished, collapse, two-press stop), pure logic in
  `web/src/lib/board-tree.ts` (`buildTree`, `decorateRows`, `matchesQuery`, `rosterSummary`). **Header chips
  stay DISJOINT: `running = live − idle`, because `live` ⊃ `idle`**. Rows show the session **title + model
  joined per row** (`boardRosterRow` embeds `board.Handle`; flat JSON).
- **An op-less teammate call self-heals**: `teammate` infers `spawn` when `prompt` is present and the empty-op
  refusal is shape-aware (`handles`/`timeout`→`wait`, `handle`→`stop`/`status`, `name`→`spawn`;
  `missingOpHint`, `internal/tool/teammate.go`), pinned by `internal/tool/teammate_test.go`.
- **A child reports back by FINISHING; there is no mid-task channel in either direction** — a teammate cannot
  ask its parent, a parent cannot ask a running child. Child loop end → `status=done` + `updated_at`; spawner
  RUNNING gets a turn-scoped `LoopControl` notice with the child's last assistant message **whole — no byte
  cap** (`DrainNotices`' 2 KB `maxNoticeBytes` bounds a BATCH only — an oversized notice is taken alone, never
  clipped), then `MarkReported`; spawner IDLE keeps `notified_at < updated_at` and `CatchUpTeammates`
  (`server/board.go:263`, hooked in `loop.go:384`) hands it over at the parent's next turn start. **Nothing
  starts a turn on its own**. Pins `TestUnreportedUntilMarkedReported`,
  `TestCatchUpTeammatesReportsAnIdleSpawnerOnce`.
- **A terminal status is claimed BEFORE the loop is cancelled** (`board.Store.SetStatusUnlessTerminal`):
  `stopTeammate`/`stopTeammatesOf` claim first and skip `cancelLoop` when the claim misses; `leaveBoard` too —
  cancelling first let a finishing child rewrite `stopped` and re-deliver its old report. Pins
  `TestStopTeammateIsNotRewrittenDone`, `TestStopTeammateOfAFinishedTeammateChangesNothing`.
- **That pending report is durable, delivery is not automatic**: `notified_at`/`spawned_by` are in the project
  DB's `board_agent` (067) and `Register`'s ON CONFLICT leaves `notified_at` alone (a restart neither loses
  nor re-delivers a report). Startup `reconcileBoard` (`server.go:691`) demotes a teammate left `running` to
  `stale` — terminal, so it is delivered as a stale report. **Marking is at DISPATCH** (2026-10-24): RunLoop
  keeps notices loop-local (`wireFresh`/`wireDigest`) and stamps via `lr.BoardNoticesDelivered` ONLY inside
  the dispatch-success block; the pre-stream gap restart calls `lc.RestoreNotices(wireFresh)` (restores to the
  FRONT of `unshown`, idempotent — a digest never shadows restored full text); `catchUpTeammates` tracks its
  pushes the same way; a turn dying before the first dispatch leaves rows unreported → next-turn catch-up
  (crashes re-deliver = at-least-once). Pins: `TestLoopBoardReportsStampOnlyAtDispatch`,
  `TestRunLoop_NoticeGapRestartRidesTheNextRequestFull`, `TestRestoreNoticesRequeuesFreshWithoutDigestLie`,
  `TestTeammateReportUndrainedAtTurnEndStillDelivers`, `TestLoopBoardNoticesDeliveredMarkTheReport`,
  `TestLoopBoardReportsDieWithTheirTurn`.
- **Delivered notices re-inject as HEADLINE digests after their first step** (2026-10-24):
  `TakeNoticesForInjection` (`internal/agent/loop_control.go`) returns fresh notices full-text once (from
  `unshown[]`) and everything already shown as `Notice.Headline()` under `noticesDigestLabel`; the notice
  interrupt path (`InterruptForNotice`/`ArmNoticeInterrupt`) was deleted as dead code. Pin
  `TestLoopControl_AReportReadsFullOnceThenDigests`.
- **Handles mint FRESH; stops REPORT; waits RESPECT the turn's cancel** (2026-10-24):
  `board.RegisterFresh` = insert-once (`ErrHandleTaken`; `Register`'s upsert untouched) + server
  `mintFreeHandle(ctx, reg, attempts, newName)` (board.go:103) rolls a 4-hex suffix up to 8 attempts — wired
  into `joinBoard`, `spawnTeammate`'s random path (claims the handle BEFORE the child session is created, so
  the title built from it stays truthful) and `spawnBackgroundTask`; typed names keep the friendly Get
  pre-check. `stopTeammate` reports "was stopped" via `reportTeammateDone` (board.go:935, guarded on
  `SpawnedBy`/`SessionID`) — drawer and tool stops both land there; `stopTeammatesOf` (abort/delete cascade)
  stays report-free and its rows ride the next catch-up. `waitTeammate` breaks its 250ms slice on
  `ctx.Err()==context.Canceled` (board.go:791 — abort/superseding prompt/delete) but NOT on
  `DeadlineExceeded` (the op bound). Supersede via `startSessionLoop` does NOT stop teammates — the cascade
  lives only in `handleAbortSession`/delete/hosted `cancelLoop`. Pins
  `TestStartSessionLoopSupersedeKeepsTeammates`, `TestMintFreeHandleRetriesPastCollisions`,
  `TestWaitTeammateReturnsPromptlyWhenTheTurnIsCancelled`, store pins `TestRegisterFresh…`/
  `TestRegisterStillUpsertsARejoin`.
- **Build-only by AGENT ID at every seam**, not by tool absence: `codingAgentTools` is shared with `TaskAgent`,
  so `canUseBoard`/`canSpawnTeammate` (`agent/agent.go:496,505`), `joinBoard` and the `CatchUpTeammates` hook
  all test `agentID == "build"`; the prompt section sits in the `if !task` branch of `codingAgentSystem`.
  Coverage: `internal/server/board_test.go` + `board_routes_test.go` and
  `TestStartSessionLoopJoinsAnEmptyAgentAsBuild`.
- **The prompt casts the build agent as an ORCHESTRATOR** (`boardPrompt`, `agent/agent.go`): "You orchestrate;
  teammates do the work" — decide the fan-out before the first read, hand streams out, and spend its own time
  on the next decision, shared files, verification and user questions (watching with `teammate wait`, never
  polling roster/status); solo only for strict sequence, one shared file, an edit too small to brief, or
  research (`task`). **Parallel-first default stated outright** (2026-10-23): side-by-side streams are "the
  default, not an upgrade", spawned in one tool block **right after `update_intent` states the plan** (which
  MAY ride the first tool block); one teammate per independent stream; the fan-out's only cap is
  verifiability, not cost. Two currencies: wall-clock (one teammate per independent file/package) and tokens
  (a child's greps/reads are billed in its own window). Two rails live ONLY in the prompt: **do not end the
  turn while a spawned teammate is still working**; **a teammate briefed to implement one stream implements
  it**. Rail exits: `teammate stop <handle>` (cascades to what it spawned) or parking a legitimately long
  child (end the turn; CatchUpTeammates delivers at the next turn start). Process step 1 (interactive only)
  points at it. `TestBoardPromptPrefersTheSplit` pins the wording, decision-before-mechanics order, and that
  no non-build agent prompt mentions `teammate`.
- **`startSessionLoop` normalizes `agentName == ""` → `"build"`**: the web client never sends `agent`, and
  `joinBoard` refuses any name but `"build"`, so without it NO interactive session joins the roster
  (`internal/server/resume_routes.go`). **`board.Store.Get` returns the ZERO Handle with a nil error** when
  nobody holds the name — check `h.Handle == ""` yourself before acting on it.
- **Rails**: **no spawn depth limit, no live-teammate cap** — any `build` session spawns, a teammate included
  (pin `TestSpawnTeammateAllowsNestingAndCapsNothing`); **steps are uncapped** — `stepBudgetFor` returns
  `agent.MaxStepsUncapped` (`-1` → `hardMax = math.MaxInt` via `overrideStepBudgets`, `internal/agent/loop.go`);
  spawn = `Ask`; `cancelLoop` cascades down every nesting level; claims and the post budget died with the
  posts — **conflict-freedom comes from the file change gate, not the board**.
- **Task tool async (`"background": true`)**: `spawnBackgroundTask` (`server/board.go`) registers a READ-ONLY
  board KindTask child — session type `"subagent"`, Title `Task: …`, prompt `backgroundTaskPrompt`,
  model/permission inherited — and returns a `task-*` handle + `/session/…` URL at once; sync `task` is
  unchanged and default (CLI `run.go` registers no Spawn → background refuses). Delivery reuses the
  teammate-report pipeline ("Background task" notice to a RUNNING parent, `CatchUpTeammates` otherwise);
  `teammate wait <handle>` blocks early; `stopTeammate`/board route/drawer accept KindTask; budget = sync
  parity (`agent.DefaultMaxSteps`, 0 resumes). **Task children are ungated on purpose and NOT
  permission-mirrored** — `resume_routes` skips `WithPermissionGating` for `agentName == "subagent"`; the
  teammate BFS in permission_routes and `ParentSessionOf` stayed teammate-only. Pins:
  `TestTaskBackgroundRouting`, `TestSpawnBackgroundTaskRegistersATaskChild`,
  `TestLeaveBoardReportsABackgroundTaskToARunningSpawner`, `TestStepBudgetForATaskChildMatchesTheSyncSubAgent`.

## 2026-10-25 — MEMORY.md diet pass-7 archive (full pass-6 working text, 23777 bytes; pre-diet originals in the diet sections above)

# Project Long-Term Memory

**Admission test.** A line earns a place only if a future session needs it *before acting*: a gotcha with its
fix, an invariant with its pinning test, a canonical value, or a decision and why. **Procedures live in
`HOWTO.md`; measurements and long-form subsystem detail in `HISTORY.md`**; the pre-split text is in
`MEMORY-ARCHIVE.md`. Budget **~20 KB** — re-read every turn on the cached prefix; curate rather than truncate.

**Standing conventions.** **Developer preference: needs SPEED — parallel by default** — batch every independent read/search in one block, and fan out teammates/task agents per independent stream instead of working serially (stated 2026-10-24). Working tree usually dirty — read hunks, never `git add -A`, stage explicit paths; nothing is committed unless asked. `web/dist` is served from an `embed.FS` (`internal/server/static.go`), so a
shipped web change needs a Go rebuild — but a live visual check does NOT: run a vite dev instance with a
throwaway config proxying `/api` to the live server (HOWTO.md: "Verify a web change against the live server
without restarting it"); Go rebuild only needed before shipping.

## Repo layout & git hygiene

- Root: markdown only; plans in `plans/` (bare inside); next migration `069`.
- **`.gitignore` paths stay root-anchored** (`/GIT_DIFF_PLAN.md`, `/dist/`, `/agent/` — `.agents/skills` is
  what `internal/skill/discover.go` reads; build `./internal/... .`). `AGENT.md` is tracked but 0 bytes.
- **README hero = `docs/media/ogcode-demo.gif`** (no WebP/video on GitHub); re-encode via `HOWTO.md`; check
  for leaked internal paths at legible resolution.

## Releases, version & install

- **Tags are immutable**: `git ls-remote --tags origin` + `gh release view vX.Y.Z` first; bump `internal/version/version.go` + both `web/package*.json`.
- **A red Linux build job blocks a release**: `ZIG_LOCAL_CACHE_DIR: /tmp/zig-cache` (else `.zig-cache` lands beside the read-only module cache).
- **macOS re-sign**: a stale in-place signature → `Killed: 9`; `make install` `rm -f`s first, ad-hoc signs, restarts nothing (check the listener pid's start time).
- **`make -s install …` BEFORE handing over a restart**: a relaunch runs `~/.local/bin/ogcode`, not repo `./ogcode`; verify `strings ~/.local/bin/ogcode | grep -c <new-symbol>` ≥1, then the pid changed.
- **`detectInstallCommand()`** = scoop → Homebrew → cargo → install.ps1 dir → winget → install.sh; Homebrew = `/Cellar/` or `/opt/homebrew/`, **never `/usr/local/`**; verify on a throwaway rig, expanding the binary path BEFORE exporting `HOME`.
## Build & dev loop

- `make build` = `build-web` (npm → `web/dist`) then `build-server` (`CGO_ENABLED=1 go build -o ./ogcode .`); `make dev` :9595; `make dev-web` Vite :5173; `ogcode version` (no `--version`); dirty builds print `…-dirty`; no linter; gofmt touched files.
- **`CGO_ENABLED=1` mandatory** (cgo Swift grammar); `make test` omits it; OGX-signed build: `make build-server OGX_APP_SECRET=$(tr -d '\n' < ~/.ogcode/ogx-app-secret)`.
- **`ogcode run --max-turns` defaults to `0` = no limit; a literal `MaxSteps == 0` still means the 1000-step default** (zero-value `LoopRunner`); the server loop (10000, 2 auto-resumes) and `ogcode index` (50) stay capped (`step_budget_test.go`).
## Deployment & first boot

- **A project keeps its port**: `~/.ogcode/ports.json`; a bare `ogcode` reuses it (this project → **7801**); `--port` overwrites. **Never restart the host server unasked** — hand over `kill <pid>; cd <dir> && ogcode` (no `--port`).
- **One server per dir**: advisory flock on `<dir>/.ogcode/server.lock`; a second `serve` exits 1 naming the holder; **`port free` ≠ dead** (SIGTERM frees the listener before the flock) — wait for the old PID to EXIT.
- Listener before preflight; `GET /api/config` 200 = health probe; end-user macOS signing is NONE (ad-hoc; runs via `com.apple.provenance`).
- **Docker**: install `git`; `HEALTHCHECK … /api/config --start-period=120s`; `docker.yml` needs `type=raw,value=latest`; ldflags `-X`: missing symbol = silent, empty = no fallback.
## OGX gateway

**Assertion**: HMAC-SHA256 (base64url) over `app + "\n" + ts + "\n" + method + "\n" + path` (skew 5 min) in `X-Client-App`/`-Timestamp`/`-Signature` — ogcode `appassertion.go`, oglab `internal/httpx/httpx.go`. **Gate CONDITIONAL**: no secrets ⇒ open; any secret ⇒ any failure ⇒ 401.

- **Secret**: `~/.ogcode/ogx-app-secret` (600, never paste); **`tr -d '\n'` mandatory** (unstripped HMAC → silent 401).
- **Provider**: `https://ogx.ogcode.in/v1`; **`/v1/models` IS the plan** (dynamic, all `ActiveByDefault`, no static fallback); priority `anthropic, openai, openrouter, ollama, ogx`; **`prompt_cache_key` = session identity for token spend**; **bodies cap 4 MiB** (`checkBodyFits`, fatal; oglab matches — raising = 3 hops, gateway first).
- **Composer images**: 1024 px long edge, JPEG q0.8/WebP; passthrough ≤512 KB AND ≤1024 px; `MAX_IMAGE_SIZE` (10 MB) = pick limit.
- **HTML from a JSON endpoint = nginx routing bug**: `upstream oglab_router` + `location /v1/` first; 15s SSE keep-alive beats Cloudflare's idle cut; settings tab `readOnly`, not-connected div needs `data-setting`; oglab breaker ignores `400`/`422`.
## Providers, models & resolution

- **`OpenAIProvider.Models()` is a pure read — never network** (pin `TestModelsNeverFetches`); fetching is `RefreshCatalog`.
- **Resolution**: `orderedSnapshot()` sorts `(providerRank, id)`; `ResolveProviderFor` = custom → explicit → catalogue (`ActiveByDefault`) → highest priority; persisted as `provider` (migration 047); a `SELECT` column missing from `rows.Scan` = silent runtime error; `lookupModel` compares raw `m.ID`; **catalogue names are aliases** (`normalizeModelID` strips org prefix/`:latest`/`:cloud`/OpenRouter variants, first-claim-wins); `gemini-4-argon`: `RejectsSampling`, `MaxOutputTokens: 32_768`.
- **`ogx-deepseek-v4.1-flash` THINKS BY DEFAULT** (2026-10-24): `defaultEffortFor` holds flash-class ids at `low` when session effort is "" unless the spec default is LIGHTER (a recorded ≥high keeps the hold) + `StreamRequest.StepMechanical` (loop tail) drops default-derived effort to `none`/`low` after a tool-only step with ≥1 WORK call (exploration-only exempt; user-set effort NEVER rewritten); pin `openai_effort_flash_test.go`.
- **ogcode.json custom providers**: extra ids via `config.CustomProviders()` → `NewOpenAICompatibleProvider`, wired `cli/run.go` + `loadProviderMap`; entry = `type` ""/"openai" + `baseUrl` (+ optional `model`/`label`); `disabled` merges ONE-WAY; **`Load` must copy every new ProviderConfig field** (dropped `Type` once).
- **Ollama slot lists only endpoint models**; budget `OGCODE_PROVIDER_MAX_CONCURRENT` 8, reserve 2 (index: `indexGate` then `total`); defer order `close(ch); body.Close(); cancel()`; **`write tcp …: result too large` = ERANGE from the LOCAL `write(2)`** (VPN/filter) — retry once on a fresh conn; **ONE pooled h2 conn to ogx, h1.1 sockets DRAINED not dropped** (`drainFinishedStream` gated on `sawDone`; discard tests need a ~20–50 ms tail gap).
## Token accounting

- **Reasoning is billed INSIDE `output`, never separately** (`ModelPrice.Cost`, `TokenCounts`, `usageFromOAI` exclude it — display count); output ≈ 1% of tokens: cache-hit rate, not effort, drives spend; **Ollama Cloud meter**: `GET https://ollama.com/api/usage` + `OLLAMA_API_KEY`.
- `tokens.input` is UNCACHED: bill `input + cacheRead`; a hit is `prompt_tokens_details.cached_tokens` (>0) else `prompt_cache_hit_tokens`; the pill totals `GET /api/session/{id}/token`.
- **Title generation inherits the session model**; **Gemini `thought_signature` must be echoed verbatim** from `extra_content.google` on every replayed tool call or turn 2 400s.
- **Teammate spend counts on the CHILD** (its own step+utility ledger rows); **NEVER `foldChildUsage` a teammate** (double-counts); the pill folds teammates on the CLIENT (`teammates` arrays from `/session/{id}/token` + `/usage`, `utility` = a subtotal; errored child read skipped per-child; `spawnedTeammates` = `board.BySession` + roster `KindTeammate && SpawnedBy == parent.Handle`).
## Permissions

- **The ruleset gates, not the mode**: `Ruleset.Evaluate` runs BEFORE the mode switch; only read/glob/grep `Allow` by default (bash/write/edit `Ask`); the catch-all `Allow`s every other tool in EVERY mode; **Yolo only changes what would have been Ask**; **no Deny tier** (Safe/Ask/Unclear — only Unclear reaches the LLM; `riskLLMMaxTokens` 1024; an unanswered check warns → Ask, uncached); PATCHing a mode also `SetDefaultMode`s machine-wide; **grants never expire**; pattern = the verbatim string.
- **A teammate's Ask AND `ask_user` mirror into the PARENT's composer** (SERVER-side): `ParentSessionOf` climbs `SpawnedBy`; `GET /session/{id}/permission|question` BFS-append the SUBTREE; a reply by id clears EVERY queue holding it (path/owner/root; pages beyond go stale until reload — accepted); the child blocks until answered (no timeout; dialog answerable live).
- **Grants + mode are machine-wide** in `~/.ogcode/config.db` (migration 048), grain = the exact command/path/skill; **test trap**: `serve_lifecycle_test.go` doesn't set HOME — isolate HOME + cwd.
## Agent memory files

- **Caps: none by default** (`OGCODE_MEMORY_MD_MAX_BYTES`/`OGCODE_AGENT_MD_MAX_BYTES`; `limit <= 0` is
  uncapped) — a positive budget is head-biased, so curate instead. `staticSystemPrompt` carries **one constant
  pointer per file, never content or a heading index**; turn digests carry tool names and the final response
  only, so procedures go to `HOWTO.md`, not the scribe.

## Prompt caching, read pressure & context windows

- Anthropic `cache_control` on the last tool def + first system block (min 1024 tokens, 4096 on Opus 4.5+, 5-min TTL); OpenAI automatic; **entry [0] stays byte-identical** (the date rides `systemReminderPrompt`).
- Read pressure: 40000 tokens (`OGCODE_READ_PRESSURE_THRESHOLD_TOKENS`); resend multiple 1 (`OGCODE_RESEND_COST_WINDOW_MULTIPLE`).
- **History is TURN-SCOPED** (`sliceTurn(findLastTextUserMessageIndex)`): prior-turn images never re-send; within a turn they repeat, riding the last-message cache breakpoint.
- **The turn's prompt and images survive compaction; tool images do not**; the ask_user Q&A must survive verbatim on all three surfaces.
- `OGCODE_COMPACT_CONTEXT` unset/empty = ON; `maxCompactions` 2, `compactionKeepRecent` 12, unknown window → 128000; learned windows kept only when `learned > promptTokens`.
## Agent tools & prompt

- **`schedule` = one-shot agent timers** (2026-10-24): `create` (`delaySeconds` OR strictly-future `fireAt` + required `note`)/`list`/`cancel`; store `internal/timer` (migration `068`, atomic `ClaimDue`); ticker `pollReminders` (10s) delivers via board-report rails; an idle session's row waits for `CatchUpReminders` at THAT session's next turn start — never starts a turn; restarts keep rows (at-least-once); refusals Result-shaped; **build-only via `canSchedule()`**.
- **`update_intent` rides the first work block**; planless reminder ONCE per session (re-arms via `markPlanSet`); 1–7 steps in `session.intent` (migration 064); **descent gate**: `grep`/`glob` before any map/read reminds once per turn; **matcher**: one routing completion per block, silent failures, no cap; no `description`/not pending-active ⇒ never routed; **chip = the step label; statuses move MID-TURN; nothing closes the plan at turn end**; the build agent has **no upfront skill/MCP list** (`turnTools` withholds `mcp_*`; bare `skill` refused in a matcher session); rich output = fenced declarative specs.
- **`read` takes `files=[{path,ranges,offset,limit}]`** (2026-10-24; the prompts' primary shape): exclusive with `path`; bookmarks stamp per CONTENT section only; **`read`/`file_map` UNCAPPED** (2026-10-25): no limits, no default window (bare read = whole file; missing `end_line` = EOF; any `limit` = a window); both set `Result.Uncapped` (50 KB backstop skips); one bound: ogx's 4 MiB body.
- **Hidden contracts**: blank ask_user = unanswered; **pathlock** bars write-vs-edit on one file, batched edits OK; **codemap** = one level per call, no threshold; `dirPrefixFilter` needs `ESCAPE '\'`; **HCL is outlineOnly**; Swift `as?`+newline+`??` misparses; non-vision models lose images (resumable); MCP images → temp file; `requires:` envs via `skills.env`.
## MCP & search

- **`OGCODE_SEARCH_ENABLED=false` removes `web_search`, `fetch_page` AND `deep_search`**; to suppress only deep_search, add a prompt clause.
- **MCP: transport is INFERRED** (`Command` ⇒ stdio, `URL` ⇒ streamable-http; `"transport"` overrides — no `type` key); tool ids `mcp_<server>/<name>`; lazy connect; a URL server without `headers` gets OAuth (`~/.ogcode/mcp-tokens/`), with `headers` = static bearer.
- **Search** results carry a `Provider` stamp before the TTL cache; `/settings/search` is a FULL-STRUCT replace; `""` = HTTP first, `safari`/`chrome` = browser first (`chrome` on macOS = Safari); Windows check: `GOOS=windows CGO_ENABLED=0 go build ./internal/search/`.
## Data, migrations & transcript

- **Migrations**: goose keys by VERSION NUMBER only — a number taken by a never-landed branch silently SKIPS your file; check `git log --all -- internal/db/0NN*` AND the real `goose_db_version`; next free = next integer past the highest file.
- **The transcript is append-only**; the only bound is `capToolOutput` (50 KB, skipped by `Truncated`/`Uncapped`); only deletes are `Store.Delete`/`DeleteMessage`; compaction changes what is re-sent, never what is stored; **nothing prunes or vacuums** — deletes fill the freelist; reclaiming needs a manual `VACUUM`.
- **FK 787 mid-turn ⇒ another writer deleted the session** (`handleDeleteSession` waits ≤5s; `Store.Get` returns `(nil, nil)`; `classifyInterruption` maps it and `ErrRequestTooLarge` to `InterruptFatal`).
- **Session DB**: `.ogcode/ogcode.db` / `~/.ogcode/config.db`; `message` = metadata, content in `part.data`; **`GetMessages` must return `Parts` as `[]`**; pagination `?before=<id>` + `X-Has-Older` at 300 rows.
## File change gate

- **Migration 066 + `internal/filechange`**: a row per `write`/`edit`/`bash` + a per-(session,file) bookmark (`seen`); ring `RingPerFile` (50); `GET /api/file-changes` (503 when nil) — no web client calls it yet; the `diff`'s one live consumer is the gate's post-restart refusal.
- **A foreign change since this session's last read refuses `write`/`edit`** (`staleness.go`), as a Result showing the diff; `Seen` = **-1 for never-read** (0 is a real bookmark); an empty ring falls through to the `History` query; **`bash` is COVERED by fingerprint** (`bash_observe.go`): every command stats tracked files, records UNATTRIBUTED changes (deletions too) — the session is refused by its own shell until it re-reads.
- **`description` required on `write`/`edit`**; a nil `FileLog` is a no-op; **fails OPEN when the history row is missing**; blind spots: MCP tools, `latex_to_pdf`'s `.tex`, outside-agent changes until the next `bash`; `file_map` is NOT a read; a map-only read does NOT stamp the bookmark; **cross-process staleness appears only after a restart**.
## Web UI: composer, sidebar & navigation

- **Composer drafts/attachments are per session**; **the running pause glyph IS the stop control**; the `+` attach menu renders in a `Portal`; the plan-mode flag gates only the warning banner; mode comes from `GET /api/mode`; **the session-row dot reports task status** (a hand-seeded row must set `permission` or `Store.List` 500s); **`⌘K` is guarded on `offsetParent`**.
- **Virtualization/scroll memory** (`message-list.tsx`/`scroll-memory.ts`): pad-based windowing keyed by message id; the id-change effect must call `restore()`; ResizeObserver must not `holdAnchor` when `anchoredByList`.
## Theme, contrast & light mode

- **Light mode is server-global**: `GET/POST /api/appearance` off **`s.globalDB`** — a throwaway rig flips
  the USER's machine-wide mode (save + restore around audits); `web/index.html` pre-paints from
  `localStorage['ogcode-theme-mode']`; Settings General is `/settings`.
- **Muted-text floor: 4.5:1 on the LIGHTEST surface the token can sit on** (`--bg-hover`); defaults:
  accent `#ff5e1f`, **accent ink is its own per-mode token**, **`onPrimary` = `labelFor`** over `--accent`
  AND `--accent-hover`.

## Preview, public serving & device panel

- **The Preview page lists only announced ports** (`<port>.<PreviewDomain()>`; `recordAnnouncedPorts` reads
  PartText only at turn end; manual `?ports=`); `previewHostDispatch` strips `X-Frame-Options` + CSP
  `frame-ancestors` or a frame-guarded app renders blank.
- `/public` serves `<data-dir>/public` with **HEAD mapped explicitly**; **scrcpy** deep links need
  `action`/`udid`/`player`/`ws` — use `resolvedUdid()`, never raw `udid()`.

## Browser takeover (bridge + extension)

- **Option B**: a first-party MV3 extension (`browser-extension/`) drives the user's real Chrome over a WS bridge; 11 `browser_*` tools ride every turn (unpaired ops refuse Result-shaped); ops 30s (60s navigate/snapshot); build-only via `canControlBrowser()`; pair GET echoes the token WITHOUT rotating (POST rotates; status always 200); **pair token persists at `.ogcode/browser-pair-token` (0600)**.
- **Traps**: OriginPatterns need `://` (`"chrome-extension://*"`); **hello `version` = bare JSON NUMBER** (a string drops hello silently); the 32 KiB read limit killed big replies — fix `maxResultFrameBytes = 4<<20` + `SetReadLimit`; re-pair fails pending ops with the ErrNotConnected sentinel; **navigate `newTab` with no tab id auto-grants the tab** (persisted; panel-revocable; omitted `tab` = last Granted).
- **Watch capture**: first op on a granted tab attaches chrome.debugger; rings 300/300, body cap 65536, **CDP timestamps are epoch SECONDS**. **A heavy op can KILL the service worker, not pairing — don't re-pair on "extension not paired"**; unchanged "Reconnecting — retrying in Ns" = SW died mid-retry — reopen the side panel. **CWS kit**: `make extension-zip` (manifest at ZIP ROOT); **`<all_urls>` dropped — never re-add**; evidence: `HISTORY.md` 2026-10-25.
**Native-messaging host = the zero-paste pair path** (`com.ogcode`, 2026-10-25): `ogcode native-host`
(stdio host, 4-byte-LE framing; `{"type":"pair"}` → `{"ok",\"version",\"servers":[{origin,url,token,port,project}]}`
— registry = `portmap.All()`, each candidate probed `GET /api/browser/pair` 750 ms, sorted port-then-project,
stale entries filtered); `ogcode native-host install [--extension-id …]` / `uninstall`; boot auto-installs
idempotently via `nativehost.EnsureRegistered(projectDir)` (server.go, warn-only) writing `com.ogcode.json`
into every browser dir whose PARENT exists (mac: Chrome/Chromium/Brave/Edge/Vivaldi/Arc). **Extension id is
PATH-DERIVED** (`ExtensionIDForPath` = Chromium GenerateIdForPath: sha256 of the LOWERCASED abs path, nibbles
→ a..p): repo `browser-extension/` ⇒ `cgeoedmgjadadineklkmhgpmolmnpfbn` — loading the same code from a
different dir ⇒ different id ⇒ Chrome refuses to launch the host. Extra ids: `--extension-id` flag and
`OGCODE_EXTENSION_IDS`; `WebStoreExtensionID=""` reserved until CWS publish. **Extension pair leg** (v1.1.0,
+`nativeMessaging`): `background.js` `autoPairThenConnect` fires ONCE per worker life (5 s) when credentials
are absent — success writes `{serverOrigin, pairToken, bgConnected:true}` then connects like manual Connect;
failure ⇒ unconfigured status "no ogcode app — paste token to pair", NEVER retries/overwrites. Chrome reads
native-messaging manifests at browser STARTUP — a first-ever com.ogcode.json needs one Chrome restart.
Pins: `internal/nativehost/*_test.go`, `portmap/all_test.go`, `cli/native_host_test.go`, harness
`browser-extension/native_pair.test.js` (34 checks).

## Indexing & the document index

- Auto-index is gated by `OGCODE_AUTO_INDEX_MAX_FILES` (500) + consent (migration 061); manual builds are
  never gated; the post-turn guard is `agentID == "build"` AND `Directory == ProjectID`;
  `OGCODE_AUTO_INDEX` is the kill switch; it must run on the turn's resolved model AND provider.
- **Labels are LLM-made, not embeddings** (`IndexAgent`, no vector store in `internal/indexer`); `.ogcode/`
  is skipped unconditionally; 39 default excludes (`excludes.go`); `doc_page_index.mod_time` drives the
  refresh.

## Controlplane, remote workers & Incus

- **Nested module `controlplane/`**: one in-process `server.Server` per worktree; **`HostSession` must
  `store.Get` first** (`Store.Create` violates UNIQUE on re-delivery); routes key `<workerID>-<route>`,
  exact whole-label lookup; **bbolt is single-writer**, a byte-truncated file SEGFAULTS at `bolt.Open`; a
  restore must set `status=offline` + `LastSeen=boot time` or `ReapStale` kills every worker;
  workers/accounts/Incus: `HISTORY.md`.

## Infrastructure, feature flags & telemetry

- **Homepage is `docs/index.html`** (nav `.nav-menu`, mobile `#navSheet` — add to BOTH). Public copy: NO em dashes; `: ` in unquoted YAML `description:` breaks the `docs-site` build — quote it; docs nav `docs-site/src/lib/nav.ts`; deploy per `docs/DEPLOY.md`; `ogcode serve` binds all interfaces, **no auth**, a proxy MUST NOT buffer; brand button `#d1440c`.
- **Cloudflare**: `ogx`/`oglab` A → `162.19.81.108`; origin nginx listens ONLY on 40009 (`origin.port=40009`); box access via `sshpass … -p 20009`; edit `sites-available` then `nginx -t && systemctl reload`.
- **PostHog `497411`**: NEVER send content; headless browsers drop events (patch UA + `webdriver`); `ogcode_turn_resources` = 1 event/turn, `distinct_id` = `s.installID`; logs need `OGCODE_POSTHOG_LOGS`; install-id: `export OGCODE_IID='…'` before a curl|sh.
- **Feature flags**: one `/decide` per refresh, fail-safe OFF; Notes drops its prompt section; device-panel 404s; plan-mode gates only the banner; serve tests `stubFeatureFlagsOn(t)`.
## Agent board & teammates

- **The board IS the agent tree, not a message log** (migration `067`): rows carry `spawned_by`/`status`/`notified_at`; `internal/board` behind `server/board.go`; tools `tool/board.go` (one op, `roster`) + `tool/teammate.go`; route `GET /api/board/roster`; Agents page (`/agents`) reuses `board-drawer-row.tsx`; **header chips: `running = live − idle` (DISJOINT)**.
- **A child reports by FINISHING — no mid-task channel**: RUNNING spawner gets the last message whole then `MarkReported`, IDLE via `CatchUpTeammates`; **nothing starts a turn on its own**; stamping at DISPATCH (`BoardNoticesDelivered`); gap-restart re-queues via `RestoreNotices(wireFresh)`; delivered notices re-inject as HEADLINE digests (`TakeNoticesForInjection`); claim terminal status BEFORE cancelling; boot `reconcileBoard` demotes `running` → `stale`.
- **Handles mint FRESH; stops REPORT; waits respect cancel; supersede stops nobody** (`RegisterFresh`+`mintFreeHandle` claim BEFORE the child session exists; `waitTeammate` breaks on `context.Canceled`, not `DeadlineExceeded`); **build-only by AGENT ID at every seam**; **`boardPrompt` = ORCHESTRATOR, parallel-first**: fan-out before the first read, spawn right after `update_intent`, rails prompt-only; `startSessionLoop` normalizes `agentName == ""` → `"build"`; `board.Store.Get` = zero Handle, nil error when free; op-less calls self-heal via `missingOpHint`.
- **Rails**: no depth limit, no live cap, **steps uncapped** (`MaxStepsUncapped` → `math.MaxInt`); spawn = `Ask`; `cancelLoop` cascades; conflict-freedom = the file change gate; **Task `"background": true`** = READ-ONLY KindTask child (`type:"subagent"`), ungated, NOT permission-mirrored.

## 2026-10-25 — MEMORY.md diet: verbatim line archive (pre-diet lines not covered by the sections above)

  read.

## 2026-10-25 — MEMORY.md diet completed (66179 B → 21991 B; teammate memory-diet pass-8; every removed byte is verbatim in the diet sections + pass-7 archive + line archive above)

## 2026-10-25 — MEMORY.md diet final recount (pass-10: the pass-8 size check counted characters, not UTF-8 bytes; 11 further micro-trims, all content already archived above; final = 21883 bytes)

## 2026-10-25 — Native-host auto-pair silently broken: Chrome hashes the unpacked path AS SPELLED

**Symptom**: ogweb side panel stuck on "no ogcode app — paste token to pair" even with the server up;
native-messaging auto-pair never filled in origin+token.

**Root cause, verified on disk** (not inferred):
- Chrome 156.0.8078.48 on macOS 15.5 recorded the repo unpacked copy (`~/Downloads/work/ogcode/browser-extension`)
  with id `lgecpjpmljakfjgcgboknboioblifleg` in Chrome's `extensions.settings` path. That equals
  `sha256("/Users/prasenjeet/Downloads/work/ogcode/browser-extension")`[:16] nibble→a..p — path hashed WITH its
  original casing. The old `ExtensionIDForPath` lowercased first (`cgeoedmgjadadineklkmhgpmolmnpfbn`) — an id
  Chrome never assigned — so the boot-written manifest's `allowed_origins` matched no installed extension and
  Chrome refused to launch `ogcode native-host` for it.
- Compounding: Chrome was running since 11:38 while the manifest was (re)written 15:39; Chrome only reads
  native-messaging manifests at STARTUP, so no manifest change takes effect for a running Chrome session.

**Fixes**:
- `internal/nativehost/extension_id.go`: dropped `strings.ToLower` (hash as spelled); `extension_id_test.go`
  pins mixed-case hashing + a case-PRESERVATION test replacing the old case-INSENSITIVITY pin (which pinned
  the bug). `go test ./internal/nativehost/ ./internal/cli/` green.
- Machine fixed immediately without rebuilding: `ogcode native-host install --extension-id
  lgecpjpmljakfjgcgboknboioblifleg` (run from the repo root so the derivation's id also stays allowlisted);
  manifest now lists both origins. Pair host verified healthy separately by piping a 4-byte-LE-framed
  `{"type":"pair"}` into `ogcode native-host` — answered `{ok:true, servers:[…port 7801…]}`.

**Compounding cause (found when the user reported still broken after the Chrome restart)**: Chrome launches
the manifest's path BARE — argv[1] is the origin `chrome-extension://<id>/` (+ optional `--parent-window=N`),
NEVER a `native-host` subcommand. `~/.local/bin/ogcode 'chrome-extension://lgecp…/'` printed
"Error: unknown command … for ogcode" and the host exited instantly, so the extension's nativePairRequest
got "Native host has exited" and fell back to paste-token even with the manifest correct. Neither test layer
could catch it: `native_pair.test.js` stubs `chrome.runtime.connectNative`, and the Go tests only pinned the
subcommand form.

**Fix (round 2)**: `internal/cli/native_host.go` adds exported `IsBrowserLaunched(argv []string)` (true on a
`chrome-extension://` argv[0], or `--parent-window=` with a second arg); `internal/cli/root.go` `Execute()`
short-circuits to `nativehost.Run(os.Stdin, os.Stdout)` BEFORE cobra when it fires. Pin:
`cli/native_host_test.go` `TestExecuteRoutesChromeLaunchToTheHost` drives the real `Execute` with pipes
(framed `{"type":"probe"}` → framed error reply; gotcha: `io.ReadAll` on an os.Pipe blocks until ALL writer
ends close — close stdinW before Execute and stdoutW right after it returns). Verified end to end:
`printf '\017\000\000\000{"type":"pair"}' | ~/.local/bin/ogcode 'chrome-extension://lgecp…/'
--parent-window=0` → framed `{ok:true, version:v0.51.0-dirty, servers:[(7801,…/ogcode),(9603,…/oglab)]}`.
Installed by rebuilding + `rm -f ~/.local/bin/ogcode` + copy + ad-hoc codesign. An earlier full-suite run
timed out at 300s only because my first test version deadlocked in that ReadAll — no other fallout.

**Aftermath (confirmed working 2026-10-25)**: a manifest change needs a FULL Chrome quit/reopen (manifests
read at startup), but once the manifest was already correct an EXTENSION RELOAD sufficed — a reload is a
fresh worker life, which re-fires `autoPairThenConnect` (it never retries after a failure within one worker
life). The host fix itself needs no restart of anything — Chrome execs the current binary file at every host
launch. User confirmed auto-pair working end to end; `browser_status` from the server shows `connected:true`.
**Rejected**: re-loading the extension from the repo dir to match the old id — ids follow the load path's
spelling, so the durable fix is allowing the observed id (or fixing the derivation), not chasing the id.

## 2026-10-25 — Per-turn context cost of the 11 browser tools (feasibility probe for lazy gating)

- Measured by a throwaway `go test` in `internal/tool` (json.Marshal of `{name, description,
  input_schema}` per tool, then deleted): the 11 `browser_*` definitions total **8106 bytes ≈ 2000 tokens
  riding every build-agent turn**. Biggest: `browser_network` 1076 B, `browser_console` 966 B,
  `browser_type` 918 B; smallest `browser_content` 502 B. Prompt-cache reads pay ~10% of that per turn on
  a warm prefix; cache misses pay full.
- Feasibility finding: the lazy seam already exists — `newTurnToolset`/`offer` (internal/agent/
  intent_tools.go:510-558) hides `mcp_*` off the wire until the intent matcher names it, and
  `browserOffered` (internal/agent/loop.go:788) only gates build-agent vs registry, not per-turn. Making
  `browser_*` lazy = extend `offer()` past the `mcp_` prefix and surface via the (already matching)
  embedded `browser-takeover` skill or a kept-on-wire `browser_status` sentinel. Skill bodies CANNOT
  carry executable tools — tools must reach the wire; the skill supplies only the knowledge. Not
  implemented this turn (advisory only).

## 2026-10-25 — Lazy browser wiring shipped: the 11 browser_* tools ride the wire only when the turn needs them

- Implemented the probe above. The family stays OFF the wire until it is needed; the allowlist (`allowIDs`) still carries the `browser_*` glob, so the permission surface and `executeTool`'s HasTool check are unchanged (grants never expire, patterns unchanged).
- `tool.BrowserToolIDs()` (internal/tool/browser.go) is the family in registration order, single source for the offer.
- Matcher path: the intent matcher now also matches the embedded **browser-takeover skill** — when a plan step matches it and the browser registry holds the bridge, `formatIntentHint` (internal/agent/intent_tools.go) puts all 11 ids in `intentHint.OfferIDs` (renamed from `MCPIDs`) plus a hint line pointing at browser_status; `suggestIntentTools` offers them via the widened `turnToolset.offer` (now accepts concrete `mcp_*` AND `browser_*` ids; no-ops when the base id or a base glob already covers one, so a non-matcher turn pays nothing).
- Skill-load path: `RunLoop`/`executeReadyToolCalls` (internal/agent/loop.go) reverse-walks the block's executed tool calls; a successful `skill` call with `{"name":"browser-takeover"}` offers the whole family for the rest of the turn — this also covers planless turns the matcher never routes.
- Sentinel fix found BY the new end-to-end test: `allowIDs` gained only the `browser_*` glob, so a real matcher turn dropped it and browser_status was off the wire too (the unit test had used a synthetic allowlist). Fix: loop.go appends `browser_status` concretely BEFORE the glob, so `newTurnToolset`'s glob drop keeps the sentinel from step one of every matcher turn.
- Non-matcher turns (intentOffered=false — e.g. CLI/headless loop where update_intent isn't registered) keep full glob expansion, so they see the whole family as before; safe fallback.
- Pins: `internal/agent/loop_browser_lazy_test.go` `TestRunLoop_BrowserSkillLoadPutsFamilyOnWire` (full RunLoop: call 1 = sentinel only, call 2 = whole family after the skill load); `browser_gate_test.go` `TestMatcherTurnKeepsTheBrowserSentinelOnly`; `intent_tools_test.go` family-offer tests; `internal/tool/browser_test.go` `TestBrowserToolIDsMatchesRegistration`. All green (`go test ./internal/agent ./internal/tool ./internal/skill ./internal/server -count=1`), build clean, go vet clean.

## 2026-10-25 — ogweb Chrome Web Store submission prepared

- Privacy policy published first (CWS requires a public URL): `docs-site/src/content/docs/privacy.md` committed as `4d0f37d`, pushed, `pages.yml` dispatched (run 40197100252, green), verified `https://ogcode.in/docs/privacy/` → 200.
- Verified `<all_urls>` removal was sufficient: drove the granted tab to example.com — navigate + snapshot + screenshot all work with host-only permissions.
- Zip: `make extension-zip` → `ogweb-1.1.0.zip`, manifest at ZIP ROOT, 10 files, perms `["debugger","tabs","sidePanel","storage","nativeMessaging"]`, no host_permissions. gitignored at repo root.
- CWS developer account: user registered + paid $5 at chromewebstore.google.com (publisher prasenjeetsymon@gmail.com, account id 908afb76-d0bd-40be-b2bc-3d466a7f7907). Dashboard interaction is manual only — our browser tools get "The extensions gallery cannot be scripted" even for snapshot; chrome-extension:// navigation is also refused, so drive by URL only.
- Listing screenshots made with `screencapture -x` (OS-level, 3420x2214) while the user opened the real ogweb side panel in Chrome, then `sips` crop (top 70 px menubar removed, `-c 2137 3420 --cropOffset 70 0`) + `-z 800 1280` → exactly 1280x800. Saved to `cws-listing/`: `ogweb-hero-1280x800.png` (ogcode.in + connected panel, pair token masked) and `ogweb-debug-banner-1280x800.png` (Google News with the "ogweb started debugging this browser" banner, showing the declared-permission flow).
- Finding: `screencapture` captures the FRONTMOST window only, so the user had to bring the Chrome window forward; `browser_navigate` to a real site updates the side panel's ALLOW row automatically (no user re-check needed).
- Listing text from `browser-extension/STORE_LISTING.md` (no em dashes), category Developer Tools, single-purpose + five permission justifications prepared; chrome.debugger means the slow review lane (a week+, possibly a justification follow-up).

## 2026-10-25 — browser-extension v1.2.0: click/type pipeline rework (6 fixes, 3 parallel streams)

All fixes land in `background.js` (the whole `browser-extension/` dir is untracked — no git diff):
locate→sleep(80ms)→rect collapsed into ONE `aimExpression` eval; `elementFromPoint` hit-test refuses
covered targets (`click point blocked by overlay: <tag> "<text>"`); click point clamped into the
element box THEN viewport (tall elements clicked on-element; fully off-viewport → dedicated
offscreen error); refs drift-gated against stored `tn`+text (`refMeta` in `opSnapshot`, tag-only
gate — text threaded but not gated to avoid false positives on live labels); snapshot covers open
shadow roots (depth ≤ 6, ` >> ` selector chains via shared `Q_SOURCE`), adds `[onclick]`,
`[tabindex]:not([tabindex="-1"])`, `[aria-labelledby]` label resolution, `disabled` flag; click
result gains `url`/`title`/`changed` (post-click read-back, fallback + changed:true on read
failure); type refuses disabled/read-only fields and read-backs `value` (capped 300, before submit
Enter).

Deviations from the original tiered plan: text drift gate deliberately NOT enforced; `sleep()`
helper and `RECT_WAIT_MS`/`locateExpression`/`rectExpression` deleted (zero repo references).
Version bumped 1.1.0 → 1.2.0 (manifest.json + native_pair.test.js MANIFEST_VERSION + README/PROTOCOL).
Streams: bg-pipeline (code), bg-tests (new `pipeline.test.js`, 27 checks), bg-docs — both suites
green after review (27/27 + 34/34, run independently).

## 2026-10-25 — Browser snapshot/token reduction: options A+B+C shipped

- **A (Go-only)**: `browser_snapshot` results render as a compact header + one line
  per element (`[1] a "Sign in" href`, `[2] textbox "Email" placeholder "…"`, `(truncated)`)
  in `internal/tool/browser.go` — `selector`/`tn`/empty fields stripped from the MODEL VIEW only;
  wire format untouched. `browserBase.run()` gained an optional `render` func; non-snapshot
  payloads still fall back to old pretty-JSON. Measured ~79% smaller (~50k→~10k billed tokens/flow).
- **B**: `browser_snapshot` gains `query` (case-insensitive substring over tag/text/placeholder/href/value + exact tokens "checked"/"unchecked") and `limit` (first N AFTER filtering, DOM order); applied INSIDE the extension's snapshot expression (background.js ~824–940), so the payload is genuinely smaller on the wire; refs NOT renumbered; optional top-level `totalMatching` when a query was applied; Go header renders `showing N of M`.
- **C**: screenshot re-encodes to JPEG q0.8, long edge ≤1024 (`SCREENSHOT_EDGE_MAX`/`SCREENSHOT_JPEG_QUALITY`, `encodeScreenshotJpeg`, OffscreenCanvas in SW); request `format` field still validates `png` (CDP capture intermediate).
- **Integration bug found + fixed**: `browser.go` hardcoded `MediaType: "image/png"`; now honors the response `format` field, falls back to png for older extensions (pin `TestBrowserScreenshotHonorsReturnedImageFormat`).
- Pins: `browser_test.go` (4 new snapshot tests + format pin), `pipeline.test.js` now 58/58, `native_pair.test.js` 34/34. PROTOCOL.md rows updated. `make build` green; extension reload required before B/C take effect in Chrome.

## 2026-10-25 — Teammate pane (variant D) folded into the transcript

- Teammate tool calls (tool id `teammate`, ops spawn/wait/stop/status) now render as the approved
  "variant D · Terminal LED pane" instead of generic tool rows, grouped per run of consecutive
  teammate parts. `teammateSegments(msg)` in `web/src/components/message-item.tsx` (~line 435)
  emits {kind:'teammates',parts} vs {kind:'part',part}; rendered with Solid `<Index>` (the callback's
  2nd arg is a PLAIN NUMBER, unlike `For`'s accessor — used for rail stagger −(i×0.45)s).
- Per-line state comes from `parsePartData<ToolPartData>(p.data)` — **bug fixed this session: parsing
  the whole Part `p` (not `p.data`) left state undefined → `DEFAULT_TOOL_STATE` → every line showed
  ✗/err**. Match how ToolPartDisplay does it; `data?.state ?? DEFAULT_TOOL_STATE`.
- Pane CSS appended to `web/src/styles/index.css` (.teammate-pane/.tp-* + @keyframes tpFlow, reduced-motion
  rules) using theme tokens, so both modes work; `.tp-name` got `white-space: nowrap` after the dark capture
  showed `rev-provider` wrapping to two lines.
- Verified live: vite verify rig + real Chrome headless (CDP); captures in `public/teammate-row/`
  (variant-d-live.png, variant-d-live-open.png). Programmatic `scrollTop` fights the chat-scroll anchor
  (message-list.tsx holdReader) — real CDP wheel events work (see HOWTO entry).
- GOTCHA hit + fixed: the throwaway vite page syncs `localStorage['ogcode-theme-mode']` to the
  SERVER-GLOBAL /api/appearance on :7801 through the proxy — flipped the user's machine-wide mode to
  dark mid-audit; restored by POSTing {"mode":"light"} back. Save the probe before, restore after.
- Earlier "light" capture (`/tmp/pane-live.png`, pre-fix) showed the bug state (6 ✗/"6 ERRORS"), which
  incidentally confirmed light-mode tokens render; not published.

## 2026-10-26 — Agents page redesigned as node graph view

- `/agents` board rows replaced by a graph: cluster per depth-0 root family (tinted region + truncated
  label), bezier edges parent-bottom → child-top colored by CHILD status, animated dashes on running
  subtrees.
- New files: `web/src/lib/board-graph.ts` (pure layout + `statusColor`; NODE_W=196/H=56, V_GAP=68,
  H_GAP=22, cluster label truncation `maxChars=max(8,min(28,floor((maxX-minX-34)/6)))`),
  `web/src/components/agent-graph.tsx` (`AgentGraph`: zoom 50–175% pill w/ backdrop-blur outside the
  scroll area, +N collapse, hover-subtree highlight, hover actions open/stop).
- `agents.tsx`: `graphRows` memo + legend row (Running/Idle/Done/Stopped/Stale); graph shown only when
  rows exist.
- Bug found in verify: `stoppable` declared as boolean but called `stoppable()` → "not a function" killed
  the whole page render; now a thunk.
- Verified via headless Chrome (`--headless --timeout` works; `--headless=new --virtual-time-budget`
  hangs) against vite :5199 proxying :7801. `npm run build` + `make build-server` pass; binary rebuilt
  with the graph embedded (server restart needed to serve it — not done unasked).

## 2026-10-26 — Agents graph rebuilt as a force-directed canvas (force-graph)

- Card-graph replaced: `agent-graph.tsx` + `board-graph.ts` left in tree UNUSED; new
  `web/src/components/agent-force-graph.tsx` + `.css` on `force-graph@1.52.0` (new web dep). Neo4j-style:
  status-tinted circular nodes with in-bubble labels (fixed 12px screen-space font, ellipsis-clipped to
  2.1r), curved edges + arrowheads colored by CHILD status, directional particles on running links, hover
  lights ancestor+descendant subtree and dims the rest, Portal'd hover card (Open session / arm→Stop).
- Gotcha: force-graph `accessorFn` props treat a plain STRING as node-property access —
  `nodeCanvasObjectMode('replace')` reads `node.replace` → undefined → custom paint silently skipped and
  the builtin `rgba(31,120,180,.92)` disc painter runs. Constants must ride functions:
  `.nodeCanvasObjectMode(() => 'replace')`.
- Gotcha: v1.52 PAUSES its d3 engine on every graphData set (kapsule update()) and nothing restarts it —
  call `graph.d3ReheatSimulation()` explicitly after each graphData, or the sim never ticks (no engineStop,
  static seed spiral).
- Gotcha: a hidden/background Chrome tab never fires rAF so an rAF-driven engine sits frozen —
  `.warmupTicks(240)` ticks the sim synchronously inside update() on every graphData set, so layout
  converges before any painted frame regardless (alphaDecay .032 → alpha ≈4.5e-4 after 240).
- Gotcha: post-convergence the engine burned ~600 idle ticks to the cooldownTicks floor;
  `.d3AlphaMin(0.005)` stops it at convergence.
- Physics recipe (final): charge `-(12 + r*1.5)` with distanceMax 120 (small reach separates families,
  compact bbox → zoomToFit zoom high); link distance 28; gather-force spring `n.vx -= n.x*0.04*alpha`;
  family-cluster seed (connected components: golden-angle-spiral centres 52·√ci, member ring 7√m + r·.66,
  singletons central cloud 11·√k); stable z-sort before graphData so running paints on top;
  `d3VelocityDecay 0.4`; settle-fit inside the gather force at gatherTicks>120 && alpha<0.012
  (onEngineStop kept as unreliable backstop).
- Verify: vite :5199 (`vite.config.verify.ts`, since deleted) proxying /api→:7801 + Chrome takeover
  screenshots on granted tabs; console via CDP (ring caps at 300 and stale entries persist — match by
  bundle `?t=` URL). `npm run build` passed; :7801 restart left to the developer.

## 2026-10-26 — Agents page graph redesign

- `/agents`: removed the stat cards (Running/Idle/Finished/Total), the filter box + "Finished N" toggle, and the bordered section card + 880px cap → header band + full-bleed graph (`h-dvh` flex chain; graph rows = `buildTree(agents())`, everything always shown; `summary()` kept for the caption only).
- `agent-force-graph.tsx`: nodes are now SMALL UNIFORM UNLABELED discs (`NODE_R = 6`, solid status hue + darker rim; zero `fillText`/`strokeText` left) — status is color-only (running accent / idle gray / stale amber / done green / stopped red). Hover tooltip (Portal) and zoom/pan kept; `counts` prop dropped; `d3ReheatSimulation`/`warmupTicks(240)`/`d3AlphaMin(0.005)` untouched.
- Latent bug found + fixed: `agent-force-graph.tsx` rendered `<Portal>` without `import { Portal } from 'solid-js/web'` → runtime crash on first hover. Pre-existing TS18048 in `positionTip` fixed (`hostEl?.getBoundingClientRect()` + early return).
- Verify: `cd web && ./node_modules/.bin/tsc --noEmit` clean; `npm run build` ✓ 16.45s; live visual via throwaway `web/vite.verify.config.ts` on :5199 (HOWTO recipe) — full-bleed canvas, color-only discs confirmed in granted tab.
- All three edited files (`web/src/pages/agents.tsx`, `web/src/components/agent-force-graph.tsx` + `.css`) are git-UNTRACKED — `git diff` shows nothing; review by reading the files. Shipping = `make build` (web/dist is embed.FS).

## 2026-10-26 — Edit tool: bare "old_string not found" now carries guidance; anchors taught as verbatim copies

- Measured on this repo's own history (`.ogcode/ogcode.db`): 5,278 edit calls, 338 errors = 6.4%, spread across ALL model families (glm-5.3-flash ~11%, deepseek-v4.1-flash 3.8-7.6%, gpt-5.6-luna 19% small n) — not one family's problem. Classes: 185 bare not-found (zero guidance), 43 stringified-edits shape, 34 ambiguous match, 32 whitespace-rescued misses, 37 user-cancelled, 7 parse-args oddities.
- After a failure the NEXT edit call failed again 342x vs succeeded 329x — models retry blind without re-reading. Diagnosed the bare misses from part JSON: 144/185 multiline, 57 with no leading whitespace anywhere — anchors RETYPED from memory instead of copied from a read window.
- Fix — no full worked JSON example added: the call shape is already stated in the schema, two prompt sections, and a tailored stringified-edits error; the dominant failure is byte-copying, not JSON shape. (1) `old_string` schema description now says copy-from-read / never retype from memory; (2) bare not-found (`notFoundHint` case 0) returns a re-read directive plus a first-line locator `firstLineMatches` (trimmed containment both ways, 4-char minimum line, >5 hits = generic); (3) Hard-rules bullet; (4) `parallelToolCallsPrompt` canWriteFiles paragraph. Pins: `TestEditTool_BareMissPointsAtTheReRead` + extended `TestParallelToolCallsPrompt_SameFileEditGuidance`.
- `TestEditTool_NotFoundHintsWhitespace` pins an invariant the new wording was built around: a genuine miss must not read as a whitespace complaint — the bare branches say "dropping every difference in spacing it still matches nowhere" and deliberately avoid "indentation".

## 2026-10-26 — End-to-end review of the working tree (browser bridge, timers, native host, web UI): 4 majors, ~22 minors, no tests broken

- Review turn ran 5 parallel teammates over the uncommitted change set (55 files, ~4.7k insertions + new browser/timer/nativehost areas); build/vet/tsc and 10 Go test packages plus both extension harnesses (34/34, 58/58) all green.
- MAJOR, confirmed by parent read: `internal/server/browser_routes.go` gates the bridge handshake on client-supplied `r.Host` (never `r.RemoteAddr`) and the pair GET has no peer gate — a LAN client can fetch the token then connect with `Host: localhost…` and take over the bridge slot (server binds all interfaces, no auth by design).
- MAJOR, confirmed by parent read: `internal/browser/bridge.go` `dropConn` never touches `b.pending` despite its doc "fails every in-flight op" — after a disconnect, pending browser ops hang until their 30/60s timers and surface "timed out" instead of `ErrNotConnected` (`swapConn` already has the right shape).
- MAJOR (extension, per stream): no `chrome.alarms` reconnect fallback (SW suspends during backoff and reconnect silently dies); `openSocket()` has no in-flight guard (double-connect orphans a live socket); a mid-flight `Disconnect` can be overridden by `onopen`/`autoPairThenConnect`; granted tab ids persist in `chrome.storage.local` across browser restarts while Chrome recycles tab ids (contradicts the STORE_LISTING claim).
- Dead code, confirmed by parent grep: `web/src/components/board-drawer-row.tsx` has zero importers after the `board-drawer.tsx` deletion; prune with it the orphaned `lib/board-tree.ts` exports (`decorateRows`, `DrawerRow`, `formatAge`).
- Minors worth the cheap fixes: read.go negative `end_line` silently means EOF + dead clamp/unused ctx/dead `readOutcome.display`; `schedule` refusal wording for negative `delaySeconds`; browser pair response reads `Token()` twice (can disagree with the URL after a rotate) and derives wss only from `r.TLS` (wrong behind a TLS-terminating tunnel); reminder notice renders dateless `15:04` (366-day cap); nativehost double `registryFor()` read + serial 750ms probes; extension `minimum_chrome_version` 114 but WS-keepalive needs 116; `tabLocks` never cleared on `onRemoved`; `setPanelBehavior` only in `onInstalled`; store zip `ogweb-1.1.0.zip` predates manifest 1.2.0.
- Optimizations flagged (correctness fine, fix if profiling warrants): extension JSON-cap loop re-stringifies the whole payload per popped node (~15 MB worst case per snapshot); sidepanel full re-render per tabs event (debounce); `teammateSegments` rebuild per part re-diff; `turnStepCount` O(n) rescan per append; `intentReminderSeen` map grows unbounded for never-planned sessions.
- Clean areas verified: timer store (migration 068 matches all queries, RETURNING fine on modernc sqlite, atomic claim), effort/flash hold + drainFinishedStream, Uncapped plumbing, filechange stamp ordering, server armStopCh move, native host framing/token security, route registration, force-graph changes, no dangling BoardDrawer references.

## 2026-10-26 — Review fix batch landed: 5 parallel streams, all green

- Streams (all integrated): web-prune ses_01M5VV96P7T443BKMZ70S7K01Q; go-tool-hygiene ses_01M5VV96PAQ00B3G2EXN5Y6AAA; ext-sw-lifecycle ses_01M5VV96PAQ00B3G2EXHZF4SAB; go-server-security ses_01M5VV96PCJ6B0K8TY586HXX25; go-agent-core ses_01M5VV96P8W0SDPCBG74ADYSEC. Parent additions at integration: `read_docx_page` in `explorationToolNames`; token-pill.tsx stale comment reworded.
- Gates green: `CGO_ENABLED=1 go build ./internal/... .` + vet; tests internal/{tool,agent,browser,nativehost} + HOME-isolated internal/server; harnesses native_pair **45/45** (was 34) + pipeline **77/77** (was 58); web `tsc --noEmit` clean; gofmt clean on all touched files. Nothing committed (working tree stays dirty by convention).
- MAJOR 1 (bridge security): `internal/server/browser_routes.go` gates pair GET (line 53), rotate (70), bridge (146) on the PEER — `isLoopbackPeer` = RemoteAddr → net.SplitHostPort → ParseIP → IsLoopback, fail-closed (172-184), `refuseForeignPeer` 403 (189-193); the old gate checked client-supplied `r.Host` (spoofable). Pair URL now honors `X-Forwarded-Proto` (wss behind a TLS edge); pair response reads the token once (a rotate between two reads could disagree with the URL). Pins: TestBrowserPairGateIsPeerBased / TestBrowserBridgeGates / TestIsLoopbackPeer / TestBrowserPairAdvertisesWssBehindATlsEdge. Stand-up E2E (fresh build on :7975, scratch HOME) 8/8 exact: LB pair 200 + 64-hex token; wrong-token bridge 401; XFP https ⇒ wss URL; LAN pair 403 with NO token in body; LAN + spoofed Host 403; LAN bridge with REAL token 403; LB-peer→LAN-dest 200 (control); LAN-peer→LB-dest 403.
- MAJOR 2: `internal/browser/bridge.go` dropConn now swaps `b.pending` out under the lock and fails everything with `ErrNotConnected` immediately (was: pending ops hung on a dead conn until their 30/60s timer). Pin TestBridgeDisconnectFailsPendingOpsImmediately.
- Extension hardening (browser-extension/ is git-UNTRACKED — verified via node harnesses only): chrome.alarms 1-min reconnect fallback (ALARM_RECONNECT + "alarms" permission — without it the SW suspends during long backoff and reconnect silently dies); minimum_chrome_version 114→**116** (114/115 drop the WS every ~30s); openSocket in-flight guard; connectEpoch (a mid-flight Disconnect wins over onopen/autoPairThenConnect); onStartup clears grantedTabs + tabLocks (Chrome reuses tab ids); tabs.onRemoved evicts; attachAttempts map so open DevTools ≠ our leftover session; setPanelBehavior at top level; navigate with both `tab` and `newTab` errors; JSON-cap binary search; sidepanel renderTabs 150ms debounce. **Manifest changed ⇒ user must reload the extension once in chrome://extensions; ogweb-1.2.0.zip regenerated at repo root.**
- nativehost: buildPairReply parses ports.json once (was twice); candidate probes concurrent, 8-worker bound, port-then-project order kept. Pins TestPairReplyProbesConcurrentlyWithinABound / TestPairReplyKeepsPortOrderWhenProbesFinishOutOfOrder.
- tool: browser.go — all 11 tools nil-dispatcher guarded via browserBase.status(); screenshot skip detection reads structured Metadata["connected"]==false (string sniffing removed); browser_status `tab` schema param dropped. read.go — end_line ≤0 refused (was: silently meant EOF), path TrimSpace'd once, dead clamp/unused ctx/dead display field removed. schedule.go:156 wording `delaySeconds must be a positive number of seconds` byte-identical to server/reminder.go:82 (pins tool/schedule_test.go:123, server/reminder_test.go:313); reminder notices render "Jan 2, 15:04".
- agent: explorationToolNames += read_pdf_page, pdf_index, docx_index, read_docx_page, memory_recall, project_memory_recall, check_syntax (PDF/recall-only steps no longer demote the next step mechanical); canSchedule() wired at the loop seam (slices.DeleteFunc + scheduleOffered); agent.ReminderCause shared with server (reminders keep their heading); intentReminderSeen now map[SessionID]time.Time + sweepIntentReminderSeen (24h TTL) at 3 turn-end sites.
- web: board-drawer-row.tsx deleted + lib/board-tree.ts dead exports pruned; formatDuration negative clamp; teammateSegments memoized; turnStepCount single backward sweep; .tp-led/.tp-state warn → var(--warning); stale drawer comments reworded (session-sidebar.tsx, token-pill.tsx).
- 404-during-E2E saga (not a bug — infra): a 2.5-day-old leaked pre-takeover server (`~/.local/bin/ogcode serve --port 7899`, PID 48395) still held 7899; new throwaway servers PORT-WALK ("port in use, trying next", server.go:806-816), so `/api/config` answered 200 from the ZOMBIE while `/api/browser/*` 404'd on its old routeset. Routes do mount unconditionally (routes.go:99-102; the browser mount is relative "/browser/…" inside /api). Lesson: **before any E2E, lsof the port — a live /api/config does not prove it is your build.** Killed 48395, 56377 (:7900), 56413 (:7897), 20514; host server (7801, PID 42234) untouched. /tmp/og-e2e binary left on disk.
- Left untouched deliberately: gofmt of pre-existing task.go/task_test.go/loop_browser_steer_test.go (developer's files, gofmt -l owns them, not this change); repo-wide `go build ./...` still broken by agent/skills/golang-cli/assets/examples imports (pre-existing; real build path unaffected).

## 2026-10-26 — MEMORY.md curated: takeover narrative compressed

- MEMORY.md was 26,139 B (> ~20 KB budget): the browser-takeover/native-host paragraphs compressed to the durable gotchas. Details that moved out already live in the 2026-10-25 entries above (pair flow, autoPairThenConnect story ~2221/3198/3476/3566, ToLower story 3528-3566, CWS facts 2135/3601-3603) and in the fix-batch entry above. Removed a malformed 2026-10-25 literal (`{"ok",\"version",\"servers"…` — stray backslashes, invalid JSON; the real reply is built by nativehost.buildPairReply). Flash-effort, edit-anchor and agent-graph lines tightened; every invariant + pin test kept. Result: ≤ ~21 KB.

## 2026-10-26 — Docs review fixes landed (4 parallel teammates + 1 orchestrated cleanup)

Findings came from a v0.51.0 accuracy review of the three public surfaces. Fixes:

- privacy.md:14 + landing FAQ: the false `OGCODE_POSTHOG` opt-out claim replaced with the verified truth (analytics always on, source: internal/server/server.go:776 "Analytics is always on; there is no user-facing toggle"; `OGCODE_POSTHOG_LOGS` is the only telemetry switch, logs-only opt-in). No server opt-out feature was added — follow-up opportunity.
- install.sh:117 + install.ps1 + the tracked `docs/install.sh` copy: `OLLAMA_BASE_URL=https://api.ollama.com/v1` → `https://ollama.com/v1`. Proof: resolveBaseURL passes an env-set value through verbatim (internal/cli/run.go:133-141, internal/server/server.go:1015-1023); PreferLiveOllamaEndpoint explicitly refuses explicit env values (internal/provider/ollama_detect.go:170-192); `api.ollama.com` appears nowhere in Go source. install.ps1 gained a Quick start examples block (Write-Host, `$env:` backtick-escaped).
- Landing page: version pill v0.39.1 → v0.51.0 (internal/version/version.go; the JS guard is a generic prefix match on data-release, no logic change); 4 dead `#remember-the-work` links repointed to real anchors (`#what-you-can-do-with-ogcode` on README / docs-site memory page); provider belt now shows the 6 catalog-first-class names minus logos (Anthropic/OpenAI/…-line: Gemini, DeepSeek, Mistral, NVIDIA, GLM, Qwen per internal/provider/models_catalog.go) and reworded to attach Groq/Cerebras/Together/SambaNova to the custom OpenAI-compatible slot.
- New docs page `docs-site/src/content/docs/agents-and-teammates.md` (~200 lines) + nav.ts registration, fully source-backed: teammate/task/board ops, file change gate incl. `/api/file-changes`, custom providers in ogcode.json (`ProviderConfig`: baseUrl/apiKey/type/model/label/disabled), one-liners for `schedule` and `update_intent`. docs-site `npm run build` green; Pagefind indexed the page.
- README.md: 13 em dashes removed (grep count 0 now), quickstart bullets for `ogcode run --max-turns` (0 = no limit) and `ogcode index`, Security bullet now points at GitHub Issues instead of a nonexistent SECURITY.md; docs/DEPLOY.md and docs/_config.yml em dashes gone; docs/_config.yml:2.
- internal/cli/run.go:52 agent help now lists all 9 GetAgent names (build, plan, task, breakdown, note, index, search, subagent, memory-recall, agent.go:646-667); `go build ./internal/cli/` green.
- `git mv docs/ARCHITECTURE_AUDIT.md plans/ARCHITECTURE_AUDIT.md` (repo-wide grep found no readers of the old path).
- Gotchas recorded in MEMORY.md: `docs/install.sh` is a hand-synced copy; docs-site rebuild regenerates `docs/docs/`.
- Not done (deliberate/deferred): no code-level analytics opt-out (feature request for a future turn); landing announce text "Now with live service preview" left (feature exists).

## 2026-10-26 — v0.52.0 released

- Logical feature commits (tools uncap/grep caps, schedule, browser takeover, provider belt, agents page, docs refresh) + `chore(release): v0.52.0` (2bfc25b); browser-extension/ + cws-listing/ committed into the repo per user decision.
- Tag push triggered 4 workflows, all green in 3–8 min: Release (7 platform assets), Publish Docker Image, CI, Deploy GitHub Pages.
- Remote is `prasenjeet-symon/ogcode` (SSH), not the mstorsjo fork guess.
- Leftovers flagged: legacy unused `web/src/agent-graph.tsx/.css` + `board-graph.ts` rode the wholesale web/src staging in e54e31a — remove in a follow-up if desired.

## 2026-10-26 — Session-level memory (per-conversation .md the agent curates)

- Added `<projectDir>/.ogcode/memory/sessions/<SessionKey>.md` — one file per conversation, keyed by the ROOT session (`sessionMemoryKey` resolves teammates to the parent via `ParentSessionOf`), so the whole agent tree shares it. Rendered into the system prompt as `<session-memory-md>` via `loadSessionMemory` (internal/agent/memorymd.go), read once at RunLoop turn start.
- `sessionMemoryPrompt` (internal/agent/prompt_builder.go) teaches the split: project MEMORY.md = what every future session profits from before it acts; session memory = only-this-conversation state; unsure → start session-level, promote on evidence, delete from the source file (a fact lives in exactly one place).
- Prompt builders' signatures grew by one `sessionMemory{relPath,block}` param (all four: buildSystemPrompt, buildSystemPromptForFamily, buildSystemPromptEntries, staticSystemPrompt); every test call site updated with `sessionMemory{}`. `mdWrapperTag` regex now also neutralizes `session-memory-md`.
- Root-session file deleted in `handleDeleteSession` after `store.Delete` (`memfile.DeleteSessionMemory`, ENOENT-tolerant). Env cap `OGCODE_SESSION_MEMORY_MAX_BYTES` (0 = uncapped).
- Pinning tests: internal/agent/session_memory_test.go (root key share, roundtrip/missing, block-forgery defense, prompt branches, projectScoped gating — utility agents get nothing).

## 2026-10-26 — Tool/guide accuracy audit (4 auditor + 2 fixer teammates)

- Audited every model-facing tool description (`internal/tool/*`), the prompt builders
  (`internal/agent/prompt_builder.go`, `agent.go`), and MEMORY.md / HOWTO.md / AGENT.md against current code.
- Core file/verification tool descriptions (read/edit/write/bash/grep/glob/file_map/check_syntax): all
  claims held, including read's bare-read map interception (no range AND no offset AND no limit).
- Fixed stale descriptions: deep_search (single-query pipeline, not parallel per-angle searches),
  web_search limit (>15 resets to 8, not clamped), browser_type `clear` param documented as ignored by
  the extension, ask_user options 2-6 (was 2-4), browser_click selector precedence + hover caveat,
  browser_scroll 600 px default, board roster includes background task children, memory_map scoped
  project-vs-session wording, read_pdf_page image-fallback trigger (<20 extractable words, text rides
  along), latex_pdf `.pdf` appended when missing, prompt_builder map escape-hatch clause, agent.go
  deep-research profile wording.
- MEMORY.md: 16 line-level corrections (mcp_<server>_<name>, POST /api/search/config, teammate Ask-gated,
  board migration split 065/067, browser-extension tracked since cbdf224, acquireIndex symbols,
  internal/docindex/excludes.go path, compactionKeepRecent clamp formula, planless-stint cadence, etc.).
  HOWTO.md: OGX-gateway entry rewritten for the migration-067 board tree (board_post/last_seq gone).
- Verified: `go test ./internal/tool/ ./internal/agent/ -count=1` pass after edits; gofmt clean on touched files.
- Pre-existing, unrelated: `agent/skills/golang-cli/assets/examples/*.go` don't compile under `go build ./...`.

## 2026-10-27 — ogweb published on the Chrome Web Store; store id allowlisted in the native host

- User confirmed the listing is live (publisher prasenjeetsymon); store-assigned id **`kacnfgnhmmnhdpokflkiemdehhjhkidp`** (read from the dashboard; deep_search cannot see CWS listings — the store is barely indexed and the ID lives only in the dashboard / detail URL).
- `internal/nativehost/nativehost.go:46` `WebStoreExtensionID` filled in (was `""`, reserved for exactly this). `CandidatesFor` now allowlists the store id on every boot-time `EnsureRegistered` manifest, so store installs auto-pair via the native host instead of falling back to manual token/origin paste.
- Symptom chain that motivated the fix: a store extension id appears in no manifest → `chrome.runtime.connectNative("com.ogcode")` refused → the extension's single native auto-pair attempt fails → manual pair UI.
- **No extension re-release needed** — extension code unchanged; the fix rides the next ogcode (server) release. Users must update ogcode and restart Chrome (manifests are read at browser startup); a failed `autoPairThenConnect` is memoized for the worker life, so a reload would also clear it.
- Pins updated: `TestEnsureRegisteredDoesNothingWithoutCandidates` replaced by `TestEnsureRegisteredWritesTheStoreIDForABareProject` (a bare end-user project entitles exactly the store id); the unpacked-id and env-id tests now expect the store id appended. `internal/nativehost` + `internal/cli` green, gofmt + vet clean.

## 2026-10-27 — v0.53.0 released

- The dirty working tree (session memory, tool-description audit, write/edit path refusals, ogweb store-id allowlist, sidebar roster cache, vite verify config) was split into 6 logical commits + `chore(release): v0.53.0` (3a4d60f). The working files' gofmt was clean; the three files gofmt flagged (loop_browser_steer_test.go, tool/task.go, tool/task_test.go) are pre-existing and untouched.
- Verified before tagging: `CGO_ENABLED=1 go build . ./internal/...`, `go vet` on touched pkgs, `go test ./internal/agent ./internal/tool ./internal/nativehost ./internal/memfile ./internal/server -count=1` all pass, `npm run build` succeeds.
- prompt_builder.go held one audit line inside the memory-feature file; split via `git apply --cached -R` of the single hunk so the memory commit doesn't claim it.
- Tag push triggered 4 workflows, all green in ~8 min: Release (7 platform assets), Publish Docker Image, CI, Deploy GitHub Pages. goreleaser's changelog body lists the conventional-commit messages verbatim (docs:/test:/chore:-prefixed subject filters apply, though the release and chore(web) commits still appear in v0.53.0's note — filters matched loosely there; harmless).
