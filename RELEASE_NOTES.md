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
