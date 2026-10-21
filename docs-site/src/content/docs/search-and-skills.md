---
title: Search, skills & MCP
description: Web search, on-demand skills, and connecting external MCP servers.
---

Sooner or later the agent needs something that is not in your repository: the current state of a library, a workflow you repeat, or a tool you already use. Ogcode has three ways to reach outside the codebase.

## Web search

Web search is built in and on by default. The agent can search, fetch specific pages, and synthesise the results alongside your local code and documents — useful for checking an API, a version, or an error message without leaving the session.

To turn it off:

```bash
export OGCODE_SEARCH_ENABLED=false
```

Point it at your own search if you have one (for example a self-hosted SearxNG), or supply a provider key:

| Variable | Purpose |
| --- | --- |
| `OGCODE_SEARXNG_URL` | Use a SearxNG instance for search |
| `TAVILY_API_KEY` | Use Tavily for search and page reading |
| `YDC_API_KEY` | Use You.com for search and page reading (select the You.com provider in Settings → Web search) |

Two knobs tune deeper research — how many pages it reads and how much of each — but the defaults are sensible and most people never touch them.

## Skills

A **skill** is a folder with a `SKILL.md` file that teaches the agent a repeatable procedure — a release checklist, a migration, how your team writes docs. When a task matches, the agent loads the skill's instructions and follows them, instead of you explaining the process every time.

Skills are discovered from standard locations automatically: an `.agents/skills/` folder in the project, and `~/.ogcode/skills/` (plus a couple of other common homes) on your machine. Drop a `SKILL.md` in one of those and it is available.

A skill can also declare the environment variables it needs (an API token, an endpoint), and you can decide per skill whether it is allowed, denied, or gated behind an approval — so a skill you do not fully trust never runs without you saying so.

## MCP servers

The **Model Context Protocol** (MCP) is how you connect external tools — an issue tracker, a design tool, a documentation service. Once connected, an MCP server's tools become available to the agent like any built-in one.

Servers are declared in `ogcode.json` at your project root (or in your global config) under an `mcp` key:

```json
{
  "mcp": {
    "ctx7": { "url": "https://context7.example/mcp" }
  }
}
```

- A server with a **`url`** is a remote endpoint. A **`command`** instead of a `url` runs a local program as the server.
- A remote server that needs a login gets an **OAuth** flow automatically — the first time it asks for authorisation, a browser opens and the token is remembered.
- A server can be turned off without deleting it by setting `"disabled": true`; nothing about it reaches the agent while it is off.

MCP tools are named after their server, so you can always tell where a tool came from in the transcript.

## Where to put the config

Project settings go in `ogcode.json` at the repo root; machine-wide settings go in your global config. A project file overrides the global one for anything it sets — so a shared `ogcode.json` can describe the project's servers while your personal tokens stay on your machine.
