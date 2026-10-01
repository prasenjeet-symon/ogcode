---
title: Core concepts
description: The mental model behind ogcode — workspace, sessions, modes, tools, memory and permissions.
---

A handful of ideas explain almost everything ogcode does. None of them need configuration to get started.

## The workspace

Ogcode works inside one directory at a time: the workspace. It is the root the agent may read and write, and the boundary the file tools refuse to cross. Start `ogcode` from a project and that becomes the workspace; switch it from the UI to work elsewhere.

A **project** is a workspace ogcode has seen before. Everything project-scoped — sessions, the index, task worktrees — lives under `.ogcode/` inside it, so the state travels with the checkout and nothing leaks between projects.

## Sessions and turns

A **session** is one conversation: a title, a chosen model, and a transcript of turns. A **turn** is one instruction from you and everything the agent does in response — reads, edits, commands, tool calls — until it stops to report back.

You can interrupt a running turn with **mid-turn guidance**: type while the agent works and the text is folded in as steering on the next step, rather than queued as a fresh turn.

## Modes

The agent you talk to changes the shape of the work:

- **Build** (`ogcode`) — the default. Chat, inspect, edit, run commands, verify. One session, many turns.
- **Plan** (`ogcode plan`) — decomposes a larger feature into a board of tasks and executes them, optionally opening pull requests.
- **Task** — the agent that runs a single plan task inside its own git worktree.
- **Breakdown** — splits a goal into a task list without executing it.

## Tools

The model decides *what* to do; the tools are *how*. Each turn offers the agent a set drawn from its agent definition — file reads and edits, shell commands, search, the code map, document indexing, web fetch, and any MCP servers you connect. Every call is recorded in the transcript with its arguments and result, so you can see exactly what happened.

A few worth knowing by name:

| Tool | What it does |
| --- | --- |
| `codebase_map` | The indexed outline of the project, folder by folder |
| `file_map` | The declarations inside one file, with line ranges |
| `read`, `glob`, `grep` | Targeted reads and searches, budgeted so context stays useful |
| `edit`, `write` | Change files; edits apply as explicit hunks you can review |
| `bash` | Run commands, with a denylist for the dangerous ones |

Long reads are tracked: when a turn accumulates enough read volume, the agent is nudged to **compact** — replace its working context with a summary — so large jobs stay coherent instead of running out of room.

## Memory

After a turn, ogcode writes a short structured summary of what happened and indexes it. The agent can search those summaries later (`project_memory_recall`), so a decision made last week is still discoverable without you repeating it. You can also keep durable notes in an `AGENTS.md` or `AGENT.md` file at the project root, which is loaded into every session.

## Permissions

Before a consequential action — a shell command, a file write or edit — the agent asks, and remembers your answer. There are three modes, chosen in the composer:

- **Ask** — the default. Each consequential action waits for approval. Approve once, or choose *Always* to allow that exact command or path from then on.
- **Auto** — routine commands (reads, builds, tests) run without asking; genuinely unclear ones are assessed and usually still put to you. A configured *deny* rule is respected in every mode.
- **Yolo** — nothing is asked and nothing is assessed. Fast, and unforgiving: keep it for scratch checkouts.

Approvals and the default mode are machine-wide — stored once and applied to every session — and a grant is scoped to the exact command or path, never a wildcard.

## Rich results

The agent can answer with more than text. Replies may carry rendered diagrams (Mermaid), LaTeX math and compiled LaTeX documents, Plotly charts, Rough sketches, and sandboxed HTML/CSS/JS widgets. It can also write files to the workspace's `public/` folder and hand you a link the UI serves.

Local services you start — a dev server, a dashboard — are reachable in the browser without exposing a port by hand.

## Local or remote

Everything above runs on one machine. The same binary also has a `worker` role, which connects to a **control plane** so a team can run agents on shared hardware and reach each worker's UI through the operator console. That is an optional deployment; the single-machine path never needs it. See [Remote deployment](/docs/deployment/) when you want it.
