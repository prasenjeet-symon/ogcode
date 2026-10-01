---
title: Sessions & modes
description: The three modes, how a session runs, and steering it mid-turn.
---

Everything in Ogcode happens inside a **session** — one conversation with a title, a chosen model, and a transcript. This guide covers what a session is, the modes you can run it in, and how to steer it while it works.

## A session and a turn

- A **session** is one conversation. It holds its own transcript, model choice and permissions, and lives in the project it was started from.
- A **turn** is one instruction from you and everything the agent does in response — reads, edits, commands and tool calls — until it stops to report back.
- Sessions are **saved and resumable**. Close the tab, restart the server, come back later; the transcript is still there, and the agent keeps its memory of what happened.

Start a new session from the sidebar. Each one keeps its own drafts, so a half-written prompt in one is not disturbed by work in another.

## The modes

The mode you launch decides the shape of the work:

| Mode | Start it with | What it does |
| --- | --- | --- |
| **Build** | `ogcode` | The default. Chat, inspect, edit, run commands, verify — one session, many turns. |
| **Plan** | `ogcode plan` | Turns a larger feature into a board of tasks and executes them. See [Plan mode & tasks](/docs/plan-mode/). |
| **Task** | (from a plan) | Runs a single plan task inside its own git worktree. |
| **Breakdown** | `ogcode run -a breakdown` | Splits a goal into a task list without executing it. |

For everyday work you stay in **Build** mode. You switch into the others when a change is too large for a single conversation.

## Watch it work

A Build turn is transparent by design. As the agent works you see each tool call in the transcript — the file it is reading, the exact diff before a change is applied, the command it ran and its output. Nothing happens off-screen.

You can stop a turn at any point with the stop button, or by pressing <kbd>Esc</kbd>.

## Steer it mid-turn

You do not have to wait for a turn to finish before correcting course. Type another instruction while the agent is working and it is handed over as **guidance** on the next step rather than queued as a fresh turn:

> actually, put the timeout in the config file, not a constant.

The agent folds that into what it is already doing. This is usually faster than stopping and starting again.

## Work without the browser

Anything a session does is available headlessly, which is what makes Ogcode usable in scripts and CI:

```bash
ogcode run "add a --json flag to the CLI and document it"
```

`run` prints the transcript to stdout as it goes. Useful flags:

| Flag | Meaning |
| --- | --- |
| `-a, --agent` | Agent to run — `build` (default), `plan`, `task`, `breakdown` |
| `-o, --output-format` | `text` (default) or `json` |
| `--max-turns` | Stop after this many steps (default `100`) |
| `--model` | Override the model for this run |

A non-zero exit status means the run did not complete, so `run` fits into a pipeline like any other command.

## Next

- [Permissions](/docs/permissions/) — how the agent decides when to ask before acting.
- [Plan mode & tasks](/docs/plan-mode/) — for work that is too big for one turn.
