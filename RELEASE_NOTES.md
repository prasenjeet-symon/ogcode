# Release Notes — v0.31.1

## Patch: PostHog Analytics on the Website, Nav-Tool Output Disclosure Fix

A small, focused release: the marketing site (ogcode.xyz) gains product
analytics via PostHog, and the web UI stops offering a dead disclosure control
on agent-navigation tool output.

### PostHog on the marketing site

`docs/index.html` now loads the official PostHog JS snippet — the same project
token as the app, `api_host: 'https://us.i.posthog.com'`, defaults snapshot
`2026-05-30` (autocapture on, pageleave, pageviews). One PostHog project, two
surfaces: site traffic is separable in PostHog by filtering `$current_url` to
`ogcode.xyz`.

The product itself remains telemetry-free; the snippet measures the marketing
site only, and a comment at the snippet states this explicitly.

Named events instrumented via an ES5-safe `phEvent()` helper (try/catch +
type-guarded so a blocked loader can never throw into page code):

| Event | Properties | Trigger |
|---|---|---|
| `download_open` | — | Download panel opened |
| `install_tab_select` | `platform` | mac/linux/win install tab |
| `install_command_copy` | `platform` | Install command copy button |
| `faq_expand` | `question` | FAQ entry opened (close ignored) |
| `github_link_click` / `discord_link_click` | `href`, `location` | Delegated click listener; `location` is the containing section |

Verified end-to-end in headless Chrome: snippet loads, all six named events
fire with the exact expected properties, autocapture fires on outbound links,
zero console errors.

### Web UI: nav-tool output no longer offers a dead disclosure

`codebase_map` and `file_map` output is how the agent orients itself in the
codebase — dense labeled trees, not prose a user reads. The tool status row for
these two tools keeps its duration/line-count summary but no longer renders a
chevron or accepts clicks to expand a disclosure that would only bury the
summary under the full tree. Output is still fully available to the agent.

### Migration

None. Analytics affect only the static docs site, and the UI change is a
read-only rendering tweak for two tool kinds.

# Release Notes — v0.31.0

## Minor: HTML & CSS Codemap Support, Bounded codebase_map Output, Exit-Reason Surfacing

This release widens the codemap's language coverage to **HTML and CSS** —
file_map and codebase_map now parse stylesheets and markup the way they parse
Go, TypeScript and Swift — and bounds codebase_map's output so a big repository
renders a few KB instead of tens of KB per call. The release pipeline also
switches its Linux cross-builds to a pinned zig toolchain.

### HTML and CSS in the codemap

Two new tree-sitter grammars (`internal/codemap/queries/html.scm`,
`internal/codemap/queries/css.scm`, backed by the new
`tree-sitter-html`/`tree-sitter-css` packages) extend symbol extraction:

- **HTML**: elements are named by `id` attribute → first class word → tag name,
  so `<div class="card">` is indexed as a `card` symbol and repeated matches
  are deduplicated.
- **CSS**: rules are indexed by their collapsed selector text; `@keyframes`
  blocks by their name; `@media`/`@supports`/`@import` by the first word of
  their first line. Patterns are deliberately unanchored so rules nested
  inside media or keyframes blocks still surface.
- Comments immediately above an element or rule become its doc line, matching
  the behavior of every other supported language.

`file_map` and `codebase_map` pick both languages up automatically — no
configuration change. Coverage is pinned by `TestFileMapDescription_NamesEveryParsedLanguage`.

### codebase_map: folder-aggregated summaries

Directories with more than ten indexed files now render as a single line —
top labels by frequency plus a file count — instead of listing every file.
Drilling in with `subdir` expands one level at a time. On a 332-file tree the
map output dropped from ~13 KB to ~1.2 KB, and a 2,000-file repository now
renders a few KB bounded by its folder count rather than its file count.

### Agent-loop exit reasons surfaced

Interruptions and exits that were previously logged only on the agent side now
propagate a structured reason to the client, so a failed run is diagnosable
from the session record instead of a silent stop.

### Release toolchain

`.github/workflows/release.yml` Linux builds now cross-compile C with zig
0.16.0 (sha256-pinned) instead of `gcc-aarch64-linux-gnu`, covering all
architecture pairs with one toolchain. Untracked `docs/` fonts and media are
also now tracked so the docs site ships complete.

### Migration

None. Codemap additions are opt-in by file extension, `codebase_map` output is
informational, and no configuration keys changed.

# Release Notes — v0.30.0

## Minor: Relative Relevance Selection for Semantic Memory Recall

This release fixes the way agentic memory decides what is relevant. Recall
selection is now **relative to the query's own score distribution** instead of
an absolute cosine floor, which with the bundled embedder was admitting
effectively every fact in the store — recall looked filtered but paid the
whole session into the prompt every turn.

### The problem: an absolute gate that admitted everything

Session recall (`internal/memory/graph.go`) scored every fact with cosine
similarity and kept anything above `0.1`. But normalized sentence embedders do
not spread similarity across 0→1: `gte-small`, the bundled model, puts two
*unrelated* English passages at roughly 0.72–0.85, and the median similarity
between facts from different sessions in a real store measured **0.78**. A 0.1
floor therefore filtered out ~1% of the corpus. The "semantically filtered"
tree came back byte-for-byte identical to the full tree, the recall limit of 50
never cut anything because most sessions have fewer facts, and the synthesis
LLM saw the entire session — then paid for it again in refinement rounds.

The same pattern existed in project recall (`internal/memory/project.go`):
limit 60, per-session cap 8, absolute floor 0.1 — "most of the project,
truncated to fit".

### The fix: selection relative to the candidate pool

New in `internal/memory/selection.go`, shared by session recall
(`BuildLightweightTree`) and project recall (`scanProject`):

- **Adaptive gate** — a fact must score at least `z = 1.0` standard deviations
  above the candidate pool's mean cosine, which self-calibrates per query and
  survives an embedder swap. The gate is skipped for pools of fewer than 5
  facts, where mean and stddev describe noise and the top-K cap suffices.
- **Sanity floor, not relevance filter** — `minUsableCosine = 0.1` now only
  drops broken vectors (zero-length or corrupt embeddings score near 0 against
  everything).
- **Recency as a normalized tiebreaker** — applied once, after scores are
  normalized into [0,1] across the surviving pool, never folded into raw
  cosine. A recent weak match can no longer evict an older strong one; project
  recall's exponential decay (45-day half-life now lives in the tiebreaker) and
  session recall's order-based recency both go through this path.
- **Limits sized to actually cut** — session recall default 50 → 8, project
  recall 60 → 12, per-session cap 8 → 4 (a third of the limit so at least three
  conversations can still reach the answer).
- **Gate observability** — recall stat lines now log `facts_scanned`,
  `scored`, `mean_cosine`, `stddev`, `cut`, and `gate_applied` so a cut that
  over- or under-fires is visible in the logs rather than silent.

### Migration

None. The selection statistics are computed at recall time over whatever
embeddings the store already holds; existing memory databases work unchanged.
If you have swapped embedders, the z-score gate re-calibrates automatically —
no re-embed and no threshold retuning required (re-embedding after a model swap
remains necessary for vector-space compatibility, as before).

# Release Notes — v0.29.0

## Minor: MCP Client with OAuth, Built-in Web Search, Prompt Caching, and Vision-Safe Sessions

This release connects ogcode to the external tool ecosystem with a full
**Model Context Protocol client** (stdio and HTTP transports, automatic OAuth),
replaces the install-a-Node-server web search with a **built-in backend that
presents a real browser's TLS fingerprint**, and lands a batch of session
correctness work: Anthropic prompt caching, tool-result images that can no
longer break a non-vision model, and redacted thinking blocks that survive a
round trip.

### Model Context Protocol (MCP) client

ogcode now connects to MCP servers declared in `ogcode.json` under the new
`"mcp"` key and exposes their tools to the coding agent alongside its own
(`internal/mcp/`, `internal/config/config.go`):

```json
{
  "mcp": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
    },
    "calcom": { "url": "https://mcp.cal.com/mcp" }
  }
}
```

- **Transports** — a server is a local subprocess (`command` + `args` + `env`,
  stdio) or a remote endpoint (`url`, streamable-http). `transport` is
  inferred when omitted. Modern HTTP servers get **automatic OAuth**
  (authorization code + PKCE + dynamic client registration): the first 401
  opens the browser, tokens persist in `~/.ogcode/mcp-tokens/`, and restarts
  refresh silently. A server configured with static `headers` keeps the
  bearer-token path instead.
- **Tools** — each server's tools are adapted into the registry as
  `mcp_<server>/<tool>` and offered to the agent through a `mcp_*` glob;
  `Registry.ForAgent` and `Agent.HasTool` both expand it, so a tool that is
  offered is also authorized at call time. Image results are written to a
  temp file with a `view_image` hint instead of being inlined into context.
- **Lifecycle** — servers dial in parallel in a background goroutine after
  the HTTP listener binds (the UI is live while an OAuth prompt waits), the
  registry itself is mutex-guarded against that concurrent registration, and
  every stdio subprocess is reaped on shutdown in both the server and the
  headless `run` path.
- Image-rejection 400s from a server are classified separately — see below.

### Built-in web search (search bridge removed)

The Node.js + Playwright `search-bridge` is gone — no install step, no
Chromium, no extra process. The backend is compiled into the binary and
**on by default** (`internal/search/`, `tools/search-bridge/` deleted):

- **Native HTTP backend** — searches Google, DuckDuckGo, Brave, Bing and
  Yahoo over plain HTTP (~1s), then fetches and extracts page text with
  goquery + go-readability.
- **Browser persona** — requests present a real browser's TLS ClientHello
  via uTLS along with the matching User-Agent, client hints and header set,
  a cookie jar, and referrer/randomised-pacing behaviour, because search
  engines block clients that do not. This is what the old bridge existed to
  provide.
- **Safari fallback (macOS)** — when the fast path returns nothing (an engine
  refusing the IP, a bot challenge, a script-only page), the query is retried
  through a real Safari via AppleScript. It only runs after the HTTP path has
  already failed, so a user who never hits a block is never asked for
  Automation permission. Compile-time gated behind `//go:build darwin`.
- `OGCODE_SEARCH_ENABLED` now wins in either direction for scripted/CI runs;
  the settings toggle decides otherwise. The unused `use_real_profile`
  setting is dropped from the stored config.

### Anthropic prompt caching

`internal/provider/anthropic.go` sends `cache_control: {type: "ephemeral"}`
breakpoints — one on the last tool definition, one on the first (static)
system block — so the tool schema and base system prompt stop being re-billed
on every request. The system prompt is split so the cacheable prefix stays
byte-identical across turns: the date, viewport and compaction summary ride
in trailing dynamic blocks. Cache reads/writes are surfaced in the token
usage the UI already shows.

### Vision-safe sessions

A tool result carrying an image used to be forwarded to the provider
unconditionally, so a non-vision model (or an OpenRouter-routed endpoint that
rejects tool-result images) would get a fatal 400. Now:

- `convertMessages` strips tool-result images whenever the resolved model
  lacks vision, for both the current turn and persisted history.
- An image-rejection 400 is detected by body hints
  (`APIError.IsImageRejection`) and classified as a new
  `InterruptModelCapability` interruption — **resumable** instead of fatal.
  Resuming invalidates the cached capability, re-probes, and retries without
  the images.

### Reasoning correctness

- **Redacted thinking blocks** are rendered back with their own type and
  `data` payload, not as a signed thinking block or dropped — the Anthropic
  API checks the sequence against what the model generated and 400s otherwise
  (`anthropicThinkingBlock`).
- **Explicit thinking mode** is requested per model from the catalog
  (`"adaptive"` for Claude 4.6+, nothing where a safe configuration cannot be
  sized). Providers without a reasoning mode ignore it.
- Stored reasoning blocks are bounded at 50k chars so a runaway thinking
  stream cannot flood the DB or the UI.
- A bare empty-body 400 now counts as context-overflow **only from Ollama**,
  the one endpoint that answers overflow that way — other providers' 400s are
  no longer mislabelled as "context too big".

### Memory

- **Enrichment before embedding** — a fact's summary is now the text that gets
  embedded, not the raw ~20 KB turn trace, whose vector mostly averaged the
  signal away and overflowed the embedder's window (`internal/memory/graph.go`).
- **Failed lookups are no longer silent** — `RecallMemory` and
  `RecallProjectMemory` return an error instead of an empty string, and the
  tools tell the model "lookup failed" rather than "nothing found" — these
  are different answers.
- **RefreshAll is detached from the HTTP request** — re-embedding a large
  graph takes minutes, and a browser that gave up first used to leave the
  corpus half-embedded (`internal/server/memory_routes.go`).
- The in-process **local embedder** serializes inference (the Go backend is
  not goroutine-safe), releases the model after an idle window, and skips
  nameless/blank concept nodes.
- Tool-pairing integrity across compaction and interruption edges is pinned
  by new tests; the read-pressure tracker nudges a long reading phase toward
  `compact_context` on uncached endpoints.

### Resource monitor and settings redesign

- New `internal/resource` sampler reports the ogcode process's own CPU and
  memory on a rolling four-minute window, served over `/api/resources` and
  pushed as SSE control frames (not bus events — periodic telemetry must not
  burn sequence numbers and force client resyncs).
- **Skills settings page** lists installed skills with name, description and
  source (`/api/skills`).
- Settings screens are rebuilt on a shared iOS-style kit (`ui.tsx`):
  models, about, and general pages included.
- **Delivery ticks** on prompts — sent / delivered / answered, read from the
  assistant message the loop created, plus TTFT moved ahead of the copy
  button in the message footer.
- Transcript scrolling now distinguishes the user's own scroll from trackpad
  momentum and follows new content only while genuinely at the bottom.

### Other

- **`task` sub-agents no longer have a hard 300s wall clock** — cancellation
  still propagates, and the timeout class of false-negative investigations is
  gone.
- `ogcode.json` is now ignored by this repo's own `.gitignore` (it holds
  per-machine API keys), and the machine-local `MEMORY.md` is untracked —
  it is re-sent in full on every model request, so a committed one becomes
  every contributor's per-turn token cost.
- The bundled `keys.json` free-pool file is unchanged. Installers and the
  website drop the bridge install steps; the GoReleaser archive and Homebrew
  caveats no longer mention Node.js.
- `golang.org/x/oauth2`, uTLS, goquery, go-readability and the MCP Go SDK
  (`v1.7.0`) land as direct dependencies.

---

# Release Notes — v0.28.0

## Minor: Auto-Mode Security Hardening, Denied-Tool Status, and UI Polish

This release closes two security gaps in Auto mode's risk classifier, hardens
the LLM risk-check verdict parser so it can no longer be talked into auto-approving
a dangerous command, introduces a first-class "denied" status for permission-
blocked tool calls, and adds several web-UI refinements.

### Security: bash command-segment splitting

The Auto-mode bash risk classifier splits a command line on shell chaining
operators (`&&`, `||`, `;`, `|`, newlines) so it can judge every segment
independently. Two bypasses in the old splitter are now closed
(`internal/permission/risk.go`):

- **Bare inline `&`.** The splitter only matched `" & "` — space-delimited on
  both sides — so `echo hi&rm -rf /` (no space after the `&`) stayed one
  segment, was judged by its first word (`echo`), and the destructive tail ran
  in the background without a prompt. A new `replaceBackgroundAmp` walker splits
  on background `&` everywhere except where it is part of an fd-dup redirection
  (`2>&1`, `>&2`, `&>file`), which must stay intact so a safe reader like
  `cat a.txt > /dev/null 2>&1` stays `RiskSafe`.
- **Newlines** were already split in v0.27.0; this release adds the regression
  test that pins it (`TestClassifyBash_BareBackgroundAmpSplits`).

### Security: LLM risk-check verdict parser

When the rules classify a bash command as `RiskUnclear`, Auto mode escalates it
to a quick LLM call that is asked to reply with exactly one word: `SAFE` or
`ASK`. The verdict parser used `strings.Contains(up, "SAFE")`, which matched
`"NOT SAFE"`, `"not safe"`, `"UNSAFE"`, and `"It is safe"` as substrings and
auto-approved all of them. The parser now requires the trimmed, uppercased
verdict to **be** `"SAFE"` (allowing trailing punctuation) via `isSafeVerdict`
(`internal/agent/loop.go`). Anything ambiguous defaults to `RiskAsk` — fail
safe. `TestIsSafeVerdict` pins the strict match.

The LLM risk check now also uses the loop's resolved model ID rather than
`sess.Model`, so it resolves the same provider the loop is using instead of
falling back to an arbitrary first provider when the session has no model
pinned.

### Denied tool-call status

A tool call blocked by the permission gate used to be recorded as a completed
call with a denial message, indistinguishable from a normal completion in the
UI and DB. There is now a `ToolDenied` status (`session.ToolStatus`) and a
`Result.Denied` flag (`tool.Result`), and the loop records the blocked call
under the new status. The web UI renders it with a distinct amber "denied"
badge and icon, and auto-collapses it like a completed/error call
(`web/src/components/message-item.tsx`, `web/src/api/client.ts`).

### .gitignore auto-management for `ogcode.json`

The per-project `ogcode.json` config file holds local connection overrides —
API keys, base URLs — that vary per machine and should not be committed. When
`config.EnsureProjectFile` creates the file, it now extends an **existing**
`.gitignore` to ignore `ogcode.json` via the new `gitignore.AddPattern`. It is
idempotent, fixes a missing trailing newline before appending, and never
creates a `.gitignore` on its own — a project with no `.gitignore` is left that
way (`internal/gitignore/gitignore.go`, `internal/config/config.go`).

### Web UI

- **Memory dialog** redesigned: design-token theming (CSS custom properties
  instead of hard-coded color classes), a variant-driven chip and header icon,
  a single accessible dialog with `role="dialog"`/`aria-modal`, and a cleaner
  layout (`web/src/components/memory-dialog.tsx`).
- **Doc index** pre-fetches git status on mount so the changed-files badge
  populates without opening the changes panel first, and shows a spinner during
  the initial fetch instead of flashing "Not a git repository" before the API
  responds (`web/src/pages/docindex.tsx`).

### Other

- Dropped the bundled free-pool Groq provider (the shared `gsk_` key was revoked
  and emitted a non-200 warning on startup). User-configured Groq is untouched:
  OpenAI-compatible baseURL detection still labels `groq.com` traffic, and the
  schema still lists Groq as a user-addable provider. OpenRouter is now the
  recommended free-pool default (`internal/provider/freepool.go`,
  `internal/provider/provider.go`).

---

# Release Notes — v0.27.0

## Minor: Skills System, Memory Maintenance, and Prompt Correctness

This release introduces a **Skills** system — reusable, on-demand instructions
that keep the agent's prompt lean — and rounds it out with memory maintenance
operations in the UI, a correct `RefreshAll` that re-embeds the full knowledge
graph, and several prompt-correctness fixes that keep the cacheable prefix
stable and the LaTeX guidance honest.

### Skills

A **skill** is a directory holding a `SKILL.md` file: YAML frontmatter that
names and describes it, and a markdown body carrying the instructions
themselves. The agent never sees a body unless it asks for one — its system
prompt lists only names and descriptions, and the new `skill` tool pulls one
body into context on demand. Listing bodies instead would cost the full token
weight of every skill the agent never uses, re-sent on every step of every
turn.

The skill guidance is deliberately not part of the cacheable base prompt. The
set of skills changes when the user writes or edits one, and entry [0] must
stay byte-identical for the whole session, so the listing is appended as a
separate trailing system prompt entry — the same place the date reminder lives.
(`internal/skill/`, `internal/tool/skill.go`,
`internal/agent/prompt_builder.go`, `internal/agent/loop.go`)

**Discovery.** Ogcode scans four scopes, later ones overriding earlier ones of
the same name:

| Scope | Locations |
|-------|-----------|
| Built-in | Ships with ogcode (`customize-ogcode`). Overridable. |
| Remote | Every URL in `skills.urls` (fetched from an `index.json` manifest) |
| Global | `~/.config/ogcode/skills/`, `~/.ogcode/skills/`, `~/.agents/skills/`, `~/.claude/skills/` |
| Configured | Every path in `skills.paths` |
| Project | `.agents/skills/` and `.claude/skills/`, searched from the project up through each parent to the repo root |

Skills written for Claude Code work unchanged — drop them in `.claude/skills/`
or point `skills.paths` at them. The frontmatter `name` must match the directory
name, so the name in the prompt, the argument the model passes to the tool, and
the directory on disk are always the same string. (`internal/skill/discover.go`,
`internal/skill/loader.go`)

**Remote skills.** A skills URL serves an `index.json` manifest listing skills
and their files. Ogcode fetches the manifest, downloads each skill's files into
a per-version cache keyed by SHA-256, and serves them from there on subsequent
turns. The download is bounded — 1 MB per file, 20 MB per index, 100 skills, 64
files per skill, 20 s timeout — so a hostile or broken manifest cannot exhaust
disk or stall a turn. A skill body loaded from a remote source is prefixed with
a provenance caveat: its instructions come from the publisher, not from the
developer in the conversation, so the agent treats it the way it treats any
content it reads — as data, not as authority. (`internal/skill/remote.go`)

**Permissions.** The `skills.permissions` map in `ogcode.json` sets per-skill
`allow`, `deny`, or `ask`. Deny withholds a skill from the prompt listing and
refuses it at the tool; ask surfaces an approval prompt before the body loads.
Configured ask and deny rules are seeded into the session's permission ruleset
before the first skill call, ahead of the default catch-all allow, and the
seeding is idempotent so it never clobbers an "always allow" the user has since
granted. (`internal/skill/permission.go`, `internal/permission/permission.go`,
`internal/agent/loop.go`)

**Built-in skill.** `customize-ogcode` ships embedded in the binary — it
guides configuring ogcode's provider settings, `AGENT.md`, `MEMORY.md`, and
authoring new skills. It has no directory on disk, so the tool tells the agent
so rather than sending it looking for files that were never there.
(`internal/skill/embedded.go`)

### Memory maintenance

The memory dialog in the web UI gains two maintenance actions:

- **Re-embed memory** — re-computes every stored embedding against the current
  embedding model, for both collection documents and graph fact nodes. This is
  the recovery path after switching embedding providers, which invalidates
  existing vectors (a dimension or model mismatch makes them silently match
  nothing). Exposed as `POST /memory/reindex`.
- **Reset memory** — wipes all memory tables (documents, nodes, edges,
  collections). Destructive and irreversible, guarded by a confirm step in the
  UI. Exposed as `POST /memory/reset`.

(`internal/server/routes.go`, `internal/server/server.go`,
`web/src/components/memory-dialog.tsx`, `web/src/api/client.ts`)

### Correct RefreshAll

`Memory.RefreshAll` previously re-embedded only collection documents and
silently skipped the graph's fact nodes. After a provider switch, session and
project recall kept scoring against stale old-dimensionality vectors — and with
the cosine dimension guard, matched nothing at all. It now re-embeds both:
documents (stored as little-endian float32 blobs) and fact nodes (stored as
JSON arrays). Rows are materialized before any UPDATE runs, because the sqlite
driver does not allow a write while a read cursor is still open on the
connection. (`internal/memory/memory.go`)

### Prompt correctness

- **LaTeX environment moved out of the static block.** The detected LaTeX
  environment (pdflatex version, distribution, available classes and packages)
  was being injected into the cacheable base system prompt. Detection is a probe
  of the host, not a session-fixed value, so it now lands in the trailing
  per-turn entries next to the index status — keeping the static prefix
  byte-identical by construction, not by caching. (`internal/agent/loop.go`)
- **NoteAgent LaTeX guidance is honest.** The markdown-capabilities section
  promised that `latex` blocks render inline with a PDF download. That is true
  in the chat viewport, but a saved note keeps them as raw fences — the file
  itself does not render. `markdownCapabilitiesPrompt` now takes a `savedToFile`
  flag so the NoteAgent describes the render path the file can actually honor.
  (`internal/agent/agent.go`, `internal/agent/prompt_builder.go`)
- **Skill tool summary.** The message-item component in the web UI now
  summarizes a `skill` tool call by its `name` argument, so the call shows the
  skill being loaded rather than a generic label. (`web/src/components/message-item.tsx`)

### Config

The `ogcode.json` config gains a `skills` section:

```json
{
  "skills": {
    "paths": ["./team-skills"],
    "urls": ["https://example.com/skills/index.json"],
    "permissions": { "git-release": "ask", "dangerous-*": "deny" }
  }
}
```

Skill sources merge differently from provider settings: `paths` and `urls` are
unioned (a project adds to the global set rather than replacing it), and
`permissions` merge key-by-key with project-local last. (`internal/config/config.go`)

### Tool reachability invariant

The tool-reachability test now pins the `skill` tool in
`mandatoryPromptTools`, so any prompt section that names `skill` by id is
guaranteed to reach only agents that hold it — the same invariant the other
tools already follow. (`internal/agent/tool_reachability_test.go`)

---

# Release Notes — v0.26.1

## Patch: In-Turn Context Compaction, Cache-Verdict Detection, File-Based Config, Streaming Hardening, and Tool Correctness

This patch release adds the long-missing ability for an agent to reclaim its
own context mid-turn on endpoints that do not cache a repeated prefix, and
gates that ability behind detection so it is never offered where it would cost
more than it saves. It also introduces file-based provider configuration with
CLI flags, consolidates build version metadata into one package, hardens the
streaming layer against stalled connections and oversized frames, makes file
writes atomic, and fixes a cluster of tool-correctness bugs around binary
files, read-only-but-existing files, and empty edit patterns.

### In-turn context compaction (compact_context)

On endpoints that bill the full accumulated prefix on every step — where there
is no prompt cache — a long agent turn grows until it overflows the context
window, even though the early steps are finished and only their conclusions
matter. The agent can now compact its own context mid-turn by calling a new
`compact_context` tool with a written summary of everything it has done so
far. The tool itself only validates the summary; the agent loop does the work:
it records a watermark at the assistant message that carried the call, and
from the next step onward assembles the model-facing request as the summary
plus everything after that watermark. Nothing is deleted — the session store
keeps every message, so history and the UI are unaffected — only the
model-facing slice narrows. (`internal/tool/compact_context.go`,
`internal/agent/compaction_watermark.go`, `internal/agent/loop.go`)

### Cache-verdict detection

Compaction pays for itself only when every step re-pays full price for the
whole prefix. On an endpoint that serves a repeated prefix from a cache
(Anthropic, OpenAI), compacting is a net loss — it invalidates the cache and
the next request re-establishes the prefix at full price. So the
`compact_context` tool is gated behind a per-endpoint verdict: `caching`,
`none`, or `unknown`. The verdict is resolved by observing what the provider
reports back (`cache_creation_input_tokens` / `cached_tokens` in the
response), persisted per `(model, endpoint)` in a new
`model_cache_support` table so the observation window is spent at most once,
and memoized in-process on top of the database. The key is composite rather
than model alone, because the same model can be served by endpoints that
differ in caching behaviour — `qwen3-coder:cloud` on a local Ollama reuses its
KV cache and bills nothing, while the same model on ollama.com is billed per
token with no prefix caching. Keying on the model alone would let one
endpoint's verdict silently answer for the other.
(`internal/provider/cache_support.go`, `internal/agent/cache_verdict.go`,
`internal/db/034_model_cache_support.sql`)

### File-based configuration

Provider base URLs and API keys can now live in a JSON file instead of only
environment variables. Ogcode reads two locations and merges them, with the
project-local file winning per field:

- `~/.config/ogcode/config.json` — global, applies to every project
- `ogcode.json` at the project root — project-local (commit it, or gitignore it
  if it holds a key). It is found even when launched from a subdirectory:
  Ogcode searches upward from the current directory through parents, stopping
  once it has checked the repo root (the directory containing `.git`), so an
  unrelated `ogcode.json` further up outside the repo is never picked up.

The precedence is **CLI flag > environment variable > config file > provider
auto-detect**; a real environment variable always overrides the config file.
`--ollama-url` and `--ollama-key` flags work on every subcommand for a one-off
override with no env var needed. If no `ogcode.json` exists anywhere in the
search, a blank one is scaffolded automatically in the current directory.
(`internal/config/`, `internal/cli/root.go`, `internal/cli/run.go`)

### Consolidated build version metadata

Version, commit, and build date previously lived as separate `ldflags`-injected
vars in both `internal/cli` and `internal/version`, which drifted apart and
duplicated the injection sites. They are now consolidated into
`internal/version` (`Version`, `Commit`, `Date`), the CLI's `version` command
sources from the version package via `version.GetInfo()`, and both the
Makefile and the GoReleaser release workflow inject only the single
`internal/version` package. (`internal/version/version.go`,
`internal/cli/version.go`, `Makefile`, `.github/workflows/release.yml`)

### Streaming hardening

The streaming layer no longer relies on a single whole-request HTTP timeout,
which killed long generations mid-stream. Liveness is now bounded where it
belongs:

- **Split idle budget.** Cloud endpoints that stream tool-call arguments as a
  run of small deltas are never quiet for long, so a tight 120 s idle watchdog
  (reset on every chunk) safely catches a dead connection. Local and batched
  endpoints (Ollama composes a whole tool call and emits it in one frame when
  the model finishes — measured at 13.4 s of silence for a 7 KB call) get a
  10-minute budget sized to outlast a large generation rather than a network
  blip. (`internal/provider/provider.go`)
- **Per-line byte cap.** A 16 MB SSE line cap so a provider that sends an
  entire tool call — a whole file's contents, JSON-escaped — in one `data:`
  frame no longer breaks `bufio` with `ErrTooLong` part-way through a large
  response. (`internal/provider/provider.go`)
- **Response-header timeout.** A 300 s header wait bounds the one phase the
  idle watchdog cannot cover (it only starts once the body exists), kept long
  so a free, queued endpoint is treated as a slow provider rather than a dead
  connection. (`internal/provider/provider.go`)

### Atomic file writes

The write tool now writes atomically — content goes to a temp file and is
renamed into place — so a crash or interruption mid-write no longer leaves a
truncated file on disk that the next step treats as the file's real contents.
(`internal/tool/atomic_write.go`, `internal/tool/write.go`)

### Tool correctness fixes

- **read — `end_line` alone.** Giving `end_line` without `start_line` now means
  "from line 1 through `end_line`" instead of silently falling back to the
  unranged default window and handing back different lines than asked for with
  no indication the argument was used. (`internal/tool/read.go`)
- **read — binary files.** A binary file has no line structure; splitting it
  on incidental `\n` bytes produced a flood of garbage "lines" with fabricated
  line numbers. The read tool now detects binary content with the same
  heuristic `file_map` uses and returns a clear message pointing to
  `view_image` / `pdf_index` / `docx_index` instead. (`internal/tool/read.go`)
- **read — phantom trailing line.** A file ending in a newline (the common
  case) no longer reports a count inflated by one, so the read tool and
  codemap agree on line counts for the same file. (`internal/tool/read.go`)
- **write — `created` vs overwritten.** File existence is now decided by
  `Stat`, not by whether `ReadFile` succeeds. A file that exists but is
  unreadable (permissions, a transient I/O error) is still an overwrite, not a
  creation — so content that existed and was lost is no longer reported as
  newly "Created", and the syntax baseline is not dropped to blame a
  pre-existing error on this write. (`internal/tool/write.go`)
- **edit — empty `old_string`.** An empty `old_string` matches everywhere (Go's
  `Count` treats it as occurring between every rune), so it either fell into
  a confusing ambiguity error or, on an empty file, silently "succeeded" by
  inserting `new_string` into content that was never matched against anything.
  It is now rejected up front with a clear reason. (`internal/tool/edit.go`)

### Permission: mutating flags on safe commands

`find` is harmless when walking a tree, which is why it sits in the safe
command list — but `-delete` removes every match and `-exec` runs an arbitrary
command per match. Without this change, `find . -delete` and
`find . -exec rm {} \;` both classified as safe and ran unprompted in Auto mode.
A per-command table of mutating flags now reclassifies these: `-delete` is
`RiskAsk` (unambiguously destructive), and the flags that run a command the
rules cannot see or write to a file (`-exec`, `-execdir`, `-ok`, `-okdir`,
`-fprint`, `-fprintf`, `-fls`) are `RiskUnclear`, leaving the LLM check to
judge the specific invocation. (`internal/permission/risk.go`)

### Model catalog: max output tokens

Anthropic catalog models now carry an explicit `MaxOutputTokens` value, sent
to the API so a response is not cut short by the provider's default. The
OpenAI-compatible path (also used for OpenRouter and Ollama) deliberately
stays at 0: it would send the limit as `max_tokens`, which the o-series and
GPT-5 reasoning models reject in favour of `max_completion_tokens`.
(`internal/provider/models_catalog.go`)

### Web UI

A 404 / not-found page, bundled Inter and JetBrains Mono variable fonts, a
large refactor of the models settings page, and updates across session,
message, prompt-input, plan, and settings components.

# Release Notes — v0.26.0

## Minor: Git Diff Viewer, Session Resume & Interruption Recovery, Gitignore-Aware Indexing, and C#/Dart Outlines

This minor release brings source control into the workspace, makes sessions
recover from being cut short, respects `.gitignore` during indexing, and grows
tree-sitter outline support to ten grammars. It also fixes two latent bugs — a
monotonic-ID ordering issue that intermittently mis-sorted messages, and a
memory-recall nil-panic — and hardens the Ollama detector so a machine with no
local install but a router or proxy still finds it.

The headline feature is a **VS Code-style Source Control panel** built into the
index explorer: browse changed files, read per-file unified diffs, stage and
unstage individual files, commit with a message, and walk recent commits with
their full diffs — all without leaving the workspace. The diff renderer parses
raw unified-diff text directly, the way GitHub and VS Code do, so untracked files
render as all-addition hunks instead of empty diffs. Stage, unstage, and commit
operations are serialized with the agent's own git operations through a shared
mutex, so the UI and the agent never race on the index.

### Session resume & interruption recovery

When an agent turn is cut short — a crash, a rate limit, a network drop, the
server restarting mid-stream — the session is now repaired and offered a
**Resume** button instead of being stranded. The failure is classified (rate
limit, auth, network, context overflow, server error, crash, stalled, fatal),
whether retrying is worthwhile is recorded, and the session history is repaired
so the next provider request is valid: completed tool work is preserved,
half-finished tool calls are paired with error results, and a text-only fragment
is deleted so the step replays cleanly. On server startup, every interrupted
interactive session is reconciled automatically — a restart no longer leaves
sessions in a half-finished state. A `POST /api/session/{id}/resume` endpoint
restarts the loop without the user having to retype anything.

1. **Interruption classification** — Provider stream errors are mapped to
   `session.Interruption` records with a resumability verdict and a
   human-facing detail string, so the Resume button can say what went wrong and
   whether retrying will help. (`internal/agent/interruption.go`)

2. **Session reconciliation** — Dangling `tool_use` blocks are paired with error
   results, half-written JSON is coerced to a closed state, and the interrupted
   turn is kept or deleted based on whether it completed any tool work. Keeping
   a turn with finished tool calls preserves the work; deleting a text-only
   fragment lets the step replay. (`internal/agent/resume.go`)

3. **Startup recovery** — On boot, the server reconciles every interrupted
   interactive session so a restart mid-turn is not a dead end.
   (`internal/server/resume_routes.go`, `internal/server/server.go`)

4. **Resume endpoint & UI** — `POST /api/session/{id}/resume` restarts the loop;
   the frontend surfaces a Resume button with the failure explanation.
   (`internal/server/resume_routes.go`, `internal/server/routes.go`)

### Git diff viewer (source control panel)

1. **Working-tree changes** — Lists modified, staged, and untracked files with
   per-file unified diffs. Untracked files render as all-addition hunks via a
   `--no-index` diff, not as empty diffs. (`internal/git/git.go`,
   `internal/server/git_routes.go`, `web/src/components/git-diff.tsx`)

2. **Stage / unstage / commit** — Individual files can be staged or unstaged and
   the staged set committed with a message. All mutating git operations are
   serialized with the agent's own git operations through `s.gitMu` so the UI
   and the agent never race on the index. (`internal/server/git_routes.go`)

3. **Commit browser** — Recent commits (default 20) are listed with their full
   diffs, browseable the same way as working-tree changes.
   (`internal/git/git.go`, `internal/server/git_routes.go`)

4. **Unified-diff renderer** — The `GitDiff` component parses raw unified-diff
   text directly into hunks and renders GitHub-style add/del/context line
   coloring, the same approach GitHub and VS Code take.
   (`web/src/components/git-diff.tsx`)

### Gitignore-aware indexing

Indexing now respects the project's own `.gitignore` instead of a hardcoded
default exclude list that was invisibly seeded into the user's store. A new
`internal/gitignore` package implements full pattern matching — negation,
directory-only patterns, nested `.gitignore` files, wildcards, and character
classes — and the indexer prunes gitignored directories and skips gitignored
files during workspace walks. The old default excludes (`node_modules`,
`vendor`, `dist`, …) are gone; only the project's `.gitignore` and user-added
patterns apply. An **Index scope** dialog in the UI shows the workspace's
`.gitignore` rules, lists nested `.gitignore` files, and lets users add extra
exclude patterns — and offers a starter template when no `.gitignore` exists.
(`internal/gitignore/`, `internal/indexer/indexer.go`,
`internal/docindex/excludes.go`, `internal/server/docindex_routes.go`,
`web/src/components/index-scope-dialog.tsx`)

### C# and Dart tree-sitter outlines

Two new grammars join the outline system, taking it from 8 grammars / 17
extensions to **10 grammars / 21 extensions**.

1. **C#** (`.cs` `.csx`) via `tree-sitter-c-sharp`. Outlines namespaces (both
   block and file-scoped forms), classes, structs, interfaces, enums, records,
   delegates, methods, constructors, destructors, operators, indexers, and
   properties. XML documentation comments (`/// <summary>`) are parsed into
   readable prose, not raw markup. (`internal/codemap/queries/csharp.scm`,
   `internal/codemap/codemap.go`)

2. **Dart** (`.dart`) via `tree-sitter-dart` (community-maintained,
   commit-pinned). Outlines classes, mixins, extensions, enums, type aliases,
   methods, getters/setters, and constructors (plain, named, const, factory).
   Required new machinery: `trailingBodyKind` extends a declaration's range over
   its body (which sits as a sibling, not a child, in the Dart grammar) and
   `attrKind` handles annotations. (`internal/codemap/queries/dart.scm`,
   `internal/codemap/codemap.go`, `internal/codemap/languages.go`)

### Ollama cloud catalog & detection improvements

1. **Cloud catalog** — Models hosted on Ollama Cloud are fetched and merged into
   the local model list so the picker shows what's actually usable (cloud models
   don't need to be pulled). When nothing is pulled locally, the 3 cheapest
   cloud models are auto-enabled so a fresh install isn't greeted by an empty
   picker. (`internal/provider/ollama_catalog.go`,
   `internal/provider/openai.go`)

2. **Fallback detection** — A machine with no local Ollama install but a router
   or proxy in front of remote instances now finds it. The detector probes the
   default endpoint, then a priority-ordered list of fallbacks
   (`OLLAMA_FALLBACK_URLS` overrides), concurrently so a dead candidate adds no
   latency. An explicit `OLLAMA_BASE_URL` is always authoritative and never
   silently swapped for a live one; a configured endpoint that has gone dead
   yields to whatever detection found. (`internal/provider/ollama_detect.go`)

### Index run dialog & index plan preview

Before an index run starts, a dialog shows exactly what it would do: how many
files will be indexed, how many are stale, a breakdown by file type
(text/pdf/docx), and which models are enabled. It distinguishes a rebuild
(re-reads everything) from an incremental run (only new + stale files), and
reports a no-op when there's nothing to do. (`web/src/components/index-run-dialog.tsx`,
`internal/indexer/indexer.go`)

### Code viewer, file tree, and subagent indicator

1. **Code viewer** — Source files in the index explorer now render with
   syntax highlighting (highlight.js) and a line-number gutter. Highlighting
   caps at 150 KB or when very-long lines are present (minified bundles),
   falling back to plain escaped text so the tab never stalls.
   (`web/src/components/code-viewer.tsx`)

2. **File tree** — The index explorer's file tree is now a standalone component
   with language-colored file icons (26 languages, including C# and Dart),
   filter highlighting, and breadth-first default expansion.
   (`web/src/components/file-tree.tsx`)

3. **Subagent indicator** — Animated "bot-head" pills appear in the session
   header whenever the main agent has delegated work to read-only sub-agents via
   the `task` tool, each showing the sub-agent's title with a hover tooltip.
   Stale loops (where the last turn ended in error/abort) are filtered out so
   the indicator doesn't show phantom active tasks.
   (`web/src/components/subagent-indicator.tsx`)

### Bug fixes

1. **Monotonic ULID generation** — ID generation now uses a monotonic entropy
   source so IDs minted within the same millisecond are strictly increasing.
   Previously, fresh random entropy was drawn each time, making sub-millisecond
   ordering a coin flip — ~50% of consecutive pairs sorted backwards. Since IDs
   are the message sort key, this intermittently placed tool-result messages
   before the assistant turn they answered, breaking any code that read position
   as causality (including the new resume reconciliation). The stateful monotonic
   reader is mutex-guarded with an overflow fallback to plain random.
   (`internal/id/id.go`)

2. **Memory graph enrichment nil-panic** — `enrichTreeWithFollowUp` no longer
   panics when `BuildLightweightTree` returns a nil map (no facts cleared the
   cosine threshold). The follow-up merge round now handles nil on either side.
   (`internal/memory/graph.go`)

3. **Ollama status test isolation** — `TestOllamaStatusEndpoint` no longer reads
   the host's real Ollama instance. The test pins the primary probe to a dead
   address and disables fallbacks so the "nothing running" path is exercised
   deterministically, regardless of what's running on the dev machine. The
   `primaryOllamaBaseURL` var is exported as `PrimaryOllamaBaseURL` so the server
   package's test can override it. (`internal/provider/ollama_detect.go`,
   `internal/server/provider_routes_test.go`)

### Dependencies

Adds `tree-sitter-c-sharp` and `tree-sitter-dart` (community-maintained,
commit-pinned) via `go.mod` for the two new outline grammars.

### Tests

New test files cover each feature area: `internal/agent/{resume,interruption}_test.go`,
`internal/git/git_test.go`, `internal/server/{git_routes,resume_routes,gitignore_info}_test.go`,
`internal/codemap/{csharp,dart}_test.go`, `internal/gitignore/gitignore_test.go`,
`internal/docindex/excludes_test.go`, `internal/id/id_test.go`,
`internal/memory/graph_enrich_test.go`, `internal/provider/{ollama_catalog,ollama_detect}_test.go`.
`CGO_ENABLED=1 go build ./...` clean; `CGO_ENABLED=1 go test ./...` green.

### New HTTP API endpoints

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/git/status` | Working-tree file status |
| GET | `/api/git/diff?path=&staged=` | Per-file unified diff |
| GET | `/api/git/commits?n=20` | Recent commits list |
| GET | `/api/git/commit/{sha}` | Full diff for a commit |
| POST | `/api/git/stage` | Stage files |
| POST | `/api/git/unstage` | Unstage files |
| POST | `/api/git/commit` | Commit staged changes |
| POST | `/api/session/{id}/resume` | Resume an interrupted session |
| GET | `/api/docindex/gitignore` | Workspace .gitignore rules + nested files |

---

# Release Notes — v0.25.0

## Minor: check_syntax Tool, Five New Tree-Sitter Grammars, and Edit-Time Syntax Checking

This minor release extends the tree-sitter foundation laid in v0.24.0 in two
directions: it adds a **`check_syntax`** tool that parses a file and reports its
syntax errors the moment they are introduced, and it grows language support from
three grammars across nine extensions to **eight grammars across seventeen
extensions** by adding PHP, Python, Rust, Swift, and Java. Together they close
the loop that `file_map` opened: a file can now be mapped, edited, and validated
without ever leaving tree-sitter, so a dropped brace or a broken indent surfaces
immediately instead of several edits later, in a build the agent may not run.

A replaced block that drops a brace, an indentation slip in Python, or a merge
of two fragments that leaves a stray token — none of these announce themselves.
The file writes fine, and the damage surfaces later, by which point the agent
has stacked more edits on a broken parse. `write` and `edit` now parse the file
on both sides of every change and append a syntax note to their result when the
change broke what had been clean, so the agent is told while the change is still
the last thing that happened. The standalone `check_syntax` tool covers the cases
those two cannot — a file changed by a shell command, a formatter, a patch, or a
generator — and confirms a fix landed.

1. **`check_syntax` tool** — Parses a source file with tree-sitter and reports
   any syntax errors with the line and column of each, returning **OK** when the
   file parses, the error locations when it does not, and **NOT CHECKED** when no
   grammar covers the file type. The three outcomes are worded to be
   unmistakable from each other: a clean parse says move on, a diagnostic says
   stop and fix, and an unchecked file says this proved nothing — an agent that
   reads "no grammar" as "no errors" has bought false confidence. Diagnostics
   carry a 1-based line/column (matching `file_map` numbering and `read` ranges),
   an end line for multi-line damage, the source line as a compiler-style
   gutter, and a cap of 20 (tree-sitter recovers and keeps parsing, so a file
   damaged near the top can cascade into dozens of complaints that all describe
   the same mistake). Checks grammar only — it is a cheap guard against having
   broken the file, not a substitute for the compiler. (`internal/tool/check_syntax.go`,
   `internal/codemap/syntax.go`, `internal/codemap/render.go`)

2. **Edit-time syntax checking in `write` and `edit`** — Both mutating tools now
   parse the file before and after the change and append a **SYNTAX ERROR** note
   to their result when the change introduced errors into a file that parsed
   cleanly, or a softer **SYNTAX NOTE** when the file was already broken (so an
   edit is not blamed for damage it did not cause — an agent that learns the
   warning fires on edits it did not break stops reading it). The before/after
   comparison is the whole point: the baseline parse only runs once the result
   is known to be broken, so the overwhelmingly common case — a change that
   leaves the file fine — costs one parse, not two. The verdict is also recorded
   in the result metadata (`syntaxOK`, `syntaxErrors`) so the UI can flag a
   damaging write without parsing the output text. Up to 5 errors are listed
   inline; `check_syntax` gives the full list. (`internal/tool/write.go`,
   `internal/tool/edit.go`, `internal/tool/check_syntax.go`)

3. **Five new tree-sitter grammars** — PHP, Python, Rust, Swift, and Java join
   Go and TypeScript, taking full parsing from 3 grammars / 9 extensions to
   **8 grammars / 17 extensions**:
   - **PHP** (`.php` `.phtml`) via `tree-sitter-php`. Registers the `php` parser
     that reads a file as text opening into code at `<?php`, so templates that
     return to HTML between blocks map the same way a plain `.php` file does.
     Classes, interfaces, traits, and enums are outlined with their methods
     nested underneath, including the `public`/`protected`/`private` forms.
   - **Python** (`.py` `.pyi` `.pyw`) via `tree-sitter-python`. Python documents
     a declaration from the inside — a docstring at the top of the body rather
     than a comment above it — so docstrings are read directly and collapsed to
     their summary sentence. Decorators hang above the declaration they modify,
     so a symbol's range is widened back up over them (`@property` changes what a
     method is; a route decorator is the only place a URL appears). Module-level
     bindings earn a line of their own.
   - **Rust** (`.rs`) via `tree-sitter-rust`. Rust states an attribute beside
     the item rather than around it, so a `#[derive(...)]` line is a sibling
     that sits between the doc comment and the declaration. Ranges are widened
     back up over the attributes and the doc is read from above them; `///` and
     `//!` markers are stripped. `impl` blocks are listed with their methods
     nested underneath and are named for the type they are written for. A
     trait's required methods carry no body, and that signature is the point of
     the trait, so they earn a line beside the defaulted ones.
   - **Swift** (`.swift`) via a **vendored** `tree-sitter-swift`. Swift needs no
     accommodation — it parses `@objc public final` into a `modifiers` node
     inside the declaration, so ranges and signatures cover attributes for free.
     `class`, `struct`, `enum`, `actor`, and `extension` are matched
     structurally so each keeps its own kind; extensions list as their own
     entry with methods nested underneath; a protocol's requirements — methods,
     property requirements, and `associatedtype` alike — are all listed. The
     grammar is vendored because upstream generates its parser at build time and
     gitignores it, publishes no tagged versions, and ships a Go binding that
     `#include`s a file no commit contains. See
     `internal/codemap/grammars/swift/README.md`.
   - **Java** (`.java`) via `tree-sitter-java`. The same shape as Swift and
     needs nothing beyond the two comment kinds — its annotations also sit in a
     `modifiers` node inside the declaration, so `@Entity public final class`
     renders whole. Classes, interfaces, enums, records, and annotation types
     are all outlined, with nested types kept because Java leans on the shape so
     heavily that a Builder is the canonical case. An enum states its methods
     inside a nested `enum_body_declarations` rather than directly in its body,
     so those need a pattern of their own.
   (`internal/codemap/languages.go`, `internal/codemap/queries/{php,python,rust,swift,java}.scm`,
   `internal/codemap/grammars/swift/`)

4. **Codemap generalisations** — The new grammars needed more than a grammar and
   a query, so the symbol builder gained three language-level knobs:
   - **`commentKinds`** replaces the single `commentKind` — Rust and Java split
     line and block comments into two node kinds, and the doc-comment walk now
     checks both. The `firstDocLine` marker-stripper handles `//`, `///`, `//!`
     (Rust outer/inner doc), `#` (Python), and `/*`/`*`/`*/` block forms, with
     the closing `*/` trimmed before the leading `*` so a lone-`*/` line does
     not leave a stray slash.
   - **`wrapperKind`** (Python's `decorated_definition`) widens a declaration's
     range back up over the decorators that wrap it.
   - **`attrKind`** (Rust's `attribute_item`) walks preceding sibling attributes
     into the range so the doc-comment walk can start from the topmost attribute.
   - **`docstrings`** (Python) reads the string literal at the top of the body
     as the doc, where a comment above the declaration does not exist.
   - **`lastContentRow`** normalises a node's end row for grammars whose
     comments consume their trailing newline (Rust's `line_comment`), without
     which every Rust doc would sit one row below the adjacency check and be
     dropped. **`startsItsLine`** rejects trailing comments that document the
     code beside them rather than the declaration that follows.
   - **`namesByKind`** is a fallback for grammars that hang a spec's identifier
     off an unnamed-field child (PHP's `const_element`), so a top-level
     `const VERSION = '1.2.0'` is named. Rust `impl` blocks are named for the
     type they are written for, resolved before the field walk would descend
     into the trait path of `impl fmt::Display for Widget`.
   (`internal/codemap/codemap.go`, `internal/codemap/signature.go`)

5. **Workflow & index hygiene** — `check_syntax` is added to the coding-agent
   toolset and the system prompt's "Verify your work" step now tells the agent
   to read what `write` and `edit` reported and to run `check_syntax` on files
   changed by a shell command, formatter, or patch. The indexer and doc-index
   now skip a `grammars` directory by name, so a vendored generated parser
   (`parser.c` runs to tens of megabytes of table data — ogcode's own Swift
   grammar is one 19.7 MB file) does not dominate the index while carrying
   nothing anyone would search for. A `.gitattributes` marks the vendored Swift
   parser as linguist-generated/vendored so it stays out of language stats and
   collapses in diffs. (`internal/agent/agent.go`, `internal/server/server.go`,
   `internal/indexer/indexer.go`, `internal/docindex/excludes.go`,
   `.gitattributes`)

### Dependencies

Adds tree-sitter bindings for Java (`tree-sitter-java`), PHP
(`tree-sitter-php`), Python (`tree-sitter-python`), and Rust (`tree-sitter-rust`)
via `go.mod`. The Swift grammar is vendored under `internal/codemap/grammars/swift/`
because upstream publishes no buildable Go package. The binary remains CGO-free.

### Tests

New test files cover each new grammar's outline behaviour:
`internal/codemap/{python,rust,swift,java,php}_test.go`, plus
`internal/codemap/syntax_test.go` for the syntax checker and
`internal/tool/check_syntax_test.go` and `internal/tool/mutation_syntax_test.go`
for the edit-time checks. `go build`/`go vet` clean; `go test ./...` green.

---

# Release Notes — v0.24.0

## Minor: File Map — Tree-Sitter Outlines and Ranged Reads

This minor release adds **`file_map`** — a tree-sitter-powered tool that parses
a source file into an outline of its declarations with 1-based line ranges, so
the agent reads the 40 lines it needs instead of the whole 600-line file. It
pairs with `codebase_map` (which file?) to answer the second navigation question
(where inside it?), and it deliberately has no dependency on the project index:
it parses fresh on every call, so its line numbers always describe the file as
it is right now, and it works in any project, indexed or not.

Reading a 600-line file to look at one 40-line function puts the other 560 lines
in the context window for the rest of the turn, and they are re-sent on every
step that follows. The map costs a few dozen lines — 5–11% of the file it
describes — and tells the agent exactly which range to ask for. On this repo's
277-file index the two changes together cut a `codebase_map` call from 86 KB of
JSON (past the 50 KB output ceiling, silently truncated) to a 44 KB indented
outline, a 34% token saving over the JSON form.

1. **`file_map` tool** — Parses a file with tree-sitter and returns every
   top-level declaration with its 1-based line range and doc comment. Nested
   entries (a class's methods, the handlers inside a component that is one large
   arrow function) are individually reachable rather than buried in one opaque
   range. Three grammars give full parsing across nine extensions: Go (`.go`),
   TypeScript (`.ts` `.mts` `.cts`), and TSX/JSX/JS (`.tsx` `.jsx` `.js` `.mjs`
   `.cjs`) via the TSX parser. Every other file type falls back to a heuristic
   scanner that recognises declarations in Python, Rust, shell, and the
   JavaScript/TypeScript family plus Markdown headings; its ranges are
   approximate but no file is left unmapped. Files above 2 MB are not mapped.
   (`internal/tool/file_map.go`, `internal/codemap/`)

2. **New `internal/codemap` package** — Pure-Go tree-sitter bindings
   (`tree-sitter/go-tree-sitter`, `tree-sitter-go`, `tree-sitter-typescript`)
   that produce an outline of a file's symbols with line ranges. Renders as
   plain text rather than JSON — measured on this repo, `MarshalIndent` spent
   ~1.4x the content's own size on braces, quotes, and one-label-per-line arrays,
   and nothing downstream unmarshals it: the output is read by a model.
   (`internal/codemap/codemap.go`, `render.go`, `signature.go`, `languages.go`,
   `fallback.go`, `queries/*.scm`)

3. **Ranged reads** — `read` now accepts `start_line`/`end_line` (1-based,
   inclusive, matching the numbering `file_map` prints), so a range is copied
   across as-is rather than translated to a 0-based `offset` by hand — a
   translation that, done wrong, silently shifts the window by a line instead of
   erroring. An unranged read of a file longer than 200 lines now returns the
   file's map instead of its contents, making "map before reading" structural
   rather than advisory: a whole file cannot land in context by accident. An
   explicit range (including `offset`/`limit` paging) is always honoured, so
   `read(path, start_line=1, end_line=N)` remains the way to demand a whole file;
   short files and files the mapper finds no declarations in still read through.
   (`internal/tool/read.go`)

4. **Compact `codebase_map` output** — The project index now renders as an
   indented outline rather than JSON, keeping a file's labels on one line. Text
   files are capped at 5 topic labels each (15 remain for PDF/DOCX); when the
   whole tree would still exceed a 49 KB budget, labels are dropped wholesale
   and the agent is told to re-scope with `subdir` for the part it cares about,
   rather than being silently truncated mid-branch by the generic output
   ceiling. (`internal/tool/project_index.go`)

5. **Workflow & docs** — The agent system prompt and `AGENT.md` now carry a
   mandatory `file_map` step between `codebase_map` and `read`, with the
   "map a file before reading it" rule and its rationale. `file_map` is added to
   every coding, plan, breakdown, note, and sub-agent toolset. `README.md`
   documents the feature, its token economics, and language support.
   (`internal/agent/prompt_builder.go`, `internal/agent/agent.go`,
   `internal/server/server.go`, `AGENT.md`, `README.md`)

### Dependencies

Adds tree-sitter bindings (`tree-sitter/go-tree-sitter`, `tree-sitter-go`,
`tree-sitter-typescript`) and the indirect `mattn/go-pointer`. The binary
remains CGO-free.

---

# Release Notes — v0.23.0

## Minor: Project-Scoped Agentic Memory Recall

This minor release adds **project-scoped memory recall** — a second memory tool
that searches every past conversation held in the same workspace, not just the
current one. Until now `memory_recall` could only see the current session, so
questions like "why did we choose PostgreSQL over Mongo here?" or "what did we
try before the deterministic search pipeline?" had no answer unless they were
asked in the session that made the decision. `project_memory_recall` closes
that gap.

1. **`project_memory_recall` tool** — A new agent tool that runs semantic
   recall across the entire agentic-memory graph for the current workspace.
   Results are **attributed to the conversation and date** they came from, and
   the synthesis step is instructed to resolve contradictions in favour of the
   most recent fact and call out supersessions explicitly, so the agent does not
   present a stale decision as current. Synthesis uses the session's own
   selected model (same contract as `memory_recall`). Supports an optional
   `since_days` window, `topic` filter, and a `scope` parameter that can be
   narrowed to `"session"` for a dated, recency-ranked view of just the current
   conversation.
   (`internal/tool/project_memory_recall.go`, `internal/memory/project.go`)

2. **Project identity & backfill** — A new `internal/project` package resolves
   the canonical (symlink-resolved) workspace path so `/var` and `/private/var`
   do not split one workspace's memory into two. Every memory write now stamps
   the workspace, session type and session name onto the node; on startup a
   `backfillMemoryProjects` pass re-stamps legacy nodes written before the
   column existed, so older conversations become recallable without re-indexing.
   (`internal/project/project.go`, `internal/memory/store.go`,
   `internal/session/store.go`, `internal/server/server.go`)

3. **Project recall engine** — The recall pipeline (`Graph.ProjectRecall`) does
   a single scan that simultaneously selects the top semantic matches (with a
   per-session cap so one 400-turn conversation cannot starve the rest) and
   builds an aggregate topic map. Matched facts are expanded with their
   immediate neighbour turns (adjacency is only meaningful inside one session,
   since per-session "order" restarts at 1), grouped by conversation oldest
   first, and fitted to a character budget by trimming per-fact text before
   dropping context-only neighbours. A multi-round refinement loop with an
   LLM-driven follow-up query re-searches the project when the first answer is
   low-confidence. (`internal/memory/project.go`, `internal/memory/store.go`)

4. **Static/dynamic system-prompt split** — `buildSystemPrompt` is refactored
   into `buildSystemPromptEntries`, returning the system prompt as separate
   entries so the Anthropic provider can attach its `cache_control` breakpoint
   to the static base only. The rendering viewport (which the browser resends
   with every prompt) now lives in a dynamic trailing entry alongside the date,
   so resizing the window no longer invalidates the cached tools+system prefix.
   The `FinalInstruction` of output-only agents is kept as the last entry so it
   sits closest to the model's response. (`internal/agent/loop.go`)

5. **"Jump to latest" UI fixes** — The floating jump-to-latest button is
   re-anchored to the message column (was `fixed left-1/2`, which centred on the
   viewport and drifted sideways when the sidebar expanded/collapsed) and now
   shows an accurate unread count via a read-marker signal (the old code
   reported the whole conversation length the moment you scrolled up). Extracted
   into a reusable `JumpToLatest` component shared by the chat and plan message
   lists. (`web/src/components/jump-to-latest.tsx`,
   `web/src/components/message-list.tsx`, `web/src/components/plan-message-list.tsx`)

**Migration**: The memory database gains `project_id` and `session_type`
columns on `nodes` and `sessions` plus three supporting indexes, added via
idempotent `ALTER TABLE` statements (safe on existing DBs). The startup
backfill then stamps project identity onto any legacy rows. No manual action
required.

Build: `npm run build` then `go build -o ./ogcode`; `go vet` clean;
`go test ./...` green.

---

# Release Notes — v0.22.1

## Patch: Inline Session-Title Editing in the Sidebar

You can now rename a session directly from the sidebar without leaving the
chat or opening a separate dialog. Hover any session row to reveal a new
pencil button, or double-click the row, to edit the title in place. Press
**Enter** to save, **Esc** to cancel, or click away to commit. Empty titles
fall back to "Untitled".

- **Rename button on hover** — a pencil icon appears at the right of each
  session row alongside the existing delete button. (`web/src/components/session-sidebar.tsx`)
- **Double-click to rename** — double-clicking a session row enters edit mode
  the same way; single-click still selects the session.
- **Inline editor** — the title becomes a focused text input with the same
  typography as the row; active-session indicator and timestamp hide while
  editing so the input gets full width.
- **Backend reuse** — the frontend `renameSession` helper calls the existing
  `PATCH /api/sessions/:id` endpoint (`updateSession`), so no new API or
  migration is needed. (`web/src/context/session.tsx`)

Build: `npm run build` then `go build -o ./ogcode`; `go vet` clean.

---

# Release Notes — v0.22.0

## Minor: Deterministic Deep-Research Pipeline, Configurable Research Tuning, and DB Index Optimization

This minor release rewrites `deep_search` as a deterministic pipeline, surfaces
user-configurable research knobs in the settings screen, and realigns the SQLite
indexes with the queries the app actually runs.

1. **Deterministic deep-research pipeline** — `RunSearchSession` is rebuilt as a
   fixed 4-stage pipeline — search → rank → fetch → synthesise — with exactly two
   LLM calls, replacing the old free-form tool-calling agent loop. The searches
   and page fetches are orchestrated in parallel on the Go side, and the final
   stage is always a plain synthesis call, so the result can no longer come back
   empty the way the old loop did on weaker models that failed to converge. The
   `deep_search` tool now also records start/end timestamps so the UI can show
   how long a search took. (`internal/agent/search_pipeline.go`,
   `internal/server/server.go`)

2. **Configurable deep-research tuning** — Adds two knobs to the Web-search
   settings card: **Pages fetched** (`fetchTopK`, 1–10, default 4) and
   **Characters per page** (`pageChars`, 1000–20000, default 6000). Values are
   clamped on both read and write so an invalid client payload can never store
   bad numbers. Tuning changes apply live on the next `deep_search` — no server
   restart needed (bridge enable / real-profile toggles still do). Backed by
   migration `032_search_research_params.sql`.
   (`internal/session/search_store.go`, `web/src/pages/settings/general.tsx`)

3. **SQLite index optimization** — Migration `033_index_optimization.sql`
   realigns indexes with the columns queries actually filter and sort on, and
   drops the ones no query reads (verified against every query site). The hot
   session/plan list queries were doing full table scans, the message loader
   sorted in a temp b-tree every turn, and several indexes merely duplicated
   UNIQUE constraints. After this pass every common list/sort path is
   index-served, and write-heavy paths (parts stream in on every token) carry
   less index upkeep.

4. **Live elapsed-time readout for `deep_search`** — The tool-part card in the
   chat now ticks an elapsed timer while a `deep_search` is running and shows
   the exact total once it completes (e.g. `12.3s`, `1m 05s`), using the
   persisted start/end timestamps. (`web/src/components/message-item.tsx`)

5. **macOS install fix** — `make install` now removes the old binary before
   copying the new one (fresh inode) and re-signs it ad-hoc with `codesign`,
   avoiding the stale cached code-signature "Killed: 9" on macOS. (Makefile)

6. **Selection highlight visibility** — Bumps the text-selection background
   from a ~12%-alpha tint (nearly invisible) to a ~40% accent tint so the
   highlight is clearly visible while keeping text high-contrast in both
   themes. (`web/src/styles/index.css`)

Build: `npm run build` then `go build -o ./ogcode`; `go vet` clean. Existing
installs pick up the new schema columns and indexes automatically via the
embedded migrations.

---

# Release Notes — v0.21.0

## Minor: Agent-Loop Hardening, Interactive Tool Permissions, and Auto-Approval Mode

This minor release brings four changes that landed after v0.20.0 — a major
agent-loop hardening pass, a redesigned tool-permission UX, and a new
auto-approval mode with hybrid risk assessment.

1. **Agent-loop hardening from the OpenCode architecture audit** — Implements
   every P0–P2 item from `docs/ARCHITECTURE_AUDIT.md`, comparing ogcode's agent
   loop against both OpenCode implementations:
   - **P0-1** Interactive tool-permission gating (a `permission.Manager` wired
     through `executeTool`, with SSE prompts and reply) plus a conservative
     bash denylist.
   - **P0-2** Per-path lock so concurrent write/edit calls to the same file
     can't clobber each other.
   - **P1-1** Four behavior-preserving `RunLoop` extractions
     (`compactRequest`, `resolveRunModel`, `executeReadyToolCalls`,
     `writeToolResultMessage`).
   - **P1-2** In-memory working set via known-ID folding (no time cursor).
   - **P1-3** Token-based compaction budgeting (a BPE-approx estimator, tool
     schemas, flat image cost).
   - **P1-4** A model-callable, read-only, depth-1 task sub-agent
     (`RunTaskSession`).
   - **P2-1** Non-destructive, window-aware compaction (prior summary folded
     in).
   - **P2-2** Structured `provider.APIError` with typed classification and
     `Retry-After` backoff.
   - **P2-3** Detectable event drops (bus `Seq` + `Dropped`) with client
     resync.
   - **P2-4** Per-model-family prompt tuning (Claude / GPT / Gemini / local).
   New tests accompany every change; `go build`/`vet` clean and
   `go test -race` green across agent, provider, tool, permission, and bus.

2. **Redesigned tool-permission prompt (Codex/Claude Code style)** — Replaces
   the cramped full-bleed amber banner with a centered card that matches the
   composer: a clear per-tool question ("Run this shell command?"), the command
   or file path in a monospace block, and prominent Allow once / Always allow /
   Reject buttons. Adds keyboard shortcuts — Enter approves (Allow is
   auto-focused), "A" allows for the session, Esc rejects (captured so it
   doesn't also abort the loop).

3. **Minimal, inline tool-permission prompt in the composer** — Moves the
   approval UI into the top of the composer card (like Claude Code / OpenCode)
   instead of a separate floating card above the messages. It's now a compact
   two-row strip — command/path on one line, Allow / Always / Reject on the
   next — sharing the input box's surface with a divider above the textarea.
   Keyboard shortcuts unchanged (Enter allow, "A" always, Esc reject).

4. **Auto-approval mode with hybrid risk assessment** — Adds a per-session
   approval mode, toggled from a compact Ask/Auto control in the composer
   toolbar (persisted in the session's `permission` field):
   - **Ask (default):** prompt before every bash/write/edit (unchanged
     behavior).
   - **Auto:** auto-run low-risk tool calls; still prompt for risky ones.
   Risk is judged hybrid (`internal/permission/risk.go`):
   - Rules classify the clear cases instantly — read-only/build/test
     commands and in-project, non-sensitive writes are safe; `rm`, `sudo`,
     `git push`, pipe-to-shell, writes outside the project or to
     secrets/keys/system files are risky.
   - The unclear middle (`mv`, `cp`, `chmod`, `npm install`, unknown tools, …)
     gets a quick LLM risk check, cached by command; any failure/timeout
     falls back to asking.
   The catastrophic bash denylist and explicit "always allow" grants still
   apply in both modes. Tests in `internal/permission/risk_test.go` cover the
   rule tiers.

---

# Release Notes — v0.19.4

## Patch: Mid-Loop Guidance Injected as User Message Content

This patch release changes how mid-loop guidance is delivered to the model,
fixing a semantic mismatch between intent and implementation:

1. **Guidance was injected as a system directive instead of user input** —
   Mid-loop guidance (the `handleGuidance` endpoint) was previously wrapped in
   a `<system-reminder>` block and appended as a trailing system-prompt entry.
   The user's intent is that guidance acts like a modification of their
   original message — additional user input within the current turn, not a
   system directive. Fixed by replacing the `guidancePrompt()` system-reminder
   wrapper with `guidanceUserContent()`, which appends the guidance as a
   labeled block (`[Mid-loop guidance]`) to the user's turn message content.
   The model now sees it as additional user input within the current turn.

2. **Guidance was one-shot instead of accumulating** — Each iteration only
   received the guidance drained at the top of that iteration; guidance sent
   earlier in the turn was not re-delivered on subsequent iterations, so the
   model could lose sight of earlier redirections after a few steps. Added a
   `delivered` accumulator to `LoopControl`: `DrainGuidance` now moves drained
   texts into `delivered`, and a new `DeliveredGuidance()` method returns the
   full accumulated set. `appendGuidanceToUserMessage` re-appends the full
   accumulated set on every iteration so the model continuously sees all
   guidance the user has sent during this turn.

3. **Late-guidance race used a fragile drain-and-re-push pattern** — The
   loop-exit guard for guidance arriving mid-iteration drained the guidance
   and re-pushed it onto the queue to keep it alive, a fragile workaround.
   Replaced with a `HasPendingGuidance()` check that avoids moving the guidance
   into the `delivered` accumulator prematurely — the next iteration's
   top-of-loop drain handles it correctly. Tests for all three guidance race
   scenarios (cancel-and-resume, without-cancel, after-finish) were updated
   to assert against user message content instead of the system prompt, and
   new tests cover `DeliveredGuidance` accumulation and
   `appendGuidanceToUserMessage`.

---

# Release Notes — v0.19.3

## Patch: Task Session Model, Tool-Cancel False-Positive, and Stale-Index Purge

This patch release fixes three bugs found after v0.19.2:

1. **Task agent session showed the wrong model** — When a plan task is
   executed, the backend creates a session with the worktree path as its
   directory, not the main project directory. The frontend
   `selectSession` looked up the authoritative session record via
   `listSessions` filtered by the main project directory, so
   worktree-based task sessions were never found. The UI fell back to a
   stub without a model field, causing `selectedModel` to pick the
   default/enabled model instead of the task's configured model. Fixed
   by falling back to `getSession` by session ID when the session is not
   in the directory-filtered list.

2. **False-positive "tool execution cancelled" on every errored tool
   call** — The `toolCtxCancelled` check read `toolCtx.Err()` *after*
   `toolCancel()` was already called unconditionally as cleanup. Since
   `toolCancel()` always cancels the child context, `toolCtx.Err()` was
   non-nil on every tool execution — even when tools completed normally
   with no user guidance or cancellation. This surfaced a spurious
   "Tool execution cancelled by user mid-loop guidance" error on every
   errored tool call. Fixed by capturing the cancellation state *before*
   the cleanup `toolCancel()` call, so it only reports true when
   `CancelTool` (via the guidance endpoint) actually cancelled the child
   context mid-flight.

3. **Stale index entries for deleted files were never purged** — On
   incremental re-index (non-rebuild), the indexer only added new files
   and skipped already-indexed ones; it never checked whether
   previously-indexed files had been deleted from disk, leaving stale
   entries in `doc_page_index` forever. Added a `purgeDeletedDocs` step
   to `Indexer.Run` that compares currently-indexed doc paths (via new
   `Store.ListDocPaths`) against the fresh filesystem walk and deletes
   entries for any file that no longer exists. Also handles the
   early-return path where all files are gone. Includes tests for
   `ListDocPaths`, `DeleteByDoc`, and the purge logic.

---

# Release Notes — v0.19.2

## Patch: Home Page Headline Update

Updated the home page hero headline from "Build software at the speed of
thought." to **"Where everyone is a software developer."** — a stronger
democratization-focused message that better communicates ogcode's mission of
making software development accessible to everyone. The subheadline was
realigned to match this narrative while preserving the core technical
differentiators (token savings, infinite context).

---

# Release Notes — v0.19.1

## Patch: Guidance Indicator & Deep-Search Context Fixes

This patch release fixes two bugs introduced by the mid-loop guidance work in
v0.19.0:

1. **Guidance indicator leaked across sessions on switch** — When you sent
   mid-loop guidance and then switched sessions while the HTTP request was
   still in flight, the "guidance active" indicator and the local "Guidance
   sent" badge could appear on the *destination* session. The root cause was an
   `await` race: the shared `guidanceActive` signal and the `PromptInput`
   component's `guidanceSent` flag were set after the await resolved, without
   verifying the active session was still the same one that initiated the
   guidance. Fixed by capturing the session ID before the await and guarding
   the state updates, plus a reactive `createEffect` that clears stale badge
   state whenever the active session changes.

2. **Deep-search child session inherited parent `LoopControl`** — Child
   sessions spawned by `deep_search` were receiving the parent's `LoopControl`
   context, causing cancellation signals (e.g. guidance cancellation) to leak
   into the child loop. Fixed by stripping the parent `LoopControl` from the
   deep-search child session context so each loop owns its own control.

---

# Release Notes — v0.19.0

## Instant Mid-Loop Guidance — Stream Cancellation

This minor release makes mid-loop guidance **feel instant**. When you steer an
agent mid-task, ogcode now interrupts the in-flight LLM generation immediately
instead of waiting for the full response to stream in — so the loop acts on your
new instructions right away. It also fixes the "guidance queued, then nothing
happens" hang that occurred on free endpoints that stay connected but emit
nothing.

---

### ⚡ Cancel the LLM Stream (Not Just Tools)

Previously, sending mid-loop guidance cancelled only the currently-running tool
call; the model still had to finish its full generation (sometimes tens of
seconds) before the loop could proceed. Now the guidance handler cancels both
the LLM stream and any running tool, so the loop advances to the next iteration
and drains your guidance without delay.

- **Stream child context** — Each loop iteration derives a per-step child
  context for the LLM stream. `LoopControl` gains `CancelStream` / `CancelAll`
  so the guidance handler can cancel this child independently of the loop
  context — the stream winds down while the loop itself keeps running.
- **No retry, no error** — A guidance-cancelled stream is treated as a normal
  "stop", not a transient failure. The loop simply proceeds to the next
  iteration, injects the guidance into the system prompt, and resumes.
- **Connection hygiene** — Stalled streams are drained in a background goroutine
  so the provider unblocks and releases the underlying HTTP connection. Without
  this, leaked connections accumulate against a rate-limited endpoint's
  concurrency budget and stall the *next* request — the root cause of the
  observed hang.

### 🔗 Cancelled Partial Tool Calls Stay Valid

When the stream is cancelled mid-generation, the model may have already emitted
partial tool-call blocks. These are never executed, but leaving them unpaired
breaks the next API request (both Anthropic and OpenAI 400 on a dangling
`tool_use` without a matching `tool_result`).

- **`cancelPartialToolCalls`** — Marks each partial tool part as cancelled (so
  the UI stops showing it as running) and emits a single paired error
  `tool_result` user message for every cancelled `tool_use`, keeping the
  conversation history valid.
- **Invalid JSON sanitization** — A tool call interrupted mid-arguments leaves
  partial, invalid JSON. This is coerced to a valid empty object `{}` before
  being re-sent, so strict OpenAI-compatible endpoints don't reject or stall the
  resumed request.

### 🔄 Guidance-First Ordering

The guidance handler now pushes the guidance text **before** issuing the
cancellation. Ordering matters: if cancellation happened first, the loop could
wake, drain an empty queue, see the finished stream as a normal "stop", and exit
— silently dropping the guidance that lands a moment later.

### 🖥️ Frontend

- The in-flight cancel checkbox is relabelled from "Cancel tool" to **"Cancel
  current work"** to reflect that both the LLM stream and running tools are now
  interrupted. The tooltip is updated accordingly.

### 🧪 Tests

- `LoopControl` stream-cancel and cancel-all unit tests (including nil-safe and
  double-cancel guards).
- `cancelPartialToolCalls` coverage: tool_use↔tool_result pairing, invalid-JSON
  sanitization, and the vanished-parts (no empty message) edge case.
- A full end-to-end loop test (`TestRunLoop_GuidanceCancelsAndResumes`) that
  reproduces the stalled-stream hang and asserts the loop resumes with guidance
  injected into the resumed system prompt.
- An OpenAI provider test verifying context cancellation promptly closes a
  silent SSE stream's event channel.

---

*Full changelog: https://github.com/prasenjeet-symon/ogcode/compare/v0.18.0...v0.19.0*

---

# Release Notes — v0.18.0

## Mid-Loop Guidance & Model-Switch Popover

This minor release lets you **steer an agent mid-task** — inject a new
instruction into an already-running loop without starting a new turn, and
optionally cancel the in-flight tool call so the agent acts on your guidance
immediately. It also adds a confirmation **popover** when you switch models
with the Alt+1–4 hotkeys and renames the sidebar index to "Project Index".

---

### 🧭 Mid-Loop User Guidance

You can now send additional instructions to an agent **while it is still
working**, within the same user turn. The guidance is delivered at the top of
the next loop iteration as a trailing `<system-reminder>` system-prompt entry.
It is ephemeral — never persisted to the message DB — so it does not interfere
with compaction turn boundaries or agentic-memory prior_context slicing.

- **Side-channel design** — A new `LoopControl` type carries a per-session
  guidance queue and tool-cancel function via context. It is nil-safe and a
  no-op for CLI / search / indexer loops that do not wrap the context.
- **In-flight tool cancellation** — Optionally cancels the currently-running
  tool call so the loop can act on the new guidance immediately instead of
  waiting for the tool to finish. Cancelled tools get a clear
  "cancelled by user mid-loop guidance" result so the call/result pairing stays
  valid.
- **API** — `POST /api/session/{id}/guidance` accepts `content` and `cancelTool`.
  Returns 409 when no loop is running, so the frontend falls back to a normal
  prompt.
- **Frontend** — While a loop is running the prompt textarea stays enabled and
  submitting sends mid-loop guidance (always cancels the current tool). A
  visible send button while running, "Guidance sent" confirmation badge, and
  "Guidance queued" inline indicator are wired to `loop.guidance` SSE events.

### ✨ Model-Switch Popover (Alt+1–4)

Activating a model with the Alt+1–4 hotkey now shows a brief **glass popover**
that pops in, holds, and fades out over ~1.8s, centered near the top of the
screen. It shows the slot number badge, provider color dot, model name, and an
"active" label so you get immediate visual confirmation of the switch.

- Rendered at the app root so it appears on every screen (chat, plan, home,
  settings, etc.).
- The plan screen now also handles Alt+1–4 to switch the plan's own model
  (previously the hotkey only affected the hidden session model on plan
  screens) and reuses the session popup signal.

### 🗂️ UI Polish

- **Project Index rename** — The sidebar entry and page header previously
  labelled "Doc Index" are now renamed to **Project Index**, matching the
  unified index terminology.
- **OpenAI-compatible providers info card** — Removed the base-URL preset
  cards that were no longer accurate, leaving a cleaner info layout.

---

### 📥 Installation

**macOS/Linux:**
```bash
curl -fsSL https://ogcode.xyz/install.sh | sh
```

**Windows:**
```powershell
irm https://ogcode.xyz/install.ps1 | iex
```

**Homebrew:**
```bash
brew install prasenjeet-symon/tap/ogcode
```

**Docker:**
```bash
docker run -p 9595:9595 -v $(pwd):/workspace -w /workspace ghcr.io/prasenjeet-symon/ogcode:latest
```

---

*Full changelog: https://github.com/prasenjeet-symon/ogcode/compare/v0.17.1...v0.18.0*

---

# Release Notes — v0.17.1

## Anthropic Multi-Turn Thinking Fix

This patch release fixes **multi-turn extended thinking** with Anthropic
(Claude) models. When a model produced `thinking` blocks on one turn, those
blocks — and their cryptographic signatures — were silently dropped before the
next API call, which broke multi-turn thinking with an API error. Thinking and
redacted-thinking blocks are now correctly preserved and forwarded.

---

### 🧠 Forward Thinking Blocks on Subsequent Turns

Anthropic requires that `thinking` blocks (with their cryptographic signatures)
be passed back unchanged on subsequent turns — dropping them breaks extended
thinking. Previously, reasoning content was captured and stored but never sent
back to the API.

- **Signature storage** — A `Signature` field was added to `ReasoningPartData` and
  `StreamEvent`, and a new `EventReasoningSignature` stream event type captures
  `signature_delta` events from Anthropic streaming responses so signatures are
  persisted alongside the reasoning text.
- **Cross-provider carry** — A `ReasoningPart` type was added to `ModelMessage`
  for carrying thinking blocks across the provider abstraction. The Anthropic
  provider now emits thinking blocks as content blocks in assistant messages,
  ordered before text/tool_use blocks per the API contract.
- **Unaffected providers** — OpenAI-family providers handle reasoning tokens
  server-side; the `ReasoningParts` field is simply ignored.

### 🔒 Redacted-Thinking Handling

- **`redacted_thinking` blocks** — Anthropic returns `redacted_thinking`
  content-block-start events carrying only a signature with no text deltas.
  Dropping them broke multi-turn thinking. The stream parser now handles these
  events, emitting an empty reasoning event plus a signature event; the
  signature handler persists it to an existing reasoning part so a
  redacted-only block still stores its signature and is forwarded correctly.

### 📏 Reasoning Counted in Request-Size Estimate

- **Proactive compaction** — `estimateRequestSize` now sums
  `ReasoningParts` text and signature lengths. Without this, a thinking-heavy
  history could silently exceed the model context limit before the proactive
  compaction heuristic triggered.
- **Tests** — Added coverage for `redacted_thinking` event parsing,
  redacted-thinking forwarded as a thinking block, and reasoning parts counted
  in the request-size estimate.

---

### 📥 Installation

**macOS/Linux:**
```bash
curl -fsSL https://ogcode.xyz/install.sh | sh
```

**Windows:**
```powershell
irm https://ogcode.xyz/install.ps1 | iex
```

**Homebrew:**
```bash
brew install prasenjeet-symon/tap/ogcode
```

**Docker:**
```bash
docker run -p 9595:9595 -v $(pwd):/workspace -w /workspace ghcr.io/prasenjeet-symon/ogcode:latest
```

---

*Full changelog: https://github.com/prasenjeet-symon/ogcode/compare/v0.17.0...v0.17.1*

---

# Release Notes — v0.17.0

## Model Hotkeys & Dark HTML Output

This minor release adds **Alt+1–4 keyboard shortcuts** for instant model switching and makes HTML code-block output render with a dark background by default.

---

### ⌨️ Model Hotkey Slots (Alt+1–4)

You can now switch models with a single keystroke — no more hunting through the picker mid-conversation.

- **Four hotkey slots** — Alt+1 through Alt+4 each map to a configurable model. The default model fills slot 1 on first launch.
- **Settings UI** — A new "Model Hotkeys" section in Settings → General lets you assign models to each slot using the same ModelSelector popover used elsewhere, keeping the UI consistent.
- **Keyboard shortcuts** — Press Alt+1 / Alt+2 / Alt+3 / Alt+4 (Option on macOS) to switch models instantly. The active model badge updates immediately.
- **Persistent** — Hotkey assignments are saved to the session store and survive page reloads.

### 🌑 Dark-by-Default HTML Output

HTML code blocks (rendered in sandboxed iframes) now default to a dark color scheme, matching the existing dark UI theme.

- **`color-scheme: dark`** — The iframe root element sets `color-scheme: dark`, which activates dark-native form controls, scrollbars, and default text colors without any custom CSS.
- **Backward-compatible** — Existing HTML content that specifies its own colors continues to work exactly as before. The `color-scheme` declaration only affects elements using system defaults.

---

### 📥 Installation

**macOS/Linux:**
```bash
curl -fsSL https://ogcode.xyz/install.sh | sh
```

**Windows:**
```powershell
irm https://ogcode.xyz/install.ps1 | iex
```

**Homebrew:**
```bash
brew install prasenjeet-symon/tap/ogcode
```

**Docker:**
```bash
docker run -p 9595:9595 -v $(pwd):/workspace -w /workspace ghcr.io/prasenjeet-symon/ogcode:latest
```

---

*Full changelog: https://github.com/prasenjeet-symon/ogcode/compare/v0.16.1...v0.17.0*

---

# Release Notes — v0.16.1

## Frontend Stability Fix

This patch release fixes a **UI crash to a blank screen** that could occur when
using Ollama-compatible proxies that emit malformed or incomplete tool-call data.

---

### 🐛 `ToolPartDisplay` Defensive Guards

- **Fix** — The `ToolPartDisplay` SolidJS component no longer throws a `TypeError`
  when the backend creates a tool part without a valid `state` or `tool` property.
  This happened when Ollama proxies sent truncated tool-call payloads, which
  previously aborted the entire render and blanked the screen.
- **Defaults** — A `DEFAULT_TOOL_STATE` fallback and safe accessor functions were
  added; all direct references to `props.data.state` / `props.data.tool` now route
  through these accessors, so missing fields degrade gracefully instead of crashing.
- **No behavior change** for well-formed data — the guards only engage when fields
  are absent or malformed.

---

### 📥 Installation

**macOS/Linux:**
```bash
curl -fsSL https://ogcode.xyz/install.sh | sh
```

**Windows:**
```powershell
irm https://ogcode.xyz/install.ps1 | iex
```

**Homebrew:**
```bash
brew install prasenjeet-symon/tap/ogcode
```

**Docker:**
```bash
docker run -p 9595:9595 -v $(pwd):/workspace -w /workspace ghcr.io/prasenjeet-symon/ogcode:latest
```

---

*Full changelog: https://github.com/prasenjeet-symon/ogcode/compare/v0.16.0...v0.16.1*

---

# Release Notes — v0.16.0

## Zero-Config Free Models

This minor release lets **new users start coding immediately — no API key, no
setup**. On first launch, ogcode fetches a community pool of free,
OpenAI-compatible models and makes them available out of the box, grouped under a
dedicated **"ogcode"** collection with a sensible default already selected.

---

### ⚡ Free Model Pool (zero setup)

ogcode now ships usable the moment it opens — no credentials required.

- **Auto-provisioned providers** — On startup, ogcode fetches a JSON pool of
  OpenAI-compatible free providers (Groq, OpenRouter) from a public URL and
  registers them automatically. They never override your own configured providers.
- **Free models only** — For OpenRouter the list is restricted to its `:free`
  variants, and every free model is enabled by default, so you land ready to chat
  instead of on an empty picker.
- **Default model** — New users start on **North Mini Code**
  (`cohere/north-mini-code:free`), a coding-focused free model, selected by default.
- **Resilient** — The pool is cached locally (24h TTL, atomic writes,
  stale-on-error fallback), so startup never blocks on the network.
- **Keys never exposed** — The `/api/providers/free` endpoint reports available
  free providers with their keys masked.

### 🗂️ "ogcode" Collection

All free-pool models are grouped under a single **ogcode** collection so they stay
separate from your own OpenAI / Anthropic / OpenRouter / Ollama / Groq models. Each
free model is tagged with its underlying provider (e.g. **OpenRouter**, **Groq**)
so you always know where a model comes from.

### 🚪 No Onboarding Required

Because free models work out of the box, new users are no longer forced through the
setup wizard. The onboarding screen stays reachable from Settings for anyone who
wants to add their own provider keys.

### ✨ Model Picker & Settings Polish

- The model-picker dropdown is wider so long model names render cleanly, with a
  per-model provider tag.
- The **Add custom model** form now opens at the top of Settings → Models, visible
  immediately when you click the button.
- **Fix** — The memory-savings popover is now correctly centered on screen (it was
  being clipped by the blurred header) with a visible close button.

---

### 📥 Installation

**macOS/Linux:**
```bash
curl -fsSL https://ogcode.xyz/install.sh | sh
```

**Windows:**
```powershell
irm https://ogcode.xyz/install.ps1 | iex
```

**Homebrew:**
```bash
brew install prasenjeet-symon/tap/ogcode
```

**Docker:**
```bash
docker run -p 9595:9595 -v $(pwd):/workspace -w /workspace ghcr.io/prasenjeet-symon/ogcode:latest
```

---

*Full changelog: https://github.com/prasenjeet-symon/ogcode/compare/v0.15.0...v0.16.0*

---

# Release Notes — v0.15.0

## `view_image` Agent Tool

This minor release adds a new agent tool that lets vision-capable models **see
image files on disk** — logos, screenshots, diagrams, photos — directly within a
task, without the user having to manually upload them.

---

### 🖼️ `view_image` Agent Tool

Agents can now read image files (PNG, JPEG, GIF, BMP, WebP) from the filesystem
and return them to the model for visual inspection. This complements the existing
image upload feature (v0.14.0) by enabling agents to proactively look at images
they discover during exploration.

- **New tool** — `view_image` accepts a file path (absolute or relative to the
  session directory) and returns the image so vision-capable models can see it.
  Large images are automatically downscaled to fit within vision-model limits.
- **Use cases** — Inspecting logos, screenshots, UI mockups, diagrams, or any
  image referenced in a task without requiring the user to attach it manually.
- **Format support** — PNG, JPEG, GIF, BMP, and WebP.

---

### 📥 Installation

**macOS/Linux:**
```bash
curl -fsSL https://ogcode.xyz/install.sh | sh
```

**Windows:**
```powershell
irm https://ogcode.xyz/install.ps1 | iex
```

**Homebrew:**
```bash
brew install prasenjeet-symon/tap/ogcode
```

**Docker:**
```bash
docker run -p 9595:9595 -v $(pwd):/workspace -w /workspace ghcr.io/prasenjeet-symon/ogcode:latest
```

---

*Full changelog: https://github.com/prasenjeet-symon/ogcode/compare/v0.14.0...v0.15.0*

---

# Release Notes — v0.14.0

## Image Uploads, Anthropic Base URL Config, and Product Analytics

This minor release introduces three significant features: **image uploads** for
vision-capable models, a **configurable Anthropic base URL** for proxy/gateway
setups, and **PostHog product analytics** for usage insights.

---

### 🖼️ Image Uploads in Chat

Users can now attach images to chat prompts — either via the file picker button
or by pasting from the clipboard. Images are sent to vision-capable models
(Claude, GPT-4o, etc.) and rendered inline in the message list.

- **Multi-modal messages** — A new `PartImage` type and `ImagePartData` struct
  persist images as first-class message parts. The agent loop attaches user
  image parts to `ModelMessage.Images` so both Anthropic and OpenAI-compatible
  providers (OpenRouter, Ollama) receive them as image content blocks — no
  provider changes were needed.

- **UI integration** — The prompt input bar has a new file picker button,
  paste-from-clipboard support, image preview thumbnails with per-image remove
  buttons, a 10 MB size limit, and file-type validation. Image parts render
  inline in user messages with click-to-expand.

---

### 🔗 Configurable Anthropic Base URL

The Anthropic provider now supports a custom base URL, mirroring the existing
OpenAI/Ollama configuration. This enables use of Anthropic-compatible proxies,
gateways, or self-hosted endpoints.

- **Resolution priority** — UI/DB setting → `ANTHROPIC_BASE_URL` environment
  variable → `https://api.anthropic.com/v1` default.
- **Settings UI** — The field appears in Settings → Models → API Keys and in the
  onboarding wizard, with an Anthropic-specific placeholder and a "set via env"
  indicator.
- **Provider wiring** — `AnthropicProvider` gains a `baseURL` field;
  `StreamChat` uses it. `NewProviderWithConfig` applies it for the `anthropic`
  case. Tests neutralise the env var for deterministic runs.

---

### 📊 PostHog Product Analytics

PostHog cloud analytics is now integrated for internal product insights —
capturing page views, session recordings, and custom events from the web UI,
plus server-side lifecycle events from the Go backend.

- **Server-side events** — A lightweight PostHog client
  (`internal/server/posthog.go`) sends server lifecycle events
  (`ogcode_server_started`, `ogcode_server_stopped`) to the PostHog `/capture`
  REST endpoint via a bounded background worker — non-blocking, always-on.
- **Frontend SDK** — The `posthog-js` library is lazily initialised on app load
  with page view capture and session recording enabled (autocapture disabled to
  avoid noise).
- **Hardcoded credentials** — Analytics uses hardcoded project credentials
  (not user-configurable); it is an internal feature, not a user-facing setting.

---

### 📥 Installation

**macOS/Linux:**
```bash
curl -fsSL https://ogcode.xyz/install.sh | sh
```

**Windows:**
```powershell
irm https://ogcode.xyz/install.ps1 | iex
```

**Homebrew:**
```bash
brew install prasenjeet-symon/tap/ogcode
```

**Docker:**
```bash
docker run -p 9595:9595 -v $(pwd):/workspace -w /workspace ghcr.io/prasenjeet-symon/ogcode:latest
```

---

*Full changelog: https://github.com/prasenjeet-symon/ogcode/compare/v0.13.7...v0.14.0*

---

# Release Notes — v0.13.7

## Anthropic Prompt Caching

This release introduces explicit prompt caching for Anthropic models (Claude
Sonnet, Haiku, Opus) to reduce latency and token costs on multi-turn
conversations. The system prompt prefix is now cached across turns so that
repeated tool definitions and base instructions are read from cache instead of
re-processed every call.

---

### ⚡ Anthropic Prompt Caching

- **Explicit `cache_control` breakpoints** — The Anthropic provider now sends
  the `system` field as an array of content blocks (required to attach
  `cache_control` markers) instead of a plain string. Two cache breakpoints are
  placed: one on the last tool definition (caches the entire tool block) and
  one on the first static system block (caches tools + base system prompt).

- **Static/dynamic system prompt separation** — The current date was extracted
  from the base system prompt and injected as a separate dynamic
  `<system-reminder>` entry. Only the static prefix receives `cache_control`;
  dynamic trailing blocks (date, compaction summaries) do not. This keeps the
  cacheable prefix byte-for-byte identical across turns.

- **Token usage tracking** — The provider reads `cache_creation_input_tokens`
  and `cache_read_input_tokens` from the Anthropic response and surfaces them
  in `TokenUsage`, so callers can observe cache hits and misses.

- **Other providers unaffected** — OpenAI (automatic prefix caching), Ollama,
  and OpenRouter remain functionally identical. The static/dynamic split is
  recombined into a single string for OpenAI-compatible providers.

- **Tests** — New test cases in `anthropic_test.go` verify cache breakpoint
  placement, system block splitting, and token usage parsing. New tests in
  `prompt_builder_test.go` verify the separation of static and dynamic system
  prompt content.

---

### 📥 Installation

**macOS/Linux:**
```bash
curl -fsSL https://ogcode.xyz/install.sh | sh
```

**Windows:**
```powershell
irm https://ogcode.xyz/install.ps1 | iex
```

---

# Release Notes — v0.13.6

## DOCX Indexing & Unified Project Index

This release adds full DOCX (Word document) support to the document indexing
pipeline and unifies PDFs into the project index tree, so agents can discover
and read both PDFs and DOCX files from a single `codebase_map` call.

---

### 📄 DOCX Indexing Support

Word documents (`.docx`) are now first-class citizens in the indexing system,
with the same level of support as PDFs:

- **Extraction pipeline** — A new `internal/docx` package parses DOCX files,
  handling paragraph properties, tables, hyperlinks, structured document tags,
  and explicit page breaks. Documents without explicit breaks are split into
  pseudo-pages (~500 words each) for consistent indexing.

- **Agent tools** — Two new tools are available to agents:
  - `docx_index` — Returns the semantic page labels for a DOCX file, just like
    `pdf_index` does for PDFs.
  - `read_docx_page` — Extracts the plain text of a single pseudo-page from a
    DOCX file, similar to `read_pdf_page`.

- **Automatic indexing** — DOCX files are detected during directory walks and
  processed in their own batch alongside PDFs. The docindex UI shows DOCX files
  with a distinct blue badge and document icon.

- **Project index** — `codebase_map` now includes DOCX files in the unified
  project tree, showing their semantic labels alongside text and code files.

- **10 test cases** covering real-world DOCX structures including tables,
  hyperlinks, nested content, and mixed page-break scenarios.

### 🗂️ Unified Project Index with PDFs

PDFs are now part of the `codebase_map` project tree instead of being separate:

- PDF entries appear as leaves with up to 15 de-duplicated topic labels — enough
  to understand what a document covers without overwhelming the agent.
- Per-page detail remains available via the dedicated `pdf_index` tool.
- The `pdf_index` tool now returns only semantic labels (keyword corpora are no
  longer exposed to agents — they were raw indexing artifacts).

---

### 📥 Installation

**macOS/Linux:**
```bash
curl -fsSL https://ogcode.xyz/install.sh | sh
```

**Windows:**
```powershell
irm https://ogcode.xyz/install.ps1 | iex
```

**Homebrew:**
```bash
brew install prasenjeet-symon/tap/ogcode
```

**Docker:**
```bash
docker run -p 9595:9595 -v $(pwd):/workspace -w /workspace ghcr.io/prasenjeet-symon/ogcode:latest
```

---

*Full changelog: https://github.com/prasenjeet-symon/ogcode/compare/v0.13.5...v0.13.6*