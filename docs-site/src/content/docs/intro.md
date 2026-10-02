---
title: Introduction
description: What Ogcode is and what you can do with it.
---

Ogcode is an **agent operating environment** for software work. Most AI coding tools answer a question or complete a line; Ogcode is built to take responsibility for multi-step work — understanding a codebase, making a bounded change across files, running the project's own tests, and leaving behind evidence you can review.

If you have used an editor plugin with a chat panel, the difference is the shape of the loop. Ogcode gives an agent a workspace, a set of tools, a budget, and a stopping condition, then lets it run until the work is done or it needs you.

## What it is

- **Open source and self-hostable.** One binary with the web UI built in — nothing else to serve, no database to provision.
- **Agentic, not autocomplete.** The agent plans, edits, executes and verifies, rather than proposing a diff you paste in yourself.
- **Browser-native.** You work in a web UI, not inside a particular editor.
- **Model-agnostic.** Anthropic, OpenAI, OpenRouter, a local Ollama endpoint, or the OG Lab subscription (OGX).
- **Built for long-running work.** Sessions survive restarts, context is compacted rather than lost, and memory carries from one turn to the next.
- **Git-native.** Plan Mode breaks a feature into tasks, runs each in its own worktree, and opens pull requests.
- **Permission-aware.** Every consequential action is gated — ask, auto-approve, or refuse by an explicit rule.
- **Growing into a general computer agent.** Code is the current focus; the same tool-and-permission model reaches documents, rich output, and local services.

## What you can do with it

- **Understand a codebase.** Ask how a feature works and get an answer grounded in the real files, pointed straight at the parts that matter.
- **Build and fix.** Describe a change and the agent edits the files, runs the build and tests, and reports what it changed and how it verified it.
- **Plan larger work.** Give it a whole feature and let Plan Mode split it into tasks it can run in parallel and turn into pull requests.
- **Research.** Let it search the web and read pages alongside your local code and documents.
- **Repeat your process.** Teach it reusable skills, and connect the tools you already use.
- **Work headlessly.** Run any of it from a script or CI with a single command.

## Where to go next

1. [Install](/docs/install/) — get the binary onto macOS, Linux or Windows, or run it in Docker.
2. [Quick start](/docs/quick-start/) — go from a fresh install to an agent editing files in your repo.
3. [Core concepts](/docs/core-concepts/) — sessions, modes, memory and permissions, the mental model behind everything else.

The rest of the site is a set of [guides](/docs/sessions-and-modes/), one per capability, when you want to go deeper.
