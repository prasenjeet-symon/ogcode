---
title: Privacy
description: What the Ogcode server and the Ogweb browser extension handle on your machine, and what leaves it.
---

# Privacy

Ogcode is local-first software. This page describes what data it handles, where it lives, and what (if anything) leaves your machine. It covers the ogcode binary and server, and the Ogweb Chrome extension that pairs with it.

## The ogcode server

**Your work stays on your machine.** Sessions, transcripts, project indexes, memory files, bookmarks and grants are stored inside the project directory and in your home directory (`.ogcode/` folders, e.g. `.ogcode/ogcode.db`). Nothing is uploaded to an ogcode-owned service for storage. If you configure a model provider, the prompt and context needed for the current turn are sent to that provider (for example an LLM API or a local Ollama endpoint) so the model can respond. Whatever you configure is what receives that traffic; ogcode adds no intermediary of its own.

**Aggregate usage events.** The server can send anonymous product analytics: counters, durations, error codes and an install identifier. File contents, prompts and code are never part of these events. You can disable analytics entirely by setting the `OGCODE_POSTHOG` (or equivalent opt-out) environment variable to a falsey value before starting the server; see `ogcode --help` and the install guide for the current switches. Feature-flag requests follow the same rule: a flag fetch carries the install identifier, never your project data.

## The Ogweb Chrome extension

The Ogweb extension (`browser-extension/` in the repository) is a companion that lets an ogcode agent view and drive Chrome tabs you explicitly allow.

- **One destination only.** The extension connects over a WebSocket to the single server origin you type into its panel (for example `http://localhost:7801`), identified by a pairing token your own ogcode server generates. It makes no other network request of any kind: no analytics, no fonts, no CDNs, no update pings.
- **Per-tab consent.** Page data, screenshots, and network or console capture are taken only from tabs you have ticked in the panel. You can untick a tab at any moment and access stops immediately. Chrome also shows its own banner whenever the extension's debugger facility is attached, so takeover is always visible.
- **One-way data flow.** What is read on a granted tab (snapshot text, page content, screenshots, network and console entries) travels over that same paired WebSocket. The extension cannot send anything anywhere else.
- **Local storage only.** The panel remembers four things in `chrome.storage.local`: the server origin, the pair token, the list of granted tab ids, and whether you pressed Connect. Nothing else is persisted.

## Sharing

We do not sell personal data, we do not use it for advertising, and we do not share it with third parties. The only outbound parties are the model providers you yourself configure, and (for the extension) the ogcode server running on your own machine.

## Contact

Questions or a deletion request for anything above can be raised as an issue at [github.com/prasenjeet-symon/ogcode](https://github.com/prasenjeet-symon/ogcode/issues).