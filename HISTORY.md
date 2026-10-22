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
