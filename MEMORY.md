# Project Long-Term Memory

## Tools

### Discord Notifications

- **Webhook URL**: stored in `~/.zshrc` as `WEBHOOK_URL` inside the `discord()` function
- **How to send messages**: use the `discord` shell function (or aliases) from zshrc

#### Commands

| Command | Description |
|---------|-------------|
| `discord 'message text'` | Send plain text |
| `discord --code 'code here'` | Send as a code block |
| `discord --lang python 'print()'` | Send as code with language |
| `discord --ping '@username' 'message'` | Ping a user + message |
| `discord --here 'Alert!'` | @here mention |
| `discord --everyone 'Announcement!'` | @everyone mention |

#### Aliases

| Alias | Expands to |
|-------|-----------|
| `dmsg` | `discord` |
| `dcode` | `discord --code` |
| `dpy` | `discord --lang python` |
| `djs` | `discord --lang javascript` |
| `dbash` | `discord --lang bash` |

#### Scripting (non-interactive)

For CI/CD or scripts, post directly with `curl`:

```bash
curl -X POST -H "Content-Type: application/json" \
  -d '{"content":"Your message here"}' \
  "$DISCORD_WEBHOOK_URL"
```

HTTP 204 = success (no content response from Discord).

### dblm — "Talk to your databases in plain English"

- **Installed at**: `/Users/admin/go/bin/dblm` (Go binary)
- **Config file**: `~/.dblm/config.json`
- **Purpose**: CLI tool that connects to databases (Postgres, MySQL, SQLite, MongoDB, Redis, ClickHouse, etc.) and lets you query them using natural language.

#### Key Commands

| Command   | Description                                    |
|-----------|------------------------------------------------|
| `connect` | Manage database connections                    |
| `query`   | Ask a question in natural language             |
| `schema`  | Inspect database schemas                       |
| `session`  | Manage conversation sessions                  |
| `history` | Search and manage query history                |
| `index`   | Manage schema indexes                          |
| `knowledge` | Manage schema knowledge documentation        |
| `module`  | Manage table modules (cross-database queries)   |
| `fassad`  | Manage reusable parameterized query templates  |
| `server`  | Run the dblm broker (master)                   |
| `remote`  | Talk to remote dblm broker(s) (worker mode)    |

## Release Process

End-to-end workflow: **build locally → bump version → commit → push tag → pipelines auto-release**. The CI pipelines on GitHub Actions handle cross-platform binaries (GoReleaser) and the Docker image automatically once a tag is pushed — you do not build release binaries locally.

### Build (local)

1. **Web UI** — `npm run build` (Vite bundles ~2,308 modules; required before the Go compile because the Go binary embeds the built assets).
2. **Go binary** — `go build -o ./ogcode`.
3. **Install** — `go install` or copy the binary to `~/.local/bin/ogcode`.
4. **Search bridge** — Playwright + Chromium installs to `~/.local/share/ogcode/search-bridge/`.

### Bump version

The version is hardcoded in **four files (six entries) that must stay in sync**. GoReleaser overrides the Go files via `-ldflags` for official releases, but local/dev builds fall back to these defaults, and the npm files always need to match:

| # | File | Entry | Format | Example |
|---|------|-------|--------|---------|
| 1 | `internal/version/version.go` | `Version` var | `vX.Y.Z` (with `v` prefix) | `Version = "v0.13.3"` |
| 2 | `internal/cli/version.go` | `version` var | `vX.Y.Z` (with `v` prefix) | `version = "v0.13.3"` |
| 3 | `web/package.json` | `"version"` field | `X.Y.Z` (**no** `v` prefix) | `"version": "0.13.3"` |
| 4a | `web/package-lock.json` | root `"version"` | `X.Y.Z` (**no** `v` prefix) | `"version": "0.13.3"` |
| 4b | `web/package-lock.json` | `packages[""].version` | `X.Y.Z` (**no** `v` prefix) | `"version": "0.13.3"` |

> **Gotcha**: The Go files use a `v` prefix; the npm files do **not**. Forgetting the npm files (or mixing the prefix) is the most common release mistake — always bump all six entries.

Files that **do NOT** need bumping:
- `.goreleaser.yaml` — uses GoReleaser's `{{ .Version }}` template, no hardcoded version.
- `tools/search-bridge/package.json` — pinned to `1.0.0`, does not track the project version.

Steps:
1. Decide the new version (e.g. `v0.13.3`).
2. Edit **all four files** above (six entries total), updating each version string to the new tag (mind the `v` prefix difference).
3. Update `RELEASE_NOTES.md` with the new version's changelog.
4. `go build -o ./ogcode` to verify the Go side compiles.
5. Commit these changes.

### Release

1. **Commit & push to `main`** — CI (`.github/workflows/ci.yml`) runs tests and must pass first.
2. **Create & push a git tag** — `git tag vX.Y.Z && git push origin vX.Y.Z`.
3. **Release pipeline auto-triggers** — `.github/workflows/release.yml` (GoReleaser) builds binaries for all platforms and creates the GitHub release.
4. **Docker pipeline auto-triggers** — `.github/workflows/docker.yml` builds and pushes the Docker image.
5. **Post-release** — update the "Current release" line below to the new version so future sessions know it without git inspection.

**Current release**: `v0.19.2`

## ogcode — Rich Output Architecture

- **Supported formats**: Mermaid diagrams, LaTeX math, LaTeX documents, Plotly charts, Rough diagrams, HTML/CSS/JS (sandboxed iframe)
- **HTML rendering**: `language-html` code blocks are extracted before DOMPurify runs, encoded as base64 data attributes, then rendered in sandboxed iframes with `allow-scripts` and `allow-same-origin`. The iframe has a **transparent background with no border** — HTML content blends seamlessly into the chat. The system prompt instructs agents NOT to add background colors or card-like containers to their HTML output.
- **LaTeX document rendering**: `language-latex` code blocks are rendered as styled previews with document class/title extraction, source code preview, and a "Download PDF" button that calls `POST /api/latex` to compile via pdflatex
- **LaTeX-to-PDF tool**: `latex_to_pdf` agent tool compiles LaTeX source to PDF using system pdflatex, saves to session directory, and renders first page as JPEG for vision-capable models
- **LaTeX API routes**: `POST /api/latex` (compile to PDF), `POST /api/latex/pages` (compile + render page images), `GET /api/latex/status` (check pdflatex availability + version info)
- **LaTeX environment detection**: `getLatexEnv()` in `internal/agent/prompt_builder.go` detects pdflatex version, distribution, available document classes, and packages via `kpsewhich`. Injected into system prompt as "LaTeX environment" section for agents with the `latex_to_pdf` tool, so they write compatible LaTeX
- **Viewport-aware design**: Browser sends `viewportWidth` and `viewportHeight` with each prompt. Backend injects a "Rendering viewport" section into the system prompt so agents can design responsive content
- **Data flow**: Frontend (`window.innerWidth/Height`) → API request body → `handlePrompt`/`handlePlanPrompt` → `RunLoop(viewportWidth, viewportHeight)` → `buildSystemPrompt()` → `viewportPrompt()` appended to system prompt
- **DOMPurify config**: `{ USE_PROFILES: { html: true, svg: true }, ADD_ATTR: ['style', 'aria-hidden', 'data-srcdoc-idx'] }` — allows safe HTML tags through, but scripts are extracted for iframe rendering before sanitization

## Agentic Memory — Inbuilt Embedder

- **What**: A pure-Go sentence-embedding provider (`all-MiniLM-L6-v2`, 384-dim) runs in-process so agentic memory needs **no API key and no external service**. Selected as provider id `"local"` (the default in the settings UI).
- **Key files**: `internal/provider/local.go` (`LocalEmbedder`), `internal/provider/embedmodel/` (embedded tokenizer + download constants), `provider.ResolveEmbedProvider` (factory).
- **Backend**: Hugot (`github.com/knights-analytics/hugot`) pure-Go `NewGoSession` (GoMLX simplego) — CGO-free. Inference is **mutex-serialized** because the GoMLX backend is not goroutine-safe.
- **Model delivery (important)**: Only the ~700 KB tokenizer/config assets are `go:embed`-ed. The ~86 MB ONNX weights are **downloaded on first use** from `https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2/resolve/main/onnx/model.onnx` to `~/.ogcode/embed-model/`, SHA-256 verified (`6fd5d72fe4589f189f8ebc006442dbb529bb7ce38f8082112682524616046452`). A `.ogcode-model.sha256` sidecar marker skips re-download + re-hash on later runs. This mirrors the search-bridge download pattern and keeps the binary ~55 MB (vs 141 MB if the weights were embedded).
- **Why not quantized int8**: The ~22 MB quantized ONNX variant was rejected because Hugot's pure-Go backend only has partial/experimental int8 (`QuantizeLinear`/`DequantizeLinear`) support — too risky for inference correctness. The FP32 model is guaranteed to work.
- **Migration note**: Switching an existing memory DB from OpenAI (1536-dim) to the local embedder (384-dim) changes vector dimensionality. Existing stored embeddings become incompatible — run `Memory.RefreshAll` (re-embeds all docs) after switching. The cosine code in `graph.go` already guards against dimension mismatch (skips mismatched vectors).
- **Env overrides**: `OGCODE_EMBED_MODEL_DIR` sets the model cache dir.

## Prompt Caching

- **Anthropic**: Prompt caching is enabled via explicit `cache_control: {"type": "ephemeral"}` breakpoints in `internal/provider/anthropic.go`. The `system` field is sent as an array of content blocks (not a plain string — this is required to attach `cache_control`). Two breakpoints are used: one on the last tool definition (caches the tool block) and one on the first (static) system block (caches tools + base system prompt). Prefix order is tools → system → messages, so the system breakpoint covers everything through the static system prompt. The provider already reads `cache_creation_input_tokens` and `cache_read_input_tokens` from the response and surfaces them in `TokenUsage`. Minimum cacheable prefix is 1,024 tokens (Sonnet/Haiku) or 4,096 (Opus 4.5+). Cache is 5-minute TTL by default; each cache hit resets the TTL timer.
- **Static/dynamic system prompt separation**: The current date is injected as a separate trailing system prompt entry via `systemReminderPrompt()` in `internal/agent/prompt_builder.go`, NOT in the base system prompt. The Anthropic provider splits multiple system entries into separate content blocks: the first (static) block gets `cache_control`, and trailing dynamic blocks (date, compaction summary) do not. This keeps the cacheable prefix byte-for-byte identical across turns. The working directory, platform, **OS version**, and **shell** remain in the base prompt because they are static within a session. The `buildSystemPrompt()` function in `internal/agent/loop.go` no longer includes `Current date:` — it's in the separate `<system-reminder>` entry appended at the call site.
- **OS/shell detection**: `getOSEnv()`/`osEnvPrompt()` in `internal/agent/prompt_builder.go` probe the host OS version (sw_vers on macOS, /etc/os-release on Linux, `ver` on Windows) once and cache it, then inject `OS:` and `Shell: sh (commands are executed via "sh -c" — write POSIX-compatible shell...)` lines into the cacheable prefix. This prevents silent bashism failures (the agent previously had no way to know the bash tool uses `sh -c`) and disambiguates the OS version that `darwin/amd64` alone can't. The bash tool description in `internal/tool/bash.go` also states the POSIX sh constraint.
- **OpenAI**: Automatic prefix caching — no explicit markers needed. `cached_tokens` is tracked from the response in `TokenUsage.CacheReadTokens`.
- **OpenRouter**: Passes through to upstream provider; depends on the underlying model.
- **Ollama**: No prompt caching at the API level.
