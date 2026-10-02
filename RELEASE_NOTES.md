# Release Notes — v0.44.0

## Minor: Project notes arrives gradually

Project notes is now released a step at a time rather than all at once. On an
install where it has not yet been switched on, the feature is simply absent —
no Notes entry in a session's sidebar, no save-to-notes action on a message,
and the notes pages fall through to the ordinary not-found page as though they
were never there. Nothing else changes, and notes already kept are untouched;
they reappear the moment the feature is switched on.

Whether it is on is decided per install and re-checked while the app runs, so
it can turn on without a restart and the interface follows in place.

## Minor: A turn's reasoning reaches the memory it writes

Every completed turn is summarised into a short note that later turns read.
That note is built from the agent's final reply, so the reasoning behind a
decision — why one approach was taken and another set aside — was lost once the
reply was written up. The agent now closes a reply that involved real decisions
with a brief "Decisions & why" section, and the memory writer keeps that
section rather than paraphrasing it away. A turn with nothing to weigh in on
leaves it out, so ordinary replies stay uncluttered.

## Other changes

- The Plan and Breakdown agents no longer list project notes among the things
  to read when they start, because notes now reach them through the switch
  above instead of being named unconditionally in their instructions.

---

# Release Notes — v0.43.1

## Patch: A generated title reaches the sidebar for any session

A session's row carries its title and its utility token totals, and the server
publishes `session.updated` whenever that row changes. The event was applied
only to the session on screen, so a generated title — or a utility total — for
any other session, or one still to be opened, never reached the sidebar.

The event's row is now patched into the session list for every session, keeping
the sidebar current; only the header and the token view stay scoped to the
active session.

## Other changes

- The guides at `/docs/` now say what the code-map, file-map, and context tools
  buy you rather than how they work inside, and the deployment guide no longer
  names internal state paths and endpoints a reader has no use for.

---

# Release Notes — v0.43.0

## Minor: Long transcripts arrive in pages, and hold their place

Opening a session with hundreds of messages used to mean rendering every one of
them, and scrolling back re-read the lot. The editor now fetches the transcript
a page at a time, renders only what is on screen, and remembers where you were.

- **Paged, newest first.** `GET /api/session/{id}/message` and
  `GET /api/plans/{id}/message` return 300 messages at a time and answer an
  `X-Has-Older` header, so the editor knows whether more history exists;
  `?limit=` fetches only the newest few on a refresh. The header is exposed
  cross-origin, so a client on another origin can read it too.
- **Only what you can see is rendered.** Rows outside the viewport are
  unmounted and two padding elements hold their height, so the scrollbar stays
  honest and an older page joins without a jump. A "Loading earlier messages"
  pill sits over the transcript while a page is on its way, positioned so it
  never shifts the text.
- **It picks up where you left off.** A session's place is remembered by the
  message you were reading rather than a pixel offset, so a long transcript
  reopens on the same row it was left on. Per-session caches, kept for the last
  eight sessions, make switching back instant.
- **The jitter is gone.** Scrolling upward no longer shudders, because a single
  component writes the scroll position each frame instead of two correcting
  each other.
- **A page loads in one query.** The tool results for a page of messages are
  read in a single batched query instead of one query per message, and a page
  always carries a `parts` array — never `null` — so a message written before
  parts existed can no longer crash the view.

## Minor: The token pill counts the whole session

The totals shown under a session summed only the messages the editor happened to
be holding — the newest page — so a long session under-reported every row.
`GET /api/session/{id}/token` now totals the whole transcript on the server,
every assistant step plus the utility calls (risk checks, compaction summaries,
titles), and the pill reads it. The plan view keeps its own totals.

## Minor: Your logs are kept, rotated, and redacted

ogcode now writes what it was doing to a log file, so a crash, a failed call,
or a slow start can be looked at after the fact instead of being re-run to be
seen.

- **Where.** One file per project under `~/.ogcode/logs/<project>-<hash>/`,
  owner-only, alongside the terminal output you already get.
- **Rotated and bounded.** A file is rolled over past 10 MB, the last 5 rollovers
  are kept (compressed), and anything older than 14 days is deleted — so the log
  cannot quietly grow without limit.
- **Redacted.** Credentials, tokens, and keys that would otherwise land in a
  request are stripped before they are written.
- **Tunable, or silent.** `OGCODE_LOG_LEVEL` (the file, default `info`),
  `OGCODE_LOG_FORMAT` (`text` or `json`), and `OGCODE_LOG_CONSOLE` (the terminal,
  default `error` — only what you need to act on) set the thresholds;
  `OGCODE_LOG_DIR`, `OGCODE_LOG_MAX_SIZE_MB`, `OGCODE_LOG_MAX_FILES`,
  `OGCODE_LOG_MAX_AGE_DAYS`, and `OGCODE_LOG_COMPRESS` control where and how
  much is kept.

## Minor: The download, the install and your first session are one person

Until now the three hops could not be joined: the website recorded the click
under one id, the installed binary minted its own, and the editor a third — so
"someone downloaded, installed, and started a session" was unmeasurable.

- **One id, carried the whole way.** Copying an install command stamps your id
  onto it; the installer saves it to `~/.ogcode/install-id`; the binary reports
  the install once (`ogcode_installed`, with the channel it detected — scoop,
  Homebrew, cargo, the install script, winget); and the editor reads it back from
  the server, so the session it starts carries the same id.
- **The kinds of projects ogcode is used on.** Once per project, the editor
  reports a single dominant label derived from the files present — a low-
  cardinality `go`, `typescript`, `python`, and so on — honouring `.gitignore`
  and the index excludes. A checkout with no recognisable source reports nothing
  rather than stamping the project done.
- **What is sent, and what is not.** Only counts, lengths, and labels — never
  content. `docs/index.html` keeps the copy button's command clean on screen;
  only the copied text is stamped, and brew (which cannot carry the variable) is
  left untouched.

## Minor: Titles run on the model you chose

A "fast model" heuristic picked the first model whose id contained `haiku`,
`mini`, or `flash`. On a machine whose catalogue lists `minimax-m3:cloud` first,
every title was generated on it — even in a session using a different model.

The title now runs on the session's own model, like the risk check, the
compaction summary, and the memory summary. There is no more substring matching
of model ids to guess at a small one.

## Minor: The Swift grammar reads what it should

Two Swift constructs misparsed, and both had workarounds users should not need:
an empty tuple `()` and a cast followed by a newline and `??`. The grammar now
parses them — regenerated from a newer upstream revision, with a repair that
rewrites an empty tuple only where the tree says it is one, and re-parses after
the first error.

## Minor: Diagrams zoom, the accent is the brand orange, and the tool rows settle

- **Zoom any diagram.** Mermaid, Plotly, Rough, and LaTeX diagrams gain a
  magnifier at their corner that opens a larger view in a dialog.
- **The default accent is the brand orange** `#ff5e1f`, with a warm tint for
  links and highlights. The violet preset stays available, and the logo and
  favicon are unchanged.
- **Smaller fixes.** The "time to first token" label now sits flush on the text
  column instead of a few pixels left of it, and the housekeeping tool rows
  (`codebase_map`, `file_map`, `compact_context`) show a quiet magic-wand glyph
  rather than a checkmark.

## Minor: The agent reviews its own work

The agent's instructions now ask it to trace what a change reaches before
calling it done — to run what it can, read past the lines it changed, and mark
what it could not check as unverified rather than reporting it as fine. It also
runs the change the way it is used, not only through its tests, and writes down
a fact only once it has been seen to hold.

# Release Notes — v0.41.1

## Patch: The Linux build ships again

v0.41.0 tagged and built every platform but Linux. The C and C++ grammars it
added ship a `build.zig`, and zig keeps its local build cache beside the build
root — which, for a grammar pulled from the Go module cache, is a read-only
directory:

    unable to open local cache directory
    '.../tree-sitter-c@v0.24.2/.zig-cache': AccessDenied

The release workflow now points `ZIG_LOCAL_CACHE_DIR` at a writable path, so the
Linux binaries build without being asked to write into the module cache.

---

# Release Notes — v0.41.0

## Minor: Syntax checks that catch what they missed, and stop crying wolf

The syntax check that runs on every `write` and `edit`, and in `check_syntax`,
was reviewed and fixed.

- **Python indentation is checked.** The grammar accepted every indentation
  error CPython rejects (unexpected indent, a block header with no body, a
  dedent to a level never opened, tabs traded for spaces), so the most common
  way an edit breaks a Python file came back "OK". A new pass applies
  CPython's own rules. Against CPython's compiler, over its whole standard
  library and about 5,000 injected mistakes, it raised no false alarms and
  caught every indentation error.
- **No more "OK" for some broken Go.** A typo such as `funcHandler(w, r) {` or
  `pa;ckage main` left an error the parser only marks invisibly, and the check
  passed the file.
- **Fewer false alarms on valid code.** Valid syntax newer than a grammar —
  CSS nesting and named `@container` queries, `22.5%` keyframes, TypeScript's
  `export type *`, Rust's `safe fn`, Java's `import module` — no longer reads
  as an error. The site's own homepage had fifteen. When something newer still
  trips the parser, the report now says to confirm with the compiler rather
  than rewrite the code.
- **More files are checked.** JSON (with comments where tools allow them),
  JSON Lines, YAML, TOML, and the `<script>` and `<style>` blocks inside HTML.
- **Clearer reports.** Columns count characters, not bytes; a capped list says
  "20+"; the hint to see the full list names the file's real path; pointing the
  tool at a directory says so instead of failing.
- **You can see it too.** A file-edit row in the session view now shows a red
  chip when the change left syntax errors (amber when the file was already
  broken), and the note appears under the diff.

## Minor: C and C++ joins the code map

The code map now reads C and C++ — the files the languages are most often
inherited by, from kernels and drivers to embedded and game code — so `file_map`,
`codebase_map` and the project index can outline them instead of falling back to
a line scanner.

- **Outline only, deliberately.** C and C++ are mapped, not syntax-checked.
  Macros let tree-sitter parse code a compiler would reject — a `#define` that
  opens a brace, a body only some configurations build — so a check tuned to
  accept real headers would have to miss real errors, and one that did not would
  cry wolf on working code. The map is where the grammar earns its keep.
- **`.h` reads as C++.** A header is far more likely to use C++ than C, and the
  C++ grammar parses the C subset, so the two do not meaningfully differ for an
  outline.
- **A region, not the whole file.** `file_map` gained `start_line` and
  `end_line`, so a very large header can be mapped one range at a time rather
  than paging through it.

## Minor: The map's labels are a sample of everything, not the top of a ranking

A folder line used to rank its labels by frequency and show the top forty — which
is the wrong signal for orientation, since the fortieth most frequent topic says
nothing about what a folder is for. Labels are now drawn as a sample spread
across the folder: up to twenty, taking the first from each distinct source, so a
line names the range of what is inside rather than repeating its most common
word.

- **Folders name their subfolders.** A folder line lists the names of the
  subfolders directly inside it (up to twenty-four), so you can jump to one with
  `subdir` instead of guessing its name or reading the parent again.
- **Memory tags are unique now.** A conversation's file-name tag was a fixed
  eight characters — the session-id prefix and the first five characters of a
  timestamp — which is identical for conversations created in the same second,
  so two could share a tag. Tags now grow as long as they need to be unique, and
  `memory_map` accepts any unambiguous prefix of the session id, refusing one
  that could mean either of two conversations instead of silently picking one.

## Minor: Your turn's requests go first

A shared in-flight budget already kept the agent from opening more provider
requests than a provider would serve at once. The indexer shared it on equal
terms, so a large re-index could fill it and leave an interactive turn waiting
behind work the user never asked to run.

- **Index requests yield to interactive ones.** A turn's requests wait only on
  other turn requests; the indexer starts work only into the room the user
  leaves — at most one request while a turn is running, and nothing for thirty
  seconds after the last turn went quiet. The budget still bounds the total, so
  a provider is never overloaded.
- **Measured.** With five index sessions running, interactive requests returned
  a median of twelve seconds (down from a fifty-second tail); on an idle install,
  2.6 seconds.

## Minor: Same-file tool calls run in the order they were written

When the model issued several calls at one file in a single batch — an edit, then
a `check_syntax`, or two edits to different parts of the same file — they ran
concurrently, so a read could return pre-edit lines, a check could parse bytes an
edit had not yet written, and two edits could race for the file lock. They now
run in the order the model wrote them.

- **A change waits for every earlier call on the file.** A look — `read`,
  `file_map`, `check_syntax` — waits only for the changes ahead of it, so reads
  stay fast. Independent files are still worked in parallel; only the calls that
  touch one file are serialised, and only against each other.

## Minor: The OGX plan is re-read from the gateway

The OGX tab showed the plan it last saw, so a plan that changed on the OGLAB side
— an upgrade, a lapse, a revocation — only showed up after a restart or a
reconnect. Settings → Models now asks the gateway directly.

- **Checked on open, and on focus.** Opening the models screen re-reads the
  plan, and returning to the tab re-checks it at most once every thirty seconds,
  so a change on OGLAB appears without a restart. A refresh button asks again on
  demand.
- **Revoked is its own state.** When the gateway refuses the install's token it
  is told so plainly — the link is gone and reconnecting is the fix — rather than
  being left to read as "connected but no models". A gateway that cannot be
  reached is reported as unreachable, not as a plan that grants nothing.

## Minor: The models screen, rebuilt around the plan

The Settings → Models screen was reorganised and the plan's product name settled
to **OGLAB** throughout.

- **The plan leads.** OGX — the OGX subscription plan by OGLAB — is the first
  tab the screen opens on, since for most people it is the reason to be there.
  Its panel shows the plan, its models, a link to usage and billing, and a
  disconnect that unlinks the install only.
- **Disconnect is a red button**, not a link buried beside a helper line, so an
  action that removes the account cannot be taken by accident.

## Patch: Guidance, from queued to applied

Mid-turn guidance used to show a single "sent" state that could not tell a
message still waiting for the loop from one the agent had picked up — so a
suggestion could look delivered while the model had not yet seen it. The
composer now tracks the two: *queued* until the running loop drains it, then
**Guidance applied — the agent is acting on it** once it has. The server
publishes the queued state the moment it accepts the text, before the loop can
drain it, so the two are never confused.

## Patch: Smaller things

- **Kotlin outlines.** A Kotlin file mapped its classes and none of its
  functions, because the modifiers stack ahead of `fun` (`override suspend fun`,
  `data class`). The fallback scanner now skips them, and an extension
  function's receiver, so the entry is named for the function.
- **Markdown headings stop eating trailing `#`s.** A heading like `# Using C#`
  came back as "Using C". A heading now follows CommonMark: a closing run of `#`s
  only counts when it is set off by a space. Fenced blocks (``` and ~~~, and YAML
  front matter) are no longer read as headings, and a final newline no longer
  appends an empty line to every map.
- **MCP image files are swept up.** Every image an MCP tool returned was written
  to a temp directory and left there for the lifetime of the process; the
  directories are now removed when the MCP manager closes.
- **Mermaid diagrams render under `antiscript`.** The renderer no longer sets
  `strict` — which stripped the click handlers and link targets people put in
  diagrams — but still refuses raw HTML and scripts.
- **Rough diagrams draw their arrows and text again** after the renderer
  update left them unfilled.
- **The token ledger's "By model" section** dropped a description that restated
  its own heading.

---

# Release Notes — v0.40.0

## Minor: A built-in catalogue of model facts

ogcode now knows the context window, prices (cache reads and writes included)
and image support of the current Anthropic and OpenAI models and the major
open-weight families — DeepSeek, Qwen, Kimi, GLM, MiniMax, Llama and Muse,
Mistral, Gemma, gpt-oss, Nemotron and Phi — checked against each vendor's docs
on 2026-09-28. The facts follow the model, not the provider: it is recognised
under any host's name for it (`anthropic/claude-sonnet-5:batch` on OpenRouter,
`glm-5.3-flash:cloud` on Ollama, `llama-3.3-70b-versatile` on Groq), including
a model you add by hand. A name that means different models on different hosts,
like `deepseek-r1`, is left unknown rather than guessed.

- **Windows.** Compaction and the context meter use the smallest window
  anything knows: the catalogue's, the host's own listing, or one learned from
  an overflow. A host that serves less than the vendor (MiniMax M3 at 512K)
  wins; one that claims more (OpenRouter's 1M for Claude Sonnet 4.5) does not.
  A model running on your own Ollama is left to what the instance reports, since
  it runs at `num_ctx`, not the model's maximum. Settings → Models shows each
  model's window next to its price, and a context meter beside the token pill
  shows how full the last request was and where compaction happens.
- **Prices.** OpenRouter's own per-model prices are now read, so a `:free`
  variant shows as free and a hosted model at OpenRouter's rate. Elsewhere the
  vendor's price applies, and local Ollama, Ollama Cloud and OGX show none, since
  none of them bills per token. `ogcode run` prices cache traffic at the
  published cache rates.
- **OpenAI requests.** GPT-5.4 and later take tools on Chat Completions only
  with reasoning off, so agent steps to them send `reasoning_effort: "none"`;
  utility calls send each model's lowest effort, reasoning models get no
  `temperature`, and `max_completion_tokens` replaces `max_tokens`. Models whose
  tools need the Responses API (GPT-6 Astra, the pro and Codex models) are no
  longer offered. Kimi's current models and Claude Opus 4.7+ get no temperature
  on any host.
- **Defaults.** New installs start on Claude Sonnet 5, GPT-6 Sol and, on
  OpenRouter, `anthropic/claude-sonnet-5`, with the current generation enabled
  (Claude Fable 5.1, Opus 5.5, Sonnet 5, Haiku 4.5; GPT-6 Sol and Luna, GPT-5.6
  Sol and Terra). Retired models are gone from the lists; the ones other hosts
  still serve stay catalogued.

## Minor: Token totals count every token, and every call

Three accounting fixes, found by checking live usage from Groq, Gemini,
OpenRouter, Ollama Cloud and a local Ollama against what ogcode recorded.

- **Gemini's thinking is counted.** Its OpenAI-compatible endpoint bills thinking
  as output but reports it only in `total_tokens`. The gap between that and
  prompt + completion now goes to output and reasoning; a live gemini-2.5-flash
  step had recorded 24 output tokens where 263 were billed. When the gap instead
  equals the cached count, the server reported the cache outside
  `prompt_tokens`, so input is kept rather than clamped to zero.
- **One definition of a total.** Every total — per step, per session, utility,
  `ogcode run`'s `total`, the token pill — is now input + cache read + cache
  write + output (`TokenCounts.Consumed`), the figure providers report as
  `total_tokens`. v0.35.0 had left cache reads out of session totals, which
  showed a well-cached step that processed 3,267 tokens as 25 and left the pill's
  rows not adding up to its total. Cache reads are processed and billed on every
  step that sends them, so they count. The pill now marks reasoning and utility
  as parts of the rows above them.
- **Work outside the step loop is charged to its session.** Task and
  memory-recall sub-agents ran in ephemeral sessions that were deleted with
  their tokens, and the deep-search pipeline, the per-turn memory summary and
  plan auto-naming discarded their usage. The loop now stamps its session on the
  context: a sub-agent's run is folded into the session that spawned it before
  deletion, and the one-shot calls charge it directly, all in the utility
  subtotal. The Notes rewrite and transform calls have no session and are still
  not counted.

## Minor: Live previews at their own hostname, served only when published

A service the agent starts is now previewed at its own origin —
`http://3000.preview.localhost:<server-port>/` — instead of under
`/preview/3000/`, so an app that boots off its own location (Next.js and
friends) starts normally. Old `/preview/<port>/` links redirect there.

A preview hostname now serves only ports that were **published**: the agent
publishes a port by writing its preview URL in its reply (it takes effect as
soon as that step ends, not when the turn does), and you can add or remove one
on the Preview page. Everything else answers 403, and ogcode's own port is never
served. A loopback port is often private precisely because it is loopback-only,
and a preview link is something you share, so the proxy no longer opens every
port on the machine. Any other name under the preview domain answers 404 rather
than reaching ogcode, so a preview wildcard exposed more widely than the main
address never exposes ogcode itself.

Previewed apps behave as they do on their own: ogcode's CORS headers no longer
leak onto their responses (or swallow their preflights), they get their own
storage and cookies inside the Preview page, redirects to their loopback address
land back on the preview, and the scheme behind a TLS-terminating proxy is passed
on. An open preview no longer reloads every ten seconds.

`*.localhost` names resolve to the browser's own machine, so the default works
from a browser on the server (or through `ssh -L`). To open previews from
another machine, set `OGCODE_PREVIEW_DOMAIN` to a dedicated wildcard DNS name that
points at the server; the Preview page and the agent say so when they detect the
mismatch.

## Minor: A shared in-flight budget for provider requests

Provider requests are now capped process-wide, so a project index that fans out
across many documents can no longer put its whole wave on the endpoint at once.
The failure this prevents is not slowness but refusal: an index starts a session
per batch, each a full agent turn of two or more requests, and the burst can trip
the endpoint's rate limiter — which the retry path then answers with further
retries, so the burst amplifies the very throttling it caused. One shared ceiling
turns the wave into a queue that drains at the rate the endpoint accepts.

The ceiling is `OGCODE_PROVIDER_MAX_CONCURRENT` (default 8, minimum 2). Two of
those slots are reserved for interactive turns: the index and your own turns
share one endpoint and one rate limit, so without a reserve a background refresh
could hold every slot and leave your next message waiting behind it. The cap sits
on the provider send path for both OpenAI and Anthropic, so it is independent of
the indexer's own concurrency setting.

## Minor: A ledger of what you have spent

Every agent step and every utility call now writes one row to a ledger, so the
spend of every project, session and model can be totalled in one view. Settings
→ Usage shows it over Today, 7 days, 30 days or all time, for this workspace
(its task worktrees included), for all projects, or for one project opened from
the list.

The ledger keeps two very different numbers apart. **Billed** is what per-token
providers charged, at their own price. **Plan value** is what a flat plan or a
local model did, priced at the vendor's list price — the bill the plan spared
you, not a charge. A daily chart shows the shape of the spend, folding to weeks
on the longer ranges.

Prices are not stored with a row; they are applied when the ledger is read, from
the provider's listing or the built-in catalogue, so a corrected price reaches
history instead of being frozen into it. Each workspace's usage from before the
ledger existed is folded in once, and a row outlives the session it came from.

- **The endpoint is recorded, not just the provider.** A provider slot pointed
  somewhere else — the OpenAI slot at Z.ai, the Anthropic slot at DeepSeek —
  bills at neither the slot's name nor the model's, so the row keeps the host it
  actually called and is priced and named by it. Work whose provider was never
  recorded is inferred from what serves the model today, and says so; work that
  cannot be priced is never counted as a charge.
- **Rows are keyed, not appended blindly.** An agent step is filed under its
  assistant message id, so the same step recorded twice overwrites rather than
  doubles, and a utility call is one row of its own. Sessions deleted with their
  tokens leave their spend behind.
- **One source for session cost too.** The token pill's cost and `ogcode run`'s
  figures read from the same pricing, so a session's total and the ledger agree.

---

# Release Notes — v0.39.1

## Minor: First-party identity for the OGX gateway

ogcode now signs every call it makes to the OGX gateway, so the gateway can
admit this install as a first-party client rather than any process that holds a
bearer token copied out of it. Each request to the plan API and to chat
completions carries `X-Client-App`, `X-Client-Timestamp` and
`X-Client-Signature` — the base64url HMAC-SHA256 of the app name, the timestamp,
and the method and path, so a signature for one endpoint authorises no other and
cannot be replayed.

The shared secret is baked in at build time (`make build-server
OGX_APP_SECRET=...`, an ldflags value rather than a runtime variable), so a
release binary carries the identity while a local build leaves it empty and
signs nothing. The gate on the gateway side is conditional: with no secret
configured it stays open, so an older client keeps working against a gateway
that has not yet been told any secrets. Every other provider leaves the identity
empty and its requests unasserted.

## Patch: A calmer update notification

The "update available" toast is rebuilt on the app's design tokens: a flat,
elevated card with a hairline border and an accent icon, in place of the old
gradient header and emoji. Release notes render as markdown, collapsed to a
two-line preview that expands into a scrollable box, with a **Copy** command and
a **View release** link. It dismisses for 24 hours from the close button, or for
a week with **Don't show again**.

---

# Release Notes — v0.39.0

## Minor: Yolo — a permission mode that never asks

Yolo is the third permission mode, beside Ask and Auto. Where Auto asks the model
for a risk assessment before it runs a consequential call, Yolo skips the
assessment entirely and runs everything without a prompt — the fully unguarded
mode. The bash tool's own danger denylist still refuses the handful of commands
that can wreck a machine, and a rule you have configured to **deny** is still
denied: Yolo removes the *asking*, not the refusals.

The mode is the third pill in the composer's permission toggle, and it persists
as the machine-wide default for new sessions.

## Minor: Live preview of local services

A process you start on a loopback port is now reachable in the browser at
`/preview/<port>/`, proxied to `127.0.0.1` with WebSockets and streaming
responses included. Only the *port* comes from the request, and the host is
always loopback, so the route cannot be turned into a way to reach anything else.

A new Preview page lists every service it can find as a grid of tiles — found by
scanning listening ports and keeping the ones that answer with HTML, labelled by
their own page title — and any of them can be opened inline or in a new tab.
Ports added by hand, and a deep link to a specific port, are kept even when
nothing is listening yet. Starting several services at once is fine: each shows
up as its own tile.

## Minor: The model catalogue persists between restarts

The list of models a provider offers is now stored, so the model picker is a
plain read from the database instead of a live call to every provider on every
page load. A background refresh keeps it current — on a timer, and on demand
from **Refresh** — and a `models.updated` event tells an open tab when the
catalogue changes, so a model that appears on the gateway shows up in the picker
without a reload.

## Minor: Token totals now include utility calls

Title generation, the Auto-mode risk assessment, and compaction all spend
tokens, and until now those calls were invisible to every total. Their usage is
recorded per session and folded into the token pill and into `ogcode run`'s
summary, which reports the utility subtotal on its own line. The per-message and
session totals are otherwise unchanged.

Two provider-side accounting fixes ride along: DeepSeek's top-level cache fields
(`prompt_cache_hit_tokens`/`prompt_cache_miss_tokens`) are now read, alongside
the nested form, and the Anthropic parser clamps its `message_start` counts so a
malformed proxy cannot report a negative.

## Minor: A stricter compaction nudge

When the context has grown past the read-pressure threshold, the reminder
appended to the next tool result is now a directive that keeps arriving until
the agent compacts — it is no longer quietly dismissible, and the escape hatch
that let it be ignored is gone. The default threshold drops from 150000 to
**40000** tokens (`OGCODE_READ_PRESSURE_THRESHOLD_TOKENS`), and a second,
independent trigger fires on how often the whole context has been re-sent to the
model, headed **Re-send cost**, tuned with `OGCODE_RESEND_COST_WINDOW_MULTIPLE`.

## Other changes

- Mid-turn compaction is decided by the environment (`OGCODE_COMPACT_CONTEXT`),
  not a per-project setting; migration `053` drops the old table.
- The composer's image button becomes a **+** that opens a small dialogue to
  choose an image or take a photo.
- The pause glyph shown while a session runs is now the stop control — clicking
  it stops the turn and the session, the same as `Esc`.
- Composer drafts are kept per session, so switching away and back restores what
  you had typed.
- The document indexer skips far more generated directories — the default exclude
  list grows from 14 names to 38 — and re-indexes a page when its file mtime has
  changed. A quiet "skipping unchanged document" line moves to debug level.
- The map tools carry a larger budget: the project and memory maps are capped at
  100 KB each, a folder line lists up to 40 labels, and a page up to 30.

---

# Release Notes — v0.38.0

## Minor: OGX — the OG Lab plan as a provider

OGX is the subscription plan sold by OG Lab, and it is now a provider you connect
from the settings screen instead of configuring with an environment variable.
Signing in on the OG Lab side links this install; the plan's models then run
through OG Lab's gateway, which speaks the OpenAI Chat Completions API, so the
provider is an ordinary OpenAI-compatible endpoint pointed at that gateway with
the token the connect flow stored.

The plan *is* the catalogue. The gateway's `/v1/models` returns exactly the
models the account's plan grants, and there is deliberately **no static
fallback** — an empty catalogue means the plan carries nothing, and a fallback
would present models the account cannot reach. Registration is gated the same
way: a link that carries no plan contributes no provider at all, so it can never
become the default ahead of a provider that works.

Connecting and disconnecting swap the provider into the running registry with no
restart. The OGX tab leads the settings sidebar and shows the connect
invitation, the plan's models with their toggles, and a **Check usage** link
through to OG Lab. `OGX_GATEWAY_URL` overrides the gateway base URL and
`OGX_CONNECT_URL` overrides the connect page.

## Minor: ask_user — questions as a first-class round trip

The `ask_user` tool puts a small batch of questions to the user in **one** call
and blocks until they answer. Each question carries a short header, the question
text, and two to four options you propose — the user can always type their own —
and the batch is shown as a short set of screens in a single dialog. The answers
come back to the model as the tool result, verbatim.

It is offered to interactive sessions only: the Build agent, under permission
gating. Headless runs (`ogcode run`, the indexer) and sub-agents never see it, so
a scripted run cannot stall on a question nobody is there to answer. A blank
answer means "no preference, use your judgement" for a preference, and "did not
answer" for a question of fact — the model is not to invent one.

## Minor: The community free key pool is gone

The shared free-tier key pool — a public list of third-party provider keys the
app provisioned automatically as `ogcode-*` providers — is removed, UI and
back-end. Quietly registering provider keys the user never entered is not
something an install should do on its own, and the OGX plan is the supported way
to chat with zero configuration. The `ogcode-*` provider ids, the settings UI
that folded them into a single slot, and the `GET /api/providers/free` route go
with it, and `OGCODE_FREE_KEYS_URL` and `OGCODE_CACHE_DIR` are no longer read.

## Minor: Web search limits move out of the settings screen

The deep-research page-fetch and characters-per-page limits leave the settings
screen and become fixed defaults — four pages of 6000 characters — overridable
per deployment with `OGCODE_SEARCH_FETCH_TOP_K` (1–10) and
`OGCODE_SEARCH_PAGE_CHARS` (1000–20000). A value outside its range is clamped
and an unparseable one warns and falls back to the default, so a bad value can
never fail a search. Migration `045` drops the two columns that held the old
per-session settings.

## Other changes

- The workspace directory names the browser tab, so a window full of ogcode tabs
  is told apart by the project each one serves.
- The landing page drops its Plan Mode narrative section.

---

# Release Notes — v0.37.4

## Minor: File edits that explain themselves

- The `edit` tool now takes every change in its `edits` array — one entry per change, and the only form it accepts. A call using the old top-level `old_string`/`new_string` shape is refused with the exact form to send instead, rather than guessed at.
- An edit whose `new_string` is identical to its `old_string` is rejected instead of reporting a replacement that changed nothing, and a batch whose hunks cancel out fails rather than writing the file back byte-identical.
- In a uniformly CRLF file, LF-authored hunks are adapted to the file's own line endings before matching: a multi-line anchor that could never match now lands, and a replacement no longer splices LF lines into a CRLF file.
- A whitespace-only miss now quotes the file's own bytes for the region it found, with the line number, ready to copy — instead of naming the problem and leaving the caller to re-read and guess again. The hint distinguishes wrong indentation from a line-ending mismatch, and reports the case where the anchor matches several places.
- An anchor carrying the read tool's `… (line truncated)` marker is called out as the marker it is, not reported as text missing from the file.
- `read` separates the line number from the content with `|` rather than a tab, so an anchor's indentation can be copied exactly; an inverted `start_line`/`end_line` range is rejected, and a window past the end of the file — or an empty file — says so with the file's real extent.
- Multi-byte characters are no longer split by line-length truncation or by an error message's excerpt, and a rewritten file keeps its setuid/setgid/sticky bits along with its permissions.

## Minor: Failures you can see

- The `bash` tool appends the exit status to its output when a command fails without printing anything — `[exit status 1]`, the signal for a killed process, or a note that the command never ran. A silent failure used to return an empty, success-shaped result, and the agent carried on as if the step had landed.

## Minor: Cross-tool instruction files

- `AGENTS.md` is now read alongside ogcode's own `AGENT.md`, from each directory on the walk from the working directory up to the filesystem root, so a project already using the cross-tool convention needs no ogcode-specific file.
- Within a directory `AGENTS.md` is added first and `AGENT.md` last, keeping the ogcode-specific file closest to the model. Identical text under both names is included once.

## Patch: Blog, and a version badge that tells the truth

- Adds a fully static blog at `/blog`, built from markdown into `docs/blog` at deploy time.
- The landing page version badge reads the running build's version from the API, instead of a hardcoded string that had drifted releases behind.
- The web UI renders an edit's diff from its `edits` array, with a fallback for older sessions that stored one flat pair — those edits had rendered as `+0 −0` since the array landed.

---

# Release Notes — v0.37.3

## Patch: More reliable mid-turn guidance, MCP schemas, and session state

- Persists guidance sent while the agent is working in the session transcript, marks it as `steered mid-turn` in the UI, and prevents it from being delivered to the model a second time on later requests.
- Sanitizes MCP tool schemas for provider compatibility by removing unsupported validation keywords while preserving useful type, enum, and format guidance, including nested schemas.
- Protects optimistic session messages from polling and SSE races, keeps failed sends visible, and ignores stale responses after switching sessions.
- Adds a fallback to stop document-index polling when completion events are missed.

---

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

## Minor: Compact context is on by default, off via the environment

`compact_context` — the tool that lets the agent replace the finished part of a
long turn with a summary it writes — is offered to every read-capable agent by
default. Set `OGCODE_COMPACT_CONTEXT=false` (also `0`, `no`, `off`) to withhold
it; unset or empty leaves it on. The switch is process-wide, read fresh each
turn, so it needs no restart and no per-project state.

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
