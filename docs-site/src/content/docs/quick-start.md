---
title: Quick start
description: From an empty terminal to an agent working in your repository.
---

This page takes you from a fresh install to a running agent. It assumes you have already put the `ogcode` binary on your `PATH` and set at least one model credential — if not, start with [Install](/docs/install/).

## Start the server

Run `ogcode` from the directory you want it to work in:

```bash
cd ~/code/my-project
ogcode
```

The server binds all interfaces on port `9595` and opens the web UI at [http://localhost:9595](http://localhost:9595). The directory you started it from is the workspace the agent can read and write; you can point it elsewhere from the workspace switcher.

If the port is already taken, ogcode remembers the next free one **per project** in `~/.ogcode/ports.json` and prints the address it settled on.

## Give it a task

Type what you want in the composer and press Enter. A first task might be:

> Explain how the request pipeline is put together, then add a timeout to the outbound HTTP client and cover it with a test.

The agent works in steps: it looks around the repository, proposes file edits, runs commands, and reports back. Every tool call is shown in the transcript — you can watch it read a file, see the exact diff before it is applied, and stop it at any point.

The agent follows the [permission mode](/docs/core-concepts/#permissions) you pick in the composer:

- **Ask** — every consequential command, write and edit waits for your approval.
- **Auto** — routine commands (reads, builds, tests) run; risky ones are still put to you.
- **Yolo** — nothing is asked. Only use it where you are happy to lose the working tree.

## Steer it mid-turn

You do not have to wait for a turn to finish. Type another instruction while the agent is working and it is handed over as guidance on the next step — "actually, put the timeout in the config file, not a constant."

Press <kbd>Esc</kbd> (or the stop button) to abort the current turn.

## Plan a larger change

When a task is too big for one turn, switch to Plan Mode:

```bash
ogcode plan
```

Plan Mode decomposes the feature into a board of tasks, each running in its own git worktree off the base branch, and can open pull requests when they finish. You review the plan before execution starts.

## Work without the browser

Everything the UI does is available headlessly:

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

A non-zero exit status means the run did not complete.

## Index the project

The agent reads files on demand, but you can pre-build a searchable index of the code and documents for faster answers and better search:

```bash
ogcode index
```

This walks the tree (honouring `.gitignore` and your exclude rules) and indexes source files, plus PDFs and DOCX documents, into the project's database. It is also refreshed automatically after a turn.

## Where things live

| Path | What it holds |
| --- | --- |
| `.ogcode/` | Per-project state: the session database, index, and worktrees |
| `~/.ogcode/` | Machine-wide config, provider keys, the port map |
| `~/.config/ogcode/config.json` | Global settings |
| `ogcode.json` | Project settings (safe to commit) |

## Next

Read [Core concepts](/docs/core-concepts/) for the mental model — modes, tools, memory and permissions — or jump into the full [architecture reference](/docs/architecture/).
