---
title: Memory & context
description: Project instructions, turn-by-turn recall, and a working context that stays a useful size.
---

A model can only reason over what is in front of it. Ogcode's job is to keep the right things in front of it — what you have told it about the project, what it learned in earlier sessions, and a working context that stays a useful size instead of growing without bound.

## Instructions you write

Two files at the project root are loaded into every session, and both count as durable instructions:

- **`AGENTS.md`** (or `AGENT.md`) — how you want the agent to behave in this project: conventions, commands to run, things to avoid. Think of it as a brief for a new teammate.
- **`MEMORY.md`** — long-lived project knowledge you want the agent to always have: decisions, definitions, facts that are not obvious from the code.

Both are read in full by default. If yours grow very large, you can cap their size, but most projects never need to.

## Recall across turns and sessions

After a turn, Ogcode writes a short structured summary of what happened and indexes it. The agent can search those summaries later, so a decision made last week is still discoverable without you repeating it — it is not a replay of the whole transcript, just the useful parts.

Because the summaries are kept as plain, readable files, the memory is something you can inspect and edit rather than an opaque black box.

## Keeping the context a useful size

Long turns would otherwise fill the model's context and start to crowd out what matters. Ogcode keeps the agent on task: as a turn grows, it moves the agent on from material it has already gathered, and when the working context gets large the agent continues from a summary of where things stand rather than the raw text it no longer needs.

This runs automatically. You do not lose the thread of a task — only the detail that has stopped being useful — and you generally notice it only as a large job staying coherent instead of slowing down or losing track.

## Indexing a project

The agent reads files on demand, but you can pre-build a searchable index of the code and documents for faster, better answers:

```bash
ogcode index
```

This walks the tree (honouring `.gitignore` and your exclude rules) and indexes source files plus PDF and DOCX documents into the project's own data. It refreshes automatically after a turn, so it stays current as the code changes.

## Where it lives

Project knowledge lives under `.ogcode/` inside the project, so it travels with the checkout and never leaks between projects. Machine-wide settings live in `~/.ogcode/`. Neither needs any setup — they are created on first run.

## Next

- [Search, skills & MCP](/docs/search-and-skills/) — bringing in knowledge from outside the repo.
