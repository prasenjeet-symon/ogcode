<div align="center">

<img src="assets/ogcode-logo.png" alt="ogcode" width="180" height="180">

# Ogcode

**The open-source, self-hostable Grok Bot for software work.**

Ogcode is an AI agent that understands your codebase, uses real tools, researches the web, remembers decisions, plans complex work, and ships changes — from your browser. Your computer, your models, your data, your rules.

**Today:** a production-ready agentic coding workbench.
**Our vision:** an open-source Grok Bot — a general-purpose digital teammate that works across your software, browser, and tools with your approval.

<br/>

[![Discord](https://img.shields.io/discord/1373677337985056828?label=Discord&logo=discord&logoColor=white&color=5865F2)](https://discord.gg/JQP9t8y2Zv)
[![Release](https://img.shields.io/github/v/release/prasenjeet-symon/ogcode?label=Release&style=flat)](https://github.com/prasenjeet-symon/ogcode/releases)
[![License](https://img.shields.io/github/license/prasenjeet-symon/ogcode?label=License&color=green)](LICENSE)
[![Stars](https://img.shields.io/github/stars/prasenjeet-symon/ogcode?style=social)](https://github.com/prasenjeet-symon/ogcode)

[Quick Start](#quick-start) · [What You Can Do](#what-you-can-do-with-ogcode) · [Configuration](#configuration) · [Contributing](#contributing) · [Docs](https://ogcode.in/docs/)

</div>

---

## What is Ogcode?

Most AI assistants answer questions. Ogcode is built to **finish multi-step work**.

Give it a goal:

> “Add GitHub OAuth, write the tests, update the documentation, and open a pull request.”

It inspects the repository, forms a plan, asks before anything sensitive, edits the files, runs the commands, validates the result, records the important decisions, and organizes the work into isolated branches and pull requests. Throughout, you can watch, steer, and approve — or step away.

Ogcode runs as one program with a built-in web interface, so you use it from any browser and keep whatever editor you already like.

- **Open source and self-hostable** — run it on a laptop, workstation, server, or private network.
- **Model-agnostic** — Anthropic, OpenAI, OpenRouter, Ollama, or any compatible endpoint.
- **Browser-native** — a complete workbench, with no IDE lock-in.
- **Built for long-running work** — persistent memory and a context that stays a useful size.
- **Git-native** — plans, isolated task branches, and pull requests.
- **Permission-aware** — explore freely; gate writes, edits, and shell commands.
- **Designed to grow** — coding is the first deeply built domain, not the last.

## What you can do with Ogcode

- **Understand unfamiliar code** — Ogcode maps a project before reading it, outlines files with exact line ranges, and reads only what matters. [Core concepts →](https://ogcode.in/docs/core-concepts/)
- **Build and fix software** — implement features and fixes across files, run your builds and tests, and investigate failures instead of retrying blindly.
- **Deliver a whole feature** — turn an objective into tasks, run independent ones in parallel, and open pull requests for review. [Plan mode & tasks →](https://ogcode.in/docs/plan-mode/)
- **Stay in control** — choose Ask, Auto, or Yolo per session, and approve or remember each decision. [Permissions →](https://ogcode.in/docs/permissions/)
- **Keep context that fits** — recall past decisions instead of replaying the whole transcript, so long sessions stay focused and affordable. [Memory & context →](https://ogcode.in/docs/memory-and-context/)
- **Research as you go** — built-in web search and page reading, reusable `SKILL.md` workflows, and external MCP servers. [Search, skills & MCP →](https://ogcode.in/docs/search-and-skills/)
- **See more than text** — Mermaid diagrams, LaTeX and rendered PDFs, Plotly charts, sandboxed HTML, and downloadable artifacts. [Rich results & preview →](https://ogcode.in/docs/rich-results/)

New here? The [Quick start guide](https://ogcode.in/docs/quick-start/) walks from a fresh install to Ogcode editing files in your repo.

---

## Quick Start

### macOS / Linux

```bash
curl -fsSL https://ogcode.in/install.sh | sh
export ANTHROPIC_API_KEY=sk-ant-...
ogcode
```

Then open `http://localhost:9595`. With Homebrew:

```bash
brew tap prasenjeet-symon/tap
brew install ogcode
```

### Windows

```powershell
irm https://ogcode.in/install.ps1 | iex
ogcode
```

Or with winget:

```powershell
winget install prasenjeet-symon.ogcode
```

### Docker

```bash
docker run -p 9595:9595 \
  -v ~/.ogcode:/root/.ogcode \
  -v "$(pwd):/workspace" -w /workspace \
  ghcr.io/prasenjeet-symon/ogcode:latest
```

Then open `http://localhost:9595`. The image is also on Docker Hub as `prasenjeetsimon/ogcode:latest` — either reference works.

### Use a local model with Ollama

```bash
ollama serve
ogcode
```

Or point Ogcode at an explicit endpoint:

```bash
export OLLAMA_BASE_URL=http://localhost:11434/v1
ogcode
```

### Modes

```bash
ogcode              # Build mode: chat, inspect, edit, execute, verify
ogcode plan         # Plan mode: decompose and execute a larger feature
ogcode -p 3000      # Run on a custom port
```

---

## Configuration

Ogcode detects your provider from the environment. Set at least one:

| Variable | Provider |
| --- | --- |
| `ANTHROPIC_API_KEY` | Anthropic / Claude |
| `OPENAI_API_KEY` | OpenAI / GPT |
| `OPENROUTER_API_KEY` | OpenRouter |
| `OLLAMA_BASE_URL` | Ollama, or an OpenAI-compatible local endpoint |

Settings can also live in `ogcode.json` at the project root (project settings) and `~/.config/ogcode/config.json` (global settings); environment variables override both.

```json
{
  "providers": {
    "ollama": { "baseUrl": "http://localhost:11434" },
    "anthropic": { "apiKey": "sk-ant-..." }
  },
  "skills": {
    "paths": ["./team-skills"],
    "permissions": { "deploy-prod": "ask" }
  }
}
```

**OGX** is a subscription plan from OG Lab. Connect it from the settings screen and the plan's models run through OG Lab's gateway — no environment variable needed.

Everything else — provider, search, skill, memory, context, and deployment tuning — lives in the [documentation](https://ogcode.in/docs/).

---

## The open-source Grok Bot vision

The long-term goal is simple: **give everyone a capable, transparent, local-first AI teammate that can turn intent into finished work.**

Ogcode starts with software because a codebase is an honest testbed — files are inspectable, changes are diffable, tests give feedback, and git is a safe history. The same approach then extends beyond the repository: to the browser and the tools you already use, moving information safely while keeping an audit trail. Some of this ships today; the rest is a roadmap we mark clearly rather than dress up as finished.

> Ogcode is an independent open-source project inspired by the emerging AI-agent category. It is not affiliated with, endorsed by, or produced by xAI, Grok, or X.

---

## Remote deployment and security

Ogcode can run on a remote machine and be reached from a browser — but it can read and modify files and run commands. **Never expose it directly to the public internet without authentication.**

Recommended boundaries:

1. Bind to localhost and reach it over an SSH tunnel.
2. Put a reverse proxy with HTTPS and authentication in front of it.
3. Use a VPN such as WireGuard or Tailscale.
4. Run high-risk work in Docker, a VM, or an isolated worker.

```bash
ssh -L 9595:localhost:9595 user@your-server
```

Working setups — SSH tunnel, reverse proxy, and Docker — are in the [remote deployment guide](https://ogcode.in/docs/deployment/). For a hosted, multi-user deployment, see the [control-plane documentation](controlplane/docs/deploy.md).

---

## Contributing

Ogcode is open source and contributions are welcome — code, tests, documentation, skills, integrations, and design ideas.

1. Fork the repository and create a branch.
2. Make your change, with tests where they matter.
3. Run `CGO_ENABLED=1 go test ./...`.
4. Open a pull request.

Development builds use `make build`. Read [CONTRIBUTING.md](CONTRIBUTING.md) before submitting, and say hello on [Discord](https://discord.gg/JQP9t8y2Zv).

---

## Security

Treat Ogcode like a powerful automation process:

- Review permissions before enabling write or shell access.
- Keep API keys and secrets out of prompts, repositories, and public artifacts.
- Use isolated environments for untrusted code.
- Do not expose an unauthenticated server to the internet.
- Report vulnerabilities privately through the project's security channels.

Your code and session data stay local; only conversation content is sent to the model provider you configure, where that provider's privacy policy applies.

---

## License

Ogcode is dual-licensed:

- **[GNU AGPL v3.0](LICENSE)** — free and open source for running, modifying, and self-hosting Ogcode. If you run a modified version over a network, AGPL §13 gives its users corresponding-source rights.
- **Ogcode Commercial License** — for embedding Ogcode in proprietary products, offering a hosted service without publishing modifications, or whenever AGPL is not suitable. See [LICENSING.md](LICENSING.md).

Releases up to and including **v0.36.1** remain MIT. **v0.37.0 onward is AGPL-3.0-only.** Bundled third-party code is listed in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

<br/>

<div align="center">

**Build the open-source AI teammate.**

[Star on GitHub](https://github.com/prasenjeet-symon/ogcode) · [Discord](https://discord.gg/JQP9t8y2Zv) · [Documentation](https://ogcode.in/docs/)

</div>
