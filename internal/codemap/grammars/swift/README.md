# tree-sitter-swift (vendored)

`parser.c` and `scanner.c` are **generated output**, not source. Do not edit
them, and do not read them looking for behaviour — the grammar is `grammar.js`
upstream.

## Why this is vendored rather than required

Every other grammar arrives through `go.mod`. Swift cannot:

- Upstream ([alex-pinkus/tree-sitter-swift]) generates `src/parser.c` at build
  time and `.gitignore`s it (`/src/*`, un-ignoring only `scanner.c` and the
  JSON).
- It publishes no tagged versions, so the Go proxy can only serve a commit
  pseudo-version — and no commit contains `parser.c`.
- Its own Go binding `#include`s `../../src/parser.c`, a file that is never in
  the tree, so `go get` yields a package that cannot compile.

Vendoring the generated parser is the only way to depend on this grammar from
Go. It is ~20 MB of C, which compresses to about 1 MB in git.

## Regenerating

Requires Node. From a scratch directory:

```bash
git clone https://github.com/alex-pinkus/tree-sitter-swift
cd tree-sitter-swift && npx --yes tree-sitter-cli@0.25.10 generate
```

Then copy `src/parser.c`, `src/scanner.c` and `src/tree_sitter/*.h` over the
copies here, keep `binding.go` and this file, and run `go test ./internal/codemap/`.

`binding.go` compiles the two `.c` files as separate translation units rather
than `#include`ing them into one the way upstream's binding does: both define
`TOKEN_COUNT`, and folding them together warns on every build.

[alex-pinkus/tree-sitter-swift]: https://github.com/alex-pinkus/tree-sitter-swift
