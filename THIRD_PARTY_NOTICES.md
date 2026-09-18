# Third-Party Notices

Ogcode bundles and links third-party software. Each component remains under its own
license and its own copyright; this file records those terms as the AGPL and the
permissive licenses require. Nothing here changes Ogcode's own license — see
[LICENSING.md](LICENSING.md).

## Copyleft component — read this before taking a commercial license

**`github.com/gen2brain/go-fitz` — AGPL-3.0**
Vendors [MuPDF](https://mupdf.com/) (Artifex Software, Inc.), which Artifex
dual-licenses: AGPL-3.0 or a paid commercial license. Used for PDF rasterization in
[`internal/tool/pdf_render.go`](internal/tool/pdf_render.go) and the LaTeX preview
([`internal/server/latex_fitz.go`](internal/server/latex_fitz.go)).

Under Ogcode's AGPL track this is fully compatible and no action is needed.

Under the **commercial** track it is not. An Ogcode commercial license covers Ogcode's
own code; it cannot grant rights to MuPDF. A commercial licensee who needs PDF rendering
must obtain a MuPDF license from [Artifex](https://artifex.com/licensing/), or build
without the component.

## Go dependencies

| Component | License |
|---|---|
| `connectrpc.com/connect` | Apache-2.0 |
| `github.com/PuerkitoBio/goquery` | BSD-3-Clause |
| `github.com/UserNobody14/tree-sitter-dart` | MIT |
| `github.com/andybalholm/brotli` | MIT |
| `github.com/gen2brain/go-fitz` (MuPDF) | **AGPL-3.0** — see above |
| `github.com/go-chi/chi/v5` | MIT |
| `github.com/go-shiori/go-readability` | MIT |
| `github.com/joho/godotenv` | MIT |
| `github.com/klauspost/compress` | BSD-3-Clause |
| `github.com/ledongthuc/pdf` | BSD-3-Clause |
| `github.com/modelcontextprotocol/go-sdk` | Apache-2.0 |
| `github.com/oklog/ulid/v2` | Apache-2.0 |
| `github.com/pressly/goose/v3` | MIT |
| `github.com/refraction-networking/utls` | BSD-3-Clause |
| `github.com/shirou/gopsutil/v4` | BSD-3-Clause |
| `github.com/spf13/cobra` | Apache-2.0 |
| `github.com/tree-sitter/go-tree-sitter` and the `tree-sitter-*` grammars | MIT |
| `golang.org/x/image`, `golang.org/x/net`, `golang.org/x/oauth2`, `golang.org/x/sync` | BSD-3-Clause |
| `modernc.org/sqlite` | BSD-3-Clause |

### Vendored in-tree

- [`internal/codemap/grammars/swift`](internal/codemap/grammars/swift) — tree-sitter-swift,
  MIT, © 2021 alex-pinkus. Vendored rather than required through `go.mod` because upstream
  generates its parser at build time and publishes no tagged versions.

## Web UI dependencies

The browser bundle is predominantly MIT and ISC, with a small number of Apache-2.0 and
BSD components. Two exceptions worth naming:

- **`dompurify`** — MPL-2.0 OR Apache-2.0. Ogcode takes it under **Apache-2.0**.
- **`lightningcss`** (and its platform binaries) — MPL-2.0. A build-time CSS toolchain
  dependency of Vite; not redistributed in the browser bundle.

MPL-2.0 is file-level copyleft: it attaches to modifications of the MPL files themselves,
not to the work that uses them, so neither Ogcode track is affected.

Full per-package license metadata is in [`web/package-lock.json`](web/package-lock.json).

---

To regenerate the Go table:

```bash
go install github.com/google/go-licenses@latest && go-licenses report ./...
```
