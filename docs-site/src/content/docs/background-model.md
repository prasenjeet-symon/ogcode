---
title: Background model
description: "One machine-wide model for the behind-the-scenes calls: titles, summaries, compaction, recall and sub-agents."
---

Your conversation is not the only thing Ogcode asks a model to do. Titles get generated, long turns get summarised, unfamiliar commands get assessed, and recall questions get answered. These are all real LLM calls, and they do not have to run on the model you are talking to. The **background model** is a machine-wide choice of which model they use.

## What counts as background work

Everything that runs behind the scenes rather than in your transcript:

| Task | What it does |
| --- | --- |
| **Session titles** | Names a new session from its first message. |
| **Turn-memory summaries** | Writes the structured note kept after a turn and indexed for recall. |
| **Context compaction** | Summarises a long turn so the agent can continue from where things stand. |
| **Command risk assessment** | Decides whether an unfamiliar shell command is routine enough to run in Auto mode. |
| **Intent tool matching** | Picks which lazily loaded tools a plan step needs. |
| **Auto-index** | Describes files for the project index refreshed after a turn. |
| **Deep search** | Runs the research pipeline behind a deep search query. |
| **Memory recall** | Answers a recall question from past turn summaries. |
| **Task sub-agents** | The read-only `task` children, and background tasks started to keep working on their own. |

Smaller jobs such as the note text transform follow the same setting when they are not handed a model of their own.

## Where to set it

Open **Settings → General** and find the **Background tasks** group; the **Background model** row is its first setting. The choice is **machine-wide**: it is stored on the server and applies to every session, agent and project there, so you set it once rather than per session. On a remote deployment, set it through that server's Settings page; the same value is reachable at `GET`/`POST /api/models/background` if you script it.

Pick any model from any configured provider. Changing it affects the next background call; there is nothing to restart.

## Inherit from the active session

The default option, **Inherit from active session**, leaves the setting empty on purpose. With nothing chosen, each background call uses the model and provider of the session doing the work: exactly what Ogcode did before the setting existed.

Choosing a model pins it instead. The setting is honoured only while that model is actually being served; if you later remove it from the catalogue or disable its provider, background calls quietly fall back to inheriting the session's model rather than failing, and saving a model that is not being served is refused outright. Switching back to **Inherit from active session** clears the pinned model and provider.

## What it does not change

Your conversation always runs on the **session's** model: the one picked in the composer, or the one passed with `--model` to `ogcode run`. The background setting never redirects a turn, so a small, fast background model is a way to keep titles and summaries cheap without touching the model you actually work with.

The reverse direction has a limit worth knowing: the background model only replaces a model that would otherwise be **inherited** from the session. When a job names a model of its own — a manual index build with an explicit `model`, or a note text transform given one — that explicit choice wins and the background setting is not consulted.

## A sensible split

If you have a fast, inexpensive model available, set it here and keep the stronger one for sessions: titles and summaries are short, mechanical jobs where the smaller model is usually indistinguishable. If you have a single provider or are unsure, leave it on **Inherit from active session**; that is a perfectly good setup.

## Next

- [Memory & context](/docs/memory-and-context/): the summaries, compaction and recall this model powers.
- [Agents & teammates](/docs/agents-and-teammates/): task sub-agents in practice.
