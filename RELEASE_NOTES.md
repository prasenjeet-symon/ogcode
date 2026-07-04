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