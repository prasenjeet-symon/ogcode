# 🚀 Ogcode v0.7.0 Release

## 📋 Executive Summary

This release adds **codebase-wide source code indexing** — the indexer now walks 30+ file types, not just PDFs — alongside an **exclude patterns** system for fine-grained control over what gets indexed, and a new **`codebase_map` tool** that lets agents navigate your project by topic labels instead of blind file searches.

---

## 📝 Changes Summary

### ✨ New Features

- **Source code indexing** — The document indexer now scans and labels text/code files (`.go`, `.ts`, `.py`, `.rs`, `.java`, `.cpp`, `.yaml`, `.sql`, etc.) in addition to PDFs. Each file is treated as a single page, extracted and keyword-labelled through the existing IndexAgent pipeline.
- **Exclude patterns** — New `index_excludes` table and CRUD API for managing skip patterns (e.g. `node_modules`, `vendor`, `*.min.js`). Defaults are seeded automatically on first index.
- **`codebase_map` tool** — Agents can now call `codebase_map` to get a labeled JSON tree of all indexed files, optionally scoped to a subdirectory. Both Build and Plan agents are instructed to use it as the first exploration step.
- **Doc Index UI redesign** — Collapsible folder tree, search/filter, and file type badges replace the flat file list.
- **Excludes management modal** — Add, list, and remove skip patterns from the web UI.

### 🔧 Improvements

- **camelCase keyword splitting** — Identifiers like `getUserName` are decomposed into `get`, `user`, `name` for richer source code indexing.
- **Skip already-indexed docs** — Re-indexing a project only processes new or changed files.
- **Index sessions hidden from UI** — `session_type = 'index'` sessions are filtered from the session list.

### 🗃️ Database Migration

- **`024_index_excludes.sql`** — Creates the `index_excludes` table.

---

## 📥 Installation

**macOS/Linux:**
```bash
curl -fsSL https://ogcode.xyz/install.sh | sh
```

**Windows:**
```powershell
irm https://ogcode.xyz/install.ps1 | iex
```

**Homebrew:**
```bash
brew install prasenjeet-symon/tap/ogcode
```

**Winget:**
```powershell
winget install prasenjeet-symon.ogcode
```

**Go Install:**
```bash
go install github.com/prasenjeet-symon/ogcode@latest
```

---

*Full changelog: https://github.com/prasenjeet-symon/ogcode/compare/v0.6.0...v0.7.0*