---
title: Permissions
description: Ask, Auto and Yolo — what is gated, and how approvals are remembered.
---

The agent can change files and run commands on your machine, so Ogcode puts a gate in front of anything consequential and lets you choose how much it should stop to ask. You pick a mode in the composer, and the agent follows it.

## The three modes

| Mode | What it does |
| --- | --- |
| **Ask** | The default. Every consequential action — a shell command, a file write or an edit — waits for your approval. |
| **Auto** | Routine actions such as reads, builds and tests run without asking; genuinely unclear ones are assessed and usually still put to you. |
| **Yolo** | Nothing is asked and nothing is assessed. Fast, and unforgiving — keep it for scratch checkouts. |

When the agent asks, it shows you exactly what it wants to do. You can approve it once, or choose **Always** to allow that exact command or path from then on.

## What "consequential" means

Read-only work never raises a prompt: listing files, reading source, searching the codebase, or mapping a project all run freely in every mode. The gate is about things that could change your machine or your repository — writing or editing files, and running shell commands.

In **Auto** mode the agent still errs toward asking. A command it recognises as routine (a build, a test run, a `git` status) goes through; something destructive or ambiguous is put to you rather than guessed at.

## Approvals are remembered

An **Always** answer is not just for this turn. It is stored against the exact command or path — never a wildcard — and applies from then on, in every session. Repeating the same safe command does not mean re-approving it every time.

The default mode is remembered too. Whichever mode you last chose applies to new sessions, so you set it once and it stays put.

## Configured refusals hold everywhere

If a command or tool is explicitly configured as **denied**, that decision is respected in every mode — including Yolo. Yolo removes *asking*; it does not remove your rules. A refusal you set up stays a refusal.

## A safe starting point

Reach for the modes in this order:

1. **Ask** (the default) while you are getting a feel for how the agent works on a new project.
2. **Auto** once you trust the routine commands it needs — builds, tests, formatters.
3. **Yolo** only in a throwaway checkout you are happy to lose.

## Where to change it

The mode toggle sits in the composer, next to the model selector, and changes apply immediately to the current session and become the default for new ones.
