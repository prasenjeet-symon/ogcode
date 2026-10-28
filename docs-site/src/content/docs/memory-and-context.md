---
title: Memory & context
description: Project instructions, turn-by-turn recall, and a working context that stays a useful size.
---

A model can only reason over what is in front of it. Ogcode's job is to keep the right things in front of it: what you have told it about the project, what it learned in earlier sessions, and a working context that stays a useful size instead of growing without bound.

## Instructions you write

Two files are loaded into every session, and both count as durable instructions. They are discovered at the project root, and parent directories are searched as well, so a monorepo or your home directory can carry a shared default:

- **`AGENTS.md`** (or `AGENT.md`): how you want the agent to behave in this project: conventions, commands to run, things to avoid. Think of it as a brief for a new teammate.
- **`MEMORY.md`** carries the hard-won knowledge the agent needs *before it acts*: gotchas with their fix, invariants and the file or test that pins them, canonical values, decisions and why. It is not a log; keep it small.

Both are read in full by default. If yours grow very large, you can cap them with `OGCODE_AGENT_MD_MAX_BYTES` and `OGCODE_MEMORY_MD_MAX_BYTES`, but most projects never need to.

A third file, **`HOWTO.md`**, records procedures the project has verified: ordered steps plus the check that says they worked. It is **not** loaded into the prompt: the agent is told the file exists and opens it when a task looks like one it already covers, so it can grow without costing every request. Record an entry at the end of a turn that ran the procedure end to end.

A fourth, **`HISTORY.md`**, is the project's log: dated changes, measurements and their numbers, experiments and what they ruled out. It is not loaded either; the agent opens it when a task touches past work or before re-running an experiment. Anything that only matters once someone is investigating the same area belongs there rather than in `MEMORY.md`.

## Recall across turns and sessions

After a turn, Ogcode writes a short structured summary of what happened and indexes it. The agent can search those summaries later, so a decision made last week is still discoverable without you repeating it: it is not a replay of the whole transcript, just the useful parts.

Because the summaries are kept as plain, readable files, the memory is something you can inspect and edit rather than an opaque black box.

## Keeping the context a useful size

Long turns would otherwise fill the model's context and start to crowd out what matters. Ogcode keeps the agent on task: as a turn grows, it moves the agent on from material it has already gathered, and when the working context gets large the agent continues from a summary of where things stand rather than the raw text it no longer needs.

This runs automatically. You do not lose the thread of a task (only the detail that has stopped being useful), and you generally notice it only as a large job staying coherent instead of slowing down or losing track.

## Indexing a project

The agent reads files on demand, but you can pre-build a searchable index of the code and documents for faster, better answers:

```bash
ogcode index
```

This walks the tree (honouring `.gitignore` and your exclude rules) and indexes source files plus PDF and DOCX documents into the project's own data. It refreshes automatically after a turn, so it stays current as the code changes. Automatic refresh is on by default; before indexing a very large project (more than about 500 files) it asks your consent first, and `OGCODE_AUTO_INDEX=false` turns it off entirely.

## Where it lives

Instruction and memory files travel with the checkout: `AGENTS.md`, `MEMORY.md`, `HOWTO.md`, and `HISTORY.md` live beside your code at the project root. Per-project runtime state (the session database, turn summaries, notes, and the project index) lives under `.ogcode/` inside the project. Machine-wide configuration lives in `~/.config/ogcode/`, and machine-wide runtime state (tokens, logs, skills) in `~/.ogcode/`. None of this needs any setup; it is created on first run.

## Next

- [Search, skills & MCP](/docs/search-and-skills/): bringing in knowledge from outside the repo.
