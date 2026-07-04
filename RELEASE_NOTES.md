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