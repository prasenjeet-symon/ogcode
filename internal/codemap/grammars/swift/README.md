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

## Vendored version

Generated from upstream commit `35245fb` (2026-09-26, `0.7.3-24-g35245fb`),
which carries the `as? T ?? value` fix (upstream #597, #610). Record the commit
here whenever you regenerate: the generated files do not say which grammar
they came from, and without it the next update cannot tell what it pulls in.

The grammar still cannot read an empty tuple expression, `()`; `swiftRepair`
in `internal/codemap/shims.go` covers it.

## Regenerating

Requires Node. From a scratch directory:

```bash
git clone https://github.com/alex-pinkus/tree-sitter-swift
cd tree-sitter-swift && npx --yes tree-sitter-cli@0.25.10 generate
```

Read `git diff <recorded commit>..HEAD -- grammar.js src/scanner.c` first:
`generate` executes `grammar.js`, and `scanner.c` is compiled into ogcode.

Then copy `src/parser.c`, `src/scanner.c` and `src/tree_sitter/*.h` over the
copies here, keep `binding.go` and this file, and run `go test ./internal/codemap/`.

`binding.go` compiles the two `.c` files as separate translation units rather
than `#include`ing them into one the way upstream's binding does: both define
`TOKEN_COUNT`, and folding them together warns on every build.

[alex-pinkus/tree-sitter-swift]: https://github.com/alex-pinkus/tree-sitter-swift
