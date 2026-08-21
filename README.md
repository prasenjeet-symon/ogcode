<div align="center">

<img src="assets/ogcode-logo-cropped.png" alt="Ogcode Logo" width="200" height="200">

# Ogcode

**The token-efficient agentic coding workbench.**

Built for a future where every token counts. Ogcode curates the *relevant* context for each turn — not the full transcript — so it cuts 70%+ of tokens on long sessions, sharpens accuracy, and lets even lower-end models outperform frontier ones. And because it *recalls* instead of *replays*, your conversations run **effectively forever — you never hit a model's context limit**, on any model, frontier or local. All while planning with you, remembering your codebase, and shipping features in parallel from a single binary that never leaves your machine.

<br/>

[![Discord](https://img.shields.io/discord/1373677337985056828?label=Discord&logo=discord&logoColor=white&color=5865F2)](https://discord.gg/JQP9t8y2Zv)
[![Release](https://img.shields.io/github/v/release/prasenjeet-symon/ogcode?label=Release&style=flat)](https://github.com/prasenjeet-symon/ogcode/releases)
[![License](https://img.shields.io/github/license/prasenjeet-symon/ogcode?label=License&color=green)](LICENSE)
[![Stars](https://img.shields.io/github/stars/prasenjeet-symon/ogcode?style=social)](https://github.com/prasenjeet-symon/ogcode)

[![Go](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![SolidJS](https://img.shields.io/badge/SolidJS-1.9-2F2E82?logo=solidjs&logoColor=white)](https://www.solidjs.com)
[![MIT License](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

<br/>

![Ogcode Demo](assets/ogcode_intro.gif)

<br/>

[Context Engineering](#context-engineering--the-real-differentiator) · [Infinite Context](#infinite-context--never-hit-a-models-limit) · [Plan Mode & Parallel PRs](#plan-mode--parallel-prs--ship-features-not-just-suggestions) · [Quick Start](#quick-start) · [Why Ogcode](#why-ogcode) · [Documentation](docs/OUTLINE.md) · [Discord](https://discord.gg/JQP9t8y2Zv)

</div>

---

## Context Engineering — the real differentiator

> **Every other coding agent resends your entire conversation history on every turn. Ogcode doesn't.**

Most coding agents operate on a naive replay loop: each turn, they bundle up the **full transcript so far** — every prior message, every tool result, every tangent — and ship it back to the model. That has two costs, and only one of them is money.

**1. It burns tokens.** The prompt grows linearly with the session, so a 200-message task can cost 5× more than a 20-message one even if the *new* work is trivial. On a fixed monthly budget this caps how much you can actually ship.

**2. It *hurts accuracy* — and this matters more than the money.** An LLM can only act on what's in its context window. When you flood that window with stale, unrelated chatter from earlier in the session, the signal gets buried in noise: the model loses sight of the current task, drifts toward half-remembered earlier decisions, and reasons against context that was relevant *then* but isn't relevant *now*. The older the conversation, the more the historical turns actively *distract* from the turn in front of the model.

Ogcode does the opposite. For each turn it **extracts only the context that is actually relevant to the task at hand** — pulling precise facts from a persistent knowledge graph via `memory_recall` and compacting stale history instead of replaying it verbatim. The model receives a short, sharp, on-point context window. Less history, fewer tokens — and *better* outcomes, because the model isn't wading through a hundred old messages to find the three facts it needs right now.

**Saving tokens isn't only about cost — it's about accuracy.** A smaller, more relevant context window lets the model focus, so it produces more correct, more on-target results per turn. The two goals reinforce each other.

### This is unique to Ogcode

No other coding agent on the market does this. Claude Code, Cursor, Copilot, Aider — every one of them replays the full conversation every turn. Token efficiency, in those tools, is an afterthought at best. Ogcode is the only agent engineered, **at the agent-loop level**, to conserve tokens *and* to curate context per turn — because it believes the real lever is **context engineering**: how efficiently and how relevantly you prepare the context for a given task.

### Context engineering lets smaller models punch above their weight

This is the deeper payoff. If the only thing context engineering did was save money, it would still be worth it — but it does more: it **extracts capability even from lower-end models**. Keep the context relevant, limited, and short, and a mid-tier model (Claude Sonnet, a local Llama, a smaller GPT) can reason just as clearly — and sometimes *outperform* — a frontier model that's been handed a bloated, noisy transcript. The frontier model isn't smarter *about your code*; it just has more raw capacity to dig itself out of the irrelevant history you buried it under. Give either model a clean, on-point context window and the gap narrows dramatically — often to zero.

In the end, it's all about context engineering. Ogcode is brilliant at this, which is why it simultaneously **cuts token cost** and **increases the accuracy of the task outcome**. Cheaper *and* better — not a tradeoff.

---

## Infinite Context — never hit a model's limit

> **Every model has a context window. Every other agent eventually slams into it. With Ogcode you never do — chat forever, on any model, no matter how small its window.**

This is the part that genuinely changes the game. Every LLM ships with a fixed context limit — 8K, 128K, 200K, a million tokens — and every other coding agent creeps toward that wall as the session grows, until the model starts dropping the start of the conversation or simply refuses to continue. **Ogcode removes the ceiling entirely.**

The reason is **Agentic Session Memory**. Because Ogcode *recalls* the few facts relevant to the current turn from a persistent knowledge graph — instead of *replaying* the entire transcript — the prompt it sends stays flat no matter how long the conversation runs. A session that's 50 messages deep and one that's 5,000 messages deep hand the model the *same* compact, on-point context window. **The conversation is unbounded; the per-turn context is not.** So you can talk to a model forever and never reach its limit — and that holds for *any* model, whatever the size of its native window.

### Three wins from one idea

- **Infinite conversations, on any model.** Drive Ogcode with a frontier model or a tiny local Llama with an 8K window — it makes no difference. The knowledge graph keeps every turn small, so the model's own context limit is never the bottleneck. You get effectively *limitless* session length even on models that could never sustain it alone.
- **Far lower cost.** A flat per-turn prompt means you stop paying the linearly-growing bill of full-transcript replay — over **70% fewer tokens** on long sessions. See [Context Engineering](#context-engineering--the-real-differentiator) and [Token Efficiency](#token-efficiency).
- **Higher accuracy.** The model reasons over only the facts that matter for the turn in front of it, not a wall of stale history — so it drifts less and stays on-target. Cleaner context is *more* accurate, not just cheaper.

Never hit a context limit, spend far fewer tokens, and get more accurate results — on whatever model you choose. That is the true beauty of Agentic Session Memory. *(For the knowledge-graph internals, see [Agentic Session Memory](#agentic-session-memory).)*

---

## Plan Mode + Parallel PRs — ship features, not just suggestions

> **Other agents suggest code. Ogcode plans the feature, decomposes it, executes the pieces in parallel, and raises the pull requests for you.**

Most coding agents stop at "here's the code for the file you asked about." Ogcode's **Plan Mode** turns a one-line goal into a shipping feature. You describe what you want to build; the planning agent reads your codebase, discusses the approach with you, and — once you lock the plan — **it becomes Ogcode's responsibility** to break that feature into smaller, implementation-ready tasks, run them, and open the pull requests.

### Plan once, ship the whole feature

```
┌──────────────┐    ┌──────────────┐    ┌──────────────┐    ┌──────────────┐
│  1. Describe │ → │  2. Lock      │ → │  3. Review    │ → │  4. Execute   │
│   your goal   │    │   the plan    │    │  Kanban board │    │   in parallel │
└──────────────┘    └──────────────┘    └──────────────┘    └──────┬───────┘
                                                                    │
                    ┌──────────────┐    ┌──────────────┐    ┌───────▼──────┐
                    │  6. Retry    │ ← │  5. Complete  │ ← │  Task runs    │
                    │  if needed    │    │  auto-PR      │    │  in isolated  │
                    └──────────────┘    └──────────────┘    │  git branch   │
                                                            └──────────────┘
```

1. **Describe** — Open a plan and describe your goal. The planning agent reads your codebase and refines the approach with you in conversation.
2. **Lock** — When you're happy, lock the plan. Ogcode runs a **breakdown agent** that turns the agreed plan into a structured task list with effort estimates (S/M/L/XL), complexity scores, and a dependency graph.
3. **Review** — Tasks land on a **visual Kanban board**. Inspect, reorder, or kick off tasks individually.
4. **Execute** — Each task gets its **own git branch and isolated agent session**. Tasks with no dependency on each other run **in parallel** — multiple agents coding at the same time.
5. **Complete** — Finished tasks auto-commit, push, and open **pull requests directly against your GitHub repo**.
6. **Retry** — A failed task is retried from a clean slate: the stale branch is deleted and the task starts fresh.

### How Ogcode breaks the feature down

The breakdown is deliberate about **parallelism and conflict-free merges** — this is the part most agents get wrong and Ogcode gets right:

- **File-ownership rule.** The breakdown agent is instructed that parallel tasks **must not touch the same files**. If two tasks would edit the same file, the agent adds a dependency between them so they run sequentially instead. This is what keeps the auto-PRs **merge-conflict-free**.
- **Strictly linear dependency chains.** Each task may depend on at most one other task, producing clean A→B→C chains rather than tangled fan-in graphs. Cycles are detected and rejected before execution ever starts.
- **Implementation-ready descriptions.** Every task description names exact file paths, function/type names, patterns to follow, and edge cases — so a developer (or the task agent) can implement it from the description alone.
- **Shared chain branches.** Tasks in a dependency chain share one branch and accumulate each completed step, so a downstream task always sees its predecessors' work. The chain raises a **single consolidated PR** when the last task completes, instead of N conflicting ones.

### Why this matters

The payoff is end-to-end **parallel feature completion**: you plan one feature, and Ogcode ships it as a set of clean, reviewable pull requests that don't fight each other. It does this because **git is baked into the Ogcode core**, not bolted on as an afterthought:

- **Git worktrees at the core.** Every task runs in an isolated `git worktree` — a real, independent working checkout on its own branch. Multiple agents work simultaneously without clobbering each other's files or your main branch.
- **Auto-commit, push, and PR.** When a task finishes, Ogcode commits any leftover changes, pushes the branch to `origin`, and opens a pull request via the `gh` CLI — idempotently (re-running won't create duplicate PRs), and with a generated PR body.
- **Stacked PRs, handled gracefully.** Dependent tasks branch from their predecessor's branch, not from HEAD. If a stacked base branch was already merged and deleted, Ogcode **falls back to the repo's default branch** automatically so the PR is still created correctly.
- **Merge-conflict-safe by construction.** Because parallel tasks own disjoint files (enforced at breakdown time) and chained tasks share a branch they merge into sequentially, the PRs Ogcode raises are designed to merge **without conflicts**.
- **Beautiful cleanup.** Worktrees, branches, and stale directories are pruned automatically — on success, on failure, on retry, and on plan deletion. Crashed sessions are recovered and cleaned up on the next server start.

In short: you describe the feature, Ogcode plans it, splits it into parallel-safe tasks, executes them across isolated branches, and opens the PRs — **directly to your upstream GitHub repo**, with no merge conflicts and no manual git wrangling. That's parallel task completion *and* parallel feature completion, from a single binary that manages the GitHub repo and its git worktrees natively because git is part of the Ogcode core.

---

Ogcode is an agentic coding assistant that runs entirely on your machine — a single Go binary with an embedded SolidJS web UI. It doesn't just suggest code. It **understands your whole codebase**, **plans complex features with you**, and **executes them across parallel git branches** — while your code stays local and private.

Unlike IDE-locked assistants (Cursor, Copilot) or cloud-only services (Claude Code), Ogcode is **browser-native, self-hosted, and model-agnostic**. Use Claude, GPT, OpenRouter, or local Ollama models, and switch anytime from the UI. No subscriptions. No vendor lock-in. Nothing leaves your machine except the prompts you send to your chosen provider.

```bash
curl -fsSL http://ogcode.xyz/install.sh | sh && ogcode
```

---

## How Ogcode Compares

|                       | Ogcode                        | Cursor          | Claude Code   | Copilot       | Aider          |
| --------------------- | ----------------------------- | --------------- | ------------- | ------------- | -------------- |
| **Interface**         | Web UI (any editor)           | VS Code fork    | Terminal      | IDE extension | Terminal       |
| **Self-Hosted**       | Single binary, zero deps      | Cloud-required  | Cloud-only    | Cloud-only    | Open source    |
| **Parallel Tasks**    | Git worktrees, conflict-free auto-PRs to upstream | Cloud agents    | Subagents     | Single agent  | Sequential     |
| **Plan Mode**         | Kanban + effort estimates     | Agents window   | Architect mode| Prompt-based  | `/architect`   |
| **Persistent Memory** | Knowledge graph               | Session-only    | CLAUDE.md     | None          | None          |
| **Token Efficiency**  | Loop-level optimization, ~70% saved, higher accuracy | No                | No            | No            | No           |
| **Model Choice**      | Claude, GPT, OpenRouter, Ollama | Built-in + custom | Claude only | MS-managed    | Any endpoint   |
| **Cost**              | BYOK (tokens only)            | $20–$40/mo      | $20–$100/mo   | $19–$39/mo    | Free (BYOK)    |
| **License**           | **MIT**                       | Proprietary     | Proprietary   | Proprietary   | Apache-2.0     |

Ogcode is the only agentic coding assistant that combines a **browser-native UI** (works with Vim, Emacs, VS Code, JetBrains, or any editor), a **formal Plan Mode** with a visual Kanban board and parallel execution that raises conflict-free PRs directly against your upstream GitHub repo, **git-native parallel execution** that gives every task its own isolated worktree branch with auto-commits and auto-PRs, a **persistent knowledge graph** for long-term memory, **loop-level token optimization** that keeps long-session token use over 70% lower than naive replay *and* sharpens per-turn accuracy, **context engineering** that lets lower-end models match or beat frontier models on clean context, and **single-binary self-hosting** with zero cloud dependencies.

---

## Features

- **Build Mode + Plan Mode** — Chat with a coding agent in real time, or collaboratively plan complex features with effort estimates and dependency graphs, then let Ogcode decompose them into parallel tasks and raise the PRs.
- **Token-Efficient by Design** — Token optimization is built into the agent loop: the knowledge graph recalls only relevant context per turn, and stale history is compacted instead of re-sent — saving 70%+ of tokens on long sessions *and* sharpening accuracy by keeping the context window focused on the task at hand.
- **Parallel Task Execution** — Independent tasks run simultaneously across isolated git worktree branches and open pull requests directly against your upstream GitHub repo. Ship entire features in parallel, with conflict-free PRs by construction.
- **Agentic Session Memory** — Infinite context via a persistent knowledge graph, with ~70% token savings on long sessions.
- **Knowledge Graph** — Semantic memory of your codebase (Topic → Concept → Fact) that persists across sessions.
- **Multi-Provider LLM Support** — Anthropic Claude, OpenAI GPT, OpenRouter, or local Ollama models. Switch anytime from the UI.
- **Deep Research Agent** — A built-in `deep_search` tool searches the web, fetches pages, and synthesizes cited research for your agent.
- **Kanban Board** — Visual task board with S/M/L/XL effort estimates, complexity scores, and dependency chains.
- **Permission-Based Safety** — Destructive operations (write, edit, bash) require explicit approval per tool; read-only tools auto-approve.
- **PDF Support** — Read and index PDF documentation directly in the agent.
- **Codebase Map** — A semantic index of your entire project for intelligent file discovery.
- **File Map** — Tree-sitter parses a source file into an outline of its declarations with line ranges, so the agent reads the 40 lines it needs instead of the whole 600-line file.
- **Single Binary, Self-Contained** — One Go binary with an embedded SolidJS frontend. Zero external server dependencies.
- **Rich Visual Output** — Agents render Mermaid diagrams, LaTeX math, Plotly charts, and full HTML/CSS/JS interactive content directly in the chat, with viewport-aware responsive design.

---

## Table of Contents

- [Context Engineering — the real differentiator](#context-engineering--the-real-differentiator)
- [Infinite Context — never hit a model's limit](#infinite-context--never-hit-a-models-limit)
- [Plan Mode + Parallel PRs](#plan-mode--parallel-prs--ship-features-not-just-suggestions)
- [Quick Start](#quick-start)
- [Why Ogcode](#why-ogcode)
- [Token Efficiency](#token-efficiency)
- [System Requirements](#system-requirements)
- [Installation](#installation)
- [Configuration](#configuration)
- [Usage](#usage)
- [Remote Deployment](#remote-deployment)
- [Agentic Session Memory](#agentic-session-memory)
- [Architecture](#architecture)
- [Roadmap](#roadmap)
- [Community](#community)
- [Contributing](#contributing)
- [Security](#security)
- [License](#license)

---

## Quick Start

```bash
# macOS / Linux — one-line install
curl -fsSL http://ogcode.xyz/install.sh | sh

# Set your API key (or use Ollama for local models)
export ANTHROPIC_API_KEY=sk-ant-...

# Start coding
ogcode
```

Opens at `http://localhost:9595`. That's it — no config files, no Docker, no IDE extension.

---

## Why Ogcode

**Cursor** is great, but it's a fork of VS Code. If you use Vim, Emacs, or JetBrains, you're out of luck — and its parallel agents run in the cloud, not on your machine.

**Claude Code** is powerful, but it's terminal-only, Anthropic-only, and cloud-only. No web UI, no plan mode, no persistent knowledge graph.

**GitHub Copilot** is everywhere, but it's a Microsoft service. Your code analysis happens in the cloud, with no parallel execution and no formal planning.

**Aider** is excellent, but it's terminal-only and sequential, with no persistent memory graph or visual planning board.

Ogcode gives you what none of the above do:

- **Context engineering** — only the relevant context is sent per turn, so tokens fall *and* accuracy rises; smaller models can match or beat frontier models on clean context
- A **web UI** that works with *any* editor
- **Formal Plan Mode** with a visual Kanban board, parallel task decomposition, and auto-PRs raised directly against your upstream GitHub repo
- A **persistent knowledge graph** that survives across sessions
- **Single-binary self-hosting** with zero cloud dependencies
- **Full model freedom** — Claude today, GPT tomorrow, local Llama next week

---

## Token Efficiency

> Nobody else is thinking about token optimization. They burn tokens like water — Claude Code, OpenCode, GitHub Copilot, every coding agent out there — because none of them are designed, at the core loop level, to actually conserve tokens. And because they resend the whole transcript every turn, they also hand the model a noisier, less accurate context window.

As the cost of using frontier AI climbs with every intelligence leap, tokens are becoming a budgeted resource. In the near future, a team — or a solo developer — will have a fixed monthly token allowance and have to ship software, features, and fixes within it. Ogcode is built for that future: token efficiency is designed into the **agent loop itself**, not bolted on after the fact — and it doubles as an **accuracy** win, because the model reasons over a curated, on-point context window instead of a wall of stale chat.

### How Ogcode saves tokens

| Mechanism | What it does | Token impact |
| --------- | ------------ | ------------ |
| **Agentic Session Memory** | Replaces "send the whole conversation every turn" with a knowledge graph that returns only the facts relevant to the current query. | Largest single saving — grows with session length |
| **Context compaction** | Summarizes stale history instead of replaying it verbatim, with truncation as a fallback. | Caps prompt size on long sessions |
| **Targeted `memory_recall`** | The agent retrieves precise historical facts (config values, past decisions) rather than re-deriving them by re-reading code. | Fewer exploration turns |
| **`file_map` ranged reads** | Tree-sitter outlines a file so the agent reads one declaration's line range instead of the entire file. Reading a file over 200 lines without a range returns the map instead. | A map costs 5–11% of the file it describes |
| **`check_syntax` after edits** | The same tree-sitter parse validates a file the moment it is written, so a dropped brace surfaces immediately instead of after several more edits land on top of it. | Removes a whole broken-build recovery loop |
| **Compact tool output** | Structural results render as indented text rather than JSON, which spends ~1.4x its own content on braces, quotes and one-label-per-line arrays. | 53% on every `codebase_map` call |

### Real session results

In real session testing, Ogcode saves **over 70% of tokens** on long-running sessions versus a naive full-replay loop — meaning a fixed monthly budget goes further, a team stays under its limit, and frontier-model cost increases hurt less. And because the model sees only the relevant facts for the current turn rather than the entire transcript, **task accuracy improves at the same time**: less drift, fewer half-remembered earlier decisions, more on-target results.

| Session Length | Traditional (full replay) | With Ogcode memory | Savings |
| -------------- | ------------------------ | ------------------ | ------- |
| 50 messages    | ~25K tokens              | ~8K tokens         | **68%** |
| 200 messages   | ~100K tokens             | ~28K tokens        | **72%** |
| 1000 messages  | ~500K tokens             | ~120K tokens       | **76%** |

Enable it with:

```bash
export OGCODE_AGENTIC_MEMORY_MODE=true
```

See [Agentic Session Memory](#agentic-session-memory) for the technical deep dive.

### File Map — read the range, not the file

Reading a 600-line file to look at one 40-line function puts the other 560 lines in the context window for the rest of the turn — and they are re-sent on every step that follows. The `file_map` tool parses a file with [tree-sitter](https://tree-sitter.github.io/) and returns an outline of its declarations with line ranges, so the agent reads only the part it needs:

```
internal/tool/glob.go — go, 98 lines
jump to any range with: read(path, start_line=N, end_line=M)

    1  package tool
 3-10  import block
   12  type GlobTool struct{}
   14  func (GlobTool) ID() string
   15  func (GlobTool) Description() string
16-24  func (GlobTool) Parameters() json.RawMessage
26-97  func (GlobTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error)
```

Each range already includes the declaration's doc comment, and ranges feed straight into `read(path, start_line, end_line)` with no arithmetic. Nested entries are indented, so a class's methods — or the handlers inside a component that is one large arrow function — are individually reachable rather than buried in a single opaque range.

The map is parsed fresh on every call and never indexed, so its line numbers always describe the file as it is right now. Reading a file longer than 200 lines without a range returns its map instead of its contents, so a whole file cannot land in context by accident.

| File | Full file | Map | Cost of the map |
| ---- | --------- | --- | --------------- |
| `internal/tool/bash.go` | 3.8 KB | 408 B | 10.8% |
| `internal/server/server.go` | 23 KB | 1.1 KB | 4.9% |
| `web/src/context/session.tsx` | 38 KB | 3.5 KB | 9.1% |
| `internal/agent/loop.go` | 111 KB | 8.3 KB | 7.5% |

#### Language support

Ten tree-sitter grammars give full parsing across twenty file extensions:

| Language | Extensions | Grammar |
| -------- | ---------- | ------- |
| Go | `.go` | `tree-sitter-go` |
| TypeScript | `.ts` `.mts` `.cts` | `tree-sitter-typescript` |
| TSX · JSX · JavaScript | `.tsx` `.jsx` `.js` `.mjs` `.cjs` | `tree-sitter-typescript` (TSX parser) |
| PHP | `.php` `.phtml` | `tree-sitter-php` |
| Python | `.py` `.pyi` `.pyw` | `tree-sitter-python` |
| Rust | `.rs` | `tree-sitter-rust` |
| Swift | `.swift` | `tree-sitter-swift` (vendored) |
| Java | `.java` | `tree-sitter-java` |
| C# | `.cs` `.csx` | `tree-sitter-c-sharp` |
| Dart | `.dart` | `tree-sitter-dart` (community) |

TypeScript is a syntactic superset of JavaScript and the TSX parser adds JSX on top, so plain `.js` and `.jsx` files — including `.js` files that carry JSX — parse correctly without a grammar of their own.

PHP ships two parsers and ogcode registers the one that reads a file as text opening into code at `<?php`, so templates that return to HTML between blocks map the same way a plain `.php` file does. Classes, interfaces, traits and enums are outlined with their methods nested underneath — including the `public` / `protected` / `private` forms that make up most of a real PHP codebase.

Python needs more than a grammar and a query. It documents a declaration from the inside, with a docstring at the top of the body rather than a comment above it, so docstrings are read directly and collapsed to their summary sentence. It also hangs decorators above the declaration they modify, so a symbol's range is widened back up over them — `@property` changes what a method is, and a route decorator is the only place a URL appears. Module-level bindings earn a line of their own, because a settings or constants module is nothing else.

Rust states an attribute beside the item rather than around it, so a `#[derive(...)]` line is a sibling that sits between the doc comment and the declaration. Ranges are widened back up over the attributes and the doc is read from above them; `///` and `//!` markers are stripped. `impl` blocks are listed with their methods nested underneath and are named for the type they are written for, so a trait impl and an inherent impl on the same type answer to the same name. A trait's required methods carry no body, and that signature is the point of the trait, so they earn a line beside the defaulted ones.

Swift needs no such accommodation — it parses `@objc public final` into a `modifiers` node inside the declaration, so ranges and signatures cover attributes for free. `class`, `struct`, `enum`, `actor` and `extension` are one grammar node told apart by a `declaration_kind` token, matched structurally so each keeps its own kind. Extensions are listed as their own entry with their methods nested underneath, and a protocol's requirements — methods, property requirements and `associatedtype` alike — are all listed, because a Swift protocol is often mostly `var x: T { get }`.

Swift's grammar is the one entry vendored into the tree rather than required through `go.mod`: upstream generates its parser at build time and gitignores it, publishes no tagged versions, and ships a Go binding that `#include`s a file no commit contains. See [internal/codemap/grammars/swift](internal/codemap/grammars/swift/README.md).

Java is the same shape as Swift and needs nothing beyond the two comment kinds — its annotations also sit in a `modifiers` node inside the declaration, so `@Entity public final class` renders whole. Classes, interfaces, enums, records and annotation types are all outlined, with nested types kept because Java leans on the shape so heavily that a Builder is the canonical case. An enum states its methods inside a nested `enum_body_declarations` rather than directly in its body, so those need a pattern of their own.

C# is the same shape again — `[Serializable] public` parses into an `attribute_list` inside the declaration — but it needs two calls the others do not. Its types sit at either of two depths depending on which namespace form the file uses: `namespace X { }` nests them in a `declaration_list`, while the file-scoped `namespace X;` leaves them as siblings at the top of the compilation unit. Both are matched, through the same patterns rather than through anchored copies, which is what keeps a block-namespace file from listing every type twice. And properties are listed where Java's fields are not: C# deliberately replaced the public field with the property, so a type's properties are its public surface, and dropping them would cost the outline most of the API. Top-level statements are outlined too, so a modern `Program.cs` with no enclosing class maps to more than its `using` directives.

C# is also the first grammar whose doc comments are markup rather than prose. `/// <summary>` would otherwise spend the excerpt budget on tags — the common block form leads with a bare `<summary>` that says nothing — so the summary element is taken, the `<param>` and `<returns>` blocks that duplicate the signature are dropped, inline `<see cref="T:Acme.Card"/>` and `<paramref name="amount"/>` are unwrapped to the names they stand for, and XML entities are decoded.

Dart is the one grammar that needed machinery none of the others did. It states a function's body *beside* its signature rather than inside it — a top-level function parses as a `function_signature` followed by a sibling `function_body`, and a method as a `method_signature` followed by that same sibling — so a range built from the captured node alone would stop at the closing parenthesis and hand a reader a signature with no code under it. A `trailingBodyKind` on the language carries the end of the range down over the body, mirroring the `attrKind` that carries the start up over Rust's attributes; every other grammar leaves it empty, which a test pins. Dart needs both halves: `@override` on a class member is a preceding sibling too.

Dart also splits its comments by role rather than by shape — `///` and `/** */` parse as `documentation_comment` and everything else as `comment` — so both kinds are listed, or a declaration documented with a plain `//` would lose it. Getters and setters keep distinct kinds, because `get` and `set` on one name are two declarations and calling both "method" would read as a duplicate. Mixins, extensions, enhanced enums and all three constructor spellings are outlined; fields, enum constants and top-level `const`/`final` are not — the last of those because the grammar gives them no node to capture, parsing `const int max = 3;` into three loose siblings of the program rather than one declaration.

Dart's grammar is the one dependency without tagged releases: it is community-maintained outside the tree-sitter organisation and is pinned to a commit pseudo-version.

Every other file type falls back to a heuristic scanner that recognises the declaration forms common across languages — `function`, `def` and `fn`, `class` and `struct`, shell functions — plus Markdown headings. Its ranges are approximate — each declaration ends where the next one begins — but no file is left unmapped.

---

## System Requirements

| Platform             | Minimum                                   |
| -------------------- | ----------------------------------------- |
| **Operating System** | macOS, Linux, or Windows                  |
| **Go**               | 1.22+ (for `go install` only)             |
| **Git**              | 2.34+ (required for worktree support)     |
| **CPU**              | Any modern x86_64 or arm64 processor      |
| **Memory**           | 512 MB free RAM                           |

An LLM API key or a local Ollama installation is required. See [Configuration](#configuration).

---

## Installation

### macOS / Linux

Via Homebrew (recommended):

```bash
brew tap prasenjeet-symon/tap
brew install ogcode
```

Via curl (one-liner):

```bash
curl -fsSL http://ogcode.xyz/install.sh | sh
```

Auto-detects your platform, downloads the latest release, and installs to `/usr/local/bin`.

### Windows

```powershell
irm http://ogcode.xyz/install.ps1 | iex
```

Downloads the latest release, extracts to `%LOCALAPPDATA%\ogcode`, and adds it to your PATH.

Via winget:

```powershell
winget install prasenjeet-symon.ogcode
```

### Go Install

```bash
go install github.com/prasenjeet-symon/ogcode@latest
```

### Docker

```bash
docker run -p 9595:9595 -v $(pwd):/workspace -w /workspace ghcr.io/prasenjeet-symon/ogcode:latest
```

---

## Configuration

Ogcode auto-detects available AI providers from environment variables. No config files required.

### AI Provider

Set at least one API key (or use Ollama):

| Variable             | Provider                  |
| -------------------- | ------------------------- |
| `ANTHROPIC_API_KEY`  | Anthropic (Claude)        |
| `OPENAI_API_KEY`     | OpenAI (GPT)              |
| `OPENROUTER_API_KEY` | OpenRouter                |
| `OLLAMA_BASE_URL`    | Ollama (local / cloud URL)|

### Ollama (local models — free, private)

```bash
# macOS / Linux — auto-detected if ollama is installed
ollama serve
ogcode

# Or explicit on any OS:
export OLLAMA_BASE_URL=http://localhost:11434/v1
ogcode
```

Available models: `qwen3`, `codellama`, `llama3.1`, `deepseek-coder-v2`, `mistral`, and any model you've pulled.

### Agentic Memory (optional)

Enable infinite-context memory across sessions:

```bash
export OGCODE_AGENTIC_MEMORY_MODE=true
```

### Web Search (optional)

Give your agent the ability to research current documentation:

```bash
export OGCODE_SEARCH_ENABLED=true
```

---

## Usage

### Build Mode (default)

```bash
ogcode
```

Chat with the agent — ask it to read files, write code, run commands, or search the codebase.

### Plan Mode

```bash
ogcode plan
```

Describe what you want to build. The planning agent reads your codebase, discusses the approach, and breaks it into tasks with dependencies and effort estimates. Lock the plan, and the tasks become a Kanban board you can execute in parallel. Completed plans are archived as markdown in `.ogcode/archives/`.

### Custom port

```bash
ogcode -p 3000
ogcode plan -p 3000
```

---

## Remote Deployment

Ogcode is just an HTTP server — host it on a remote machine and reach it from any browser.

### Expose the port

```bash
docker run -p 9595:9595 \
  -v ~/.ogcode:/root/.ogcode \
  -v $(pwd):/workspace -w /workspace \
  ghcr.io/prasenjeet-symon/ogcode:latest
```

Then open `http://<your-server-ip>:9595` from any browser.

### Behind a reverse proxy with HTTPS

```nginx
# nginx config
server {
    listen 443 ssl;
    server_name ogcode.yourdomain.com;

    location / {
        proxy_pass http://127.0.0.1:9595;
        proxy_set_header Upgrade $http_upgrade;     # WebSocket support
        proxy_set_header Connection "upgrade";
    }
}
```

Access via `https://ogcode.yourdomain.com` — encrypted and clean.

### Docker Compose

```yaml
services:
  ogcode:
    image: ghcr.io/prasenjeet-symon/ogcode:latest
    volumes:
      - ~/.ogcode:/root/.ogcode
    ports:
      - "127.0.0.1:9595:9595"   # only localhost — nginx handles public access
    restart: unless-stopped
```

### Security considerations

Ogcode is a coding agent that can execute shell commands, read and write files, and modify your system. **Never expose it to the public internet without authentication.**

| Risk                          | Mitigation                                   |
| ----------------------------- | -------------------------------------------- |
| Anyone can hit port 9595      | Bind to `127.0.0.1` + use a reverse proxy    |
| No auth on the web UI         | Add HTTP Basic Auth in nginx, or use a VPN   |
| Full shell access via the agent | Run in a restricted environment (Docker, VM) |

Recommended approaches:

1. **SSH tunnel** — most secure, zero config:
   ```bash
   # On your laptop:
   ssh -L 9595:localhost:9595 user@your-server
   # Then open http://localhost:9595 in your browser
   ```
2. **nginx + HTTP Basic Auth** — a simple password gate:
   ```bash
   htpasswd -c /etc/nginx/.htpasswd your_username
   ```
3. **Cloudflare Tunnel** — zero open ports; add Cloudflare Access for auth.
4. **VPN (WireGuard / Tailscale)** — a private network with no public exposure.

### Rich output

Agents can produce rich visual content directly in the chat — not just plain text:

| Format               | Syntax                  | Use For                                                  |
| -------------------- | ----------------------- | -------------------------------------------------------- |
| **Mermaid diagrams** | ` ```mermaid `          | Flows, architectures, sequences, ER diagrams             |
| **LaTeX math**       | `$...$` or `$$...$$`     | Mathematical formulas and equations                      |
| **Plotly charts**    | ` ```plotly `           | Bar, line, scatter, pie, heatmap, and more               |
| **Rough diagrams**   | ` ```rough `            | Hand-drawn style 2D diagrams                             |
| **HTML/CSS/JS**      | ` ```html `             | Interactive dashboards, styled tables, animations        |

HTML blocks render in a **sandboxed iframe** — scripts run in isolation with no access to the parent page. The agent is given your viewport dimensions so it can design responsive content that fits your screen.

---

## Agentic Session Memory

![Agentic Memory Demo](assets/agentic_memory.gif)

Traditional assistants send the *entire conversation history* to the LLM every turn — expensive, and quick to hit token limits. Ogcode's **Agentic Session Memory** extracts, stores, and retrieves only the context relevant to each query.

| Benefit              | Impact                                              |
| -------------------- | --------------------------------------------------- |
| **~70% token savings** | Drastically reduced API costs on long sessions    |
| **Infinite context** | No practical limit on session length or codebase size |
| **Higher accuracy**  | Only relevant memories are retrieved per query      |

### How it works

Ogcode maintains a persistent **Topic → Concept → Fact** hierarchy with vector embeddings. This knowledge graph survives across sessions, so your agent remembers your codebase structure, your conventions, and your past decisions.

```
Topic: "Ogcode Authentication"
  └─ Concept: "JWT Middleware"
     └─ Fact: "Token validation lives in internal/auth/jwt.go:47"
     └─ Fact: "Refresh tokens expire after 7 days (config: AUTH_REFRESH_TTL)"
  └─ Concept: "OAuth Flow"
     └─ Fact: "GitHub OAuth uses PKCE, implemented in internal/auth/oauth.go"
```

Enable it with:

```bash
export OGCODE_AGENTIC_MEMORY_MODE=true
```

---

## Architecture

Ogcode is a single Go binary that embeds a SolidJS web UI and runs its own HTTP server.

```
┌─────────────┐     REST + SSE      ┌──────────────┐
│  Web UI     │ ◄────────────────► │  Go Server   │
│  (SolidJS)  │                    │  (port 9595) │
└─────────────┘                    └──────┬───────┘
                                          │
                    ┌─────────────────────┼─────────────────────┐
                    ▼                     ▼                     ▼
            ┌─────────────┐    ┌─────────────┐    ┌──────────────┐
            │ Agent Loop  │    │  SQLite DB  │    │ LLM Provider │
            │ (Claude,    │    │ (workspace  │    │ (Anthropic,  │
            │  GPT, etc.) │    │  + config)  │    │ OpenAI, ...) │
            └─────────────┘    └─────────────┘    └──────────────┘
                    │
                    ▼
            ┌─────────────┐    ┌─────────────┐    ┌──────────────┐
            │  Knowledge  │    │   Call      │    │   Search    │
            │   Graph     │    │   Graph     │    │   Bridge    │
            │  (Memory)   │    │ (Code Rel)  │    │  (Web/JS)   │
            └─────────────┘    └─────────────┘    └──────────────┘
```

| Component         | Responsibility                                                                 |
| ----------------- | ------------------------------------------------------------------------------ |
| **Agent Loop**    | Streaming LLM chat with tool execution (bash, read, write, edit, glob, grep, memory_recall, deep_search) |
| **Session Store** | SQLite database for conversations, plans, tasks, and permissions               |
| **Git Worktrees** | An isolated branch per task, so multiple agents work in parallel               |
| **Knowledge Graph** | Persistent semantic memory with vector embeddings                            |
| **Search Bridge** | Playwright-based headless Chrome for web research                              |

---

## Roadmap

- [ ] **Advanced Task Planning & Parallel Execution** — Enhanced plan decomposition with manual/automatic agent assignment to tasks.
- [ ] **AI Daily Standups** — Voice-enabled meetings where agents report progress and discuss their work.
- [ ] **Ogland Integration** — Connect external services (Slack, Email, Jira) for planning-phase use.
- [ ] **Agentic Deployment** — End-to-end agentic deployment for major cloud providers, starting with AWS.

---

## Community

Join the Ogcode community on Discord to ask questions, share feedback and feature ideas, and stay up to date with releases.

[**Join us on Discord →**](https://discord.gg/JQP9t8y2Zv)

If Ogcode is useful to you, **starring the repo** helps more developers discover it.

---

## Contributing

Contributions are welcome — bug fixes, features, and documentation alike.

1. **Fork** the repository.
2. Create a feature branch: `git checkout -b feature/my-improvement`.
3. Make your changes and add tests where applicable.
4. Submit a pull request.

Please ensure your code follows the existing Go style and passes `go test ./...`.

---

## Security

- **Destructive operations require approval** — The agent cannot write files or run shell commands without your explicit permission. Approve once, or always per tool.
- **Git worktree isolation** — Each task runs in a separate git worktree, preventing accidental contamination of your working branch.
- **No data sent to third parties** — Your codebase is analyzed locally. Only conversation text is sent to your chosen LLM provider.
- **Local-first** — All data (sessions, plans, memory) is stored in local SQLite databases.

For security concerns, please open an issue or reach out on Discord.

---

## License

MIT License — see [LICENSE](LICENSE) for details.

<br/>

<div align="center">

**Made with care by the Ogcode team and contributors.**

[Star on GitHub](https://github.com/prasenjeet-symon/ogcode) · [Discord](https://discord.gg/JQP9t8y2Zv) · [Docs](docs/OUTLINE.md)

</div>
