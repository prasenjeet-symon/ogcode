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

