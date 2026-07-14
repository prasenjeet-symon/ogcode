# Release Notes — v0.19.3

## Patch: Task Session Model, Tool-Cancel False-Positive, and Stale-Index Purge

This patch release fixes three bugs found after v0.19.2:

1. **Task agent session showed the wrong model** — When a plan task is
   executed, the backend creates a session with the worktree path as its
   directory, not the main project directory. The frontend
   `selectSession` looked up the authoritative session record via
   `listSessions` filtered by the main project directory, so
   worktree-based task sessions were never found. The UI fell back to a
   stub without a model field, causing `selectedModel` to pick the
   default/enabled model instead of the task's configured model. Fixed
   by falling back to `getSession` by session ID when the session is not
   in the directory-filtered list.

2. **False-positive "tool execution cancelled" on every errored tool
   call** — The `toolCtxCancelled` check read `toolCtx.Err()` *after*
   `toolCancel()` was already called unconditionally as cleanup. Since
   `toolCancel()` always cancels the child context, `toolCtx.Err()` was
   non-nil on every tool execution — even when tools completed normally
   with no user guidance or cancellation. This surfaced a spurious
   "Tool execution cancelled by user mid-loop guidance" error on every
   errored tool call. Fixed by capturing the cancellation state *before*
   the cleanup `toolCancel()` call, so it only reports true when
   `CancelTool` (via the guidance endpoint) actually cancelled the child
   context mid-flight.

3. **Stale index entries for deleted files were never purged** — On
   incremental re-index (non-rebuild), the indexer only added new files
   and skipped already-indexed ones; it never checked whether
   previously-indexed files had been deleted from disk, leaving stale
   entries in `doc_page_index` forever. Added a `purgeDeletedDocs` step
   to `Indexer.Run` that compares currently-indexed doc paths (via new
   `Store.ListDocPaths`) against the fresh filesystem walk and deletes
   entries for any file that no longer exists. Also handles the
   early-return path where all files are gone. Includes tests for
   `ListDocPaths`, `DeleteByDoc`, and the purge logic.

---

# Release Notes — v0.19.2

## Patch: Home Page Headline Update

Updated the home page hero headline from "Build software at the speed of
thought." to **"Where everyone is a software developer."** — a stronger
democratization-focused message that better communicates ogcode's mission of
making software development accessible to everyone. The subheadline was
realigned to match this narrative while preserving the core technical
differentiators (token savings, infinite context).

---

# Release Notes — v0.19.1

## Patch: Guidance Indicator & Deep-Search Context Fixes

This patch release fixes two bugs introduced by the mid-loop guidance work in
v0.19.0:

1. **Guidance indicator leaked across sessions on switch** — When you sent
   mid-loop guidance and then switched sessions while the HTTP request was
   still in flight, the "guidance active" indicator and the local "Guidance
   sent" badge could appear on the *destination* session. The root cause was an
   `await` race: the shared `guidanceActive` signal and the `PromptInput`
   component's `guidanceSent` flag were set after the await resolved, without
   verifying the active session was still the same one that initiated the
   guidance. Fixed by capturing the session ID before the await and guarding
   the state updates, plus a reactive `createEffect` that clears stale badge
   state whenever the active session changes.

2. **Deep-search child session inherited parent `LoopControl`** — Child
   sessions spawned by `deep_search` were receiving the parent's `LoopControl`
   context, causing cancellation signals (e.g. guidance cancellation) to leak
   into the child loop. Fixed by stripping the parent `LoopControl` from the
   deep-search child session context so each loop owns its own control.

---

# Release Notes — v0.19.0

## Instant Mid-Loop Guidance — Stream Cancellation

This minor release makes mid-loop guidance **feel instant**. When you steer an
agent mid-task, ogcode now interrupts the in-flight LLM generation immediately
instead of waiting for the full response to stream in — so the loop acts on your
new instructions right away. It also fixes the "guidance queued, then nothing
happens" hang that occurred on free endpoints that stay connected but emit
nothing.

---

### ⚡ Cancel the LLM Stream (Not Just Tools)

Previously, sending mid-loop guidance cancelled only the currently-running tool
call; the model still had to finish its full generation (sometimes tens of
seconds) before the loop could proceed. Now the guidance handler cancels both
the LLM stream and any running tool, so the loop advances to the next iteration
and drains your guidance without delay.

- **Stream child context** — Each loop iteration derives a per-step child
  context for the LLM stream. `LoopControl` gains `CancelStream` / `CancelAll`
  so the guidance handler can cancel this child independently of the loop
  context — the stream winds down while the loop itself keeps running.
- **No retry, no error** — A guidance-cancelled stream is treated as a normal
  "stop", not a transient failure. The loop simply proceeds to the next
  iteration, injects the guidance into the system prompt, and resumes.
- **Connection hygiene** — Stalled streams are drained in a background goroutine
  so the provider unblocks and releases the underlying HTTP connection. Without
  this, leaked connections accumulate against a rate-limited endpoint's
  concurrency budget and stall the *next* request — the root cause of the
  observed hang.

### 🔗 Cancelled Partial Tool Calls Stay Valid

When the stream is cancelled mid-generation, the model may have already emitted
partial tool-call blocks. These are never executed, but leaving them unpaired
breaks the next API request (both Anthropic and OpenAI 400 on a dangling
`tool_use` without a matching `tool_result`).

- **`cancelPartialToolCalls`** — Marks each partial tool part as cancelled (so
  the UI stops showing it as running) and emits a single paired error
  `tool_result` user message for every cancelled `tool_use`, keeping the
  conversation history valid.
- **Invalid JSON sanitization** — A tool call interrupted mid-arguments leaves
  partial, invalid JSON. This is coerced to a valid empty object `{}` before
  being re-sent, so strict OpenAI-compatible endpoints don't reject or stall the
  resumed request.

### 🔄 Guidance-First Ordering

The guidance handler now pushes the guidance text **before** issuing the
cancellation. Ordering matters: if cancellation happened first, the loop could
wake, drain an empty queue, see the finished stream as a normal "stop", and exit
— silently dropping the guidance that lands a moment later.

### 🖥️ Frontend

- The in-flight cancel checkbox is relabelled from "Cancel tool" to **"Cancel
  current work"** to reflect that both the LLM stream and running tools are now
  interrupted. The tooltip is updated accordingly.

### 🧪 Tests

- `LoopControl` stream-cancel and cancel-all unit tests (including nil-safe and
  double-cancel guards).
- `cancelPartialToolCalls` coverage: tool_use↔tool_result pairing, invalid-JSON
  sanitization, and the vanished-parts (no empty message) edge case.
- A full end-to-end loop test (`TestRunLoop_GuidanceCancelsAndResumes`) that
  reproduces the stalled-stream hang and asserts the loop resumes with guidance
  injected into the resumed system prompt.
- An OpenAI provider test verifying context cancellation promptly closes a
  silent SSE stream's event channel.

---

*Full changelog: https://github.com/prasenjeet-symon/ogcode/compare/v0.18.0...v0.19.0*

---

# Release Notes — v0.18.0

## Mid-Loop Guidance & Model-Switch Popover

This minor release lets you **steer an agent mid-task** — inject a new
instruction into an already-running loop without starting a new turn, and
optionally cancel the in-flight tool call so the agent acts on your guidance
immediately. It also adds a confirmation **popover** when you switch models
with the Alt+1–4 hotkeys and renames the sidebar index to "Project Index".

---

### 🧭 Mid-Loop User Guidance

You can now send additional instructions to an agent **while it is still
working**, within the same user turn. The guidance is delivered at the top of
the next loop iteration as a trailing `<system-reminder>` system-prompt entry.
It is ephemeral — never persisted to the message DB — so it does not interfere
with compaction turn boundaries or agentic-memory prior_context slicing.

- **Side-channel design** — A new `LoopControl` type carries a per-session
  guidance queue and tool-cancel function via context. It is nil-safe and a
  no-op for CLI / search / indexer loops that do not wrap the context.
- **In-flight tool cancellation** — Optionally cancels the currently-running
  tool call so the loop can act on the new guidance immediately instead of
  waiting for the tool to finish. Cancelled tools get a clear
  "cancelled by user mid-loop guidance" result so the call/result pairing stays
  valid.
- **API** — `POST /api/session/{id}/guidance` accepts `content` and `cancelTool`.
  Returns 409 when no loop is running, so the frontend falls back to a normal
  prompt.
- **Frontend** — While a loop is running the prompt textarea stays enabled and
  submitting sends mid-loop guidance (always cancels the current tool). A
  visible send button while running, "Guidance sent" confirmation badge, and
  "Guidance queued" inline indicator are wired to `loop.guidance` SSE events.

### ✨ Model-Switch Popover (Alt+1–4)

Activating a model with the Alt+1–4 hotkey now shows a brief **glass popover**
that pops in, holds, and fades out over ~1.8s, centered near the top of the
screen. It shows the slot number badge, provider color dot, model name, and an
"active" label so you get immediate visual confirmation of the switch.

- Rendered at the app root so it appears on every screen (chat, plan, home,
  settings, etc.).
- The plan screen now also handles Alt+1–4 to switch the plan's own model
  (previously the hotkey only affected the hidden session model on plan
  screens) and reuses the session popup signal.

### 🗂️ UI Polish

- **Project Index rename** — The sidebar entry and page header previously
  labelled "Doc Index" are now renamed to **Project Index**, matching the
  unified index terminology.
- **OpenAI-compatible providers info card** — Removed the base-URL preset
  cards that were no longer accurate, leaving a cleaner info layout.

---

### 📥 Installation

**macOS/Linux:**
```bash
curl -fsSL http://ogcode.xyz/install.sh | sh
```

**Windows:**
```powershell
irm http://ogcode.xyz/install.ps1 | iex
```

**Homebrew:**
```bash
brew install prasenjeet-symon/tap/ogcode
```

**Docker:**
```bash
docker run -p 9595:9595 -v $(pwd):/workspace -w /workspace ghcr.io/prasenjeet-symon/ogcode:latest
```

---

*Full changelog: https://github.com/prasenjeet-symon/ogcode/compare/v0.17.1...v0.18.0*

---

# Release Notes — v0.17.1

## Anthropic Multi-Turn Thinking Fix

This patch release fixes **multi-turn extended thinking** with Anthropic
(Claude) models. When a model produced `thinking` blocks on one turn, those
blocks — and their cryptographic signatures — were silently dropped before the
next API call, which broke multi-turn thinking with an API error. Thinking and
redacted-thinking blocks are now correctly preserved and forwarded.

---

### 🧠 Forward Thinking Blocks on Subsequent Turns

Anthropic requires that `thinking` blocks (with their cryptographic signatures)
be passed back unchanged on subsequent turns — dropping them breaks extended
thinking. Previously, reasoning content was captured and stored but never sent
back to the API.

- **Signature storage** — A `Signature` field was added to `ReasoningPartData` and
  `StreamEvent`, and a new `EventReasoningSignature` stream event type captures
  `signature_delta` events from Anthropic streaming responses so signatures are
  persisted alongside the reasoning text.
- **Cross-provider carry** — A `ReasoningPart` type was added to `ModelMessage`
  for carrying thinking blocks across the provider abstraction. The Anthropic
  provider now emits thinking blocks as content blocks in assistant messages,
  ordered before text/tool_use blocks per the API contract.
- **Unaffected providers** — OpenAI-family providers handle reasoning tokens
  server-side; the `ReasoningParts` field is simply ignored.

### 🔒 Redacted-Thinking Handling

- **`redacted_thinking` blocks** — Anthropic returns `redacted_thinking`
  content-block-start events carrying only a signature with no text deltas.
  Dropping them broke multi-turn thinking. The stream parser now handles these
  events, emitting an empty reasoning event plus a signature event; the
  signature handler persists it to an existing reasoning part so a
  redacted-only block still stores its signature and is forwarded correctly.

### 📏 Reasoning Counted in Request-Size Estimate

- **Proactive compaction** — `estimateRequestSize` now sums
  `ReasoningParts` text and signature lengths. Without this, a thinking-heavy
  history could silently exceed the model context limit before the proactive
  compaction heuristic triggered.
- **Tests** — Added coverage for `redacted_thinking` event parsing,
  redacted-thinking forwarded as a thinking block, and reasoning parts counted
  in the request-size estimate.

---

### 📥 Installation

**macOS/Linux:**
```bash
curl -fsSL http://ogcode.xyz/install.sh | sh
```

**Windows:**
```powershell
irm http://ogcode.xyz/install.ps1 | iex
```

**Homebrew:**
```bash
brew install prasenjeet-symon/tap/ogcode
```

**Docker:**
```bash
docker run -p 9595:9595 -v $(pwd):/workspace -w /workspace ghcr.io/prasenjeet-symon/ogcode:latest
```

---

*Full changelog: https://github.com/prasenjeet-symon/ogcode/compare/v0.17.0...v0.17.1*

---

# Release Notes — v0.17.0

## Model Hotkeys & Dark HTML Output

This minor release adds **Alt+1–4 keyboard shortcuts** for instant model switching and makes HTML code-block output render with a dark background by default.

---

### ⌨️ Model Hotkey Slots (Alt+1–4)

You can now switch models with a single keystroke — no more hunting through the picker mid-conversation.

- **Four hotkey slots** — Alt+1 through Alt+4 each map to a configurable model. The default model fills slot 1 on first launch.
- **Settings UI** — A new "Model Hotkeys" section in Settings → General lets you assign models to each slot using the same ModelSelector popover used elsewhere, keeping the UI consistent.
- **Keyboard shortcuts** — Press Alt+1 / Alt+2 / Alt+3 / Alt+4 (Option on macOS) to switch models instantly. The active model badge updates immediately.
- **Persistent** — Hotkey assignments are saved to the session store and survive page reloads.

### 🌑 Dark-by-Default HTML Output

HTML code blocks (rendered in sandboxed iframes) now default to a dark color scheme, matching the existing dark UI theme.

- **`color-scheme: dark`** — The iframe root element sets `color-scheme: dark`, which activates dark-native form controls, scrollbars, and default text colors without any custom CSS.
- **Backward-compatible** — Existing HTML content that specifies its own colors continues to work exactly as before. The `color-scheme` declaration only affects elements using system defaults.

---

### 📥 Installation

**macOS/Linux:**
```bash
curl -fsSL http://ogcode.xyz/install.sh | sh
```

**Windows:**
```powershell
irm http://ogcode.xyz/install.ps1 | iex
```

**Homebrew:**
```bash
brew install prasenjeet-symon/tap/ogcode
```

**Docker:**
```bash
docker run -p 9595:9595 -v $(pwd):/workspace -w /workspace ghcr.io/prasenjeet-symon/ogcode:latest
```

---

*Full changelog: https://github.com/prasenjeet-symon/ogcode/compare/v0.16.1...v0.17.0*

---

# Release Notes — v0.16.1

## Frontend Stability Fix

This patch release fixes a **UI crash to a blank screen** that could occur when
using Ollama-compatible proxies that emit malformed or incomplete tool-call data.

---

### 🐛 `ToolPartDisplay` Defensive Guards

- **Fix** — The `ToolPartDisplay` SolidJS component no longer throws a `TypeError`
  when the backend creates a tool part without a valid `state` or `tool` property.
  This happened when Ollama proxies sent truncated tool-call payloads, which
  previously aborted the entire render and blanked the screen.
- **Defaults** — A `DEFAULT_TOOL_STATE` fallback and safe accessor functions were
  added; all direct references to `props.data.state` / `props.data.tool` now route
  through these accessors, so missing fields degrade gracefully instead of crashing.
- **No behavior change** for well-formed data — the guards only engage when fields
  are absent or malformed.

---

### 📥 Installation

**macOS/Linux:**
```bash
curl -fsSL http://ogcode.xyz/install.sh | sh
```

**Windows:**
```powershell
irm http://ogcode.xyz/install.ps1 | iex
```

**Homebrew:**
```bash
brew install prasenjeet-symon/tap/ogcode
```

**Docker:**
```bash
docker run -p 9595:9595 -v $(pwd):/workspace -w /workspace ghcr.io/prasenjeet-symon/ogcode:latest
```

---

*Full changelog: https://github.com/prasenjeet-symon/ogcode/compare/v0.16.0...v0.16.1*

---

# Release Notes — v0.16.0

## Zero-Config Free Models

This minor release lets **new users start coding immediately — no API key, no
setup**. On first launch, ogcode fetches a community pool of free,
OpenAI-compatible models and makes them available out of the box, grouped under a
dedicated **"ogcode"** collection with a sensible default already selected.

---

### ⚡ Free Model Pool (zero setup)

ogcode now ships usable the moment it opens — no credentials required.

- **Auto-provisioned providers** — On startup, ogcode fetches a JSON pool of
  OpenAI-compatible free providers (Groq, OpenRouter) from a public URL and
  registers them automatically. They never override your own configured providers.
- **Free models only** — For OpenRouter the list is restricted to its `:free`
  variants, and every free model is enabled by default, so you land ready to chat
  instead of on an empty picker.
- **Default model** — New users start on **North Mini Code**
  (`cohere/north-mini-code:free`), a coding-focused free model, selected by default.
- **Resilient** — The pool is cached locally (24h TTL, atomic writes,
  stale-on-error fallback), so startup never blocks on the network.
- **Keys never exposed** — The `/api/providers/free` endpoint reports available
  free providers with their keys masked.

### 🗂️ "ogcode" Collection

All free-pool models are grouped under a single **ogcode** collection so they stay
separate from your own OpenAI / Anthropic / OpenRouter / Ollama / Groq models. Each
free model is tagged with its underlying provider (e.g. **OpenRouter**, **Groq**)
so you always know where a model comes from.

### 🚪 No Onboarding Required

Because free models work out of the box, new users are no longer forced through the
setup wizard. The onboarding screen stays reachable from Settings for anyone who
wants to add their own provider keys.

### ✨ Model Picker & Settings Polish

- The model-picker dropdown is wider so long model names render cleanly, with a
  per-model provider tag.
- The **Add custom model** form now opens at the top of Settings → Models, visible
  immediately when you click the button.
- **Fix** — The memory-savings popover is now correctly centered on screen (it was
  being clipped by the blurred header) with a visible close button.

---

### 📥 Installation

**macOS/Linux:**
```bash
curl -fsSL http://ogcode.xyz/install.sh | sh
```

**Windows:**
```powershell
irm http://ogcode.xyz/install.ps1 | iex
```

**Homebrew:**
```bash
brew install prasenjeet-symon/tap/ogcode
```

**Docker:**
```bash
docker run -p 9595:9595 -v $(pwd):/workspace -w /workspace ghcr.io/prasenjeet-symon/ogcode:latest
```

---

*Full changelog: https://github.com/prasenjeet-symon/ogcode/compare/v0.15.0...v0.16.0*

---

# Release Notes — v0.15.0

## `view_image` Agent Tool

This minor release adds a new agent tool that lets vision-capable models **see
image files on disk** — logos, screenshots, diagrams, photos — directly within a
task, without the user having to manually upload them.

---

### 🖼️ `view_image` Agent Tool

Agents can now read image files (PNG, JPEG, GIF, BMP, WebP) from the filesystem
and return them to the model for visual inspection. This complements the existing
image upload feature (v0.14.0) by enabling agents to proactively look at images
they discover during exploration.

- **New tool** — `view_image` accepts a file path (absolute or relative to the
  session directory) and returns the image so vision-capable models can see it.
  Large images are automatically downscaled to fit within vision-model limits.
- **Use cases** — Inspecting logos, screenshots, UI mockups, diagrams, or any
  image referenced in a task without requiring the user to attach it manually.
- **Format support** — PNG, JPEG, GIF, BMP, and WebP.

---

### 📥 Installation

**macOS/Linux:**
```bash
curl -fsSL http://ogcode.xyz/install.sh | sh
```

**Windows:**
```powershell
irm http://ogcode.xyz/install.ps1 | iex
```

**Homebrew:**
```bash
brew install prasenjeet-symon/tap/ogcode
```

**Docker:**
```bash
docker run -p 9595:9595 -v $(pwd):/workspace -w /workspace ghcr.io/prasenjeet-symon/ogcode:latest
```

---

*Full changelog: https://github.com/prasenjeet-symon/ogcode/compare/v0.14.0...v0.15.0*

---

# Release Notes — v0.14.0

## Image Uploads, Anthropic Base URL Config, and Product Analytics

This minor release introduces three significant features: **image uploads** for
vision-capable models, a **configurable Anthropic base URL** for proxy/gateway
setups, and **PostHog product analytics** for usage insights.

---

### 🖼️ Image Uploads in Chat

Users can now attach images to chat prompts — either via the file picker button
or by pasting from the clipboard. Images are sent to vision-capable models
(Claude, GPT-4o, etc.) and rendered inline in the message list.

- **Multi-modal messages** — A new `PartImage` type and `ImagePartData` struct
  persist images as first-class message parts. The agent loop attaches user
  image parts to `ModelMessage.Images` so both Anthropic and OpenAI-compatible
  providers (OpenRouter, Ollama) receive them as image content blocks — no
  provider changes were needed.

- **UI integration** — The prompt input bar has a new file picker button,
  paste-from-clipboard support, image preview thumbnails with per-image remove
  buttons, a 10 MB size limit, and file-type validation. Image parts render
  inline in user messages with click-to-expand.

---

### 🔗 Configurable Anthropic Base URL

The Anthropic provider now supports a custom base URL, mirroring the existing
OpenAI/Ollama configuration. This enables use of Anthropic-compatible proxies,
gateways, or self-hosted endpoints.

- **Resolution priority** — UI/DB setting → `ANTHROPIC_BASE_URL` environment
  variable → `https://api.anthropic.com/v1` default.
- **Settings UI** — The field appears in Settings → Models → API Keys and in the
  onboarding wizard, with an Anthropic-specific placeholder and a "set via env"
  indicator.
- **Provider wiring** — `AnthropicProvider` gains a `baseURL` field;
  `StreamChat` uses it. `NewProviderWithConfig` applies it for the `anthropic`
  case. Tests neutralise the env var for deterministic runs.

---

### 📊 PostHog Product Analytics

PostHog cloud analytics is now integrated for internal product insights —
capturing page views, session recordings, and custom events from the web UI,
plus server-side lifecycle events from the Go backend.

- **Server-side events** — A lightweight PostHog client
  (`internal/server/posthog.go`) sends server lifecycle events
  (`ogcode_server_started`, `ogcode_server_stopped`) to the PostHog `/capture`
  REST endpoint via a bounded background worker — non-blocking, always-on.
- **Frontend SDK** — The `posthog-js` library is lazily initialised on app load
  with page view capture and session recording enabled (autocapture disabled to
  avoid noise).
- **Hardcoded credentials** — Analytics uses hardcoded project credentials
  (not user-configurable); it is an internal feature, not a user-facing setting.

---

### 📥 Installation

**macOS/Linux:**
```bash
curl -fsSL http://ogcode.xyz/install.sh | sh
```

**Windows:**
```powershell
irm http://ogcode.xyz/install.ps1 | iex
```

**Homebrew:**
```bash
brew install prasenjeet-symon/tap/ogcode
```

**Docker:**
```bash
docker run -p 9595:9595 -v $(pwd):/workspace -w /workspace ghcr.io/prasenjeet-symon/ogcode:latest
```

---

*Full changelog: https://github.com/prasenjeet-symon/ogcode/compare/v0.13.7...v0.14.0*

---

# Release Notes — v0.13.7

## Anthropic Prompt Caching

This release introduces explicit prompt caching for Anthropic models (Claude
Sonnet, Haiku, Opus) to reduce latency and token costs on multi-turn
conversations. The system prompt prefix is now cached across turns so that
repeated tool definitions and base instructions are read from cache instead of
re-processed every call.

---

### ⚡ Anthropic Prompt Caching

- **Explicit `cache_control` breakpoints** — The Anthropic provider now sends
  the `system` field as an array of content blocks (required to attach
  `cache_control` markers) instead of a plain string. Two cache breakpoints are
  placed: one on the last tool definition (caches the entire tool block) and
  one on the first static system block (caches tools + base system prompt).

- **Static/dynamic system prompt separation** — The current date was extracted
  from the base system prompt and injected as a separate dynamic
  `<system-reminder>` entry. Only the static prefix receives `cache_control`;
  dynamic trailing blocks (date, compaction summaries) do not. This keeps the
  cacheable prefix byte-for-byte identical across turns.

- **Token usage tracking** — The provider reads `cache_creation_input_tokens`
  and `cache_read_input_tokens` from the Anthropic response and surfaces them
  in `TokenUsage`, so callers can observe cache hits and misses.

- **Other providers unaffected** — OpenAI (automatic prefix caching), Ollama,
  and OpenRouter remain functionally identical. The static/dynamic split is
  recombined into a single string for OpenAI-compatible providers.

- **Tests** — New test cases in `anthropic_test.go` verify cache breakpoint
  placement, system block splitting, and token usage parsing. New tests in
  `prompt_builder_test.go` verify the separation of static and dynamic system
  prompt content.

---

### 📥 Installation

**macOS/Linux:**
```bash
curl -fsSL http://ogcode.xyz/install.sh | sh
```

**Windows:**
```powershell
irm http://ogcode.xyz/install.ps1 | iex
```

---

# Release Notes — v0.13.6

## DOCX Indexing & Unified Project Index

This release adds full DOCX (Word document) support to the document indexing
pipeline and unifies PDFs into the project index tree, so agents can discover
and read both PDFs and DOCX files from a single `codebase_map` call.

---

### 📄 DOCX Indexing Support

Word documents (`.docx`) are now first-class citizens in the indexing system,
with the same level of support as PDFs:

- **Extraction pipeline** — A new `internal/docx` package parses DOCX files,
  handling paragraph properties, tables, hyperlinks, structured document tags,
  and explicit page breaks. Documents without explicit breaks are split into
  pseudo-pages (~500 words each) for consistent indexing.

- **Agent tools** — Two new tools are available to agents:
  - `docx_index` — Returns the semantic page labels for a DOCX file, just like
    `pdf_index` does for PDFs.
  - `read_docx_page` — Extracts the plain text of a single pseudo-page from a
    DOCX file, similar to `read_pdf_page`.

- **Automatic indexing** — DOCX files are detected during directory walks and
  processed in their own batch alongside PDFs. The docindex UI shows DOCX files
  with a distinct blue badge and document icon.

- **Project index** — `codebase_map` now includes DOCX files in the unified
  project tree, showing their semantic labels alongside text and code files.

- **10 test cases** covering real-world DOCX structures including tables,
  hyperlinks, nested content, and mixed page-break scenarios.

### 🗂️ Unified Project Index with PDFs

PDFs are now part of the `codebase_map` project tree instead of being separate:

- PDF entries appear as leaves with up to 15 de-duplicated topic labels — enough
  to understand what a document covers without overwhelming the agent.
- Per-page detail remains available via the dedicated `pdf_index` tool.
- The `pdf_index` tool now returns only semantic labels (keyword corpora are no
  longer exposed to agents — they were raw indexing artifacts).

---

### 📥 Installation

**macOS/Linux:**
```bash
curl -fsSL http://ogcode.xyz/install.sh | sh
```

**Windows:**
```powershell
irm http://ogcode.xyz/install.ps1 | iex
```

**Homebrew:**
```bash
brew install prasenjeet-symon/tap/ogcode
```

**Docker:**
```bash
docker run -p 9595:9595 -v $(pwd):/workspace -w /workspace ghcr.io/prasenjeet-symon/ogcode:latest
```

---

*Full changelog: https://github.com/prasenjeet-symon/ogcode/compare/v0.13.5...v0.13.6*