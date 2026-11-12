# Release Notes — v0.59.1

## Minor: An update check that fails says so instead of claiming you are current

`GET /api/version` used to answer 500 when GitHub could not be reached, so the
installed version — which is local and always knowable — disappeared with the
release it could not fetch, and the About screen fell back to a card that still
read **Up to date**. The endpoint now always answers 200 with the local version
and carries an `updateError` naming the failure; the release fields still hold
the last release fetched successfully, so the card keeps its last known version
while saying the check failed. The forced check behind the About screen's button
(`POST /api/version/check`) answers **503** with the same body, because a caller
that asked to reach GitHub did not get there. A failed fetch is remembered for a
minute, so an offline or rate-limited client walks into the 10-second timeout
once a minute rather than once per page load, and a forced check still falls
back to the last known release rather than blanking the card. `publishedAt` is
now optional, so a release whose date is unknown omits the field instead of
dating itself `0001-01-01T00:00:00Z`, and the release-notes excerpt is cut at
500 runes on a character boundary rather than mid-way through an em dash or
emoji. The About screen renders its "Update check failed. Are you online?"
banner from `updateError` and never shows the up-to-date chip on a failed check;
`ogcode check-updates` prints a published date only when there is one. Tests
point the version manager at a fake GitHub (`version.WithAPIBase`,
`version.WithHTTPClient`), so the whole contract is pinned without a network.

## Minor: Text, controls and meters reach WCAG AA contrast in both themes

A colour-only pass fixed the places where the interface's own colours were the
reason something could not be read. The dark focus ring was a 35% accent tint
measuring 1.69:1 against the surfaces drawn under it, so it is now the opaque
accent ink per mode and clears the 3:1 non-text floor by construction; the light
ring takes its own ink for the same reason. Two new tokens carry the boundary
floors: `--border-control` (dark `#70707a`, light `#82828c`) outlines the
controls whose only affordance is their 1px line — code-block copy, LaTeX
toggles, diagram zoom — at 3.5:1 or better on every surface they sit on, while
the hairline `--border-default`/`-subtle` stay for dividers and chrome;
`--meter-track` gives the unfilled rail behind the context meter and its usage
bar the same 3:1 treatment. The dark danger red moves from 4.06:1 on a hovered
row to 5.30:1, and light mode darkens success, warning, danger, the pending and
progress status colours and links until each holds 4.5:1 on the lightest surface
a hovered row can be. The context meter and the default rough-diagram palette
read those tokens instead of hardcoded literals, `theme.tsx` no longer writes
`--accent-ring` inline, and the stop button takes `var(--danger)`. The
marketing, blog, docs and browser-extension stylesheets follow the same palette,
and the OGX page's three highlighted elements get distinct colours. Nothing
moves: no layout, structure or behaviour changes.

## Patch: A new post on why long sessions beat fresh ones

`blog/src/content/posts/long-session-beats-fresh.md` is published with its
search-accuracy chart, covering what a long session actually buys and where a
fresh one wins.

# Release Notes — v0.59.0

## Major: One background model for everything that is not the conversation

Titles, turn-memory summaries, compaction, command risk assessment, intent tool
matching, auto-indexing, deep search, memory recall and the `task` sub-agents all
run on a model you never picked. They now share one machine-wide choice under
**Settings → General → Background tasks**, stored on the server and reachable at
`GET`/`POST /api/models/background`. The default, **Inherit from active session**,
changes nothing: each call keeps using the model of the session doing the work.
Pin a model and it serves every background call; a model named explicitly by a
caller still wins, a pinned model that is later removed falls back to inheriting
rather than failing, and a model that is not being served is refused when you
save it instead of at the first summary. Because a verdict belongs to the model
that gave it, the command-risk cache is keyed by model and command, and compaction
now sizes its request to the smaller of the session window and the background
model's window instead of overrunning a smaller summarizer. A new guide,
**Background model**, covers what counts as background work and where the line
sits.

## Major: Word documents and PDFs are read page by page

A `.docx` is now parsed from its own XML parts rather than flattened: paragraph
styles resolve through the `basedOn` chain, text boxes and footnotes stay in
document order, and a page boundary comes from an explicit break, a style's
`pageBreakBefore` or a `sectPr` page start, so pagination matches what Word shows.
Tracked changes no longer leak a reverted style or break into the live document,
and a break inside a table cell no longer swallows the row. Encoding is handled
by charset labels and UTF-16 BOMs, every part is bounded against zip bombs, and
unspaced scripts such as Chinese or Japanese paginate by volume instead of piling
into one enormous page. A corrupt or empty `document.xml` is an error, not an
empty document.

Reading follows the same page model. `doc_index` replaces the listing-only
`docx_index` and `pdf_index` and returns the page catalogue a step can choose
from; `read_docx_page` and `read_pdf_page` accept an `end_page` range and mark each
page with `--- page N ---`, so a long contract costs only the pages actually read.
A listing whose file changed, vanished or now carries an older timestamp than the
index is labelled stale and points at the index tool, and the indexer itself
reports per-document progress, publishes a terminal phase even when a run fails,
sweeps only the documents whose chunks failed, and makes `ogcode index` exit
non-zero instead of printing a success it did not have.

## Major: Every model call says what it is for

Each provider request now carries `X-Client-Task`, naming one of fourteen call
kinds: the turn, a sub-agent, indexing, a note, search, recall, intent matching,
turn memory, compaction, a title, the risk gate, a rewrite, a probe or credential
validation. A delegated child adds what its spawner predicted about the work —
`X-Client-Complexity` as easy, medium or hard, and `X-Client-Workload` as
read-heavy or write-heavy — so a router can serve a child that only reads from a
cheaper model than one that edits. Both predictions persist on the session and are
read once per turn, so a resumed or restarted child still sends what it was
briefed with. `task` and `teammate` accept both as arguments and bounce a
misspelling rather than silently sending nothing. The fields never enter the
request body, so prompts and their cached prefixes are unchanged.

## Minor: A child's memory stays under its parent

Session recall used to index a sub-agent's turn summaries against the child, so
what a teammate learned disappeared from the conversation that asked for it. A
child's memory now rolls up to the root session that started the work, resolved
through the board's spawn edges, and the command palette hides the child's own
row instead of listing it beside the parent's. The board also publishes a new
session only after its spawn edge exists, and the sidebar merges a refreshed
roster into a new array, so a hard reload nests running teammates instead of
painting them flat.

## Minor: The client survives the state it was opened under

An audit pass over the web client fixed eleven high-severity issues and a long
tail of smaller ones. Escape now closes only the topmost overlay and returns
focus where it came from; a failed configuration fetch no longer wipes a stored
API key; a note poll stops after three misses instead of running forever; a
device iframe survives a status blip; and switching sessions can no longer land an
older page of messages on the newer session. Diffs keep `---` and `+++` lines that
are content rather than headers, the HTML preview loses `allow-same-origin`, the
sidebar mounts once per page, and the model picker on first run reloads instead of
reading a stale list. Smaller hardening covers pointer capture on the resize grip,
a debounced colour picker, a windowed project tree for very large repositories, and
client-side pageview tracking.

## Patch: Deep search falls back instead of ranking a chain of thought

The ranker's output budget rises from 256 to 1024 tokens. Probing showed a
reasoning model spending the whole 256 on its thinking and returning no numbers
with `finish_reason: length`, so the cap now fits an answer; it remains a bound,
not an allocation. When an empty answer does end because the budget ran out, the
search falls back to the pages it found instead of parsing the unfinished
reasoning for digits, which had been ranking sources by line numbers quoted in
the model's own thoughts. A model that answers as reasoning and stops normally is
still accepted.

# Release Notes — v0.58.4

## Major: Memory files are opened when they matter, not pasted into every request

The system prompt no longer inlines `MEMORY.md`, `AGENTS.md`/`AGENT.md` or the
session memory file. It lists their paths and says when to open each, and a
pre-task check now runs `file_map` over every discovered memory file before the
first edit, command or teammate spawn of a task, following it with a ranged
`read` or `grep` where a heading bears on the work. On a project whose
`MEMORY.md` runs to 60 KB, the assembled Build prompt falls by about 60 KB, and
editing a memory file no longer invalidates the cached prefix of every session.
The writing recipe moves with the files: rules go in `AGENTS.md`, facts to know
before acting in `MEMORY.md`, procedures in `HOWTO.md`, dated history in
`HISTORY.md`, and the claim belongs in the markdown heading so the `file_map`
outline is itself the summary. The docs page on memory and context describes
the new flow.

## Major: Document tools ride the wire only when a step asks for them

`pdf_index`, `read_pdf_page`, `docx_index`, `read_docx_page`, `query_sheet`,
`latex_to_pdf` and `schedule` are no longer present in every request. The
`update_intent` matcher names them per tool — a single tool, a companion pair
such as `pdf_index` with `read_pdf_page`, or the whole family — and they join
the turn's toolset only when a step calls for them. A build turn that needs none
of them carries about 5.7 KB less tool schema, roughly 1,400 tokens, alongside
the browser operations and whole MCP servers that already load on demand.

## Minor: Compaction keeps the request it just built

Reactive and proactive compaction now settle the context watermark and pressure
state before the wire body is rebuilt, so the body a compaction produces is the
body the next step sends. The summary stays out of `System` and is folded into
`messages[0]`, which preserves the longest common prefix across the boundary and
stops the reactive path from summarizing and re-billing the same history on
every step until the compaction budget is spent.

## Minor: Screenshot shedding keeps a newest-frames window

The gateway body cap is enforced with a 4 MiB window over the newest screenshot
payload: older tool frames are replaced by a short note, and the retained set is
identical from one step to the next instead of the shed boundary walking forward
through the turn. What stays uncached is the window rather than the whole
conversation, and user attachments are still dropped only as a last resort when
text alone fills the cap.

## Minor: The Build prompt says one thing once

A line-by-line pass over the assembled prompt removed duplicate restatements of
the memory rule, settled the plan, memory-check and fan-out ordering, and
corrected contradictions about when lazy document tools arrive and when tool
calls may be batched. No section was reordered.

## Minor: A standalone benchmark harness ships in the repository

`benchmark/` carries `ogcode-benchmark` 1.0.2: six fixed software-engineering
fixtures, five provider presets, and reports for solve rate, cost, wall-clock
time, turns and tool calls against one shared price table. Version 1.0.2 adds a
per-fixture index cache — built once with `--build-index` and restored into each
attempt — and per-run peak RSS and CPU accounting, plus a serve-mode probe that
measures cold start, memory and binary size without a model call.

# Release Notes — v0.58.3

## Patch: The subreddit link is gone from every public surface

r/ogcode is gone, so every link to it is removed rather than left pointing at a room
nobody is in. The home page loses it in the top navigation, the mobile menu, the
footer social row, the Community column and the legal row; the README's community
line drops it too, as do the blog and docs footers. The home page's click analytics
no longer carries a branch for a link nobody can click, and the mobile menu's one
remaining GitHub button now spans its row instead of half of it. In the app, the
About page in Settings lists Telegram and WhatsApp in a two-up row, with the
subreddit card and its glyph gone.

## Patch: The small screens get their targets back

On a phone the sites' inline text links were only as tall as their font, so the footer
rows, the legal line, the docs search field and the blog's comment fallback all asked
for a steadier finger than they gave. Each now carries its own padding, taken out of
the surrounding gap so the column rhythm is unchanged. The docs sidebar stops repeating
its heading and full navigation on a narrow screen, leaving the search box above the
disclosure that already lists every page, and the search field's clear button now sits
inside the field instead of trailing it as a stray label. The three measured charts in
the context window post keep the size they were drawn at and pan sideways rather than
scaling their labels down to four pixels, and the home page's narrow-screen sheet
labels wrap instead of being cut off.

# Release Notes — v0.58.2

## Patch: Activity heatmap and tooling refinements

Adds an activity heatmap to the web UI for visualising session activity over
time, plus supporting server and session endpoints. Includes minor tooling
adjustments across the agent loop, prompt helpers, and read/glob/grep tools,
and the corresponding unit tests.

