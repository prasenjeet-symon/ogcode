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