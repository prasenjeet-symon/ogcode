# Release Notes — v0.37.2

## Patch: Safer, more capable file editing

- Extends the `edit` tool with atomic multi-edit batches, `replace_all`, and `expected_count` safeguards.
- Rejects missing replacement text instead of interpreting it as an accidental deletion, while preserving explicit empty-string deletions.
- Improves ambiguous-match and whitespace-mismatch diagnostics so repeated workflow steps and similar anchors are easier to repair safely.
- Updates the coding-agent guidance and regression coverage to match the edit contract.

---

# Release Notes — v0.37.1

## Patch: Faster release and Docker workflows

- Builds the web UI once per release and reuses it across platform binaries.
- Builds Docker images natively for amd64 and arm64, smoke-tests the exact amd64 image before publishing, and creates multi-architecture version, major/minor, and `latest` tags.
- Adds architecture-specific BuildKit caches and passes the release version into Docker image builds.


## Major: Ogcode is now AGPL-3.0 — with a commercial track

Ogcode is **dual-licensed** from this release. The license changes from MIT to
the **GNU Affero General Public License v3.0**, alongside an **Ogcode Commercial
License** for organizations that cannot meet the AGPL's terms.

Nothing changes for most people. Running Ogcode on your own machine, for your own
work — at home or at a company of any size — carries no obligation, and private
modifications are never triggered. The obligations attach when you *give Ogcode
to someone else*, by shipping it or by putting it in front of users over a
network: AGPL §13 then entitles those users to the source of the exact version
you are running. Embedding Ogcode in a closed-source product, or running it as a
hosted service without publishing your changes, needs the commercial license.

- **[LICENSING.md](LICENSING.md)** is new: a plain-language table of which track
  applies to which use, and how to obtain a commercial license.
- **[CONTRIBUTING.md](CONTRIBUTING.md)** is new: contributions carry an inbound
  license grant (you keep your copyright; the maintainer may license it under
  both tracks), which is what makes the commercial track possible at all.
- **[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)** is new and lists bundled
  third-party code. One entry matters to commercial licensees: PDF rendering
  uses `github.com/gen2brain/go-fitz`, which vendors **MuPDF** and is itself
  AGPL-3.0. A commercial license covers Ogcode's own code and cannot grant rights
  to MuPDF — a commercial licensee needing PDF rendering must license MuPDF from
  Artifex or build without the component.

Releases up to and including **v0.36.1** were published under MIT and stay MIT; a
license change is not retroactive. The README, the landing page, and the
Homebrew/winget manifests all carry the new terms.

## Major: Scrcpy device panel

A new **Device** page (`/device`) streams a connected Android device straight
into the web UI — embedded ws-scrcpy behind ogcode's own `/scrcpy` proxy, with a
deep link that skips ws-scrcpy's device picker and autostarts the stream. The
player is chosen from what the browser supports (WebCodecs, then MSE, then the
WebAssembly decoders), and `?udid=` / `?player=` pin a specific device or player.
Devtools and file-browsing open as in-page dialogs over the same proxy.

Supporting this: `GET /api/scrcpy/status` reports whether ws-scrcpy is reachable
(with the target URL) and `GET /api/scrcpy/devices` enumerates devices from
`adb devices -l`. A missing adb, or none attached, is reported as an empty list
rather than an error. A status pill in the session header polls the endpoint, so
the page offers a start hint when the stream server is down instead of a blank
frame.

## Major: Control plane container mode (Incus)

The control plane can now isolate each assignment in its **own Incus
container** rather than a git worktree on a shared host. Configuring a
`master.incus` block switches the grain: assigning a user to a repo creates a
container from the configured image and profile, seeds it via cloud-init, and
lets the in-guest `ogcode-worker` register as an ordinary worker whose **id is
the container name** (`og-<repo>-<user>`, folded to DNS-label-safe form under a
40-byte budget) — so containers appear in the panel's URL table like any other
worker.

Readiness is the worker's `Register`, never an Incus operation: a placement
starts `provisioning`, flips `ready` when a worker with that id registers, and a
reaper fails placements still pending past the register timeout. The Repositories
page gains a placements table with create/destroy actions, and capacity is
capped by live container count. **Omitting the `incus` block leaves the
bare-worker flow byte-for-byte unchanged.**

`controlplane/scripts/incus/` ships the operator-side scripts (`build-image.sh`,
`assign.sh`, `unassign.sh`, the guest systemd units and profile) with a README
covering the run order. This mode is new and only partly exercised — the driver
and placement store are unit-tested against a REST fake, and the scripts were
verified on a real Linux box; treat it as early.

## Minor: Agent reliability

### Mid-stream connection resets are retried

A TCP reset after the response headers is not an error from `StreamChat` — it
arrives as an error event on an already-live stream, which the old retry loop
never saw. The loop now recognises a reset that lands **before any output**, and
re-dispatches the request run-wide (like the compaction budget, so a per-step
counter cannot reset itself). A stream that already produced text or a tool call
is deliberately *not* replayed — that would duplicate what is already on screen —
so it surfaces with a Resume button instead.

### IPv6 fallback

If two streams in one run die with an unreachable-class error against an IPv6
peer, provider connections stop using IPv6 for the rest of the process and say so
in the log. One failure is a blip; two is a pattern. It never falls back where
there is no usable IPv4 address, since that would turn an intermittent failure
into a total one. `OGCODE_FORCE_IPV4` overrides in either direction (`1` pins
IPv4 from the start, `off` keeps IPv6 and disarms the fallback).

### Stream idle timeout

The idle watchdog — how long a stream may go with **no data at all** — is now
configurable for slow local models whose prompt evaluation can outlast the
built-in budget. `OGCODE_STREAM_IDLE_TIMEOUT` takes a duration (`30m`), bare
seconds (`1800`), or `off`/`none`/`never`/`0` to disable it. A value below ten
seconds or one that does not parse is refused with a warning rather than honoured
into a stream that can never finish.

### System-prompt stability

The current date moved out of the base prompt into a separate `<system-reminder>`
entry at **day** granularity. This was previously harmless on Anthropic, where
the dynamic block already sat outside the cached prefix, but it silently
destroyed prefix caching on OpenAI and Ollama — the same provider, which joins
every system entry into one message at `messages[0]`. A byte-for-byte stability
test now pins the joined prompt across a simulated step boundary.

### Prompt-injection hardening

- **AGENT.md / MEMORY.md blocks** are neutralized before interpolation. A
  MEMORY.md that closed its own block and opened an `<agent-md>` one would
  inherit the instruction authority AGENT.md is granted — and since MEMORY.md is
  written by the agent every turn, that forgery would outlive the turn that
  planted it. Wrapper tags inside either document are defused, and a truncated
  document ends with an explicit marker rather than mid-sentence.
- **The deep-research pipeline** — the one place genuinely adversarial text
  arrives — now frames fetched pages as data for both the ranking and synthesis
  calls, requires actionable claims to stay attributed so the calling agent's own
  boundary rule can still fire, and defuses any forged source separator inside a
  page body.

### `deep_search` guidance is scoped to whether the tool exists

The external-knowledge section of the prompt is emitted only when a search
backend was actually built, and worded per role (a build agent unblocking itself
mid-change reads differently from a planner validating a library choice).
Previously six agents were told to reach for `deep_search` on endpoints that
never offered the call.

### Public-folder hosting narrowed to the build agent

The "drop files in `public/`" section now goes only to the build agent. It was
gated on "can write files", which named the **agent's** directory while the
served folder belongs to the **server's** — the same folder only for a session
running in the project itself. A task agent in a disposable worktree would write
the file, hand back a `/public/<name>` URL, and the user would get a 404 with
nothing logged anywhere.

## Minor: Headless runs match the interactive toolset

`ogcode run` now offers the same core tools as the server, through a shared
`tool.RegisterCoreTools`. It previously shipped without eleven of them —
including `codebase_map` and `file_map`, which the system prompt names under a
"Mandatory:" heading. Nothing failed when they were missing: no error, no
rejected call, the agent simply explored worse. Headless runs also get web search
on the same terms as the server, so the prompt's `deep_search` guidance is
followable.

## Minor: Turn memory

- **Topic labels.** The summary writer now emits a `Topics: a, b, c` line, which
  the indexer extracts into the turn index. `memory_map` renders each
  conversation as one collapsed line carrying its most frequent topics — the same
  folder-label contract `codebase_map` uses.
- **`memory_map` reshaped** to match `codebase_map`: conversations collapse to one
  line at project scope, `subdir` drills into a conversation, and `topic` filters
  by label substring. The stored heading outline and title column were written
  but never read, and are dropped.

## Minor: Skills

A skill can declare the environment variables it needs:

```markdown
---
name: deploy
description: Ship the service to staging. Use when asked to deploy.
requires: DEPLOY_TOKEN
---
```

Loading a skill with a missing variable is refused and names it, rather than
handing over instructions that cannot run. Values come from the environment
ogcode was started from or from a `skills.env` block in `ogcode.json` — a real
environment variable always wins.

## Minor: Context settings, per project

A new **Context** group on the Settings page holds settings that belong to one
project rather than the user as a whole, stored in that project's own database
(one row; the DB is per project). The first is a switch for `compact_context`.
It applies from the next step of the next turn — the loop re-reads it every
step — so there is no restart and no session to reopen.

## Other changes

- `compact_context` is offered to every read-capable agent on every provider; it
  no longer depends on whether an endpoint caches a repeated prefix, and the
  observer that resolved that verdict (and its `model_cache_support` table) is
  removed.
- The remaining vestiges of the graph/embedding memory system — its per-session
  token-savings counter and the SSE event that fed it — are gone.

---

# Release Notes — v0.36.1

## Patch: Docker image build fix

The v0.36.0 Docker image never published: the main module's new
`replace ... => ./controlplane` directive made `go mod download` (which runs
after copying only the root `go.mod`/`go.sum`) fail — the replaced module's
`go.mod` wasn't in the layer yet. The Dockerfile now copies
`controlplane/go.mod` and `go.sum` ahead of `go mod download`. GitHub binaries
were unaffected; this release exists to publish the Docker image
(`ghcr.io/prasenjeet-symon/ogcode:latest` and `0.36.x` tags).

---

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
