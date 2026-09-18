<div align="center">

<img src="assets/ogcode-logo.png" alt="ogcode" width="180" height="180">

# Ogcode

**The open-source, self-hostable Grok Bot for software work.**

Ogcode is an AI agent that can understand a codebase, use tools, research the web, remember decisions, plan complex work, and execute changes from a browser. It is designed as an open-source alternative to autonomous AI workspaces: your computer, your models, your data, and your rules.

**Today:** Ogcode is a production-ready agentic coding workbench.

**Our vision:** Ogcode becomes an open-source Grok Bot — a general-purpose digital teammate that can operate across your software, browser, development tools, and business workflows with your approval.

> Ogcode is an independent open-source project inspired by the emerging AI-agent category. It is not affiliated with, endorsed by, or produced by xAI, Grok, or X.

<br/>

[![Discord](https://img.shields.io/discord/1373677337985056828?label=Discord&logo=discord&logoColor=white&color=5865F2)](https://discord.gg/JQP9t8y2Zv)
[![Release](https://img.shields.io/github/v/release/prasenjeet-symon/ogcode?label=Release&style=flat)](https://github.com/prasenjeet-symon/ogcode/releases)
[![License](https://img.shields.io/github/license/prasenjeet-symon/ogcode?label=License&color=green)](LICENSE)
[![Stars](https://img.shields.io/github/stars/prasenjeet-symon/ogcode?style=social)](https://github.com/prasenjeet-symon/ogcode)

[Quick Start](#quick-start) · [What It Can Do](#what-ogcode-can-do-today) · [The Grok Bot Vision](#the-open-source-grok-bot-vision) · [Architecture](#how-it-works) · [Contributing](#contributing)

</div>

---

## What is Ogcode?

Most AI assistants answer questions. Ogcode is built to **take responsibility for multi-step work**.

You give it a goal such as:

> “Add GitHub OAuth, write the tests, update the documentation, and open a pull request.”

Ogcode can inspect the repository, form a plan, ask for permission when an action is sensitive, edit files, run commands, validate the result, remember the important decisions, and organize the work into isolated branches and pull requests.

It runs as a single Go binary with an embedded web interface. You can use it from any browser and keep using your preferred editor — Vim, Emacs, VS Code, JetBrains, or no editor at all. Use a hosted model, a local model, or switch providers without changing your workflow.

### The short version

- **Open source and self-hostable** — run Ogcode on your laptop, workstation, server, or private network.
- **Agentic, not autocomplete-only** — it reads, reasons, searches, edits, executes, tests, and iterates.
- **Browser-native** — a full workbench without locking you into a particular IDE.
- **Model-agnostic** — use Anthropic, OpenAI, OpenRouter, Ollama, or compatible endpoints.
- **Built for long-running work** — persistent memory, context curation, compaction, and resumable sessions.
- **Git-native** — plan features, split work into tasks, isolate changes in worktrees, and raise pull requests.
- **Permission-aware** — read-only exploration can be automatic; file writes, edits, and shell commands can require approval.
- **Designed to grow into a general computer agent** — coding is the first deeply implemented domain, not the final destination.

## What Ogcode can do today

### Understand projects instead of guessing

Ogcode has tools for discovering and understanding real repositories:

- Semantic `codebase_map` indexing for project-wide navigation.
- `file_map` outlines that show declarations and exact line ranges before a file is read.
- Tree-sitter parsing for Go, TypeScript/JavaScript, TSX, PHP, Python, Rust, Swift, Java, C#, and Dart.
- Targeted `read`, `glob`, and `grep` operations that keep irrelevant code out of the model context.
- Immediate syntax checking after supported file mutations.
- PDF and DOCX indexing for project documentation.

This is the foundation of a useful autonomous agent: it must know where to look before it starts changing things.

### Build and modify software

In Build Mode, Ogcode can:

- Explain an unfamiliar codebase and trace how a feature works.
- Implement features and bug fixes across multiple files.
- Create and edit source code, documentation, configuration, and tests.
- Run shell commands, builds, tests, formatters, and project-specific workflows.
- Investigate failures, read the relevant source, and try a different fix instead of blindly retrying.
- Produce a concise summary of what changed and how it was verified.

Sensitive tools can be configured to allow, deny, or ask for approval. Ogcode is powerful enough to modify your machine, so review permissions and deployment boundaries carefully.

### Plan and execute complete features

Plan Mode turns a broad objective into an executable delivery plan:

1. Describe the outcome you want.
2. Let the planning agent inspect the repository and refine the approach with you.
3. Lock the plan.
4. Review the generated Kanban board, task descriptions, estimates, and dependencies.
5. Execute independent tasks in parallel in isolated git worktrees.
6. Commit, push, and open pull requests when configured.
7. Retry failed tasks from a clean state.

The breakdown agent is instructed to keep parallel tasks from touching the same files. Dependent tasks share a branch and execute in order, which helps produce reviewable, conflict-resistant changes rather than a pile of competing patches.

### Remember the work

Agentic Session Memory stores useful facts from long-running sessions and retrieves only what is relevant to the current turn. Instead of replaying thousands of old messages, Ogcode can recall:

- Architectural decisions and rejected approaches.
- Configuration values and project conventions.
- Where an implementation lives.
- What was tried, what failed, and what remains.
- Important facts from prior sessions.

This keeps prompts smaller and more focused. In internal long-session testing, the memory loop reduced token use by more than 70% compared with naive full-transcript replay. The exact savings depend on the model, task, and session.

### Research and use external knowledge

Web search is built into the binary and can search, fetch, and synthesize research for the agent. It can use multiple search engines, an optional SearxNG instance, and a macOS Safari fallback for pages that require a real browser. Research results can be used alongside your local codebase and documents.

Reusable `SKILL.md` instructions let teams teach Ogcode repeatable workflows such as releases, migrations, deployment, incident response, or documentation generation. Skills can declare required environment variables and can be allowed, denied, or approval-gated.

### Create rich results

Ogcode can render more than plain text in the chat:

- Mermaid architecture, flow, sequence, and entity-relationship diagrams.
- LaTeX mathematics and complete LaTeX documents, including PDF generation.
- Plotly charts.
- Rough-style diagrams.
- Sandboxed HTML, CSS, and JavaScript experiences.
- Generated files placed in the workspace `public/` folder for browser download or preview.

### Work locally or remotely

Run Ogcode directly on your computer, in Docker, on a private server, or behind a reverse proxy. The browser UI works over HTTP, while sessions, project data, and memory remain on the host you choose. For teams, the control plane supports authenticated access, per-user workspace scoping, and remote worker sessions.

---

## The open-source Grok Bot vision

The long-term goal is simple:

> **Give everyone a capable, transparent, local-first AI teammate that can turn intent into completed work across the digital world.**

Ogcode is starting with software because codebases provide a clear, high-value environment for agent reliability: files are inspectable, changes are diffable, tests provide feedback, and git provides a safe history. The same agent loop can grow beyond the repository.

### 1. A conversational digital teammate

You should be able to tell Ogcode what outcome you want in natural language, without translating it into a checklist of commands.

It should:

- Ask the minimum necessary clarifying questions.
- Understand goals, constraints, preferences, deadlines, and definitions of done.
- Break ambiguous work into observable steps.
- Show what it plans to do before risky actions.
- Keep you informed without narrating every trivial operation.
- Pause for decisions and continue when you approve.
- Learn stable preferences without silently inventing them.

### 2. A computer-use agent with a browser and tools

The vision includes controlled interaction with the tools people already use:

- Open websites and navigate authenticated web applications.
- Fill forms, upload and download files, and extract information.
- Operate developer dashboards, issue trackers, project-management tools, and documentation systems.
- Use terminals, APIs, local files, git, containers, and automation scripts.
- Move information between systems while preserving an audit trail.
- Run repeatable routines on a schedule or in response to an event.

This is a **planned direction**, not a claim that every browser or desktop action is already implemented in the current release. Ogcode will add these capabilities incrementally, with explicit permissions and strong isolation rather than unrestricted computer control by default.

### 3. Software delivery from idea to production

The complete software-agent experience should cover the entire lifecycle:

- Discover and understand a product or codebase.
- Convert an idea, issue, or conversation into a technical plan.
- Delegate independent work to parallel agents.
- Implement code, tests, migrations, UI, documentation, and infrastructure.
- Run tests, static analysis, previews, and security checks.
- Review diffs and explain tradeoffs.
- Open, update, and merge pull requests with human approval.
- Deploy to staging or production through approved integrations.
- Monitor the result, diagnose incidents, and prepare safe rollback steps.

### 4. A persistent memory that belongs to you

An open-source Grok Bot should remember the context that makes it useful without turning your history into a vendor-owned black box. The vision is:

- Project memory that survives sessions and model changes.
- Personal preferences that are inspectable, editable, and deletable.
- Team knowledge with explicit ownership and access controls.
- Memory citations that show why the agent believes something.
- Automatic expiration for temporary or sensitive facts.
- Separation between instructions, observations, credentials, and untrusted content.
- Import and export so your memory is portable.

Ogcode already has persistent agentic memory and local storage. These controls and portability features are part of the direction we want to complete.

### 5. Model freedom and local intelligence

The open-source version of this category should not require one model vendor. Ogcode aims to provide:

- A consistent agent interface across hosted and local models.
- Automatic model selection by task, cost, latency, or privacy requirement.
- Local models for private or offline work.
- Provider fallbacks when a service is unavailable.
- Clear token, latency, and tool-use telemetry.
- The ability to bring your own API key, endpoint, GPU, or model.
- No hidden requirement to send your source code to Ogcode's maintainers.

### 6. Safe autonomy instead of blind autonomy

Capability without control is not a product philosophy. The Grok Bot vision includes:

- Fine-grained permissions for files, shell commands, websites, credentials, and deployments.
- Approval checkpoints for destructive, external, expensive, or irreversible actions.
- Sandboxed workers and disposable execution environments.
- Secrets passed only to the tool that needs them, never placed in prompts unnecessarily.
- Complete action logs and replayable task histories.
- Dry-run and plan-only modes.
- Resource limits, timeouts, network policies, and emergency stop controls.
- Clear distinction between trusted instructions and untrusted web or repository content.

### 7. A team of specialized agents

A single agent should be able to delegate without making the workflow opaque. Future Ogcode workers may specialize in:

- Research and source verification.
- Architecture and design.
- Implementation.
- Testing and debugging.
- Security review.
- Documentation and release communication.
- Operations and incident response.

The user should see the plan, ownership, dependencies, evidence, and final result — not just a mysterious answer from a swarm.

### 8. An extensible open platform

Ogcode should become a platform for community-built capabilities:

- Skills and tool plugins.
- Connectors for GitHub, GitLab, Slack, Jira, Linear, email, cloud providers, and databases.
- Standard protocols for remote agents and workers.
- Events and webhooks for automation.
- Shared team workflows that remain reviewable and version-controlled.
- A local-first core that can be self-hosted or operated as a private service.

The roadmap is intentionally ambitious. Some items above are available now, some are in active development, and some are future goals. The project will mark capability boundaries clearly rather than present a prototype as a finished feature.

---

## Why Ogcode?

| Capability | Ogcode's approach |
| --- | --- |
| Interface | Browser-native workbench; use any editor alongside it |
| Hosting | Self-hosted single binary, Docker, or private server |
| Models | Anthropic, OpenAI, OpenRouter, Ollama, and compatible endpoints |
| Execution | Tool-using agent loop with permission controls |
| Planning | Formal plans, Kanban tasks, dependencies, parallel worktrees |
| Memory | Persistent local agentic memory and project knowledge |
| Context | Targeted file reads, semantic maps, compaction, and recall |
| Research | Built-in web search and page fetching |
| Output | Code changes, PRs, diagrams, charts, documents, and downloadable artifacts |
| Governance | Open source, inspectable, extensible, and self-hostable |

Ogcode is not trying to be an IDE plugin with a chat panel. It is trying to be an **agent operating environment**: a place where an AI can understand work, use tools, take bounded action, and leave behind evidence that a person can review.

---

## Quick Start

### macOS / Linux

```bash
curl -fsSL https://ogcode.xyz/install.sh | sh
export ANTHROPIC_API_KEY=sk-ant-...
ogcode
```

Open `http://localhost:9595`. You can also use Homebrew:

```bash
brew tap prasenjeet-symon/tap
brew install ogcode
```

### Windows

```powershell
irm https://ogcode.xyz/install.ps1 | iex
ogcode
```

Or:

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

Then open `http://localhost:9595`.

### Use a local model with Ollama

```bash
ollama serve
ogcode
```

Or configure an explicit endpoint:

```bash
export OLLAMA_BASE_URL=http://localhost:11434/v1
ogcode
```

### Modes

```bash
ogcode              # Build Mode: chat, inspect, edit, execute, verify
ogcode plan         # Plan Mode: decompose and execute a larger feature
ogcode -p 3000      # Use a custom port
```

---

## Configuration

Ogcode detects providers from the environment. Set at least one of:

| Variable | Provider |
| --- | --- |
| `ANTHROPIC_API_KEY` | Anthropic / Claude |
| `OPENAI_API_KEY` | OpenAI / GPT |
| `OPENROUTER_API_KEY` | OpenRouter |
| `OLLAMA_BASE_URL` | Ollama or an OpenAI-compatible local endpoint |

You can also use `~/.config/ogcode/config.json` for global settings and `ogcode.json` at the project root for project settings. Environment variables override config values.

```json
{
  "providers": {
    "ollama": { "baseUrl": "http://localhost:11434" },
    "anthropic": { "apiKey": "sk-ant-..." }
  },
  "skills": {
    "paths": ["./team-skills"],
    "permissions": { "deploy-prod": "ask" },
    "env": { "DEPLOY_TOKEN": "..." }
  }
}
```

For slow local models, configure the stream idle watchdog:

```bash
export OGCODE_STREAM_IDLE_TIMEOUT=30m
# Disable it only when you understand the risk:
export OGCODE_STREAM_IDLE_TIMEOUT=off
```

> If copying these examples, use the exact variable name `OGCODE_STREAM_IDLE_TIMEOUT`.

Agentic memory is enabled with:

```bash
export OGCODE_AGENTIC_MEMORY_MODE=true
```

Web search is enabled by default. Disable it with `OGCODE_SEARCH_ENABLED=false`. See the [documentation](docs/OUTLINE.md) for provider, skill, search, remote deployment, and control-plane configuration.

---

## How it works

Ogcode is a Go server with an embedded SolidJS web UI. The agent loop connects your chosen model to a controlled set of tools and local stores:

```text
Browser UI
    │ REST + SSE
    ▼
Go server ─── Agent loop ─── LLM provider
    │             │
    │             ├── Files, git, shell, tests
    │             ├── Codebase maps and syntax checks
    │             ├── Web search and document readers
    │             └── Skills and persistent memory
    │
    └── Local session database, plans, tasks, permissions, and artifacts
```

The core design principle is **relevant context over complete history**. Ogcode maps before reading, reads ranges instead of blindly loading large files, recalls facts instead of replaying every turn, and compacts stale context when needed. This improves cost, focus, and reliability on long-running work.

---

## Remote deployment and security

Ogcode can run on a remote machine and be reached from a browser, but it can read and modify files and execute commands. **Do not expose it directly to the public internet without authentication.**

Recommended boundaries:

1. Bind to localhost and use an SSH tunnel.
2. Put a reverse proxy with HTTPS and authentication in front of it.
3. Use a VPN such as WireGuard or Tailscale.
4. Run high-risk work in Docker, a VM, or an isolated worker.
5. Mount only the workspace and data directories the agent needs.

```bash
ssh -L 9595:localhost:9595 user@your-server
```

For a hosted deployment, review the control-plane documentation and configure account authentication, workspace allowlists, TLS, and worker isolation before inviting other users.

---

## Project status and roadmap

Ogcode is actively developed. The coding-agent foundation is implemented; the broader Grok Bot capabilities are an evolving roadmap.

### Implemented foundation

- Agentic coding in Build and Plan modes.
- Browser UI and single-binary distribution.
- Multiple hosted and local model providers.
- Tool permissions and approval flows.
- Semantic project and file maps.
- Persistent session memory and context compaction.
- Web research and document reading.
- Parallel git worktrees and pull-request workflows.
- Skills, rich output, public artifacts, and remote workers.

### Next horizons

- More reliable browser and computer-use tools.
- First-class connectors for communication, project management, and deployment systems.
- Scheduled routines, event triggers, and long-running jobs.
- Better multi-agent delegation and observability.
- Stronger sandboxing, secret isolation, and audit controls.
- Portable, inspectable personal and team memory.
- More capable local-model routing and offline operation.
- Agent-assisted deployment, monitoring, and incident response.

See [RELEASE_NOTES.md](RELEASE_NOTES.md), [docs/OUTLINE.md](docs/OUTLINE.md), and the project plans for implementation details.

---

## Contributing

Ogcode is open source and contributions are welcome — code, tests, documentation, skills, integrations, design ideas, and security reviews.

1. Fork the repository.
2. Create a branch: `git checkout -b feature/my-improvement`.
3. Make changes and add tests where applicable.
4. Run `CGO_ENABLED=1 go test ./...`.
5. Open a pull request.

For development builds, run `make build`. Read [CONTRIBUTING.md](CONTRIBUTING.md) before submitting a change. Join the [Discord community](https://discord.gg/JQP9t8y2Zv) to discuss the roadmap.

---

## Security

Ogcode agents can access the workspace and, when approved, execute shell commands. Treat an agent like a powerful automation process:

- Review permissions before enabling write or shell access.
- Keep API keys and secrets out of prompts, repositories, and public artifacts.
- Use isolated environments for untrusted code.
- Do not expose an unauthenticated server to the internet.
- Report vulnerabilities privately through the project's security channels.

The project is designed to keep your code and session data local. Conversation content is sent to the model provider you configure; provider retention and privacy policies still apply.

---

## License

Ogcode is dual-licensed:

- **[GNU AGPL v3.0](LICENSE)** — free and open source for running, modifying, and self-hosting Ogcode. If you run a modified version over a network, AGPL §13 provides corresponding-source rights to its users.
- **Ogcode Commercial License** — for embedding Ogcode in proprietary products, offering a hosted service without publishing modifications, or when AGPL is not suitable. See [LICENSING.md](LICENSING.md).

Releases up to and including **v0.36.1** remain MIT. **v0.37.0 onward is AGPL-3.0-only.** Bundled third-party code is listed in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

<br/>

<div align="center">

**Build the open-source AI teammate.**

[Star on GitHub](https://github.com/prasenjeet-symon/ogcode) · [Discord](https://discord.gg/JQP9t8y2Zv) · [Documentation](docs/OUTLINE.md)

</div>
