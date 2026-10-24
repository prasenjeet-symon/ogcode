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
