---
title: Core concepts
description: The mental model behind ogcode — workspace, sessions, modes, memory and permissions.
---

A handful of ideas explain almost everything Ogcode does. None of them need configuration to get started.

## The workspace

Ogcode works inside one directory at a time: the **workspace**. It is the root the agent may read and write, and the boundary its file tools refuse to cross. Start `ogcode` from a project and that becomes the workspace; switch it from the UI to work elsewhere.

A **project** is a workspace Ogcode has seen before. Everything project-scoped — sessions, the index, task worktrees — lives under `.ogcode/` inside it, so the state travels with the checkout and nothing leaks between projects.

## Sessions and turns

A **session** is one conversation: a title, a chosen model, and a transcript of turns. A **turn** is one instruction from you and everything the agent does in response until it stops to report back. Sessions are saved and resumable, and you can steer a running turn rather than waiting for it to finish. See [Sessions & modes](/docs/sessions-and-modes/).

## Modes

The mode you launch decides the shape of the work: **Build** for everyday conversation and edits, **Plan** to split a large feature into tasks each running in its own worktree, **Task** to run one of those tasks, and **Breakdown** to produce a task list without executing it. See [Plan mode & tasks](/docs/plan-mode/).

## Tools

The model decides *what* to do; the tools are *how*. Each turn offers the agent a set drawn from its definition — file reads and edits, shell commands, search, the code map, document indexing, web fetch, and any external tools you connect. Every call is recorded in the transcript with its arguments and result, so you can see exactly what happened.

Three of them work together to keep the agent fast on a large project:

- **`codebase_map`** — gives the agent a feel for an unfamiliar codebase, so it starts from your project's structure instead of opening files at random.
- **`file_map`** — points the agent at the part of a file that matters, so it goes straight there instead of reading the whole file.
- **`compact_context`** — lets a long turn carry on coherently, setting aside detail it has finished with and keeping the thread of the task.

A few more worth knowing by name:

| Tool | What it does |
| --- | --- |
| `read`, `glob`, `grep` | Targeted reads and searches, budgeted so context stays useful |
| `edit`, `write` | Change files; edits apply as explicit hunks you can review |
| `bash` | Run commands, with a denylist for the dangerous ones |

## Memory

Ogcode remembers the work: project instructions you write, and short summaries of past turns it can search later, so a decision from an earlier session is still discoverable. See [Memory & context](/docs/memory-and-context/).

## Permissions

Before a consequential action — a shell command, a file write or an edit — the agent asks, and remembers your answer. The three modes, **Ask**, **Auto** and **Yolo**, control how often it stops. See [Permissions](/docs/permissions/).

## Rich results and local services

Replies can carry rendered diagrams, math, charts and interactive pages, and the agent can hand you a link to anything running locally so you open it without hunting for a port. See [Rich results & preview](/docs/rich-results/).

## Local or remote

Everything above runs on one machine. The same binary can also run on a remote host or in a container and be reached from a browser elsewhere. See [Remote deployment](/docs/deployment/) when you want that.
