---
title: Privacy
description: What the Ogcode server and the Ogweb browser extension handle on your machine, and what leaves it.
---

# Privacy

Ogcode is local-first software. This page describes what data it handles, where it lives, and what (if anything) leaves your machine. It covers the ogcode binary and server, and the Ogweb Chrome extension that pairs with it.

## The ogcode server

**Your work stays on your machine.** Sessions, transcripts, project indexes, memory files, bookmarks and grants are stored inside the project directory and in your home directory (`.ogcode/` folders, e.g. `.ogcode/ogcode.db`). Nothing is uploaded to an ogcode-owned service for storage. If you configure a model provider, the prompt and context needed for the current turn are sent to that provider (for example an LLM API or a local Ollama endpoint) so the model can respond. Whatever you configure is what that traffic is addressed to; the one exception is the first-party OGX plan, where inference is routed through OG Lab's gateway at ogx.ogcode.in before it reaches the model provider.

**Aggregate usage events.** The server sends anonymous product analytics to PostHog (app.posthog.com) by default: an install identifier, general system details (operating system, architecture, app version and the machine hostname), and events such as install, server start and stop, the detected project type, and per-turn resource counters (timings, memory, CPU). The web interface sends its own page-view and product events to the same PostHog project, identified by the same install identifier. These events never include file contents, prompts, code, or model responses. There is currently no setting or environment variable that turns these events off, for the server or the web interface. A separate, opt-in switch ships server log records: set `OGCODE_POSTHOG_LOGS` to a truthy value before starting the server, and warnings and errors (floor configurable with `OGCODE_POSTHOG_LOGS_LEVEL`) go to PostHog Logs. Leave it unset and nothing ships: setting `DO_NOT_TRACK` before starting the server turns this off regardless of the switch. Feature-flag requests follow the same rule: a flag fetch carries the install identifier, never your project data.

## The Ogweb Chrome extension

The Ogweb extension (`browser-extension/` in the repository) is a companion that lets an ogcode agent view and drive Chrome tabs you explicitly allow.

- **One destination only.** The extension connects over a WebSocket to the single server origin you type into its panel (for example `http://localhost:7801`), identified by a pairing token your own ogcode server generates. It makes no other network request of any kind: no analytics, no fonts, no CDNs, no update pings.
- **Per-tab consent.** Page data, screenshots, and network or console capture are taken only from tabs you have ticked in the panel. You can untick a tab at any moment and access stops immediately. Chrome also shows its own banner whenever the extension's debugger facility is attached, so takeover is always visible.
- **One-way data flow.** What is read on a granted tab (snapshot text, page content, screenshots, network and console entries) travels over that same paired WebSocket. The extension cannot send anything anywhere else.
- **Local storage only.** The panel remembers four things in `chrome.storage.local`: the server origin, the pair token, the list of granted tab ids, and whether you pressed Connect. Nothing else is persisted.

## Sharing

We do not sell personal data, we do not use it for advertising, and we do not share it with third parties. The only outbound parties are the model providers you yourself configure, PostHog as described above, and (for the extension) the ogcode server running on your own machine. When the agent uses web search or fetches a page for you, the search query or that page's URL reaches its destination as well: the default search backend queries DuckDuckGo or Brave, and model-pricing lookups fetch OpenRouter's public price list.

## Contact

Questions or a deletion request for anything above can be raised as an issue at [github.com/prasenjeet-symon/ogcode](https://github.com/prasenjeet-symon/ogcode/issues).