---
title: "Memory is table stakes"
description: "Grok Build just learned to remember your project. Good. Every agent should. Here is how Ogcode remembers: markdown on your disk, one note per turn, recalled only when a turn actually needs it."
pubDate: 2026-09-20
tags: ["memory", "context-engine"]
---

xAI shipped memory in Grok Build this week: after a session, it writes markdown
notes about your conventions and decisions in the background, organizes them
into topics, and reads the relevant ones back before related work. There's a
browser to inspect what it knows. You can read their announcement
[here](https://x.ai/news/grok-build-memory).

It's a good release, and a better signal. The industry is converging on
something this project was built around from day one: **a coding agent that
forgets your project is broken**, no matter how large its context window is.

So instead of a hot take, here's the same story told for Ogcode. What gets
written down, when it gets read back, and why the answer to "where does my
project's memory live?" should never be "on someone else's server."

## One note per turn

Most agents that remember do it at the end of a session. Ogcode writes after
**every turn**. A background scribe reviews what just happened and drops one
markdown note into `.ogcode/memory/`, right next to your repo:

```
.ogcode/memory/
├── 2026-09-12T091412Z--3f8c21aa--dispatch-retry-fix.md
├── 2026-09-12T104033Z--3f8c21aa--why-worktrees-share-a-branch.md
└── 2026-09-14T183957Z--b04d77e1--polyglot-bench-setup.md
```

The timestamp sorts the directory chronologically, the eight-character tag
groups a conversation, and the slug says what the turn was about. Inside, a
note looks like this:

```markdown
---
title: Dispatch retry fix
session_id: 3f8c21aa…
created_at: 2026-09-12T09:14:12Z
---

# Dispatch retry fix

Topics: dispatch, retries, backoff, testing

## Request
Dispatch API calls fail permanently on 429s. Add retry with backoff.

## What was done
Rewrote the send path to retry 429/5xx with jittered exponential backoff…

## Key files & symbols
- internal/dispatch/retry.go: Backoff, Send
- internal/dispatch/retry_test.go: uses the recorded-clock helper

## Outcome
All dispatch tests green. Retries cap at 5 attempts; budget lives in config.
```

The digest the scribe works from is deliberately thin: your request, which
tools were called and with what arguments, and the final answer. Tool
*output* never goes in. No file contents, no command logs, no API responses.
That's the entire token economy of the system. The note remembers *that*
`internal/dispatch/retry.go` was rewritten and *why*, not the four hundred
lines that came back from reading it.

And capture never blocks. The note is written by a detached background job
after your turn is already finished; if it takes two minutes, you don't feel
it. Sub-agents, background indexing, and the recall agent itself don't count
as memorable work, so they're excluded and memory never fills up with the
agent talking to itself.

## Nothing is replayed

Here's the part memory announcements usually skip: what happens *inside* a
session.

Almost every agent ships your entire conversation back to the model on every
turn. The transcript is the memory, and you rent it back token by token.
Turn forty pays for turns one through thirty-nine all over again. That's the
quadratic bill curve, and no end-of-session notebook fixes it.

Ogcode doesn't replay. A request carries the current turn: your message,
plus the previous turn's final response for continuity. Everything older
stays home, on disk, already written down. What turn forty costs is shaped
by what turn forty *does*, not by the thirty-nine turns before it.

## Recall is deliberate

Stored memory is useless if the model can't find it, and noise if all of it
is shoved into every prompt. Ogcode treats recall like a library visit, not a
feed.

When the agent needs history, it calls a recall tool, scoped either to the
current conversation or, with `project_memory_recall`, to every conversation
this workspace has ever had. The tool runs a separate librarian agent that is
read-only by construction. It holds exactly three tools (`memory_map`,
`file_map`, `read`), no bash, no write, and no recall tools of its own, so it
can't recurse.

Browsing what Ogcode knows works exactly like browsing your code.
`memory_map` collapses each conversation to one line (tag, topics, turn
count) the way `codebase_map` collapses a folder. The librarian picks a note,
unfolds its heading outline, and reads just the section it needs. Never a
whole file when a section will do; never the archive when one file will do.

Two details we're particular about. The recall scope is pinned server-side,
so the model cannot ask its way into a wider scope than the turn granted.
And recall waits for any note still being written in the background, which
means what it searches is complete up to the previous turn, every time.

## Files, not a feature

Grok Build ships `/memory`, a browser for what it remembers. Our answer is
more boring, and that's the point: **memory is markdown in your repo**. `ls`
is the browser. `grep` is search. Your editor is the edit button. Check the
folder into git or leave it ignored. Your call either way.

Two more files ride along, and you may already have them. `AGENT.md` holds
how you want the agent to behave, and Ogcode reads the cross-tool `AGENTS.md`
the same way. `MEMORY.md` holds durable facts, and Ogcode records them there
on its own initiative the moment they prove out: *tried X, got Y, do Z
instead*. Both are discovered by walking up from the working directory, so
root-level rules cover the monorepo and a subproject can override them. What
you say in the session always outranks what's on file.

Because it's files and SQLite under `.ogcode/`, memory has no server
dependency and no model dependency. Switch from a cloud model to local Ollama
mid-project; memory doesn't notice. Self-host everything; memory never leaves
your machine.

## The honest gap

We said memory is table stakes, so here's where ours isn't finished yet:

- **A memory view in the UI.** Today the browser is your file manager. A
  first-class view with search, preview, and prune belongs in the web UI too.
- **Consolidation.** Forty notes that each mention your testing conventions
  should eventually become one. Ogcode organizes at write time but doesn't
  yet merge across sessions.
- **A global scope.** Memory is per-project by design. Preferences that are
  about *you*, like how you want commits, reviews, and explanations, deserve
  a home that follows you across repos.
- **Secret scrubbing.** Notes capture tool arguments verbatim. Keep secrets
  in your environment, not your prompts. That's good advice with any agent,
  and we'd rather enforce it mechanically than advise it.
- **The bigger list.** Citations for every recalled fact, expiration,
  import/export, team-owned memory. It's on the roadmap in the open, in the
  README.

Each of these will land the way everything above works: as something you can
read on your own disk.

---

The next year of coding agents won't be decided by whether they remember.
Everyone will remember. It'll be decided by what remembering *costs* you: in
tokens, in trust, in lock-in.

Ours costs a folder of markdown.
