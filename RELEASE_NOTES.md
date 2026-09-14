# Release Notes — v0.36.0

## Major: Remote Agent Workers & Control Plane

### Remote agent workers

`ogcode worker` turns a machine into an agent worker that serves workspaces from
a control plane over ConnectRPC/HTTP2:

- Workspaces are auto-discovered via `git worktree list`; each worktree hosts a
  full standalone ogcode server **in-process** (loopback-only, context-driven,
  no browser).
- The control plane mints global session ids, routes sessions to worktrees,
  and reverse-proxies each worktree's UI over multiplexed tunnels keyed
  `<workerID>-<worktreeLabel>` — the same web UI, reachable through the tunnel.
- Robust connect semantics: capped-backoff reconnect, re-pair fallback, and a
  persistent worker token (`~/.ogcode/worker-cred`, `~/.ogcode/worker-id`), so
  restarts resume rather than re-register.
- Pairing is secret-based, with the secret passed via file or environment only —
  never on the command line.
- Git bootstrap: workspaces absent on the worker are cloned from origin
  automatically before serving.
- Permission gating and approval travel over RPC; sessions interrupted by a
  worker restart are recovered on boot.

### Control plane (standalone daemon)

The control plane ships as a separate binary, `ogcode-control-plane`
(`controlplane/` module):

- Per-employee accounts (bcrypt + HMAC cookie sessions), operator console at
  the apex listing workers and their workspaces.
- **Live session monitor** — `/sessions/<id>` page with an SSE event feed,
  real-time status pill, and the start-session banner linking straight to it.
- **Multi-user repo assignment** — `EnsureRepo`/`EnsureUserWorktree` placement,
  worktree-per-user on `user/<name>` branches, auto-clone on the worker with
  the most free space, per-user workspace allowlists, and a console users
  page. (Repo lifecycle — merge back / deprovision — is still pending.)

### Public file hosting

Every server now auto-creates `<workspace>/public/` and serves it at
`/public` with `Last-Modified`/`Range`/304 revalidation and `HEAD` support.
Agents are told to drop downloadable artifacts there and return
`/public/<filename>` URLs, so files a build or report produce are directly
fetchable by the browser without an API round-trip.

## Minor: Reliability & UX

### Keep-awake

A reference-counted macOS display-sleep assertion is held for the duration of
each agent turn, so the screen and system stay awake while a generation is
running. No-op on other platforms or when `OGCODE_NO_KEEP_AWAKE` is set.

### Port memory

`~/.ogcode/ports.json` remembers the port each project's server used last.
Explicit `--port` always wins and is recorded; known projects reuse their
remembered port; new projects get a suggested unclaimed port, with the
actually-bound port recorded at listen time.

### Runaway-compaction fixes

- Compaction is now budgeted **per run** (max 2 per RunLoop, shared across the
  proactive and reactive paths) — previously the budget reset every step,
  letting a stuck conversation compact over and over.
- The proactive compaction watermark advances with the kept slice anchored on
  an assistant message, so a narrowed history never orphans tool results.
- `llmCompact` keeps the turn prompt verbatim ahead of the tail, fixing silent
  no-op compactions on turn-scoped history that burned retries without
  shrinking the request.

### Server lifecycle

`Server.Serve(ctx)/Stop/Port` and `Options{NoBrowser, Loopback, OnListen}`
extracted from `Start` so hosted servers (worker-hosted worktrees) are
context-driven and loopback-only. Interactive `ogcode serve` behavior is
unchanged.

### Host sessions

Sessions started from the console are recorded as host sessions and resumable
from the local machine that started them.

### Deploy

`deploy/cloudflared/ogcode-dev.yml` — named tunnel config for the local dev
server (`ogcode-dev.ogcode.xyz`).

---

# Release Notes — v0.35.0

## Minor: Learned Context Windows

### Learned context windows from overflow errors

When a model's catalog doesn't report a context window (Ollama local models,
dynamic OpenAI-compatible endpoints), the agent loop now **learns** it the
first time the provider rejects an oversized prompt: the cap figure is parsed
from the error body ("maximum context length is 8192 tokens",
"prompt is too long: 195000 tokens > 200000 maximum", …), sanity-checked
against the size of the request that just failed, and persisted with the
model's capability record (migration 038). Every later run sizes compaction
from the real window instead of the fixed 128k fallback — so small local
models compact early instead of erroring, and large ones use far more of
their window. The manual "refresh capability" action clears the learned
figure along with the image verdict.

### Real context windows from model catalogs

- **Ollama cloud catalog** now fills each model's window from the host's
  `POST /api/show` (`<arch>.context_length`), bounded (3s per lookup, fan-out
  8, ≤40 lookups, silent failure → 0). Previously the `/api/tags` catalog
  carried no window data at all.
- **OpenAI-compatible `/models`** now parses `context_length` (number or
  string, e.g. OpenRouter); absent → 0, never guessed.
- Static fallback lists carry real probed windows for known models.

### Fix: session token totals no longer double-count cache reads

Session/CLI totals (`ogcode run` usage summary and the UI token pill)
previously summed `cacheRead` on top of input, re-counting the same context
prefix once per turn. Totals are now input + cacheWrite + output. Cache
write stays — providers that report it never count it inside input.

---

# Release Notes — v0.34.0

## Major: Agentic Turn-Memory, MCP & Skill Management UI

This release rebuilds the agent's memory from the ground up. The graph +
embedding "agentic memory" subsystem is gone, replaced by **turn memory**:
after each completed turn a small model writes a dated, structured markdown
**memfile** under the project's `.ogcode/memory/`, indexed in the turn index.
The model now sends **only the current turn** to the provider — older context
is reached on demand through memory tools backed by a read-only recall
sub-agent. That cuts per-step token cost and lets the agent recall exactly what
it needs. This release also adds a Skills & MCP Servers management surface to
the settings UI.

### Turn memory (replaces the graph/embedding system)

- **Turn summaries.** After each turn, a "memory scribe" summarises the turn
  into a tightly-structured markdown file (H1 title + Request / What was done /
  Key files & symbols / Outcome sections) with YAML frontmatter. Filenames sort
  chronologically and carry the session tag and title.
- **Turn index.** The `memory_turn_index` table is a cheap incremental index
  over those files (one row per dated turn file, with a heading outline + line
  ranges), so recall can browse chronological memory without any embedding
  lookup. The old `memory_config` settings table is dropped.
- **Current-turn-only routing.** The model's message history now sends just the
  current turn on the wire. Previous turns live on disk as summaries.
- **Continuity.** A bare follow-up still has context: the previous turn's final
  response is reinjected as a `<previous_response>` tag at the top of the first
  user message. Anything older is pulled in on demand via recall.
- **Memory tools.** `memory_map` lists indexed summaries newest-first with each
  file's outline (the memory analogue of `codebase_map`); `memory_recall`
  answers against the current session, `project_memory_recall` against the whole
  project (optionally scoped to the session). Recall scope comes from the
  context, never the model. All delegation to a read-only recall sub-agent.
- **Toggle** via the `OGCODE_TURN_MEMORY` env var (default ON).

### Skills & MCP management in the settings UI

- **Skill permissions.** A project's `ogcode.json` can now hold
  `skills.permissions` rules mapping a skill to `allow` / `deny` / `ask`,
  merged across global and project config. The Skills settings page lists every
  discovered skill with a toggle: switching one off writes a `deny` and drops it
  from the agent's prompt on the next turn — no restart needed. `GET /skills`
  and `POST /skills/{name}` back the page.
- **MCP server toggles.** MCP servers gain an explicit `disabled` flag so you
  can turn one off without deleting config or OAuth tokens. The new MCP Servers
  settings page lists every configured server with its live connection status
  (transport, scope, auth class, connected, tool count) and a switch. Toggling
  writes to the project `ogcode.json` and instantly registers/removes the
  server's tools from the agent's toolset. `GET /mcp` and `POST /mcp/{name}`
  back the page; the list endpoint never exposes headers or tokens.

### Benchmark harnesses

`bench/` now documents and ships adapters for three SWE benchmark harnesses to
evaluate ogcode headlessly: **DeepSWE** (Pier/Harbor), the **Aider Polyglot**
runner, and **SWE-bench Lite** (`swebench_runner.py`, `swebench_hardest.py`,
`run_swebench_amd64.sh`).

---

# Release Notes — v0.33.0

## Minor: Desktop Notifications, Geist UI, Deployment & First-Boot Fixes, Benchmark Harness
